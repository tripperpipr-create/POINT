package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"local-agent-workbench/internal/app"
	"local-agent-workbench/internal/events"
)

func TestMasterDevelopmentAPIWithoutProject(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := app.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	server := httptest.NewServer(New(application, events.NewHub(), nil, "", "").Handler())
	defer server.Close()
	response, err := http.Get(server.URL + "/api/master/skills")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var view app.MasterDevelopment
	if err = json.NewDecoder(response.Body).Decode(&view); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || len(view.Skills) != 7 || !view.Config.Enabled {
		t.Fatalf("status=%d skills=%d", response.StatusCode, len(view.Skills))
	}
	response, err = http.Post(server.URL+"/api/master/learning", "application/json", strings.NewReader(`{"enabled":false}`))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if err = json.NewDecoder(response.Body).Decode(&view); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || view.Config.Enabled {
		t.Fatal("toggle not applied")
	}
	response, err = http.Post(server.URL+"/api/master/skills/missing/rollback", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode == 200 {
		t.Fatal("unknown rollback accepted")
	}
}
