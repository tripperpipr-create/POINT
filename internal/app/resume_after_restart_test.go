package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

// Unattended slice: after MarkInterrupted (Point restart), a paused run that
// still has a resumable checkpoint stays paused and is not treated as a
// forbidden cold restart of the structured assignment.
func TestPausedCheckpointSurvivesRestartAndRemainsResumable(t *testing.T) {
	a, ws := outcomeWorld(t)
	ctx := context.Background()
	now := time.Now().UTC()
	run := domain.Run{
		ID: "run-restart", WorkspaceID: ws.ID, ProfileID: "p", Task: "long autonomy",
		Status: domain.RunPaused, StartedAt: now,
		ConfigurationSnapshot: domain.NewRunConfigurationSnapshot("test", domain.DefaultProfile(), nil, now),
	}
	flowRun := domain.FlowRun{
		ID: "flow-run-restart", FlowID: "flow-restart", WorkspaceID: ws.ID,
		QuestID: "quest-restart", Status: domain.RunRunning, StartedAt: now,
		NodeStates: map[string]domain.FlowNodeState{"agent": {Status: "running"}},
	}
	if err := a.store.SaveFlowRun(ctx, flowRun); err != nil {
		t.Fatal(err)
	}
	if err := a.store.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	exec := domain.ExecutionInstance{
		ID: "exec-restart", WorkspaceID: ws.ID, ProjectAgentID: "agent", RunID: run.ID,
		FlowRunID: flowRun.ID, FlowNodeID: "agent",
		Task: "long autonomy", Status: domain.RunPaused, StartedAt: now, Snapshot: run.ConfigurationSnapshot,
	}
	if err := a.store.SaveExecution(ctx, exec); err != nil {
		t.Fatal(err)
	}
	if err := a.store.SaveRunCheckpoint(ctx, domain.RunCheckpoint{
		RunID: run.ID, Seq: 1, ExecutionID: exec.ID, SandboxPath: ws.Path,
		CreatedAt: now, HistoryJSON: []byte("[]"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.store.MarkInterrupted(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := a.store.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.RunPaused {
		t.Fatalf("paused+checkpoint must survive MarkInterrupted as paused, got %s", got.Status)
	}
	exec2, err := a.store.GetExecution(ctx, exec.ID)
	if err != nil {
		t.Fatal(err)
	}
	resumableFlow, err := a.store.GetFlowRun(ctx, flowRun.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resumableFlow.Status != domain.RunPaused {
		t.Fatalf("parent flow with safe checkpoint must survive restart as paused, got %s", resumableFlow.Status)
	}
	_, err = a.resumeStructuredExecutionIfSafe(&exec2, "")
	if err != nil && strings.Contains(err.Error(), "повтор задания с начала запрещён") {
		t.Fatalf("checkpoint after restart must be resumable, got %v", err)
	}
}
