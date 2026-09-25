package mcpclient

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func dialFake(t *testing.T, mode string, tune func(*Config)) *Client {
	t.Helper()
	cfg := fakeConfig(t, mode)
	if tune != nil {
		tune(&cfg)
	}
	client, err := Dial(context.Background(), cfg)
	if err != nil {
		t.Fatalf("dial %s: %v", mode, err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestStdioHandshakeToolsAndCall(t *testing.T) {
	client := dialFake(t, "normal", nil)
	info := client.Info()
	if info.Name != "fake" || info.ProtocolVersion != "2025-06-18" || !info.ToolsListChanged {
		t.Fatalf("info = %+v", info)
	}
	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 2 || tools[0].Name != "echo" || tools[1].Name != "merge" {
		t.Fatalf("pagination lost tools: %+v", tools)
	}
	if tools[0].Annotations == nil || tools[0].Annotations.ReadOnlyHint == nil || !*tools[0].Annotations.ReadOnlyHint {
		t.Fatal("annotations lost")
	}
	result, err := client.CallTool(context.Background(), "echo", json.RawMessage(`{"x":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.Text() != `{"x":1}` || result.IsError {
		t.Fatalf("echo = %+v", result)
	}
}

// Сервер вправе спросить клиента. На ping Point отвечает, в модели (sampling)
// отказывает: инструмент дождётся обоих ответов и перескажет их.
func TestStdioAnswersPingAndRefusesSampling(t *testing.T) {
	client := dialFake(t, "normal", nil)
	result, err := client.CallTool(context.Background(), "ask", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Text() != "ping:{} sampling:-32601" {
		t.Fatalf("server saw %q", result.Text())
	}
	if !strings.Contains(client.Log(), "refused server request sampling/createMessage") {
		t.Fatalf("refusal not logged: %q", client.Log())
	}
}

func TestStdioLogIsScrubbed(t *testing.T) {
	client := dialFake(t, "normal", nil)
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(client.Log(), "starting") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	log := client.Log()
	if strings.Contains(log, "glpat-") || strings.Contains(log, "SUPERSECRETVALUE") {
		t.Fatalf("secret leaked into log: %q", log)
	}
	if !strings.Contains(log, "[REDACTED]") {
		t.Fatalf("log was not scrubbed but emptied: %q", log)
	}
	result, err := client.CallTool(context.Background(), "fail", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("isError lost")
	}
}

func TestStdioCrashReportsStderr(t *testing.T) {
	_, err := Dial(context.Background(), fakeConfig(t, "crash"))
	if KindOf(err) != KindExited {
		t.Fatalf("kind = %q, err = %v", KindOf(err), err)
	}
	typed := err.(*Error)
	if !strings.Contains(typed.Stderr, "boom: cannot reach GitLab") {
		t.Fatalf("stderr tail lost: %q", typed.Stderr)
	}
	if strings.Contains(typed.Stderr, "glpat-") || strings.Contains(typed.Stderr, "SUPERSECRETVALUE") {
		t.Fatalf("secret in stderr tail: %q", typed.Stderr)
	}
}

func TestStdioRefusesUnknownProtocolVersion(t *testing.T) {
	_, err := Dial(context.Background(), fakeConfig(t, "badversion"))
	if KindOf(err) != KindHandshake {
		t.Fatalf("kind = %q, err = %v", KindOf(err), err)
	}
}

func TestStdioMissingCommand(t *testing.T) {
	cfg := fakeConfig(t, "normal")
	cfg.Command = "point-no-such-command-for-test"
	_, err := Dial(context.Background(), cfg)
	if KindOf(err) != KindSpawn {
		t.Fatalf("kind = %q, err = %v", KindOf(err), err)
	}
}

func TestStdioTimeoutKeepsConnectionUsable(t *testing.T) {
	client := dialFake(t, "normal", func(cfg *Config) { cfg.CallTimeout = 300 * time.Millisecond })
	_, err := client.CallTool(context.Background(), "slow", nil)
	if KindOf(err) != KindTimeout {
		t.Fatalf("kind = %q, err = %v", KindOf(err), err)
	}
	// Опоздавший ответ сервера не должен достаться следующему вызову.
	time.Sleep(3 * time.Second)
	result, err := client.CallTool(context.Background(), "echo", json.RawMessage(`{"after":"timeout"}`))
	if err != nil || result.Text() != `{"after":"timeout"}` {
		t.Fatalf("after timeout: %v %+v", err, result)
	}
}

func TestStdioTruncatesOnRuneBoundary(t *testing.T) {
	client := dialFake(t, "normal", func(cfg *Config) { cfg.MaxResultBytes = 1001 })
	result, err := client.CallTool(context.Background(), "big", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Truncated || len(result.Text()) > 1001 || !utf8.ValidString(result.Text()) {
		t.Fatalf("truncated=%v len=%d valid=%v", result.Truncated, len(result.Text()), utf8.ValidString(result.Text()))
	}
}

func TestStdioBannerOnStdoutIsLoggedNotParsed(t *testing.T) {
	client := dialFake(t, "banner", nil)
	if _, err := client.ListTools(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(client.Log(), "[stdout] GitLab MCP server ready") {
		t.Fatalf("banner not logged: %q", client.Log())
	}
}

func TestStdioListChangedMarksStale(t *testing.T) {
	client := dialFake(t, "listchanged", nil)
	if _, err := client.ListTools(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for !client.Stale() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !client.Stale() {
		t.Fatal("tools/list_changed did not mark the list stale")
	}
}

func TestStdioServerDeathFailsPendingCall(t *testing.T) {
	client := dialFake(t, "normal", nil)
	_, err := client.CallTool(context.Background(), "die", nil)
	if KindOf(err) != KindExited {
		t.Fatalf("kind = %q, err = %v", KindOf(err), err)
	}
	select {
	case <-client.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Done not closed after exit")
	}
}

func TestStdioCloseStopsProcess(t *testing.T) {
	cfg := fakeConfig(t, "normal")
	client, err := Dial(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-client.Done():
	case <-time.After(stopGrace + time.Second):
		t.Fatal("process outlived Close")
	}
}

func TestChildEnvironmentKeepsOnlyAllowedAndServerValues(t *testing.T) {
	env := ChildEnvironment([]string{
		"PATH=/usr/bin", "HOME=/home/u", "POINT_API_TOKEN=abc", "DATA_DIR=/data", "VSCODE_PID=1", "https_proxy=http://proxy:3128",
	}, map[string]string{"GITLAB_API_URL": "https://gitlab.local/api/v4", "path": "/opt/node/bin"})
	joined := strings.Join(env, "\n")
	for _, banned := range []string{"POINT_API_TOKEN", "DATA_DIR", "VSCODE_PID"} {
		if strings.Contains(joined, banned) {
			t.Fatalf("%s leaked: %v", banned, env)
		}
	}
	for _, want := range []string{"HOME=/home/u", "https_proxy=http://proxy:3128", "GITLAB_API_URL=https://gitlab.local/api/v4", "path=/opt/node/bin"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %s: %v", want, env)
		}
	}
	if strings.Contains(joined, "PATH=/usr/bin") {
		t.Fatalf("server PATH did not override inherited: %v", env)
	}
}
