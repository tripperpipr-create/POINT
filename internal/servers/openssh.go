package servers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"

	"local-agent-workbench/internal/osproc"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"
)

// Runner executes non-interactive SSH operations via the host OpenSSH client.
type Runner interface {
	Probe(ctx context.Context, profile Profile, password string) (ProbeResult, error)
	List(ctx context.Context, profile Profile, remotePath, password string) ([]string, string, error)
	Read(ctx context.Context, profile Profile, remotePath, password string) (string, bool, error)
	Exec(ctx context.Context, profile Profile, command, password string, timeout time.Duration) (stdout, stderr string, exitCode int, err error)
}

type OpenSSHRunner struct{}

type cappedBuffer struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (b *cappedBuffer) Write(value []byte) (int, error) {
	originalLength := len(value)
	remaining := b.limit - b.buffer.Len()
	if remaining > 0 {
		if len(value) > remaining {
			value = value[:remaining]
			b.truncated = true
		}
		_, _ = b.buffer.Write(value)
	}
	if originalLength > remaining {
		b.truncated = true
	}
	// Report the whole input as consumed so os/exec keeps draining the pipe
	// while memory remains bounded.
	return originalLength, nil
}

func (b *cappedBuffer) String() string { return b.buffer.String() }

func LookPathSSH() (string, error) {
	candidates := []string{"ssh"}
	if runtime.GOOS == "windows" {
		candidates = append([]string{"ssh.exe"}, candidates...)
		if root := strings.TrimSpace(os.Getenv("SystemRoot")); root != "" {
			candidates = append(candidates,
				filepath.Join(root, "System32", "OpenSSH", "ssh.exe"),
				filepath.Join(root, "Sysnative", "OpenSSH", "ssh.exe"),
			)
		}
	}
	var tried []string
	for _, name := range candidates {
		path, err := exec.LookPath(name)
		if err == nil {
			return path, nil
		}
		tried = append(tried, name)
	}
	return "", fmt.Errorf("OpenSSH-клиент (ssh) не найден (%s). Установите «OpenSSH Client» в Параметры Windows → Приложения → Дополнительные компоненты", strings.Join(tried, ", "))
}

func (OpenSSHRunner) Probe(ctx context.Context, profile Profile, password string) (ProbeResult, error) {
	if profile.AuthMethod == AuthPassword && strings.TrimSpace(password) == "" {
		return ProbeResult{}, fmt.Errorf("автоматическая проверка пароля требует одноразовую передачу пароля из SecretStorage; предпочтительны ключ или ssh-agent")
	}
	stdout, stderr, exitCode, err := runSSH(ctx, profile, password, true, 15*time.Second, "echo", "POINT_SSH_OK")
	combined := strings.TrimSpace(stdout + "\n" + stderr)
	if err != nil {
		return ProbeResult{Banner: combined}, fmt.Errorf("%s", humanizeSSHError(err, combined, exitCode))
	}
	if exitCode != 0 || !strings.Contains(stdout, "POINT_SSH_OK") {
		return ProbeResult{Banner: combined}, fmt.Errorf("%s", humanizeSSHError(fmt.Errorf("exit %d", exitCode), combined, exitCode))
	}
	return ProbeResult{Message: "Соединение установлено", Banner: firstLine(stdout)}, nil
}

func (OpenSSHRunner) List(ctx context.Context, profile Profile, remotePath, password string) ([]string, string, error) {
	if err := validateRemotePath(remotePath); err != nil {
		return nil, "", err
	}
	if profile.AuthMethod == AuthPassword && strings.TrimSpace(password) == "" {
		return nil, "", fmt.Errorf("листинг по паролю требует одноразовый пароль; используйте ключ или ssh-agent")
	}
	// OpenSSH joins all remote argv items into one command line for the remote
	// shell. Passing the path as a separate local argv item therefore does not
	// preserve its boundary. Quote it explicitly before it crosses that shell.
	stdout, stderr, exitCode, err := runSSH(ctx, profile, password, true, 20*time.Second, remoteListCommand(remotePath))
	combined := strings.TrimSpace(stdout + "\n" + stderr)
	if err != nil || exitCode != 0 {
		return nil, combined, fmt.Errorf("%s", humanizeSSHError(err, combined, exitCode))
	}
	lines := splitNonEmpty(stdout)
	return lines, stdout, nil
}

const remotePreviewLimit = 64 * 1024

