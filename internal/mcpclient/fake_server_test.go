package mcpclient

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// Поддельный stdio-сервер — этот же тестовый бинарь, запущенный с переменной
// MCP_FAKE_MODE. Так тест проверяет настоящий процесс, настоящие трубы и
// настоящий выход, а не заглушку транспорта.
func TestMain(m *testing.M) {
	if mode := os.Getenv("MCP_FAKE_MODE"); mode != "" {
		runFakeServer(mode)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func fakeConfig(t *testing.T, mode string) Config {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Config{
		Name:         "fake",
		Transport:    TransportStdio,
		Command:      self,
		Args:         []string{"-test.run=^$"},
		Env:          append(os.Environ(), "MCP_FAKE_MODE="+mode),
		Secrets:      []string{"SUPERSECRETVALUE"},
		StartTimeout: 10 * time.Second,
		CallTimeout:  5 * time.Second,
	}
}

func runFakeServer(mode string) {
	out := bufio.NewWriter(os.Stdout)
	send := func(v any) {
		raw, _ := json.Marshal(v)
		out.Write(raw)
		out.WriteByte('\n')
		out.Flush()
	}
	fmt.Fprintln(os.Stderr, "starting with glpat-abcdefghijklmnopqrstuv and secret=SUPERSECRETVALUE")
	if mode == "crash" {
		fmt.Fprintln(os.Stderr, "boom: cannot reach GitLab")
		os.Exit(3)
	}
	if mode == "banner" {
		fmt.Fprintln(os.Stdout, "GitLab MCP server ready")
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1<<20), 16<<20)
	var pendingAsk json.RawMessage
	replies := map[string]json.RawMessage{}
	for scanner.Scan() {
		var msg map[string]json.RawMessage
		if json.Unmarshal(scanner.Bytes(), &msg) != nil {
			continue
		}
		var method string
		_ = json.Unmarshal(msg["method"], &method)
		id := msg["id"]
		if method == "" && id != nil {
			// Ответ клиента на запрос сервера.
			var key string
			_ = json.Unmarshal(id, &key)
			if msg["error"] != nil {
				replies[key] = msg["error"]
			} else {
				replies[key] = msg["result"]
			}
			if pendingAsk != nil && replies["s1"] != nil && replies["s2"] != nil {
				var refusal struct {
					Code int `json:"code"`
				}
				_ = json.Unmarshal(replies["s2"], &refusal)
				send(map[string]any{"jsonrpc": "2.0", "id": pendingAsk, "result": map[string]any{
					"content": []map[string]any{{"type": "text", "text": fmt.Sprintf("ping:%s sampling:%d", replies["s1"], refusal.Code)}},
				}})
				pendingAsk = nil
			}
			continue
		}
		switch method {
		case "initialize":
			version := "2025-06-18"
			if mode == "badversion" {
				version = "1999-01-01"
			}
			send(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{
				"protocolVersion": version,
				"capabilities":    map[string]any{"tools": map[string]any{"listChanged": true}},
				"serverInfo":      map[string]any{"name": "fake", "version": "1.0"},
			}})
		case "tools/list":
			var params struct {
				Cursor string `json:"cursor"`
			}
			_ = json.Unmarshal(msg["params"], &params)
			if params.Cursor == "" {
				send(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{
					"tools":      []map[string]any{{"name": "echo", "description": "Echo", "inputSchema": map[string]any{"type": "object"}, "annotations": map[string]any{"readOnlyHint": true}}},
					"nextCursor": "page-2",
				}})
			} else {
				send(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{
					"tools": []map[string]any{{"name": "merge", "inputSchema": map[string]any{"type": "object"}, "annotations": map[string]any{"destructiveHint": true}}},
				}})
				if mode == "listchanged" {
					send(map[string]any{"jsonrpc": "2.0", "method": "notifications/tools/list_changed"})
				}
			}
		case "tools/call":
			var params struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			_ = json.Unmarshal(msg["params"], &params)
			text := func(value string) map[string]any {
				return map[string]any{"content": []map[string]any{{"type": "text", "text": value}}}
			}
			switch params.Name {
			case "echo":
				send(map[string]any{"jsonrpc": "2.0", "id": id, "result": text(string(params.Arguments))})
			case "ask":
				pendingAsk = id
				send(map[string]any{"jsonrpc": "2.0", "id": "s1", "method": "ping"})
				send(map[string]any{"jsonrpc": "2.0", "id": "s2", "method": "sampling/createMessage", "params": map[string]any{}})
			case "big":
				send(map[string]any{"jsonrpc": "2.0", "id": id, "result": text(strings.Repeat("я", 3000))})
			case "slow":
				time.Sleep(3 * time.Second)
				send(map[string]any{"jsonrpc": "2.0", "id": id, "result": text("late")})
			case "fail":
				send(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"isError": true, "content": []map[string]any{{"type": "text", "text": "401 for glpat-abcdefghijklmnopqrstuv"}}}})
			case "die":
				os.Exit(7)
			default:
				send(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32602, "message": "unknown tool " + params.Name}})
			}
		}
	}
}
