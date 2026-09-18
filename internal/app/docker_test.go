package app

import (
	"context"
	"encoding/json"
	"testing"

	"local-agent-workbench/internal/tools"
)

func TestDockerOverviewWithoutCLI(t *testing.T) {
	application := newTestApp(t)
	var err error
	if _, err = application.OpenWorkspace(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	overview, err := application.DockerOverview()
	if err != nil {
		t.Fatal(err)
	}
	if overview.Status == nil {
		t.Fatal("expected status map")
	}
	if overview.Containers == nil || overview.Images == nil {
		t.Fatal("expected non-nil slices")
	}
}

func TestDockerContainerActionRejectsRemove(t *testing.T) {
	application := newTestApp(t)
	var err error
	if _, err = application.OpenWorkspace(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	_, err = application.DockerContainerAction(DockerContainerActionRequest{Action: "rm", Container: "web"})
	if err == nil {
		t.Fatal("rm must be rejected")
	}
}

func TestDockerRefValidationShared(t *testing.T) {
	if err := tools.ValidateDockerRef("good_name.1"); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"action": "start", "container": "x;y", "reason": "no"})
	result := (tools.DockerControl{}).Execute(context.Background(), raw)
	if result.OK {
		t.Fatal("invalid ref must fail")
	}
}
