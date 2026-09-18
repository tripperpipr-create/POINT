package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"local-agent-workbench/internal/app"
	"local-agent-workbench/internal/connections"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/events"
)

func TestHealthBootstrapAndWorkspaceBoundary(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0600)
	application, err := app.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	if err = application.SetWorkspaceBoundary(root); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(application, events.NewHub(), nil, "", "").Handler())
	defer server.Close()
	response, err := http.Get(server.URL + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 {
		t.Fatalf("health status %d", response.StatusCode)
	}
	_ = response.Body.Close()
	response, err = http.Get(server.URL + "/api/system/diagnostics")
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("system diagnostics status %d", response.StatusCode)
	}
	var systemHealth struct {
		Status string `json:"status"`
		Checks []struct {
			Code string `json:"code"`
		} `json:"checks"`
	}
	if err = json.NewDecoder(response.Body).Decode(&systemHealth); err != nil {
		_ = response.Body.Close()
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if systemHealth.Status != "DEGRADED" || len(systemHealth.Checks) != 9 {
		t.Fatalf("system health=%#v", systemHealth)
	}
	response, err = http.Post(server.URL+"/api/system/backups", "application/json", strings.NewReader(`{"reason":"manual"}`))
	if err != nil {
		t.Fatal(err)
	}
	backupBody, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("backup response: status=%d err=%v body=%s", response.StatusCode, readErr, backupBody)
	}
	if !strings.Contains(string(backupBody), `"integrity":"ok"`) || !strings.Contains(string(backupBody), `"sha256":"`) || strings.Contains(string(backupBody), `"path"`) {
		t.Fatalf("backup response must contain verification without local path: %s", backupBody)
	}
	response, err = http.Get(server.URL + "/api/system/backups")
	if err != nil {
		t.Fatal(err)
	}
	backupListBody, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil || response.StatusCode != http.StatusOK || !strings.Contains(string(backupListBody), `"integrity":"ok"`) || strings.Contains(string(backupListBody), `"path"`) {
		t.Fatalf("backup list must contain only verified metadata: status=%d err=%v body=%s", response.StatusCode, readErr, backupListBody)
	}
	payload, _ := json.Marshal(map[string]string{"path": root})
	response, err = http.Post(server.URL+"/api/workspaces/open", "application/json", strings.NewReader(string(payload)))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 {
		t.Fatalf("open status %d", response.StatusCode)
	}
	_ = response.Body.Close()
	payload, _ = json.Marshal(map[string]string{"path": "main.go", "content": "package edited\n"})
	request, err := http.NewRequest(http.MethodPut, server.URL+"/api/files", strings.NewReader(string(payload)))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err = http.DefaultClient.Do(request)
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("save response: %v, status %v", err, response.StatusCode)
	}
	_ = response.Body.Close()
	payload, _ = json.Marshal(map[string]any{
		"tool":      map[string]any{"kind": "process", "displayName": "Preview", "description": "Preview fixed argv", "program": "go", "arguments": []string{"version"}, "cwd": ".", "timeoutSeconds": 30},
		"arguments": map[string]any{"reason": "API dry run"},
	})
	response, err = http.Post(server.URL+"/api/custom-tools/preview", "application/json", strings.NewReader(string(payload)))
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("custom tool preview response: %v, status %v", err, response.StatusCode)
	}
	var toolPreview struct {
		Program   string   `json:"program"`
		Arguments []string `json:"arguments"`
	}
	if err = json.NewDecoder(response.Body).Decode(&toolPreview); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if toolPreview.Program != "go" || len(toolPreview.Arguments) != 1 || toolPreview.Arguments[0] != "version" {
		t.Fatalf("custom tool preview=%#v", toolPreview)
	}
	payload, _ = json.Marshal(map[string]any{"profileId": "default", "task": "Inspect API", "contextItems": []map[string]any{{"kind": "workspace_file", "path": "main.go"}}})
	response, err = http.Post(server.URL+"/api/runs/preview", "application/json", strings.NewReader(string(payload)))
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("run preflight response: %v, status %v", err, response.StatusCode)
	}
	var runPreview struct {
		Fingerprint string `json:"fingerprint"`
		Tools       []any  `json:"tools"`
		Tokens      struct {
			Total int `json:"total"`
		} `json:"tokens"`
	}
	if err = json.NewDecoder(response.Body).Decode(&runPreview); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if !strings.HasPrefix(runPreview.Fingerprint, "sha256:") || len(runPreview.Tools) != 8 || runPreview.Tokens.Total <= 0 {
		t.Fatalf("run preflight=%#v", runPreview)
	}
	command := "printf api-terminal-ok"
	if runtime.GOOS == "windows" {
		command = "echo api-terminal-ok"
	}
	payload, _ = json.Marshal(map[string]any{"command": command, "timeoutSeconds": 10})
	response, err = http.Post(server.URL+"/api/terminal", "application/json", strings.NewReader(string(payload)))
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("terminal response: %v, status %v", err, response.StatusCode)
	}
	_ = response.Body.Close()
	payload, _ = json.Marshal(map[string]string{"path": filepath.Dir(root)})
	response, err = http.Post(server.URL+"/api/workspaces/open", "application/json", strings.NewReader(string(payload)))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 400 {
		t.Fatalf("outside boundary status %d", response.StatusCode)
	}
	_ = response.Body.Close()
	response, err = http.Get(server.URL + "/api/bootstrap")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var boot map[string]any
	if err = json.NewDecoder(response.Body).Decode(&boot); err != nil {
		t.Fatal(err)
	}
	if boot["currentWorkspace"] == nil {
		t.Fatal("bootstrap did not include the open workspace")
	}
	if templates, ok := boot["profileTemplates"].([]any); !ok || len(templates) < 4 {
		t.Fatalf("bootstrap profile templates=%#v", boot["profileTemplates"])
	}
	if catalog, ok := boot["toolCatalog"].([]any); !ok || len(catalog) < 19 || !catalogHasTool(catalog, "ssh_read_remote") {
		t.Fatalf("bootstrap tool catalog=%#v", boot["toolCatalog"])
	}
}

