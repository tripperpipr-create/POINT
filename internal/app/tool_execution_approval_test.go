package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
)

func approveManualToolExecution(t *testing.T, application *App, toolID string, arguments json.RawMessage) string {
	t.Helper()
	requested, err := application.RequestToolExecutionApproval(ToolExecutionApprovalRequest{ToolName: toolID, Arguments: arguments})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := application.ResolveToolExecutionApproval(requested.Approval.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != domain.ToolExecutionApprovalAllowed {
		t.Fatalf("resolved status=%q", resolved.Status)
	}
	return resolved.ID
}

func TestManualToolApprovalBindsArgumentsAndIsConsumedOnce(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	if _, err = application.OpenWorkspace(root); err != nil {
		t.Fatal(err)
	}
	custom, err := application.SaveCustomTool(domain.CustomTool{
		Kind: domain.CustomToolProcess, DisplayName: "Go version", Description: "Version",
		Program: "go", Arguments: []string{"version"}, CWD: ".", TimeoutSeconds: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	arguments := json.RawMessage(`{"reason":"review exact invocation"}`)
	if _, err = application.ExecuteTool(ToolExecutionRequest{ToolName: custom.ID, Arguments: arguments, Mode: "execute_readonly", Approved: true}); err == nil || !strings.Contains(err.Error(), "approved=true is not an approval") {
		t.Fatalf("client boolean unexpectedly authorized execution: %v", err)
	}
	approvalID := approveManualToolExecution(t, application, custom.ID, arguments)
	if _, err = application.ExecuteTool(ToolExecutionRequest{ToolName: custom.ID, Arguments: json.RawMessage(`{"reason":"substituted"}`), Mode: "execute_readonly", ApprovalID: approvalID}); err == nil {
		t.Fatal("approval accepted substituted arguments")
	}
	result, err := application.ExecuteTool(ToolExecutionRequest{ToolName: custom.ID, Arguments: arguments, Mode: "execute_readonly", ApprovalID: approvalID})
	if err != nil || !result.OK {
		t.Fatalf("approved execution result=%#v err=%v", result, err)
	}
	if _, err = application.ExecuteTool(ToolExecutionRequest{ToolName: custom.ID, Arguments: arguments, Mode: "execute_readonly", ApprovalID: approvalID}); err == nil {
		t.Fatal("consumed approval was replayed")
	}
}

func TestManualToolApprovalRejectsToolRevisionAndRedactsDurableArguments(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	root := t.TempDir()
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	if _, err = application.OpenWorkspace(root); err != nil {
		t.Fatal(err)
	}
	custom, err := application.SaveCustomTool(domain.CustomTool{
		Kind: domain.CustomToolCommand, DisplayName: "Echo", Description: "Fixed command",
		Command: "echo safe", CWD: ".", TimeoutSeconds: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	arguments := json.RawMessage(`{"reason":"Authorization: Bearer manual-tool-secret"}`)
	approvalID := approveManualToolExecution(t, application, custom.ID, arguments)
	stored, err := application.store.ToolExecutionApproval(context.Background(), approvalID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored.Arguments), "manual-tool-secret") || strings.Contains(stored.Reason, "manual-tool-secret") {
		t.Fatalf("durable approval leaked raw arguments: arguments=%s reason=%q", stored.Arguments, stored.Reason)
	}
	custom.Description = "Changed after approval"
	if _, err = application.SaveCustomTool(custom); err != nil {
		t.Fatal(err)
	}
	if _, err = application.ExecuteTool(ToolExecutionRequest{ToolName: custom.ID, Arguments: arguments, Mode: "execute_readonly", ApprovalID: approvalID}); err == nil {
		t.Fatal("approval accepted a changed tool definition")
	}
}
