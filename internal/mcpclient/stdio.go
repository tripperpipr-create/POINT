package mcpclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"local-agent-workbench/internal/osproc"
)

// stopGrace — сколько сервер получает, чтобы выйти сам по закрытому stdin.
const stopGrace = 5 * time.Second

// stdioTransport — сервер как дочерний процесс: сообщения построчно в stdin и
// из stdout, журнал — stderr.
type stdioTransport struct {
	cmd     *exec.Cmd
	group   *osproc.Group
	stdin   io.WriteCloser
	writeMu sync.Mutex
	log     *ring
	handle  func(message) *message

	pendingMu sync.Mutex
	pending   map[string]chan message

	exited    chan struct{}
	exitErr   error
	closeOnce sync.Once
}

func startStdio(cfg Config, log *ring, handle func(message) *message) (*stdioTransport, error) {
	if strings.TrimSpace(cfg.Command) == "" {
		return nil, newError(KindSpawn, nil, "command is empty")
	}
	path, err := exec.LookPath(cfg.Command)
	if err != nil {
		return nil, newError(KindSpawn, err, "%s not found on PATH", cfg.Command)
	}
	if err := checkBatchArguments(path, cfg.Args); err != nil {
		return nil, err
	}
	cmd := osproc.Command(path, cfg.Args...)
	cmd.Env = cfg.Env
	cmd.Dir = cfg.Dir
	cmd.Stderr = log
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, newError(KindSpawn, err, "stdin pipe")
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, newError(KindSpawn, err, "stdout pipe")
	}
	group, err := osproc.StartGroup(cmd, osproc.GroupOptions{DieWithParent: true})
	if err != nil {
		return nil, newError(KindSpawn, err, "start %s", filepath.Base(path))
	}
	t := &stdioTransport{
		cmd: cmd, group: group, stdin: stdin, log: log, handle: handle,
		pending: map[string]chan message{}, exited: make(chan struct{}),
	}
	readDone := make(chan struct{})
	go t.read(stdout, readDone)
	go func() {
		// Wait читает stderr до конца, а stdout дочитывает read: ждём обоих,
		// иначе последний ответ сервера мог бы потеряться при выходе.
		<-readDone
		t.exitErr = cmd.Wait()
		group.Release()
		close(t.exited)
	}()
	return t, nil
}

// checkBatchArguments: на Windows `npx` — это npx.cmd, а пакетный файл
// разбирает аргументы через cmd.exe. Метасимвол cmd в аргументе стал бы
// командой. Go с 1.22.2 отказывает в таком запуске сам; здесь отказ с
// понятной причиной и до попытки.
func checkBatchArguments(path string, args []string) error {
	if runtime.GOOS != "windows" {
		return nil
	}
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".cmd" && ext != ".bat" {
		return nil
	}
	for _, arg := range args {
		if strings.ContainsAny(arg, "&|<>^%\"\r\n") {
			return newError(KindSpawn, nil, "argument %q contains a cmd.exe metacharacter; %s is a batch file", arg, filepath.Base(path))
		}
	}
	return nil
}

func (t *stdioTransport) read(stdout io.Reader, done chan<- struct{}) {
	defer close(done)
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64<<10), maxMessageBytes)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		if line[0] != '{' && line[0] != '[' {
			// Сервер не должен писать в stdout ничего, кроме протокола, но
			// некоторые пишут баннер. Строка уходит в журнал, а не в разбор.
			_, _ = fmt.Fprintf(t.log, "\n[stdout] %s\n", clip(string(line), 500))
			continue
		}
		t.dispatch(line)
	}
	if err := scanner.Err(); err != nil {
		_, _ = fmt.Fprintf(t.log, "\n[point] stdout closed: %s\n", err.Error())
		// Сообщение больше потолка разбору не поддаётся: соединение не
		// восстановить, сервер гасится, и надзор поднимет его заново.
		t.group.Kill()
	}
}

func (t *stdioTransport) dispatch(line []byte) {
	var batch []message
	if line[0] == '[' {
		if err := json.Unmarshal(line, &batch); err != nil {
			_, _ = fmt.Fprintf(t.log, "\n[point] unreadable batch from server\n")
			return
		}
	} else {
		var single message
		if err := json.Unmarshal(line, &single); err != nil {
			_, _ = fmt.Fprintf(t.log, "\n[point] unreadable message from server\n")
			return
		}
		batch = []message{single}
	}
	for _, msg := range batch {
		if msg.isResponse() {
			t.pendingMu.Lock()
			ch := t.pending[idKey(msg.ID)]
			delete(t.pending, idKey(msg.ID))
			t.pendingMu.Unlock()
			if ch != nil {
				ch <- msg
			}
			continue
		}
		if reply := t.handle(msg); reply != nil {
			_ = t.write(*reply)
		}
	}
}

func (t *stdioTransport) write(msg message) error {
	raw, err := json.Marshal(msg)
	if err != nil {
		return newError(KindProtocol, err, "encode message")
	}
	raw = append(raw, '\n')
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	select {
	case <-t.exited:
		return t.exitError()
	default:
	}
	if _, err := t.stdin.Write(raw); err != nil {
		return newError(KindExited, err, "write to server: %s", err.Error())
	}
	return nil
}

func (t *stdioTransport) call(ctx context.Context, msg message) (message, error) {
	ch := make(chan message, 1)
	key := idKey(msg.ID)
	t.pendingMu.Lock()
	t.pending[key] = ch
	t.pendingMu.Unlock()
	forget := func() {
		t.pendingMu.Lock()
		delete(t.pending, key)
		t.pendingMu.Unlock()
	}
	if err := t.write(msg); err != nil {
		forget()
		return message{}, err
	}
	select {
	case response := <-ch:
		return response, nil
	case <-t.exited:
		forget()
		// Ответ мог прийти в тот же миг, что и выход.
		select {
		case response := <-ch:
			return response, nil
		default:
		}
		return message{}, t.exitError()
	case <-ctx.Done():
		forget()
		return message{}, ctx.Err()
	}
}

func (t *stdioTransport) notify(_ context.Context, msg message) error { return t.write(msg) }

func (t *stdioTransport) negotiated(string) {}

func (t *stdioTransport) done() <-chan struct{} { return t.exited }

func (t *stdioTransport) exitError() error {
	detail := "server process exited"
	if t.exitErr != nil {
		detail += ": " + t.exitErr.Error()
	}
	var exit *exec.ExitError
	if errors.As(t.exitErr, &exit) {
		detail = fmt.Sprintf("server process exited with code %d", exit.ExitCode())
	}
	return newError(KindExited, t.exitErr, "%s", detail)
}

// close: закрытый stdin — вежливая просьба выйти (так завершают stdio-сервер
// по протоколу), через stopGrace — вся группа гасится.
func (t *stdioTransport) close() error {
	t.closeOnce.Do(func() {
		_ = t.stdin.Close()
		select {
		case <-t.exited:
		case <-time.After(stopGrace):
			t.group.Kill()
			<-t.exited
		}
	})
	return nil
}