func catalogHasTool(catalog []any, name string) bool {
	for _, raw := range catalog {
		item, ok := raw.(map[string]any)
		if ok && item["name"] == name {
			return true
		}
	}
	return false
}

func TestManualCustomToolExecutionRequiresExactOneTimeHTTPApproval(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := app.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	if _, err = application.OpenWorkspace(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(application, events.NewHub(), nil, "", "").Handler())
	defer server.Close()

	postJSON := func(route string, payload any, want int, target any) {
		t.Helper()
		encoded, encodeErr := json.Marshal(payload)
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		response, postErr := http.Post(server.URL+route, "application/json", strings.NewReader(string(encoded)))
		if postErr != nil {
			t.Fatal(postErr)
		}
		defer response.Body.Close()
		if response.StatusCode != want {
			body, _ := io.ReadAll(response.Body)
			t.Fatalf("POST %s status=%d body=%s", route, response.StatusCode, body)
		}
		if target != nil {
			if decodeErr := json.NewDecoder(response.Body).Decode(target); decodeErr != nil {
				t.Fatal(decodeErr)
			}
		}
	}

	var tool domain.CustomTool
	postJSON("/api/custom-tools", map[string]any{
		"kind": "process", "displayName": "Go version", "description": "HTTP approval fixture",
		"program": "go", "arguments": []string{"version"}, "cwd": ".", "timeoutSeconds": 30,
	}, http.StatusOK, &tool)
	arguments := map[string]any{"reason": "review exact HTTP invocation"}
	postJSON("/api/tools/execute", map[string]any{
		"toolName": tool.ID, "arguments": arguments, "mode": "execute_readonly", "approved": true,
	}, http.StatusBadRequest, nil)

	var requested app.ToolExecutionApprovalPreview
	postJSON("/api/tools/execution-approvals", map[string]any{
		"toolName": tool.ID, "arguments": arguments,
	}, http.StatusOK, &requested)
	if requested.Approval.ID == "" || requested.Approval.Status != domain.ToolExecutionApprovalPending {
		t.Fatalf("requested approval=%#v", requested.Approval)
	}
	var resolved domain.ToolExecutionApproval
	postJSON("/api/tools/execution-approvals/"+requested.Approval.ID+"/resolve", map[string]any{
		"allow": true,
	}, http.StatusOK, &resolved)
	if resolved.Status != domain.ToolExecutionApprovalAllowed {
		t.Fatalf("resolved approval status=%q", resolved.Status)
	}

	postJSON("/api/tools/execute", map[string]any{
		"toolName": tool.ID, "arguments": map[string]any{"reason": "substituted"},
		"mode": "execute_readonly", "approvalId": resolved.ID,
	}, http.StatusBadRequest, nil)
	var result domain.ToolResult
	postJSON("/api/tools/execute", map[string]any{
		"toolName": tool.ID, "arguments": arguments, "mode": "execute_readonly", "approvalId": resolved.ID,
	}, http.StatusOK, &result)
	if !result.OK {
		t.Fatalf("approved execution result=%#v", result)
	}
	postJSON("/api/tools/execute", map[string]any{
		"toolName": tool.ID, "arguments": arguments, "mode": "execute_readonly", "approvalId": resolved.ID,
	}, http.StatusBadRequest, nil)
}

