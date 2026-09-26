package sandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestDockerProbeHelperProcess is not a test: the deferred-probe test runs the
// test binary as a fake docker CLI that answers every query with one line.
func TestDockerProbeHelperProcess(t *testing.T) {
	for _, argument := range os.Args {
		if argument == "fake-docker-up" {
			fmt.Print("27.1.0")
			os.Exit(0)
		}
	}
}

// Docker Desktop stopped at startup used to abort the core, taking the chat
// and the IDE down with it. The backend now starts, refuses execution with the
// reason, and recovers by itself once the daemon answers.
func TestContainerBackendWaitsForDockerInsteadOfFailingStartup(t *testing.T) {
	workspace := t.TempDir()
	backend := NewContainerBackend(filepath.Join(t.TempDir(), "sandboxes"))
	backend.command = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, filepath.Join(t.TempDir(), "missing-docker"))
	}
	if err := backend.Probe(context.Background()); err == nil {
		t.Fatal("fake daemon must be down")
	} else {
		backend.deferProbe(err)
	}
	if !strings.Contains(backend.Capabilities().Unavailable, "запустите Docker Desktop") {
		t.Fatalf("capabilities hide the stopped daemon: %#v", backend.Capabilities())
	}
	if _, err := backend.Create(context.Background(), CreateRequest{WorkspaceID: "ws", WorkspacePath: workspace, ExecutionID: "exec"}); err == nil || !strings.Contains(err.Error(), "Docker недоступен") {
		t.Fatalf("execution without Docker must fail with the reason, got %v", err)
	}

	backend.command = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDockerProbeHelperProcess$", "fake-docker-up")
	}
	backend.availability.mu.Lock()
	backend.availability.lastAt = time.Now().Add(-dockerReprobeInterval)
	backend.availability.mu.Unlock()
	record, err := backend.Create(context.Background(), CreateRequest{WorkspaceID: "ws", WorkspacePath: workspace, ExecutionID: "exec"})
	if err != nil {
		t.Fatalf("backend did not recover once Docker answered: %v", err)
	}
	defer backend.Close(context.Background(), record, workspace)
	if backend.Unavailable() != "" || record.BackendVersion != "27.1.0" {
		t.Fatalf("recovered backend lost attribution: unavailable=%q record=%#v", backend.Unavailable(), record)
	}
}
