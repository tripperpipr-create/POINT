package observability_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"local-agent-workbench/internal/observability"
)

func TestConfigureLoggerDefaultsToInfo(t *testing.T) {
	t.Setenv("POINT_LOG_LEVEL", "")
	logger := observability.ConfigureLogger()
	if logger.Enabled(context.Background(), slog.LevelDebug) {
		t.Fatal("default logger should suppress debug chatter")
	}
	if !logger.Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("default logger should retain operational info")
	}
	t.Setenv("POINT_LOG_LEVEL", "debug")
	if !observability.ConfigureLogger().Enabled(context.Background(), slog.LevelDebug) {
		t.Fatal("explicit debug level should remain available")
	}
}

func TestConfigureLoggerCanWriteDetachedCoreLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "point-core.log")
	t.Setenv("POINT_LOG_FILE", path)
	t.Setenv("POINT_LOG_FORMAT", "text")
	observability.ConfigureLogger().Info("detached core is alive", "quest_id", "quest-1")
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "detached core is alive") || !strings.Contains(string(data), "quest_id=quest-1") {
		t.Fatalf("detached log=%q err=%v", data, err)
	}
}

func TestSnippetAndHostOnly(t *testing.T) {
	if got := observability.Snippet("abcdef", 4); got != "abcd…" {
		t.Fatalf("snippet=%q", got)
	}
	if got := observability.HostOnly("https://llmux.example/v1"); got != "https://llmux.example" {
		t.Fatalf("host=%q", got)
	}
	roles := observability.RoleSummary([]string{"system", "user", "assistant", "user"})
	if roles["system"] != 1 || roles["user"] != 2 || roles["assistant"] != 1 {
		t.Fatalf("roles=%v", roles)
	}
}

func TestRequestIDRoundTrip(t *testing.T) {
	id := observability.NewRequestID()
	if !strings.HasPrefix(id, "req_") || len(id) < 8 {
		t.Fatalf("id=%q", id)
	}
	ctx := observability.WithRequestID(context.Background(), id)
	if got := observability.RequestID(ctx); got != id {
		t.Fatalf("request_id=%q", got)
	}
	ctx = observability.With(ctx, "run_id", "run_1")
	if observability.RequestID(ctx) != id {
		t.Fatalf("request_id lost after With")
	}
}

func TestQuietHTTP(t *testing.T) {
	if !observability.QuietHTTP("GET", "/api/bootstrap") {
		t.Fatal("bootstrap should be quiet")
	}
	if !observability.QuietHTTP("POST", "/api/ide/observations") {
		t.Fatal("ide observations should be quiet")
	}
	if observability.QuietHTTP("POST", "/api/index/update") != true {
		t.Fatal("index update should be quiet")
	}
	if !observability.QuietHTTP("POST", "/api/project-agents/capability") || !observability.QuietHTTP("POST", "/api/project-agents/capability-delta") {
		t.Fatal("background capability probes should be quiet")
	}
	if observability.QuietHTTP("POST", "/api/companion/chat") {
		t.Fatal("companion chat must stay visible")
	}
	if observability.QuietHTTP("POST", "/api/runs") {
		t.Fatal("start run must stay visible")
	}
}
