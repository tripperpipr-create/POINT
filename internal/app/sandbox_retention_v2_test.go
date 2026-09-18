package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/sandbox"
)

func TestExpiredCancelledQuestSandboxIsRemovedInsideManagedRoot(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	root := t.TempDir()
	backend := &sandbox.Manager{Root: root}
	application, err := New(t.TempDir(), WithSandboxBackend(backend))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { application.Shutdown(context.Background()) })
	workspace := openTestWorld(t, application)
	now := time.Now().UTC()
	quest := domain.Quest{
		ID: "quest-expired", WorkspaceID: workspace.ID, Title: "cancelled",
		Status: domain.QuestCancelled, CreatedAt: now.Add(-40 * 24 * time.Hour), UpdatedAt: now,
	}
	if err = application.store.SaveQuest(context.Background(), quest); err != nil {
		t.Fatal(err)
	}
	execution := domain.ExecutionInstance{
		ID: "execution-expired", WorkspaceID: workspace.ID, ProjectAgentID: "agent",
		QuestID: quest.ID, Task: "partial", Status: domain.RunCancelled, StartedAt: quest.CreatedAt,
	}
	if err = application.store.SaveExecution(context.Background(), execution); err != nil {
		t.Fatal(err)
	}
	sandboxPath := filepath.Join(root, "sandbox-expired")
	baselinePath := filepath.Join(root, "sandbox-expired-baseline")
	for _, path := range []string{sandboxPath, baselinePath} {
		if err = os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	record := domain.SandboxRecord{
		ID: "sandbox-expired", WorkspaceID: workspace.ID, ExecutionID: execution.ID,
		Kind: "copy", Backend: "filtered-copy", Path: sandboxPath, BaselinePath: baselinePath,
		CreatedAt: quest.CreatedAt,
	}
	if err = application.store.SaveSandbox(context.Background(), record); err != nil {
		t.Fatal(err)
	}

	application.cleanupExpiredQuestSandboxes(context.Background(), now)
	if _, err = os.Stat(sandboxPath); !os.IsNotExist(err) {
		t.Fatalf("expired sandbox still exists: %v", err)
	}
	stored, err := application.store.GetSandbox(context.Background(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ClosedAt == nil {
		t.Fatal("retention cleanup was not journaled")
	}
}
