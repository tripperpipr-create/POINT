package sandbox_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"local-agent-workbench/internal/sandbox"
)

func TestLiveWorkspaceCreateWritesOpenProjectAndCloseKeepsIt(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "hello.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager := &sandbox.Manager{Root: filepath.Join(t.TempDir(), "sandboxes")}
	record, err := manager.Create(context.Background(), sandbox.CreateRequest{
		WorkspaceID: "ws", WorkspacePath: workspace, ExecutionID: "exec-live", LiveWorkspace: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if record.Kind != "live" || record.Path != filepath.Clean(workspace) {
		t.Fatalf("record=%#v", record)
	}
	if record.BaselinePath == "" {
		t.Fatal("baseline missing")
	}
	if err := os.WriteFile(filepath.Join(record.Path, "hello.txt"), []byte("live write"), 0o644); err != nil {
		t.Fatal(err)
	}
	live, err := os.ReadFile(filepath.Join(workspace, "hello.txt"))
	if err != nil || string(live) != "live write" {
		t.Fatalf("live=%q err=%v", live, err)
	}
	if err := manager.Close(context.Background(), record, workspace); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(workspace); err != nil {
		t.Fatalf("close deleted live workspace: %v", err)
	}
	if _, err := os.Stat(record.BaselinePath); !os.IsNotExist(err) {
		t.Fatalf("baseline should be removed, err=%v", err)
	}
}

func TestLiveFileMutationEnabledDefaultsOn(t *testing.T) {
	t.Setenv("POINT_FILE_ISOLATION", "")
	t.Setenv("POINT_LIVE_WORKSPACE", "1")
	if !sandbox.LiveFileMutationEnabled() {
		t.Fatal("expected live when POINT_LIVE_WORKSPACE=1")
	}
	t.Setenv("POINT_LIVE_WORKSPACE", "0")
	if sandbox.LiveFileMutationEnabled() {
		t.Fatal("expected opt-out")
	}
	t.Setenv("POINT_LIVE_WORKSPACE", "1")
	t.Setenv("POINT_FILE_ISOLATION", "sandbox")
	if sandbox.LiveFileMutationEnabled() {
		t.Fatal("isolation flag should win")
	}
	t.Setenv("POINT_FILE_ISOLATION", "")
	os.Unsetenv("POINT_LIVE_WORKSPACE")
	if !sandbox.LiveFileMutationEnabled() {
		t.Fatal("core default without env is live for personal Point")
	}
}