func TestDatabaseAndSSHConnectionAPIs(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	workspaceRoot := t.TempDir()
	application, err := app.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	if _, err = application.OpenWorkspace(workspaceRoot); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(application, events.NewHub(), nil, "", "").Handler())
	defer server.Close()

	postJSON := func(route, payload string, want int, target any) {
		t.Helper()
		response, postErr := http.Post(server.URL+route, "application/json", strings.NewReader(payload))
		if postErr != nil {
			t.Fatal(postErr)
		}
		defer response.Body.Close()
		if response.StatusCode != want {
			body, _ := io.ReadAll(response.Body)
			t.Fatalf("POST %s status=%d body=%s", route, response.StatusCode, body)
		}
		if target != nil {
			if decodeErr := json.NewDecoder(response.Body).Decode(target); decodeErr != nil {
				t.Fatal(decodeErr)
			}
		}
	}

	var savedDB struct {
		ID       string `json:"id"`
		Driver   string `json:"driver"`
		Database string `json:"database"`
	}
	postJSON("/api/db-connections", `{"displayName":"fixture-db","driver":"sqlite","database":"data/fixture.db","readOnlyDefault":true}`, http.StatusOK, &savedDB)
	if savedDB.ID == "" || savedDB.Driver != "sqlite" || savedDB.Database != "data/fixture.db" {
		t.Fatalf("saved database=%#v", savedDB)
	}
	queryRoute := "/api/db-connections/" + savedDB.ID + "/query"
	postJSON(queryRoute, `{"sql":"CREATE TABLE items(id INTEGER PRIMARY KEY, name TEXT)","allowWrite":true,"approved":true}`, http.StatusOK, nil)
	postJSON(queryRoute, `{"sql":"INSERT INTO items(name) VALUES ('Point')","allowWrite":true,"approved":true}`, http.StatusOK, nil)
	postJSON(queryRoute, `{"sql":"DELETE FROM items","allowWrite":true,"approved":false}`, http.StatusBadRequest, nil)
	var selected struct {
		Kind     string   `json:"kind"`
		Columns  []string `json:"columns"`
		Rows     [][]any  `json:"rows"`
		RowCount int      `json:"rowCount"`
	}
	postJSON(queryRoute, `{"sql":"SELECT id, name FROM items","maxRows":10}`, http.StatusOK, &selected)
	if selected.Kind != "read" || selected.RowCount != 1 || len(selected.Columns) != 2 || len(selected.Rows) != 1 {
		t.Fatalf("database query=%#v", selected)
	}
	var schema struct {
		Driver string `json:"driver"`
		Tables []struct {
			Name    string   `json:"name"`
			Columns []string `json:"columns"`
		} `json:"tables"`
	}
	postJSON("/api/db-connections/"+savedDB.ID+"/schema", `{}`, http.StatusOK, &schema)
	if schema.Driver != "sqlite" || len(schema.Tables) != 1 || schema.Tables[0].Name != "items" || len(schema.Tables[0].Columns) < 2 {
		t.Fatalf("database schema=%#v", schema)
	}
	postJSON("/api/db-connections/"+savedDB.ID+"/test", `{}`, http.StatusOK, nil)

	var savedSSH struct {
		ID         string `json:"id"`
		Host       string `json:"host"`
		User       string `json:"user"`
		Port       int    `json:"port"`
		AuthMethod string `json:"authMethod"`
	}
	postJSON("/api/servers", `{"displayName":"prod-api","host":"prod.example","port":2222,"user":"deploy","authMethod":"agent","defaultRemotePath":"~"}`, http.StatusOK, &savedSSH)
	if savedSSH.ID == "" || savedSSH.Host != "prod.example" || savedSSH.User != "deploy" || savedSSH.Port != 2222 || savedSSH.AuthMethod != "agent" {
		t.Fatalf("saved SSH profile=%#v", savedSSH)
	}
	// The preview route must be wired and reject an empty file path before any
	// network operation. A missing handler would return 404, while accidentally
	// falling through to the profile root could contact the saved host.
	postJSON("/api/servers/"+savedSSH.ID+"/read", `{"path":""}`, http.StatusBadRequest, nil)
	postJSON("/api/servers", `{"host":"bad host","user":"deploy"}`, http.StatusBadRequest, nil)
	response, err := http.Get(server.URL + "/api/servers")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var profiles []map[string]any
	if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&profiles) != nil || len(profiles) != 1 {
		t.Fatalf("SSH profiles status=%d profiles=%#v", response.StatusCode, profiles)
	}
}

