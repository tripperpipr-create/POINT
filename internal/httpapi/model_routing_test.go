package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"local-agent-workbench/internal/app"
	"local-agent-workbench/internal/connections"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/events"
)

func TestWorkspaceModelRoutingGetAndPut(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := app.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	workspaceView, err := application.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	connection, err := application.SaveConnection(connections.UpsertRequest{
		ID: "route-http", Provider: domain.ProviderOpenAI, PresetID: "openai",
		DisplayName: "Routing", BaseURL: "https://routing.example.invalid/v1",
		Status: domain.ConnectionConnected, DefaultModel: "coding-default",
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(application, events.NewHub(), nil, "", "").Handler())
	defer server.Close()

	request, err := http.NewRequest(http.MethodPut, server.URL+"/api/workspace/model-routing", strings.NewReader(
		`{"codingConnectionId":"`+connection.ID+`","cheapModel":"cheap-test"}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var saved app.WorkspaceModelRoutingView
	if err = json.NewDecoder(response.Body).Decode(&saved); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || saved.WorkspaceID != workspaceView.Workspace.ID || !saved.CodingRequired || !saved.CodingReady {
		t.Fatalf("PUT routing status=%d value=%#v", response.StatusCode, saved)
	}

	response, err = http.Get(server.URL + "/api/workspace/model-routing")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var loaded app.WorkspaceModelRoutingView
	if err = json.NewDecoder(response.Body).Decode(&loaded); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || loaded.CodingConnectionID != connection.ID || loaded.CheapModel != "cheap-test" {
		t.Fatalf("GET routing status=%d value=%#v", response.StatusCode, loaded)
	}
}
