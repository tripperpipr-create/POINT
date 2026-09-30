package providers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Сколько поток может молчать после первого байта, прежде чем его признают
// зависшим. Отсчёт начинается только с первого байта тела: до него локальная
// модель считает prefill длинного контекста, и это законные минуты, а не
// обрыв. Молчание посреди ответа — другое дело: живой квест 30.09 видел, как
// оборванное соединение держало прогон до общего срока.
const defaultStreamIdleSeconds = 180

// errStreamStalled — поток начался и замолчал дольше срока тишины. Ошибка
// временная: тот же запрос повторяется, как при обрыве соединения.
var errStreamStalled = errors.New("provider stream stalled: no data within the idle timeout")

func (c Config) streamIdle() time.Duration {
	switch {
	case c.StreamIdleSeconds < 0:
		return 0
	case c.StreamIdleSeconds == 0:
		return defaultStreamIdleSeconds * time.Second
	default:
		return time.Duration(c.StreamIdleSeconds) * time.Second
	}
}

// idleReader закрывает тело ответа, если после первого байта оно молчит
// дольше idle. Чтение, прерванное таким закрытием, возвращает errStreamStalled,
// а не безликую ошибку закрытого соединения.
type idleReader struct {
	body    io.ReadCloser
	idle    time.Duration
	mu      sync.Mutex
	timer   *time.Timer
	stalled atomic.Bool
}

func newIdleReader(body io.ReadCloser, idle time.Duration) io.ReadCloser {
	if idle <= 0 {
		return body
	}
	return &idleReader{body: body, idle: idle}
}

func (r *idleReader) Read(p []byte) (int, error) {
	n, err := r.body.Read(p)
	if r.stalled.Load() {
		return n, errStreamStalled
	}
	if n > 0 {
		r.mu.Lock()
		if r.timer == nil {
			r.timer = time.AfterFunc(r.idle, func() {
				r.stalled.Store(true)
				_ = r.body.Close()
			})
		} else {
			r.timer.Reset(r.idle)
		}
		r.mu.Unlock()
	}
	return n, err
}

func (r *idleReader) Close() error {
	r.mu.Lock()
	if r.timer != nil {
		r.timer.Stop()
	}
	r.mu.Unlock()
	return r.body.Close()
}

// Windows сообщает обрыв своими кодами WinSock, а не POSIX: живой прогон E6
// упал на «wsarecv: An established connection was aborted», который не узнал
// ни один список. Значения задаются числом, чтобы сборка под Linux тоже их
// различала в обёрнутых ошибках.
const (
	wsaeConnAborted syscall.Errno = 10053
	wsaeConnReset   syscall.Errno = 10054
)

// isTransientStreamError — обрыв или зависание обращения, после которого тот
// же запрос имеет смысл повторить. Отмена и срок вызывающего сюда не входят:
// их повтор ничего не спасёт.
func isTransientStreamError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, errStreamStalled) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	for _, code := range []syscall.Errno{syscall.ECONNRESET, syscall.ECONNABORTED, syscall.EPIPE, wsaeConnAborted, wsaeConnReset} {
		if errors.Is(err, code) {
			return true
		}
	}
	msg := strings.ToLower(err.Error())
	markers := []string{
		"upstream closed the stream without sending any content",
		"stream closed without sending any content",
		"connection reset",
		"unexpected eof",
		"http2: server sent goaway",
		"http2: stream closed",
		"wsarecv",
		"wsasend",
		"forcibly closed",
		"established connection was aborted",
		"broken pipe",
		"provider stream stalled",
		"overloaded_error",
	}
	for _, marker := range markers {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// Сколько неразборчивых строк data: подряд поток переживает.
const maxUndecodableStreamLines = 5

// normalizeToolArguments возвращает аргументы вызова как JSON-объект или nil,
// если их не удалось прочесть. Пустая строка — законные аргументы инструмента
// без параметров: llama.cpp и часть парсеров vLLM шлют именно её.
func normalizeToolArguments(raw string) json.RawMessage {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return json.RawMessage(`{}`)
	}
	if json.Valid([]byte(trimmed)) {
		return json.RawMessage(trimmed)
	}
	return nil
}

// toolArgumentError объясняет, почему аргументы не прочитаны. Обрезанный
// пределом вывода вызов чинится иначе, чем неверный JSON: его надо разбить.
func toolArgumentError(position int, finishReason string) string {
	if finishReason == "length" || finishReason == "max_tokens" {
		return "tool call " + strconv.Itoa(position) + " was cut off by the output token limit before its arguments were complete; make the call smaller (for example, split a large patch into several edits or files)"
	}
	return "tool call " + strconv.Itoa(position) + " returned arguments that are not valid JSON"
}

// IsContextOverflowError — провайдер отказал потому, что запрос не влез в окно
// модели. Оценка токенов у Point приблизительная (байты/4), а окно профиля
// бывает больше настоящего, поэтому такой отказ лечится сжатием разговора, а
// не концом прогона.
func IsContextOverflowError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, marker := range []string{
		"context_length_exceeded",
		"maximum context length",
		"context length is",
		"prompt is too long",
		"exceeds the context window",
		"input is too long",
		"too many tokens",
		"exceed_context_size",
		"context window exceeded",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}
