package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"local-agent-workbench/internal/changesets"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/sandbox"
	"local-agent-workbench/internal/storage"
)

func TestLivePreciseQuestLeavesFilesWithoutManualApplyGate(t *testing.T) {
	t.Setenv("POINT_LIVE_WORKSPACE", "1")
	t.Setenv("POINT_FILE_ISOLATION", "")
	if !sandbox.LiveFileMutationEnabled() {
		t.Fatal("live mutation disabled")
	}
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "clamp.go"), []byte("package clamp\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager := &sandbox.Manager{Root: t.TempDir()}
	record, err := manager.Create(context.Background(), sandbox.CreateRequest{
		WorkspaceID: "ws", WorkspacePath: workspace, ExecutionID: "precise-1", LiveWorkspace: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	body := "package clamp\n\nfunc Clamp(v, lo, hi int) int {\n\tif v < lo { return lo }\n\tif v > hi { return hi }\n\treturn v\n}\n"
	if err := os.WriteFile(filepath.Join(record.Path, "clamp.go"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(workspace, "clamp.go"))
	if err != nil || string(got) != body {
		t.Fatalf("live file missing after agent write: %q err=%v", got, err)
	}
	db, err := storage.Open(filepath.Join(t.TempDir(), "hub.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	applier := changesets.Applier{Store: db}
	set, err := applier.BuildFromSandbox(context.Background(), changesets.BuildRequest{
		WorkspaceID: "ws", ExecutionID: "precise-1", Title: "precise live",
		WorkspacePath: workspace, BaselinePath: record.BaselinePath, SandboxPath: record.Path,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Items) == 0 {
		t.Fatal("expected journal items from baseline diff")
	}
	result, err := applier.Apply(context.Background(), workspace, set.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.ChangeSet.Status != domain.ChangeSetApplied {
		t.Fatalf("journal auto-apply status=%s conflicts=%v", result.ChangeSet.Status, result.Conflicts)
	}
	kept := false
	for _, item := range result.ChangeSet.Items {
		if item.Path == "clamp.go" && item.AppliedOperation == "kept" {
			kept = true
		}
	}
	if !kept {
		t.Fatalf("expected kept for already-written file: %#v", result.ChangeSet.Items)
	}
	if err := manager.Close(context.Background(), record, workspace); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "clamp.go")); err != nil {
		t.Fatal("workspace deleted on sandbox close")
	}
}

func TestLiveProjectStillRequiresDockerBoundary(t *testing.T) {
	a, _ := outcomeWorld(t)
	brief, err := domain.ApproveTaskBrief(domain.NormalizeTaskBrief(domain.TaskBrief{
		Mode: domain.TaskModeProject, Goal: "Small app", ResultKind: "workspace_change",
		Permissions: domain.TaskPermissions{WriteFiles: true, ExecuteCommands: true},
		Criteria:    []domain.AcceptanceCriterion{{ID: "c", Kind: "manual", Text: "Works"}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if a.sandboxCapabilities().StrongOSBoundary {
		t.Skip("docker backend already configured")
	}
	if err := a.validateTaskEnvironment(&brief); err == nil {
		t.Fatal("project without Docker must fail closed")
	}
}
