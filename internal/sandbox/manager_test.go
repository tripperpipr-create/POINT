package sandbox_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"local-agent-workbench/internal/changesets"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/sandbox"
	"local-agent-workbench/internal/storage"
)

func TestFilteredCopyCapabilitiesDoNotClaimOSIsolation(t *testing.T) {
	capabilities := (&sandbox.Manager{}).Capabilities()
	if capabilities.Backend != "filtered-copy" || !capabilities.LiveWorkspaceIsolation {
		t.Fatalf("unexpected filtered-copy capabilities: %#v", capabilities)
	}
	if capabilities.ProcessIsolation || capabilities.NetworkIsolation || capabilities.StrongOSBoundary {
		t.Fatalf("filtered-copy must not claim an OS or network boundary: %#v", capabilities)
	}
	if !capabilities.SecretEnvironmentSanitization || !capabilities.SymlinkIsolation {
		t.Fatalf("implemented defense-in-depth controls are missing: %#v", capabilities)
	}
}

func TestCloseRefusesPathOutsideManagedRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	manager := &sandbox.Manager{Root: root}
	record := domain.SandboxRecord{ID: "foreign", Kind: "copy", Path: outside}
	if err := manager.Close(context.Background(), record, ""); err == nil {
		t.Fatal("cleanup outside managed root was accepted")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside path was modified: %v", err)
	}
}

