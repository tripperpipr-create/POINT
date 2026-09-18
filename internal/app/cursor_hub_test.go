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

func TestBootstrapUsesHTTPAPINotCursorCLI(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	root := t.TempDir()
	dataDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o600)
	application, err := New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	if err = application.SetWorkspaceBoundary(root); err != nil {
		t.Fatal(err)
	}
	boot, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	var httpDefault *domain.AgentProfile
	for index := range boot.Profiles {
		profile := boot.Profiles[index]
		if profile.ID == "default" || profile.Provider == domain.ProviderOllama {
			httpDefault = &boot.Profiles[index]
			break
		}
	}
	if httpDefault == nil {
		t.Fatal("expected seeded HTTP API default profile")
	}
	if domain.IsAgentCLIProvider(httpDefault.Provider) {
		t.Fatalf("default profile still uses CLI provider: %#v", httpDefault)
	}
	if boot.Preferences == nil {
		t.Fatal("expected hub preferences object")
	}

	_, err = application.SaveProfile(domain.DefaultCursorProfile())
	if err == nil || !strings.Contains(err.Error(), "removed") {
		t.Fatalf("expected Cursor save to fail as removed, got %v", err)
	}
}

func TestPreviewToolAcceptsDraftCustomProcess(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o600)
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	if _, err = application.OpenWorkspace(root); err != nil {
		t.Fatal(err)
	}
	tool := domain.CustomTool{
		Kind: domain.CustomToolProcess, DisplayName: "Go version", Description: "Preview argv",
		Program: "go", Arguments: []string{"version"}, CWD: ".", TimeoutSeconds: 30,
	}
	preview, err := application.PreviewTool(ToolExecutionRequest{
		Tool:      &tool,
		Arguments: json.RawMessage(`{"reason":"sandbox"}`),
		Mode:      "validate",
	})
	if err != nil {
		t.Fatal(err)
	}
	if preview == nil {
		t.Fatal("expected preview payload")
	}
	_, err = application.ExecuteTool(ToolExecutionRequest{
		ToolName:  "missing-tool",
		Arguments: json.RawMessage(`{}`),
		Mode:      "execute_readonly",
		Approved:  true,
	})
	if err == nil {
		t.Fatal("expected missing tool failure")
	}
}
