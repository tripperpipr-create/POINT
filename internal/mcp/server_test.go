package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
)

// Исполнитель сущности: ровно то, чем пользуется обычный путь — список
// доступного и вызов с проверкой прав.
type fakeExecutor struct {
	definitions []domain.ToolDefinition
	calls       []string
	refuse      bool
}

func (e *fakeExecutor) Definitions() []domain.ToolDefinition { return e.definitions }

func (e *fakeExecutor) Execute(_ context.Context, name string, _ json.RawMessage) domain.ToolResult {
	e.calls = append(e.calls, name)
	if e.refuse {
		return domain.ToolResult{OK: false, Error: &domain.ToolError{
			Code: "tool_not_allowed", Message: "компаньону доступны только читающие инструменты",
			Hint: "изменения выполняет агент по квесту с подтверждением",
		}}
	}
	return domain.ToolResult{OK: true, Output: json.RawMessage(`{"branches":["master"]}`)}
}

func newTestServer(t *testing.T, executor Executor) (*Registry, *httptest.Server, string) {
	t.Helper()
	registry := NewRegistry()
	key := "session-key-1"
	if err := registry.Open(Session{Key: key, Subject: "companion", Executor: executor}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(registry.Handler())
	t.Cleanup(server.Close)
	return registry, server, key
}

func call(t *testing.T, server *httptest.Server, key, body string) (int, map[string]any) {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, server.URL, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var decoded map[string]any
	_ = json.NewDecoder(response.Body).Decode(&decoded)
	return response.StatusCode, decoded
}

// Сервер отдаёт ровно тот набор, который сущности выдал обычный путь: второго
// списка прав здесь нет и быть не должно.
func TestServerListsOnlyWhatTheSubjectMay(t *testing.T) {
	executor := &fakeExecutor{definitions: []domain.ToolDefinition{
		{Name: "git_log", Description: "история", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "read_file", Description: "чтение", InputSchema: json.RawMessage(`{"type":"object"}`)},
	}}
	_, server, key := newTestServer(t, executor)
	status, body := call(t, server, key, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if status != http.StatusOK {
		t.Fatalf("список отдан со статусом %d", status)
	}
	result, _ := body["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("в списке %d инструментов вместо двух: %#v", len(tools), result)
	}
	first, _ := tools[0].(map[string]any)
	if first["name"] != "git_log" || first["inputSchema"] == nil {
		t.Fatalf("описание инструмента потеряно: %#v", first)
	}
}

// Вызов идёт через исполнителя сущности, а не мимо него.
func TestServerCallsThroughSubjectExecutor(t *testing.T) {
	executor := &fakeExecutor{definitions: []domain.ToolDefinition{{Name: "git_log"}}}
	_, server, key := newTestServer(t, executor)
	_, body := call(t, server, key, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"git_log","arguments":{"limit":5}}}`)
	if len(executor.calls) != 1 || executor.calls[0] != "git_log" {
		t.Fatalf("вызов не дошёл до исполнителя: %#v", executor.calls)
	}
	result, _ := body["result"].(map[string]any)
	content, _ := result["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("ответ инструмента потерян: %#v", result)
	}
	block, _ := content[0].(map[string]any)
	if !strings.Contains(block["text"].(string), "master") {
		t.Fatalf("выдача инструмента не доехала: %#v", block)
	}
}

// Отказ по правам — это ответ с пометкой, а не обвал транспорта: исполнитель
// обязан прочитать причину и объяснить её человеку.
func TestServerReportsRefusalAsAnswer(t *testing.T) {
	executor := &fakeExecutor{definitions: []domain.ToolDefinition{{Name: "propose_patch"}}, refuse: true}
	_, server, key := newTestServer(t, executor)
	status, body := call(t, server, key, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"propose_patch"}}`)
	if status != http.StatusOK {
		t.Fatalf("отказ по правам пришёл статусом %d", status)
	}
	result, _ := body["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("отказ не помечен: %#v", result)
	}
	content, _ := result["content"].([]any)
	block, _ := content[0].(map[string]any)
	if !strings.Contains(block["text"].(string), "читающие инструменты") {
		t.Fatalf("причина отказа не названа: %#v", block)
	}
}

// Без ключа сессии сервер не разговаривает вовсе: имя сущности подделывается,
// ключ выдаёт ядро.
func TestServerRefusesUnknownSession(t *testing.T) {
	_, server, _ := newTestServer(t, &fakeExecutor{})
	if status, _ := call(t, server, "", `{"jsonrpc":"2.0","id":4,"method":"tools/list"}`); status != http.StatusUnauthorized {
		t.Fatalf("без ключа сервер ответил %d", status)
	}
	if status, _ := call(t, server, "чужой-ключ", `{"jsonrpc":"2.0","id":5,"method":"tools/list"}`); status != http.StatusUnauthorized {
		t.Fatalf("с чужим ключом сервер ответил %d", status)
	}
}

// Закрытая сессия перестаёт работать сразу: ключ, переживший работу, — дыра в
// правах.
func TestServerStopsAfterSessionClosed(t *testing.T) {
	registry, server, key := newTestServer(t, &fakeExecutor{definitions: []domain.ToolDefinition{{Name: "git_log"}}})
	if status, _ := call(t, server, key, `{"jsonrpc":"2.0","id":6,"method":"tools/list"}`); status != http.StatusOK {
		t.Fatalf("открытая сессия ответила %d", status)
	}
	registry.Close(key)
	if status, _ := call(t, server, key, `{"jsonrpc":"2.0","id":7,"method":"tools/list"}`); status != http.StatusUnauthorized {
		t.Fatalf("закрытая сессия всё ещё отвечает: %d", status)
	}
	if registry.Count() != 0 {
		t.Fatalf("сессия осталась в реестре: %d", registry.Count())
	}
}

// Приветствие называет версию протокола и имя сервера: по имени исполнитель
// строит имена инструментов, и разъехаться им нельзя.
func TestServerAnnouncesProtocolAndName(t *testing.T) {
	_, server, key := newTestServer(t, &fakeExecutor{})
	_, body := call(t, server, key, `{"jsonrpc":"2.0","id":8,"method":"initialize"}`)
	result, _ := body["result"].(map[string]any)
	if result["protocolVersion"] != ProtocolVersion {
		t.Fatalf("версия протокола не названа: %#v", result)
	}
	info, _ := result["serverInfo"].(map[string]any)
	if info["name"] != ServerName {
		t.Fatalf("имя сервера не названо: %#v", info)
	}
	if got := ToolPattern("git_log"); got != "mcp__point__git_log" {
		t.Fatalf("имя инструмента у исполнителя собрано иначе: %q", got)
	}
}

// Конфиг для исполнителя несёт адрес и ключ — и ничего сверх: ключ уходит в
// чужой процесс, и всё лишнее рядом с ним тоже уйдёт.
func TestClientConfigCarriesAddressAndKey(t *testing.T) {
	config, err := ClientConfig("http://127.0.0.1:8080", "k1")
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		MCPServers map[string]struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(config), &decoded); err != nil {
		t.Fatal(err)
	}
	server, ok := decoded.MCPServers[ServerName]
	if !ok {
		t.Fatalf("сервер Point не описан: %s", config)
	}
	if server.URL != "http://127.0.0.1:8080/mcp" || server.Headers["Authorization"] != "Bearer k1" {
		t.Fatalf("адрес или ключ собраны неверно: %#v", server)
	}
	if _, err := ClientConfig("", "k1"); err == nil {
		t.Fatal("конфиг без адреса принят")
	}
}
