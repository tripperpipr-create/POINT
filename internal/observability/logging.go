package observability

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

type ctxAttrsKey struct{}

// ConfigureLogger builds a slog logger from POINT_LOG_LEVEL
// (debug|info|warn|error, default info) and POINT_LOG_FORMAT
// (text|json, default text so the IDE Output channel stays readable).
func ConfigureLogger() *slog.Logger {
	level := slog.LevelInfo
	switch strings.ToLower(strings.TrimSpace(os.Getenv("POINT_LOG_LEVEL"))) {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	case "":
		level = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) == 0 && a.Key == slog.TimeKey && a.Value.Kind() == slog.KindTime {
				return slog.String(slog.TimeKey, a.Value.Time().In(time.Local).Format("15:04:05.000"))
			}
			return a
		},
	}
	output := io.Writer(os.Stdout)
	if logPath := strings.TrimSpace(os.Getenv("POINT_LOG_FILE")); logPath != "" {
		if file, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
			_ = file.Close()
			output = appendLogWriter(logPath)
		}
	}
	var handler slog.Handler
	if strings.EqualFold(strings.TrimSpace(os.Getenv("POINT_LOG_FORMAT")), "json") {
		handler = slog.NewJSONHandler(output, opts)
	} else {
		handler = slog.NewTextHandler(output, opts)
	}
	return slog.New(handler)
}

// appendLogWriter opens the detached service log for each complete slog
// record. That avoids holding a Windows file lock across log rotation and lets
// the Code-OSS host append its own lifecycle messages to the same journal.
type appendLogWriter string

func (path appendLogWriter) Write(value []byte) (int, error) {
	file, err := os.OpenFile(string(path), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, err
	}
	written, writeErr := file.Write(value)
	closeErr := file.Close()
	if writeErr != nil {
		return written, writeErr
	}
	return written, closeErr
}

// NewRequestID returns a short opaque id for correlating one IDE/core turn.
func NewRequestID() string {
	var buf [6]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "req_unknown"
	}
	return "req_" + hex.EncodeToString(buf[:])
}

// With returns ctx carrying extra slog attributes (request_id, run_id, …).
func With(ctx context.Context, attrs ...any) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(attrs) == 0 {
		return ctx
	}
	existing := Attrs(ctx)
	copied := make([]any, 0, len(existing)+len(attrs))
	copied = append(copied, existing...)
	copied = append(copied, attrs...)
	return context.WithValue(ctx, ctxAttrsKey{}, copied)
}

// WithRequestID stores request_id on ctx for downstream slog calls.
func WithRequestID(ctx context.Context, id string) context.Context {
	id = strings.TrimSpace(id)
	if id == "" {
		return ctx
	}
	return With(ctx, "request_id", id)
}

// RequestID returns the correlation id stored on ctx, if any.
func RequestID(ctx context.Context) string {
	attrs := Attrs(ctx)
	for i := 0; i+1 < len(attrs); i += 2 {
		key, _ := attrs[i].(string)
		if key == "request_id" {
			value, _ := attrs[i+1].(string)
			return value
		}
	}
	return ""
}

// Attrs returns slog key/value pairs stored on ctx.
func Attrs(ctx context.Context) []any {
	if ctx == nil {
		return nil
	}
	attrs, _ := ctx.Value(ctxAttrsKey{}).([]any)
	return attrs
}

// From returns a logger with ctx correlation attributes attached.
func From(ctx context.Context) *slog.Logger {
	attrs := Attrs(ctx)
	if len(attrs) == 0 {
		return slog.Default()
	}
	return slog.Default().With(attrs...)
}

// QuietHTTP is true for high-frequency poll routes that should stay at debug
// unless they fail. Errors are still logged at warn/info by the HTTP layer.
func QuietHTTP(method, path string) bool {
	method = strings.ToUpper(strings.TrimSpace(method))
	path = strings.TrimSpace(path)
	if path == "/api/health" || path == "/api/events" {
		return true
	}
	if method != "GET" {
		if method == "POST" && (path == "/api/ide/observations" || path == "/api/index/update" || path == "/api/index/invalidate" || path == "/api/project-agents/capability" || path == "/api/project-agents/capability-delta") {
			return true
		}
		return false
	}
	switch path {
	case "/api/bootstrap", "/api/state/runtime", "/api/state/guild", "/api/companion/live", "/api/index/status", "/api/runs", "/api/workflow-runs":
		return true
	}
	// Карточка утверждённого наряда опрашивается, пока квест живой: раз в
	// несколько секунд и минутами подряд. В журнале это шум, за которым не
	// видно самой работы.
	return strings.HasPrefix(path, "/api/v2/work-orders/")
}

// Snippet returns a truncated UTF-8-safe preview for logs.
func Snippet(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 || value == "" {
		return value
	}
	if utf8.RuneCountInString(value) <= max {
		return value
	}
	runes := []rune(value)
	return string(runes[:max]) + "…"
}

// HostOnly returns scheme://host from a base URL for safe logging.
func HostOnly(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return Snippet(raw, 80)
	}
	if parsed.Scheme == "" {
		return parsed.Host
	}
	return parsed.Scheme + "://" + parsed.Host
}

// RoleSummary counts message roles for compact logs.
func RoleSummary(roles []string) map[string]int {
	out := map[string]int{}
	for _, role := range roles {
		role = strings.ToLower(strings.TrimSpace(role))
		if role == "" {
			continue
		}
		out[role]++
	}
	return out
}
