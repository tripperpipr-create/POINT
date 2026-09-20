package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

func TestDeleteExecutionSandboxOnlyRemovesUnstartedLaunch(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "cleanup.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	record := domain.SandboxRecord{ID: "sandbox-unstarted", WorkspaceID: "ws", ExecutionID: "execution-unstarted", Kind: "copy", Path: t.TempDir(), CreatedAt: now}
	execution := domain.ExecutionInstance{ID: record.ExecutionID, WorkspaceID: record.WorkspaceID, ProjectAgentID: "agent", SandboxID: record.ID, Task: "task", Status: domain.RunPending, StartedAt: now}
	if err = store.SaveSandbox(ctx, record); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveExecution(ctx, execution); err != nil {
		t.Fatal(err)
	}
	if err = store.DeleteExecutionSandbox(ctx, execution.ID, record.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.GetExecution(ctx, execution.ID); err == nil {
		t.Fatal("unstarted execution metadata survived cleanup")
	}
	if _, err = store.GetSandbox(ctx, record.ID); err == nil {
		t.Fatal("unstarted sandbox metadata survived cleanup")
	}

	record.ID, record.ExecutionID = "sandbox-started", "execution-started"
	execution.ID, execution.SandboxID, execution.RunID = record.ExecutionID, record.ID, "run-visible"
	if err = store.SaveSandbox(ctx, record); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveExecution(ctx, execution); err != nil {
		t.Fatal(err)
	}
	if err = store.DeleteExecutionSandbox(ctx, execution.ID, record.ID); err == nil {
		t.Fatal("cleanup erased an execution with an observable Run")
	}
	if _, err = store.GetExecution(ctx, execution.ID); err != nil {
		t.Fatalf("started execution was lost: %v", err)
	}
}
