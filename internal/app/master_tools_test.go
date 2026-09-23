package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

func TestMasterReadExecutionIsWorkspaceScopedAndIncludesEvents(t *testing.T) {
	application := newTestApp(t)
	view, err := application.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Now().UTC()
	run := domain.Run{
		ID: "run-failed", AgentID: "agent", ProfileID: "agent", WorkspaceID: view.Workspace.ID,
		Task: "Inspect failure", Provider: "openai", Model: "test", Status: domain.RunFailed,
		Error: "provider timed out", StartedAt: now,
	}
	if err = application.store.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	execution := domain.ExecutionInstance{
		ID: "execution-failed", WorkspaceID: view.Workspace.ID, ProjectAgentID: "agent",
		RunID: run.ID, Task: run.Task, Status: domain.RunFailed, Error: run.Error, StartedAt: now,
	}
	if err = application.store.SaveExecution(ctx, execution); err != nil {
		t.Fatal(err)
	}
	if err = application.store.Append(ctx, domain.Event{
		ID: "event-failed", WorkspaceID: view.Workspace.ID, RunID: run.ID,
		Type: domain.EventRunFailed, Actor: "engine", Data: json.RawMessage(`{"error":"provider timed out"}`), CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	tools := newMasterReadTools(nil, application.store, view.Workspace.ID, application.ObserveRoster)
	for _, definition := range tools.Definitions() {
		if definition.Name == masterRosterToolName {
			t.Fatal("Master must delegate roster assembly to agent-selector")
		}
	}
	result := tools.Execute(ctx, "read_execution", json.RawMessage(`{"id":"run-failed"}`))
	if !result.OK {
		t.Fatalf("read_execution failed: %#v", result.Error)
	}
	var summary struct {
		Status string `json:"status"`
		Error  string `json:"error"`
		Events []any  `json:"events"`
	}
	if err = json.Unmarshal(result.Output, &summary); err != nil {
		t.Fatal(err)
	}
	if summary.Status != string(domain.RunFailed) || summary.Error != run.Error || len(summary.Events) != 1 {
		t.Fatalf("execution summary lost failure evidence: %#v", summary)
	}

	foreign := execution
	foreign.ID, foreign.WorkspaceID = "execution-foreign", "other-workspace"
	if err = application.store.SaveExecution(ctx, foreign); err != nil {
		t.Fatal(err)
	}
	if result = tools.Execute(ctx, "read_execution", json.RawMessage(`{"id":"execution-foreign"}`)); result.OK || result.Error == nil || result.Error.Code != "not_found" {
		t.Fatalf("foreign execution escaped workspace boundary: %#v", result)
	}
	if result = tools.Execute(ctx, "run_command", json.RawMessage(`{"command":"whoami"}`)); result.OK || result.Error == nil || result.Error.Code != "tool_not_allowed" {
		t.Fatalf("mutating tool escaped read-only grants: %#v", result)
	}
}