func (OpenSSHRunner) Read(ctx context.Context, profile Profile, remotePath, password string) (string, bool, error) {
	if err := validateRemotePath(remotePath); err != nil {
		return "", false, err
	}
	if profile.AuthMethod == AuthPassword && strings.TrimSpace(password) == "" {
		return "", false, fmt.Errorf("чтение по паролю требует одноразовый пароль; используйте ключ или ssh-agent")
	}
	stdout, stderr, exitCode, err := runSSH(ctx, profile, password, true, 20*time.Second, remoteReadCommand(remotePath))
	combined := strings.TrimSpace(stderr)
	if err != nil || exitCode != 0 {
		return "", false, fmt.Errorf("%s", humanizeSSHError(err, combined, exitCode))
	}
	if strings.IndexByte(stdout, 0) >= 0 || !utf8.ValidString(stdout) {
		return "", false, fmt.Errorf("удалённый файл не является UTF-8 текстом; откройте его через SSH-терминал")
	}
	truncated := len(stdout) > remotePreviewLimit
	if truncated {
		stdout = stdout[:remotePreviewLimit]
	}
	return stdout, truncated, nil
}

func (OpenSSHRunner) Exec(ctx context.Context, profile Profile, command, password string, timeout time.Duration) (string, string, int, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return "", "", -1, fmt.Errorf("команда пуста")
	}
	if len(command) > 8*1024 {
		return "", "", -1, fmt.Errorf("команда слишком длинная")
	}
	if strings.ContainsAny(command, "\x00\r\n") {
		return "", "", -1, fmt.Errorf("команда не должна содержать переводы строк")
	}
	if profile.AuthMethod == AuthPassword && strings.TrimSpace(password) == "" {
		return "", "", -1, fmt.Errorf("удалённый exec по паролю требует одноразовый пароль; используйте ключ или ssh-agent")
	}
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	// Remote command is passed as a single ssh remote argv; OpenSSH runs it via the user shell.
	return runSSH(ctx, profile, password, true, timeout, command)
}

func validateRemotePath(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("удалённый путь пуст")
	}
	if len(path) > 1024 {
		return fmt.Errorf("удалённый путь слишком длинный")
	}
	if strings.ContainsAny(path, "\x00\r\n") {
		return fmt.Errorf("удалённый путь содержит недопустимые символы")
	}
	return nil
}

