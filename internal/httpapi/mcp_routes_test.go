package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"local-agent-workbench/internal/app"
	"local-agent-workbench/internal/events"
)

// Маршруты MCP разбирают тело строго, кроме самого mcp.json: он произвольный
// JSON и приходит в поле config.
func TestMCPRoutesImportListAndUnlock(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := app.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	server := httptest.NewServer(New(application, events.NewHub(), nil, "", "").Handler())
	defer server.Close()

	post := func(route, payload string) (int, string) {
		t.Helper()
		response, postErr := http.Post(server.URL+route, "application/json", strings.NewReader(payload))
		if postErr != nil {
			t.Fatal(postErr)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		return response.StatusCode, string(body)
	}

	status, body := post("/api/mcp/import/preview", `{"config":{"mcpServers":{"gitlab":{"command":"npx","args":["-y","@zereight/mcp-gitlab@2.1.66"],"env":{"GITLAB_PERSONAL_ACCESS_TOKEN":"glpat-abcdefghijklmnopqrstuv","CUSTOM_FLAG":"on"}}}}}`)
	if status != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", status, body)
	}
	var preview app.MCPImportPreview
	if err = json.Unmarshal([]byte(body), &preview); err != nil || len(preview.Candidates) != 1 || preview.Candidates[0].Server.Env["CUSTOM_FLAG"] != "on" {
		t.Fatalf("preview = %s", body)
	}

	status, body = post("/api/mcp/servers", `{"displayName":"remote","transport":"http","url":"https://mcp.example.com/mcp","secretHeaders":["Authorization"]}`)
	if status != http.StatusOK || !strings.Contains(body, `"secretsLocked":["Authorization"]`) {
		t.Fatalf("save status=%d body=%s", status, body)
	}
	response, err := http.Get(server.URL + "/api/mcp/servers")
	if err != nil {
		t.Fatal(err)
	}
	listed, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if !strings.Contains(string(listed), `"displayName":"remote"`) {
		t.Fatalf("list = %s", listed)
	}

	if status, body = post("/api/mcp/secrets/unlock", `{"values":{"point.db.other":"x"}}`); status == http.StatusOK {
		t.Fatalf("foreign secret ref accepted: %s", body)
	}
	if status, _ = post("/api/mcp/servers", `{"transport":"stdio","command":"npx","unknownField":1}`); status != http.StatusBadRequest {
		t.Fatalf("unknown field accepted: %d", status)
	}
}
