package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

func TestResumeStructuredExecutionRequiresResumableCheckpoint(t *testing.T) {
	a, ws := outcomeWorld(t)
	ctx := context.Background()
	now := time.Now().UTC()
	run := domain.Run{
		ID: "run-resume", WorkspaceID: ws.ID, ProfileID: "p", Task: "paused work",
		Status: domain.RunPaused, StartedAt: now,
		ConfigurationSnapshot: domain.NewRunConfigurationSnapshot("test", domain.DefaultProfile(), nil, now),
	}
	if err := a.store.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	exec := domain.ExecutionInstance{
		ID: "exec-resume", WorkspaceID: ws.ID, ProjectAgentID: "agent", RunID: run.ID,
		Task: "paused work", Status: domain.RunPaused, StartedAt: now,
		Snapshot: run.ConfigurationSnapshot,
	}
	if err := a.store.SaveExecution(ctx, exec); err != nil {
		t.Fatal(err)
	}
	_, err := a.resumeStructuredExecutionIfSafe(&exec, "")
	if err == nil || !strings.Contains(err.Error(), "unknown_outcome") {
		t.Fatalf("expected unknown_outcome without checkpoint, got %v", err)
	}
	if err := a.store.SaveRunCheckpoint(ctx, domain.RunCheckpoint{
		RunID: run.ID, Seq: 1, ExecutionID: exec.ID, SandboxPath: ws.Path,
		CreatedAt: now, HistoryJSON: []byte("[]"),
	}); err != nil {
		t.Fatal(err)
	}
	_, err = a.resumeStructuredExecutionIfSafe(&exec, "")
	if err != nil && strings.Contains(err.Error(), "повтор задания с начала запрещён") {
		t.Fatalf("should attempt resume from verified checkpoint, got %v", err)
	}
}

func TestResumeStructuredExecutionRejectsInFlightCall(t *testing.T) {
	a, ws := outcomeWorld(t)
	ctx := context.Background()
	now := time.Now().UTC()
	run := domain.Run{
		ID: "run-inflight", WorkspaceID: ws.ID, ProfileID: "p", Task: "mid tool",
		Status: domain.RunInterrupted, StartedAt: now,
		ConfigurationSnapshot: domain.NewRunConfigurationSnapshot("test", domain.DefaultProfile(), nil, now),
	}
	if err := a.store.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	exec := domain.ExecutionInstance{
		ID: "exec-inflight", WorkspaceID: ws.ID, ProjectAgentID: "agent", RunID: run.ID,
		Task: "mid tool", Status: domain.RunInterrupted, StartedAt: now, Snapshot: run.ConfigurationSnapshot,
	}
	if err := a.store.SaveExecution(ctx, exec); err != nil {
		t.Fatal(err)
	}
	if err := a.store.SaveRunCheckpoint(ctx, domain.RunCheckpoint{
		RunID: run.ID, Seq: 1, ExecutionID: exec.ID, InFlightCallID: "tool-1", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	_, err := a.resumeStructuredExecutionIfSafe(&exec, "")
	if err == nil || !strings.Contains(err.Error(), "unknown_outcome") {
		t.Fatalf("expected mid-action refusal, got %v", err)
	}
}