func remoteShellQuote(value string) string {
	// POSIX shells cannot represent a single quote inside single quotes. Close
	// the quoted segment, emit a quoted single quote, then resume it.
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func remoteListCommand(path string) string {
	// One entry per line plus a trailing slash for directories gives the IDE a
	// navigable result without parsing locale-dependent `ls -la` columns.
	return "LC_ALL=C ls -1Ap -- " + remoteShellQuote(path)
}

func remoteReadCommand(path string) string {
	// Bound the preview on the remote side as well as locally. The extra byte
	// lets the client distinguish a full preview from a shorter file.
	return fmt.Sprintf("head -c %d -- %s", remotePreviewLimit+1, remoteShellQuote(path))
}

func buildSSHArgs(profile Profile, batch bool, remoteCommand []string) []string {
	args := []string{
		"-p", fmt.Sprintf("%d", profile.Port),
		"-o", "ConnectTimeout=12",
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "IdentitiesOnly=yes",
	}
	if batch {
		args = append(args, "-o", "BatchMode=yes")
	}
	if profile.AuthMethod == AuthKey && profile.PrivateKeyPath != "" {
		args = append(args, "-i", profile.PrivateKeyPath)
	}
	if profile.AuthMethod == AuthPassword {
		args = append(args, "-o", "PreferredAuthentications=password", "-o", "PubkeyAuthentication=no")
	}
	if profile.AuthMethod == AuthAgent {
		args = append(args, "-o", "PreferredAuthentications=publickey", "-o", "PasswordAuthentication=no")
	}
	// Terminate local ssh options before the user-controlled destination. This
	// marker belongs before the destination; after it, it would become part of
	// the command executed by the remote shell.
	args = append(args, "--", profile.Destination())
	if len(remoteCommand) > 0 {
		args = append(args, remoteCommand...)
	}
	return args
}

func runSSH(ctx context.Context, profile Profile, password string, batch bool, timeout time.Duration, remoteCommand ...string) (stdout, stderr string, exitCode int, err error) {
	sshPath, err := LookPathSSH()
	if err != nil {
		return "", "", -1, err
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	useBatch := batch && profile.AuthMethod != AuthPassword
	args := buildSSHArgs(profile, useBatch, remoteCommand)
	cmd := osproc.CommandContext(ctx, sshPath, args...)
	cmd.Env = sanitizedEnv()
	var askPassCleanup func()
	if profile.AuthMethod == AuthPassword && strings.TrimSpace(password) != "" {
		askPassCleanup, err = attachAskPass(cmd, password)
		if err != nil {
			return "", "", -1, err
		}
		defer askPassCleanup()
	}
	outBuf := cappedBuffer{limit: remotePreviewLimit + 1}
	errBuf := cappedBuffer{limit: 64 * 1024}
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	runErr := cmd.Run()
	// Preserve one look-ahead byte so bounded readers can report truncation
	// accurately. Both pipes are drained while retained memory stays bounded.
	stdout = outBuf.String()
	stderr = errBuf.String()
	exitCode = 0
	if runErr != nil {
		var exit *exec.ExitError
		if errors.As(runErr, &exit) {
			exitCode = exit.ExitCode()
			return stdout, stderr, exitCode, nil
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return stdout, stderr, -1, fmt.Errorf("тайм-аут SSH-соединения")
		}
		return stdout, stderr, -1, runErr
	}
	return stdout, stderr, 0, nil
}

func attachAskPass(cmd *exec.Cmd, password string) (func(), error) {
	dir, err := os.MkdirTemp("", "point-ssh-askpass-*")
	if err != nil {
		return nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	script := filepath.Join(dir, "askpass")
	body := "#!/bin/sh\nprintf '%s\\n' \"$POINT_SSH_PASSWORD\"\n"
	if runtime.GOOS == "windows" {
		script = filepath.Join(dir, "askpass.cmd")
		// cmd.exe echo is unreliable for special chars; PowerShell write is heavier.
		// Password is only in the process environment, not the script body.
		body = "@echo off\r\npowershell -NoProfile -Command \"[Console]::Out.Write($env:POINT_SSH_PASSWORD)\"\r\n"
	}
	if err = os.WriteFile(script, []byte(body), 0o700); err != nil {
		cleanup()
		return nil, err
	}
	cmd.Env = append(cmd.Env,
		"SSH_ASKPASS="+script,
		"SSH_ASKPASS_REQUIRE=force",
		"DISPLAY=point-local",
		"POINT_SSH_PASSWORD="+password,
	)
	return cleanup, nil
}

func sanitizedEnv() []string {
	keep := map[string]bool{
		"PATH": true, "SystemRoot": true, "WINDIR": true, "HOME": true, "USERPROFILE": true,
		"HOMEDRIVE": true, "HOMEPATH": true, "SSH_AUTH_SOCK": true, "SSH_AGENT_PID": true,
		"SYSTEMDRIVE": true, "COMSPEC": true, "PROGRAMDATA": true, "LOCALAPPDATA": true, "APPDATA": true,
		"LANG": true, "LC_ALL": true, "TMP": true, "TEMP": true, "TMPDIR": true,
	}
	var out []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		upper := strings.ToUpper(key)
		if keep[key] || keep[upper] || strings.HasPrefix(upper, "SSH_") {
			out = append(out, entry)
		}
	}
	return out
}

func humanizeSSHError(err error, combined string, exitCode int) string {
	lower := strings.ToLower(combined + " " + fmt.Sprint(err))
	switch {
	case strings.Contains(lower, "permission denied"):
		return "отказано в доступе: проверьте пользователя, ключ или агент"
	case strings.Contains(lower, "connection refused"):
		return "сервер отклонил соединение (порт закрыт или sshd не запущен)"
	case strings.Contains(lower, "could not resolve") || strings.Contains(lower, "no such host"):
		return "не удалось разрешить имя хоста"
	case strings.Contains(lower, "network is unreachable") || strings.Contains(lower, "no route to host"):
		return "хост недоступен по сети"
	case strings.Contains(lower, "timed out") || strings.Contains(lower, "timeout"):
		return "тайм-аут SSH-соединения"
	case strings.Contains(lower, "openssh") && strings.Contains(lower, "не найден"):
		return err.Error()
	case exitCode != 0 && strings.TrimSpace(combined) != "":
		return firstLine(combined)
	case err != nil:
		return err.Error()
	default:
		return "SSH-команда завершилась с ошибкой"
	}
}

func firstLine(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if i := strings.IndexAny(value, "\r\n"); i >= 0 {
		return strings.TrimSpace(value[:i])
	}
	return value
}

func splitNonEmpty(value string) []string {
	raw := strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(raw))
	for _, line := range raw {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		out = append(out, line)
	}
	return out
}
