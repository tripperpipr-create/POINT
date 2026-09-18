package changesets_test

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

func TestConflictResolutionRetryIsIdempotent(t *testing.T) {
	ctx := context.Background()
	workspacePath := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspacePath, "a.txt"), []byte("base a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspacePath, "b.txt"), []byte("base b"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager := sandbox.Manager{Root: t.TempDir()}
	record, err := manager.Create(ctx, sandbox.CreateRequest{
		WorkspaceID: "ws", WorkspacePath: workspacePath, ExecutionID: "exec",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(record.Path, "a.txt"), []byte("agent a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(record.Path, "b.txt"), []byte("agent b"), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(filepath.Join(t.TempDir(), "changes.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	applier := changesets.Applier{Store: db}
	set, err := applier.BuildFromSandbox(ctx, changesets.BuildRequest{
		WorkspaceID: "ws", ExecutionID: "exec", WorkspacePath: workspacePath, SandboxPath: record.Path,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(workspacePath, "b.txt"), []byte("user b"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := applier.Apply(ctx, workspacePath, set.ID)
	if err != nil {
		t.Fatal(err)
	}
	if first.ChangeSet.Status != domain.ChangeSetConflict || len(first.Conflicts) != 1 || first.Conflicts[0] != "b.txt" {
		t.Fatalf("first=%#v", first)
	}
	if len(first.ChangeSet.Resolutions) != 1 || first.ChangeSet.Resolutions[0].Strategy != "unresolved" {
		t.Fatalf("resolutions=%#v", first.ChangeSet.Resolutions)
	}
	if _, err = applier.ResolveConflict(ctx, set.ID, changesets.ResolveRequest{Path: "b.txt", Strategy: "keep_ours"}); err != nil {
		t.Fatal(err)
	}
	second, err := applier.Apply(ctx, workspacePath, set.ID)
	if err != nil {
		t.Fatal(err)
	}
	if second.ChangeSet.Status != domain.ChangeSetApplied || len(second.Conflicts) != 0 {
		t.Fatalf("second=%#v", second)
	}
	a, _ := os.ReadFile(filepath.Join(workspacePath, "a.txt"))
	b, _ := os.ReadFile(filepath.Join(workspacePath, "b.txt"))
	if string(a) != "agent a" || string(b) != "user b" {
		t.Fatalf("a=%q b=%q", a, b)
	}
}

func TestManualConflictResolutionWritesExplicitContent(t *testing.T) {
	ctx := context.Background()
	workspacePath := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspacePath, "value.txt"), []byte("base"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager := sandbox.Manager{Root: t.TempDir()}
	record, err := manager.Create(ctx, sandbox.CreateRequest{
		WorkspaceID: "ws", WorkspacePath: workspacePath, ExecutionID: "exec",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(record.Path, "value.txt"), []byte("agent"), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(filepath.Join(t.TempDir(), "manual.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	applier := changesets.Applier{Store: db}
	set, err := applier.BuildFromSandbox(ctx, changesets.BuildRequest{
		WorkspaceID: "ws", ExecutionID: "exec", WorkspacePath: workspacePath, SandboxPath: record.Path,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(workspacePath, "value.txt"), []byte("user"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err = applier.Apply(ctx, workspacePath, set.ID); err != nil {
		t.Fatal(err)
	}
	manual := "merged"
	if _, err = applier.ResolveConflict(ctx, set.ID, changesets.ResolveRequest{
		Path: "value.txt", Strategy: "manual", Content: &manual,
	}); err != nil {
		t.Fatal(err)
	}
	result, err := applier.Apply(ctx, workspacePath, set.ID)
	if err != nil {
		t.Fatal(err)
	}
	content, _ := os.ReadFile(filepath.Join(workspacePath, "value.txt"))
	if result.ChangeSet.Status != domain.ChangeSetApplied || string(content) != manual {
		t.Fatalf("result=%#v content=%q", result, content)
	}
}