func TestAPITokenRequired(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := app.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	const token = "test-api-token-value-32chars!!!!!!"
	server := httptest.NewServer(New(application, events.NewHub(), nil, "", token).Handler())
	defer server.Close()

	response, err := http.Get(server.URL + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 {
		t.Fatalf("health without token status %d", response.StatusCode)
	}
	_ = response.Body.Close()

	response, err = http.Get(server.URL + "/api/bootstrap")
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bootstrap without token status %d", response.StatusCode)
	}
	_ = response.Body.Close()

	request, err := http.NewRequest(http.MethodGet, server.URL+"/api/bootstrap", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 {
		t.Fatalf("bootstrap with token status %d", response.StatusCode)
	}
	_ = response.Body.Close()
}

func TestProjectBudgetAPI(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := app.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	workspace, err := application.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(application, events.NewHub(), nil, "", "").Handler())
	defer server.Close()

	payload := `{"dailyCents":125,"monthlyCents":2500,"hardStop":true}`
	response, err := http.Post(server.URL+"/api/budget", "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("save budget status %d", response.StatusCode)
	}
	var saved app.HubBudgetSettings
	if err = json.NewDecoder(response.Body).Decode(&saved); err != nil {
		t.Fatal(err)
	}
	if saved.WorkspaceID != workspace.Workspace.ID || saved.DailyCents != 125 || !saved.HardStop {
		t.Fatalf("saved budget=%#v", saved)
	}

	response, err = http.Get(server.URL + "/api/statistics")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var statistics map[string]any
	if err = json.NewDecoder(response.Body).Decode(&statistics); err != nil {
		t.Fatal(err)
	}
	if statistics["budgetDailyCents"] != float64(125) || statistics["budgetMonthlyCents"] != float64(2500) || statistics["budgetHardStop"] != true {
		t.Fatalf("statistics budget=%#v", statistics)
	}
}

// Неверный токен обязан отвергаться.
//
// Соседний тест проверяет только наличие: без токена — 401, с верным — 200.
// Само сравнение при этом не проверялось: замена tokenMatches на «возвращать
// true» оставляла весь пакет зелёным, то есть подошёл бы любой токен. Это самое
// дорогое сравнение в продукте — им закрыты все 110 маршрутов ядра.
func TestAPIRejectsWrongToken(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := app.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	const token = "test-api-token-value-32chars!!!!!!"
	server := httptest.NewServer(New(application, events.NewHub(), nil, "", token).Handler())
	defer server.Close()

	get := func(t *testing.T, header, query string) int {
		t.Helper()
		url := server.URL + "/api/bootstrap"
		if query != "" {
			url += "?access_token=" + query
		}
		request, requestErr := http.NewRequest(http.MethodGet, url, nil)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		if header != "" {
			request.Header.Set("Authorization", header)
		}
		response, doErr := http.DefaultClient.Do(request)
		if doErr != nil {
			t.Fatal(doErr)
		}
		_ = response.Body.Close()
		return response.StatusCode
	}

	// Защита от холостого хода: верный токен обязан проходить, иначе отказы ниже
	// доказывали бы лишь то, что закрыто вообще всё.
	if code := get(t, "Bearer "+token, ""); code != http.StatusOK {
		t.Fatalf("верный токен не прошёл: %d", code)
	}

	cases := map[string]struct{ header, query string }{
		// Ровно той же длины — иначе отказ придёт от проверки длины, а само
		// сравнение так и останется непроверенным.
		"чужой токен той же длины":     {header: "Bearer test-api-token-value-32chars!!!!!?"},
		"чужой токен другой длины":     {header: "Bearer короткий"},
		"верный токен с опечаткой":     {header: "Bearer " + token + "x"},
		"чужой токен в строке запроса": {query: "чужой-токен"},
	}
	for name, item := range cases {
		if code := get(t, item.header, item.query); code != http.StatusUnauthorized {
			t.Errorf("%s: ожидался 401, получен %d", name, code)
		}
	}

	// Токен в строке запроса — отдельный путь разбора, он тоже обязан работать.
	if code := get(t, "", token); code != http.StatusOK {
		t.Errorf("верный токен в строке запроса не прошёл: %d", code)
	}
}

