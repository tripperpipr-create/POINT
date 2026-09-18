package app

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/sandbox"
)

type recordingSandboxBackend struct {
	*sandbox.Manager
	mu              sync.Mutex
	createRequests  []sandbox.CreateRequest
	records         []domain.SandboxRecord
	processRequests []sandbox.ProcessRequest
}

func (b *recordingSandboxBackend) Create(ctx context.Context, request sandbox.CreateRequest) (domain.SandboxRecord, error) {
	record, err := b.Manager.Create(ctx, request)
	if err != nil {
		return domain.SandboxRecord{}, err
	}
	b.mu.Lock()
	b.createRequests = append(b.createRequests, request)
	b.records = append(b.records, record)
	b.mu.Unlock()
	return record, nil
}

func (b *recordingSandboxBackend) PrepareProcess(ctx context.Context, request sandbox.ProcessRequest) (sandbox.PreparedProcess, error) {
	b.mu.Lock()
	b.processRequests = append(b.processRequests, request)
	b.mu.Unlock()
	return sandbox.PreparedProcess{Command: exec.CommandContext(ctx, "go", "version")}, nil
}

func TestExecuteToolUsesDisposableSnapshotAsStrongProcessRoot(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	workspaceRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspaceRoot, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	backend := &recordingSandboxBackend{Manager: &sandbox.Manager{Root: t.TempDir()}}
	application, err := New(t.TempDir(), WithSandboxBackend(backend))
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	if _, err = application.OpenWorkspace(workspaceRoot); err != nil {
		t.Fatal(err)
	}
	custom, err := application.SaveCustomTool(domain.CustomTool{
		Kind: domain.CustomToolProcess, DisplayName: "Go version", Description: "Verifier",
		Program: "go", Arguments: []string{"version"}, CWD: ".", TimeoutSeconds: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	arguments := json.RawMessage(`{"reason":"verify disposable isolation"}`)
	approvalID := approveManualToolExecution(t, application, custom.ID, arguments)
	result, err := application.ExecuteTool(ToolExecutionRequest{
		ToolName: custom.ID, Arguments: arguments,
		Mode: "execute_readonly", ApprovalID: approvalID,
	})
	if err != nil || !result.OK {
		t.Fatalf("execute result=%#v err=%v", result, err)
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if len(backend.records) != 1 || len(backend.processRequests) != 1 {
		t.Fatalf("sandbox creates=%d processes=%d", len(backend.records), len(backend.processRequests))
	}
	processRoot := filepath.Clean(backend.processRequests[0].WorkspaceRoot)
	if strings.EqualFold(processRoot, filepath.Clean(workspaceRoot)) || processRoot != filepath.Clean(backend.records[0].Path) {
		t.Fatalf("process root=%q live=%q record=%q", processRoot, workspaceRoot, backend.records[0].Path)
	}
	if _, statErr := os.Stat(processRoot); !os.IsNotExist(statErr) {
		t.Fatalf("disposable sandbox was not removed: %v", statErr)
	}
}

func TestExecuteReadonlyDiscardsHostCommandWrites(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	workspaceRoot := t.TempDir()
	target := filepath.Join(workspaceRoot, "main.txt")
	if err := os.WriteFile(target, []byte("live\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	application, err := New(t.TempDir(), WithSandboxBackend(&sandbox.Manager{Root: t.TempDir()}))
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	if _, err = application.OpenWorkspace(workspaceRoot); err != nil {
		t.Fatal(err)
	}
	custom, err := application.SaveCustomTool(domain.CustomTool{
		Kind: domain.CustomToolCommand, DisplayName: "Write marker", Description: "Mutates its current root",
		Command: "echo sandbox > main.txt", CWD: ".", TimeoutSeconds: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	arguments := json.RawMessage(`{"reason":"prove discard semantics"}`)
	approvalID := approveManualToolExecution(t, application, custom.ID, arguments)
	result, err := application.ExecuteTool(ToolExecutionRequest{
		ToolName: custom.ID, Arguments: arguments,
		Mode: "execute_readonly", ApprovalID: approvalID,
	})
	if err != nil || !result.OK {
		t.Fatalf("execute result=%#v err=%v", result, err)
	}
	content, err := os.ReadFile(target)
	if err != nil || string(content) != "live\n" {
		t.Fatalf("live workspace changed: %q err=%v", content, err)
	}
}