func TestSandboxDoesNotWriteLiveWorkspaceUntilApply(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "hello.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	manager := &sandbox.Manager{Root: filepath.Join(t.TempDir(), "sandboxes")}
	record, err := manager.Create(context.Background(), sandbox.CreateRequest{
		WorkspaceID: "ws", WorkspacePath: workspace, ExecutionID: "exec-1",
	})
	if err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	if err := os.WriteFile(filepath.Join(record.Path, "hello.txt"), []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}
	live, err := os.ReadFile(filepath.Join(workspace, "hello.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(live) != "hello" {
		t.Fatalf("live workspace mutated before apply: %q", live)
	}

	applier := changesets.Applier{Store: db}
	set, err := applier.BuildFromSandbox(context.Background(), changesets.BuildRequest{
		WorkspaceID: "ws", ExecutionID: "exec-1", Title: "test",
		WorkspacePath: workspace, SandboxPath: record.Path,
	})
	if err != nil {
		t.Fatalf("build change set: %v", err)
	}
	if len(set.Items) == 0 {
		t.Fatalf("expected change items")
	}
	result, err := applier.Apply(context.Background(), workspace, set.ID)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(result.Conflicts) > 0 {
		t.Fatalf("unexpected conflicts: %v", result.Conflicts)
	}
	live, err = os.ReadFile(filepath.Join(workspace, "hello.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(live) != "hello world" {
		t.Fatalf("expected applied content, got %q", live)
	}
}

func TestSandboxDiffUsesTheSameFilterAsCopy(t *testing.T) {
	workspace := t.TempDir()
	for path, content := range map[string]string{
		"main.go":               "package main\n",
		"build/generated.txt":   "generated\n",
		".vscode/settings.json": "{}\n",
		".env":                  "SECRET=value\n",
		"environment.go":        "package environment\n",
	} {
		absolute := filepath.Join(workspace, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absolute, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manager := &sandbox.Manager{Root: filepath.Join(t.TempDir(), "sandboxes")}
	record, err := manager.Create(context.Background(), sandbox.CreateRequest{
		WorkspaceID: "ws", WorkspacePath: workspace, ExecutionID: "exec-filter",
	})
	if err != nil {
		t.Fatal(err)
	}
	diffs, err := manager.Diff(context.Background(), workspace, record.Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(diffs) != 0 {
		t.Fatalf("filtered files must not appear as deletions: %#v", diffs)
	}
	if _, err = os.Stat(filepath.Join(record.Path, "environment.go")); err != nil {
		t.Fatalf("ordinary filename containing 'env' was incorrectly filtered: %v", err)
	}
}

func TestSequentialSandboxKeepsImmutableParentBaseline(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "state.txt"), []byte("live"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager := &sandbox.Manager{Root: filepath.Join(t.TempDir(), "sandboxes")}
	parent, err := manager.Create(context.Background(), sandbox.CreateRequest{
		WorkspaceID: "ws", WorkspacePath: workspace, ExecutionID: "exec-parent",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(parent.Path, "state.txt"), []byte("stage-one"), 0o644); err != nil {
		t.Fatal(err)
	}
	child, err := manager.Create(context.Background(), sandbox.CreateRequest{
		WorkspaceID: "ws", WorkspacePath: workspace, ExecutionID: "exec-child",
		SeedPath: parent.Path, ParentSandboxID: parent.ID, ParentExecutionID: "exec-parent",
	})
	if err != nil {
		t.Fatal(err)
	}
	if child.ParentExecutionID != "exec-parent" || child.ParentSandboxID != parent.ID || child.BaselinePath == "" {
		t.Fatalf("lineage=%#v", child)
	}
	seeded, err := os.ReadFile(filepath.Join(child.Path, "state.txt"))
	if err != nil || string(seeded) != "stage-one" {
		t.Fatalf("seeded child=%q err=%v", seeded, err)
	}
	if err = os.WriteFile(filepath.Join(child.Path, "state.txt"), []byte("stage-two"), 0o644); err != nil {
		t.Fatal(err)
	}
	diffs, err := manager.Diff(context.Background(), child.BaselinePath, child.Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(diffs) != 1 || diffs[0].Original != "stage-one" || diffs[0].Proposed != "stage-two" {
		t.Fatalf("incremental diffs=%#v", diffs)
	}
	live, err := os.ReadFile(filepath.Join(workspace, "state.txt"))
	if err != nil || string(live) != "live" {
		t.Fatalf("live workspace=%q err=%v", live, err)
	}
	baseline := child.BaselinePath
	if err = manager.Close(context.Background(), child, workspace); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(child.Path); !os.IsNotExist(err) {
		t.Fatalf("child sandbox was not removed: %v", err)
	}
	if _, err = os.Stat(baseline); !os.IsNotExist(err) {
		t.Fatalf("child baseline was not removed: %v", err)
	}
}

func TestSequentialSandboxSeedCopiesVendorTree(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "composer.json"), []byte(`{"name":"demo/app"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	manager := sandbox.Manager{Root: t.TempDir()}
	parent, err := manager.Create(ctx, sandbox.CreateRequest{
		WorkspaceID: "ws", WorkspacePath: workspace, ExecutionID: "exec-boot",
	})
	if err != nil {
		t.Fatal(err)
	}
	vendorFile := filepath.Join(parent.Path, "vendor", "autoload.php")
	if err = os.MkdirAll(filepath.Dir(vendorFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(vendorFile, []byte("<?php\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	child, err := manager.Create(ctx, sandbox.CreateRequest{
		WorkspaceID: "ws", WorkspacePath: workspace, ExecutionID: "exec-impl",
		SeedPath: parent.Path, ParentSandboxID: parent.ID, ParentExecutionID: "exec-boot",
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(child.Path, "vendor", "autoload.php"))
	if err != nil || string(got) != "<?php\n" {
		t.Fatalf("vendor not seeded into child: %q err=%v", got, err)
	}
}

func TestRootSandboxKeepsImmutableWorkspaceBaseline(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "state.txt"), []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager := sandbox.Manager{Root: t.TempDir()}
	record, err := manager.Create(ctx, sandbox.CreateRequest{
		WorkspaceID: "ws", WorkspacePath: workspace, ExecutionID: "exec-root",
	})
	if err != nil {
		t.Fatal(err)
	}
	if record.BaselinePath == "" {
		t.Fatal("root sandbox has no immutable baseline")
	}
	if err = os.WriteFile(filepath.Join(record.Path, "state.txt"), []byte("agent"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(workspace, "state.txt"), []byte("user-drift"), 0o644); err != nil {
		t.Fatal(err)
	}
	diffs, err := manager.Diff(ctx, record.BaselinePath, record.Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(diffs) != 1 || diffs[0].Original != "before" || diffs[0].Proposed != "agent" {
		t.Fatalf("root diff changed with live workspace: %#v", diffs)
	}
	baseline, err := os.ReadFile(filepath.Join(record.BaselinePath, "state.txt"))
	if err != nil || string(baseline) != "before" {
		t.Fatalf("baseline=%q err=%v", baseline, err)
	}
}

func TestFilteredCopySkipsLinkedWorktreeGitFile(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, ".git"), []byte("gitdir: C:/repo/.git/worktrees/branch"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager := sandbox.Manager{Root: t.TempDir()}
	record, err := manager.Create(context.Background(), sandbox.CreateRequest{
		WorkspaceID: "ws", WorkspacePath: workspace, ExecutionID: "exec",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{record.Path, record.BaselinePath} {
		if _, statErr := os.Stat(filepath.Join(root, ".git")); !os.IsNotExist(statErr) {
			t.Fatalf("copied worktree .git link into %s: %v", root, statErr)
		}
		if _, statErr := os.Stat(filepath.Join(root, "main.go")); statErr != nil {
			t.Fatalf("ordinary source missing from %s: %v", root, statErr)
		}
	}
}

func TestMergeSandboxesCombinesDisjointBranchChanges(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "a.txt"), []byte("a-base"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "b.txt"), []byte("b-base"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager := sandbox.Manager{Root: t.TempDir()}
	branchA, err := manager.Create(ctx, sandbox.CreateRequest{WorkspaceID: "ws", WorkspacePath: base, ExecutionID: "exec-a"})
	if err != nil {
		t.Fatal(err)
	}
	branchB, err := manager.Create(ctx, sandbox.CreateRequest{WorkspaceID: "ws", WorkspacePath: base, ExecutionID: "exec-b"})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(branchA.Path, "a.txt"), []byte("a-branch"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(branchB.Path, "b.txt"), []byte("b-branch"), 0o644); err != nil {
		t.Fatal(err)
	}
	merged, err := manager.Merge(ctx, sandbox.MergeRequest{
		WorkspaceID: "ws", ExecutionID: "exec-merged", BasePath: branchA.BaselinePath,
		Seeds: []sandbox.MergeSeed{
			{ExecutionID: "exec-a", SandboxID: branchA.ID, Path: branchA.Path},
			{ExecutionID: "exec-b", SandboxID: branchB.ID, Path: branchB.Path},
		},
		BaselineChangeSetIDs: []string{"merge-set"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(merged.Conflicts) != 0 || merged.Record.Kind != "merge-copy" || len(merged.Record.ParentExecutionIDs) != 2 {
		t.Fatalf("merge result=%#v", merged)
	}
	for path, expected := range map[string]string{"a.txt": "a-branch", "b.txt": "b-branch"} {
		content, readErr := os.ReadFile(filepath.Join(merged.Record.Path, path))
		if readErr != nil || string(content) != expected {
			t.Fatalf("%s=%q err=%v", path, content, readErr)
		}
		baseline, readErr := os.ReadFile(filepath.Join(merged.Record.BaselinePath, path))
		if readErr != nil || string(baseline) != expected {
			t.Fatalf("baseline %s=%q err=%v", path, baseline, readErr)
		}
	}
}

func TestMergeSandboxesReturnsStructuredConflictAndRequiresResolution(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "shared.txt"), []byte("base"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager := sandbox.Manager{Root: t.TempDir()}
	branchA, err := manager.Create(ctx, sandbox.CreateRequest{WorkspaceID: "ws", WorkspacePath: base, ExecutionID: "exec-a"})
	if err != nil {
		t.Fatal(err)
	}
	branchB, err := manager.Create(ctx, sandbox.CreateRequest{WorkspaceID: "ws", WorkspacePath: base, ExecutionID: "exec-b"})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(branchA.Path, "shared.txt"), []byte("from-a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(branchB.Path, "shared.txt"), []byte("from-b"), 0o644); err != nil {
		t.Fatal(err)
	}
	request := sandbox.MergeRequest{
		WorkspaceID: "ws", ExecutionID: "exec-merged", BasePath: branchA.BaselinePath,
		Seeds: []sandbox.MergeSeed{
			{ExecutionID: "exec-a", SandboxID: branchA.ID, Path: branchA.Path},
			{ExecutionID: "exec-b", SandboxID: branchB.ID, Path: branchB.Path},
		},
	}
	conflicted, err := manager.Merge(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if conflicted.Record.ID != "" || len(conflicted.Conflicts) != 1 || conflicted.Conflicts[0].Path != "shared.txt" || len(conflicted.Conflicts[0].Candidates) != 2 {
		t.Fatalf("conflicted=%#v", conflicted)
	}
	request.Resolutions = []sandbox.MergeResolution{{Path: "shared.txt", Strategy: "use_parent", ExecutionID: "exec-b"}}
	resolved, err := manager.Merge(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(resolved.Record.Path, "shared.txt"))
	if err != nil || string(content) != "from-b" {
		t.Fatalf("resolved=%q err=%v", content, err)
	}
}

func TestMergeSandboxesCombinesNonOverlappingEditsInOneFile(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	original := "header\nalpha = 1\nmiddle\nomega = 1\nfooter\n"
	if err := os.WriteFile(filepath.Join(base, "shared.txt"), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	manager := sandbox.Manager{Root: t.TempDir()}
	branchA, err := manager.Create(ctx, sandbox.CreateRequest{WorkspaceID: "ws", WorkspacePath: base, ExecutionID: "exec-a"})
	if err != nil {
		t.Fatal(err)
	}
	branchB, err := manager.Create(ctx, sandbox.CreateRequest{WorkspaceID: "ws", WorkspacePath: base, ExecutionID: "exec-b"})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(branchA.Path, "shared.txt"), []byte(strings.Replace(original, "alpha = 1", "alpha = 2", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(branchB.Path, "shared.txt"), []byte(strings.Replace(original, "omega = 1", "omega = 2", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	merged, err := manager.Merge(ctx, sandbox.MergeRequest{
		WorkspaceID: "ws", ExecutionID: "exec-merged", BasePath: branchA.BaselinePath,
		Seeds: []sandbox.MergeSeed{
			{ExecutionID: "exec-a", SandboxID: branchA.ID, Path: branchA.Path},
			{ExecutionID: "exec-b", SandboxID: branchB.ID, Path: branchB.Path},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(merged.Conflicts) != 0 {
		t.Fatalf("non-overlapping edits conflicted: %#v", merged.Conflicts)
	}
	content, err := os.ReadFile(filepath.Join(merged.Record.Path, "shared.txt"))
	expected := strings.Replace(strings.Replace(original, "alpha = 1", "alpha = 2", 1), "omega = 1", "omega = 2", 1)
	if err != nil || string(content) != expected {
		t.Fatalf("merged=%q expected=%q err=%v", content, expected, err)
	}
}

func TestMergeSandboxesKeepsOverlappingSameFileEditsAsConflict(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "shared.txt"), []byte("before\nvalue = 1\nafter\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager := sandbox.Manager{Root: t.TempDir()}
	branchA, err := manager.Create(ctx, sandbox.CreateRequest{WorkspaceID: "ws", WorkspacePath: base, ExecutionID: "exec-a"})
	if err != nil {
		t.Fatal(err)
	}
	branchB, err := manager.Create(ctx, sandbox.CreateRequest{WorkspaceID: "ws", WorkspacePath: base, ExecutionID: "exec-b"})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(branchA.Path, "shared.txt"), []byte("before\nvalue = 2\nafter\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(branchB.Path, "shared.txt"), []byte("before\nvalue = 3\nafter\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	merged, err := manager.Merge(ctx, sandbox.MergeRequest{
		WorkspaceID: "ws", ExecutionID: "exec-merged", BasePath: branchA.BaselinePath,
		Seeds: []sandbox.MergeSeed{
			{ExecutionID: "exec-a", SandboxID: branchA.ID, Path: branchA.Path},
			{ExecutionID: "exec-b", SandboxID: branchB.ID, Path: branchB.Path},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(merged.Conflicts) != 1 || merged.Conflicts[0].Path != "shared.txt" {
		t.Fatalf("overlapping edits did not conflict: %#v", merged)
	}
}

func TestChangeSetApplyIsAtomicAndRevertUsesExactSnapshots(t *testing.T) {
	workspace := t.TempDir()
	for path, content := range map[string]string{"a.txt": "a-before", "b.txt": "b-before"} {
		if err := os.WriteFile(filepath.Join(workspace, path), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	db, err := storage.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	manager := &sandbox.Manager{Root: filepath.Join(t.TempDir(), "sandboxes")}
	record, err := manager.Create(context.Background(), sandbox.CreateRequest{WorkspaceID: "ws", WorkspacePath: workspace, ExecutionID: "exec-atomic"})
	if err != nil {
		t.Fatal(err)
	}
	// No trailing newline and diff-marker prefixes ensure apply cannot safely
	// reconstruct the proposed file from a display diff.
	if err = os.WriteFile(filepath.Join(record.Path, "a.txt"), []byte("--- exact\n+++ content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(record.Path, "b.txt"), []byte("b-after"), 0o644); err != nil {
		t.Fatal(err)
	}
	applier := changesets.Applier{Store: db}
	set, err := applier.BuildFromSandbox(context.Background(), changesets.BuildRequest{
		WorkspaceID: "ws", ExecutionID: "exec-atomic", WorkspacePath: workspace, SandboxPath: record.Path,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(workspace, "b.txt"), []byte("user-edit"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := applier.Apply(context.Background(), workspace, set.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Conflicts) != 1 || result.Conflicts[0] != "b.txt" {
		t.Fatalf("conflicts=%v", result.Conflicts)
	}
	data, err := os.ReadFile(filepath.Join(workspace, "a.txt"))
	if err != nil || string(data) != "a-before" {
		t.Fatalf("atomic preflight allowed a partial write: %q, %v", data, err)
	}
	manual := "b-before"
	if _, err = applier.ResolveConflict(context.Background(), set.ID, changesets.ResolveRequest{Strategy: "manual", Path: "b.txt", Content: &manual}); err != nil {
		t.Fatal(err)
	}
	result, err = applier.Apply(context.Background(), workspace, set.ID)
	if err != nil || len(result.Conflicts) != 0 {
		t.Fatalf("resolved apply failed: result=%#v err=%v", result, err)
	}
	data, err = os.ReadFile(filepath.Join(workspace, "a.txt"))
	if err != nil || string(data) != "--- exact\n+++ content" {
		t.Fatalf("exact snapshot was not applied: %q, %v", data, err)
	}
	result, err = applier.Revert(context.Background(), workspace, set.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.ChangeSet.Status != "reverted" {
		t.Fatalf("revert status=%s", result.ChangeSet.Status)
	}
	for path, want := range map[string]string{"a.txt": "a-before", "b.txt": "b-before"} {
		data, readErr := os.ReadFile(filepath.Join(workspace, path))
		if readErr != nil || string(data) != want {
			t.Fatalf("%s after revert=%q err=%v want=%q", path, data, readErr, want)
		}
	}
}

func TestChangeSetRevertRefusesLaterUserEdit(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "file.txt"), []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	manager := &sandbox.Manager{Root: filepath.Join(t.TempDir(), "sandboxes")}
	record, err := manager.Create(context.Background(), sandbox.CreateRequest{WorkspaceID: "ws", WorkspacePath: workspace, ExecutionID: "exec-drift"})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(record.Path, "file.txt"), []byte("applied"), 0o644); err != nil {
		t.Fatal(err)
	}
	applier := changesets.Applier{Store: db}
	set, err := applier.BuildFromSandbox(context.Background(), changesets.BuildRequest{WorkspaceID: "ws", ExecutionID: "exec-drift", WorkspacePath: workspace, SandboxPath: record.Path})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = applier.Apply(context.Background(), workspace, set.ID); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(workspace, "file.txt"), []byte("later-user-edit"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err = applier.Revert(context.Background(), workspace, set.ID); err == nil {
		t.Fatal("revert must refuse a file changed after apply")
	}
}

func TestPreferWorktreeCreatesDetachedCheckout(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "hello.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", workspace}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Point", "GIT_AUTHOR_EMAIL=point@local", "GIT_COMMITTER_NAME=Point", "GIT_COMMITTER_EMAIL=point@local")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
	}
	run("init")
	run("add", "hello.txt")
	run("commit", "-m", "init")

	manager := &sandbox.Manager{Root: filepath.Join(t.TempDir(), "sandboxes")}
	record, err := manager.Create(context.Background(), sandbox.CreateRequest{
		WorkspaceID: "ws", WorkspacePath: workspace, ExecutionID: "exec-git", PreferWorktree: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if record.Kind != "worktree" || record.BaseCommit == "" {
		t.Fatalf("expected detached worktree, got %#v", record)
	}
	if err = os.WriteFile(filepath.Join(record.Path, "hello.txt"), []byte("sandbox"), 0o644); err != nil {
		t.Fatal(err)
	}
	live, err := os.ReadFile(filepath.Join(workspace, "hello.txt"))
	if err != nil || string(live) != "hello" {
		t.Fatalf("live workspace mutated: %q err=%v", live, err)
	}
	if err = manager.Close(context.Background(), record, workspace); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(record.Path); !os.IsNotExist(err) {
		t.Fatalf("worktree path still exists: %v", err)
	}
}

func TestPreferWorktreeFallsBackToCurrentFilesWhenGitIsDirty(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "hello.txt"), []byte("committed"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", workspace}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Point", "GIT_AUTHOR_EMAIL=point@local", "GIT_COMMITTER_NAME=Point", "GIT_COMMITTER_EMAIL=point@local")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
	}
	run("init")
	run("add", "hello.txt")
	run("commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(workspace, "hello.txt"), []byte("dirty-current"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "new.txt"), []byte("untracked-current"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager := &sandbox.Manager{Root: filepath.Join(t.TempDir(), "sandboxes")}
	record, err := manager.Create(context.Background(), sandbox.CreateRequest{
		WorkspaceID: "ws", WorkspacePath: workspace, ExecutionID: "exec-dirty", PreferWorktree: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if record.Kind != "copy" || record.BaseCommit != "" {
		t.Fatalf("dirty repository must use current-file copy: %#v", record)
	}
	for path, expected := range map[string]string{"hello.txt": "dirty-current", "new.txt": "untracked-current"} {
		content, readErr := os.ReadFile(filepath.Join(record.Path, path))
		if readErr != nil || string(content) != expected {
			t.Fatalf("sandbox %s=%q err=%v", path, content, readErr)
		}
	}
}

func TestIsGitRepoDetectsLinkedWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "hello.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", workspace}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Point", "GIT_AUTHOR_EMAIL=point@local", "GIT_COMMITTER_NAME=Point", "GIT_COMMITTER_EMAIL=point@local")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
	}
	run("init")
	run("add", "hello.txt")
	run("commit", "-m", "init")

	manager := &sandbox.Manager{Root: filepath.Join(t.TempDir(), "sandboxes")}
	record, err := manager.Create(context.Background(), sandbox.CreateRequest{
		WorkspaceID: "ws", WorkspacePath: workspace, ExecutionID: "exec-link", PreferWorktree: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if record.Kind != "worktree" {
		t.Fatalf("expected worktree sandbox, got %#v", record)
	}
	info, err := os.Stat(filepath.Join(record.Path, ".git"))
	if err != nil || info.IsDir() {
		t.Fatalf("linked worktree should expose a .git file, got err=%v info=%v", err, info)
	}
	// PreferWorktree must still recognize the linked worktree as a git repo.
	nested, err := manager.Create(context.Background(), sandbox.CreateRequest{
		WorkspaceID: "ws", WorkspacePath: record.Path, ExecutionID: "exec-nested", PreferWorktree: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if nested.Kind != "worktree" {
		t.Fatalf("linked worktree was not detected as git repo: %#v", nested)
	}
	_ = manager.Close(context.Background(), nested, record.Path)
	_ = manager.Close(context.Background(), record, workspace)
}

func TestMergeSandboxesConflictsWhenOneBranchDeletesAndAnotherEdits(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "shared.txt"), []byte("base"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager := sandbox.Manager{Root: t.TempDir()}
	branchA, err := manager.Create(ctx, sandbox.CreateRequest{WorkspaceID: "ws", WorkspacePath: base, ExecutionID: "exec-a"})
	if err != nil {
		t.Fatal(err)
	}
	branchB, err := manager.Create(ctx, sandbox.CreateRequest{WorkspaceID: "ws", WorkspacePath: base, ExecutionID: "exec-b"})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(branchA.Path, "shared.txt"), []byte("kept-by-a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(branchB.Path, "shared.txt")); err != nil {
		t.Fatal(err)
	}
	conflicted, err := manager.Merge(ctx, sandbox.MergeRequest{
		WorkspaceID: "ws", ExecutionID: "exec-merged", BasePath: branchA.BaselinePath,
		Seeds: []sandbox.MergeSeed{
			{ExecutionID: "exec-a", SandboxID: branchA.ID, Path: branchA.Path},
			{ExecutionID: "exec-b", SandboxID: branchB.ID, Path: branchB.Path},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if conflicted.Record.ID != "" || len(conflicted.Conflicts) != 1 || conflicted.Conflicts[0].Path != "shared.txt" {
		t.Fatalf("delete/edit should conflict: %#v", conflicted)
	}
}

func TestLateStartSiblingKeepsOriginalWorkspaceBaseline(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "app.go"), []byte("package app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager := sandbox.Manager{Root: t.TempDir()}
	first, err := manager.Create(ctx, sandbox.CreateRequest{WorkspaceID: "ws", WorkspacePath: workspace, ExecutionID: "exec-first"})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(first.Path, "app.go"), []byte("package first\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	late, err := manager.Create(ctx, sandbox.CreateRequest{WorkspaceID: "ws", WorkspacePath: workspace, ExecutionID: "exec-late"})
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(late.Path, "app.go"))
	if err != nil || string(content) != "package app\n" {
		t.Fatalf("late start saw sibling writes: %q err=%v", content, err)
	}
}

func TestMergeSandboxesConflictsOnRenameVersusEdit(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "old.txt"), []byte("base"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager := sandbox.Manager{Root: t.TempDir()}
	branchA, err := manager.Create(ctx, sandbox.CreateRequest{WorkspaceID: "ws", WorkspacePath: base, ExecutionID: "exec-a"})
	if err != nil {
		t.Fatal(err)
	}
	branchB, err := manager.Create(ctx, sandbox.CreateRequest{WorkspaceID: "ws", WorkspacePath: base, ExecutionID: "exec-b"})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(filepath.Join(branchA.Path, "old.txt"), filepath.Join(branchA.Path, "new.txt")); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(branchB.Path, "old.txt"), []byte("edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	conflicted, err := manager.Merge(ctx, sandbox.MergeRequest{
		WorkspaceID: "ws", ExecutionID: "exec-merged", BasePath: branchA.BaselinePath,
		Seeds: []sandbox.MergeSeed{
			{ExecutionID: "exec-a", SandboxID: branchA.ID, Path: branchA.Path},
			{ExecutionID: "exec-b", SandboxID: branchB.ID, Path: branchB.Path},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if conflicted.Record.ID != "" || len(conflicted.Conflicts) == 0 {
		t.Fatalf("rename vs edit should conflict: %#v", conflicted)
	}
}
