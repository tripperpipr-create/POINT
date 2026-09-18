package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

func TestCompatibilityTelemetryIsExactPrivateAndPreviewDoesNotCountAsUse(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "private-project-name.txt"), []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	view, err := application.OpenWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := application.SaveProfile(domain.AgentProfile{
		Name: "Private agent name", SystemPrompt: "private prompt body", Provider: domain.ProviderOllama,
		BaseURL: "http://127.0.0.1:11434", Model: "private-model", AllowedTools: []string{"read_file"},
		MaxSteps: 5, MaxDurationSeconds: 60, ApprovalMode: domain.ApprovalSafe,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = application.PreviewAgentRun(AgentRunPreviewRequest{ProfileID: profile.ID, Task: "private preview task"}); err != nil {
		t.Fatal(err)
	}
	if _, err = application.SaveWorkflow(domain.AgentWorkflow{
		Name: "Private workflow", Steps: []domain.WorkflowStep{{Name: "Inspect", ProfileID: profile.ID, Instruction: "private handoff"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = application.SaveCustomTool(domain.CustomTool{
		Kind: domain.CustomToolCommand, DisplayName: "Private tool", Description: "private description",
		Command: "echo private-secret", CWD: ".", TimeoutSeconds: 30,
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	finished := now.Add(time.Second)
	legacyRun := domain.Run{
		ID: "legacy-history-run", AgentID: "legacy-agent", ProfileID: profile.ID, WorkspaceID: view.Workspace.ID,
		Task: "private historical task", Provider: "ollama", Model: "private-model", Status: domain.RunCompleted,
		StartedAt: now, FinishedAt: &finished, ToolsUsed: []string{}, ChangedFiles: []string{},
	}
	if err = application.store.SaveRun(context.Background(), legacyRun); err != nil {
		t.Fatal(err)
	}
	if _, err = application.RunDetails(legacyRun.ID); err != nil {
		t.Fatal(err)
	}

	stats, err := application.Statistics(view.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	items, ok := stats["compatibilityUsage"].([]domain.CompatibilityUsage)
	if !ok {
		t.Fatalf("compatibility usage type=%T", stats["compatibilityUsage"])
	}
	counts := map[domain.CompatibilityFeature]int64{}
	for _, item := range items {
		if item.WorkspaceID != "" {
			t.Fatalf("public compatibility telemetry leaked workspace scope: %#v", item)
		}
		counts[item.Feature] += item.Count
		if item.ApplicationVersion != Version {
			t.Fatalf("application attribution=%#v", item)
		}
	}
	if counts[domain.CompatibilityProfileSave] != 1 || counts[domain.CompatibilityWorkflowSave] != 1 ||
		counts[domain.CompatibilityCustomCommandSave] != 1 || counts[domain.CompatibilityRunSnapshotRead] != 1 {
		t.Fatalf("compatibility counts=%#v", counts)
	}
	if counts[domain.CompatibilityProfileRunFallback] != 0 {
		t.Fatal("read-only run preview was incorrectly counted as legacy execution")
	}
	encoded, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"Private agent", "private prompt", "private workflow", "private handoff", "echo private", "private-project-name", root} {
		if strings.Contains(strings.ToLower(string(encoded)), strings.ToLower(forbidden)) {
			t.Fatalf("private value %q leaked into compatibility telemetry: %s", forbidden, encoded)
		}
	}
}

func TestPublicCompatibilityUsageMergesScopesWithoutExposingWorkspaceKey(t *testing.T) {
	first := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	last := first.Add(48 * time.Hour)
	items := publicCompatibilityUsage([]domain.CompatibilityUsage{
		{Feature: domain.CompatibilityWorkflowRun, WorkspaceID: "workspace-secret-a", ApplicationVersion: "1.2.2", LegacyVersion: "workflow-snapshot-v1", Count: 2, FirstSeen: first, LastSeen: first},
		{Feature: domain.CompatibilityWorkflowRun, WorkspaceID: "", ApplicationVersion: "1.2.2", LegacyVersion: "workflow-snapshot-v1", Count: 3, FirstSeen: first.Add(time.Hour), LastSeen: last},
	})
	if len(items) != 1 || items[0].WorkspaceID != "" || items[0].Count != 5 || !items[0].FirstSeen.Equal(first) || !items[0].LastSeen.Equal(last) {
		t.Fatalf("public compatibility aggregate=%#v", items)
	}
}
