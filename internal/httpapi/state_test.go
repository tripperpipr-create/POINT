package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"local-agent-workbench/internal/app"
	"local-agent-workbench/internal/events"
)

func TestNarrowStateRoutesExcludeBootstrapCatalogs(t *testing.T) {
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

	cases := []struct {
		path     string
		required string
		excluded string
	}{
		{path: "/api/state/runtime", required: "runs", excluded: "connections"},
		{path: "/api/state/guild", required: "projectAgents", excluded: "companionMessages"},
	}
	for _, test := range cases {
		response, err := http.Get(server.URL + test.path)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusOK {
			response.Body.Close()
			t.Fatalf("%s status %d", test.path, response.StatusCode)
		}
		var payload map[string]any
		if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
			response.Body.Close()
			t.Fatal(err)
		}
		response.Body.Close()
		if _, ok := payload[test.required]; !ok {
			t.Fatalf("%s omitted %q", test.path, test.required)
		}
		if _, ok := payload[test.excluded]; ok {
			t.Fatalf("%s leaked bootstrap-only field %q", test.path, test.excluded)
		}
	}
}