// Ответы ядра не несут секретов.
//
// Хаб обещает: «Токен берётся из SecretStorage и никогда не возвращается в
// интерфейс». Сегодня это верно даже с запасом — ядро секретов не хранит вовсе:
// connections.Manager создаётся без SecretStore, а ключ приходит с каждым
// запуском от расширения. В ответах остаётся только ссылка (secretRef).
//
// Обещание держится на форме данных, и сломать её легко: достаточно добавить в
// любую структуру поле apiKey «для удобства». Проверяем не строку-значение,
// которой сейчас нет, а именно форму — ни одно поле ответа не должно называться
// как носитель секрета.
func TestBootstrapCarriesNoSecretFields(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := app.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	if _, err = application.SaveConnection(connections.UpsertRequest{
		Provider: domain.ProviderOpenAI, PresetID: "openai", DisplayName: "OpenAI",
		BaseURL: "https://api.openai.com/v1", SecretRef: "point.connection.openai",
	}); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(New(application, events.NewHub(), nil, "", "").Handler())
	defer server.Close()
	response, err := http.Get(server.URL + "/api/bootstrap")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}

	// Защита от холостого хода: связь обязана попасть в ответ, иначе проверка
	// пройдёт на пустом месте.
	if !strings.Contains(string(body), "point.connection.openai") {
		t.Fatalf("сохранённая связь не попала в bootstrap — проверка прошла бы вхолостую")
	}

	forbidden := regexp.MustCompile(`"(apiKey|api_key|secret|token|password|credential)"\s*:`)
	if hit := forbidden.FindString(string(body)); hit != "" {
		t.Errorf("в ответе ядра появилось поле-носитель секрета: %s", hit)
	}
}

// Носитель секрета не должен даже объявляться в типах, которые уходят в
// интерфейс.
//
// Проверка ответа выше ловит утечку значения, но поле с `omitempty` в пустом
// виде до ответа не доходит — а появится оно ровно тогда, когда кто-то решит
// «положить сюда ключ для удобства». Поэтому смотрим и на сами объявления.
func TestUIFacingTypesDeclareNoSecretFields(t *testing.T) {
	forbidden := regexp.MustCompile(`json:"(apiKey|api_key|secret|token|password|credential)[",]`)
	for _, file := range []string{"../domain/hub.go", "../domain/types.go"} {
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		// Защита от холостого хода: файл обязан содержать сами структуры связи и
		// профиля, иначе проверка смотрит не туда.
		if !strings.Contains(string(source), "json:\"secretRef,omitempty\"") &&
			!strings.Contains(string(source), "type AgentProfile struct") {
			continue
		}
		for index, line := range strings.Split(string(source), "\n") {
			if hit := forbidden.FindString(line); hit != "" {
				t.Errorf("%s:%d объявляет поле-носитель секрета: %s", file, index+1, strings.TrimSpace(line))
			}
		}
	}
}

// Обработчик чата компаньона обязан передавать контекст запроса.
//
// Кнопка ⏹ работает так: расширение обрывает HTTP-запрос, ядро видит закрытое
// соединение и отменяет контекст, отмена доходит до model.Stream — генерация
// прекращается, расход останавливается. Поведение второй половины цепочки
// (служба → модель) проверено в internal/companion; здесь остаётся первая, и
// сломать её проще всего одной правкой «чтобы отмена не мешала»:
// r.Context() → context.Background().
//
// Проверка формы, а не поведения: поднять настоящую отмену через httptest со
// своей моделью нельзя — App собирает companion.Service сам.
func TestCompanionChatHandlerPassesRequestContext(t *testing.T) {
	// Файл ищется по всему пакету, а не по имени: обработчики переезжают между
	// файлами, и проверка, привязанная к server.go, однажды молча перестанет
	// что-либо находить.
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	text, at := "", -1
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		source, readErr := os.ReadFile(entry.Name())
		if readErr != nil {
			t.Fatal(readErr)
		}
		if index := strings.Index(string(source), "func (s *Server) companionChat("); index >= 0 {
			text, at = string(source), index
			break
		}
	}
	if at < 0 {
		t.Fatal("обработчик companionChat не найден — проверка прошла бы вхолостую")
	}
	body := text[at:]
	if end := strings.Index(body, "\nfunc "); end > 0 {
		body = body[:end]
	}
	if !strings.Contains(body, "s.app.CompanionChat(r.Context()") {
		t.Errorf("companionChat больше не передаёт контекст запроса: остановка перестанет прекращать генерацию")
	}
	if strings.Contains(body, "context.Background()") || strings.Contains(body, "context.TODO()") {
		t.Errorf("в обработчике companionChat появился контекст, не связанный с запросом")
	}
}
