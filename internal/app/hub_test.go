package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

func TestBootstrapIncludesHubOntology(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o600)
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	if err = application.SetWorkspaceBoundary(root); err != nil {
		t.Fatal(err)
	}
	if _, err = application.OpenWorkspace(root); err != nil {
		t.Fatal(err)
	}
	boot, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	if len(boot.Blueprints) == 0 {
		t.Fatal("expected blueprints from profile bridge")
	}
	if len(boot.ProjectAgents) != 0 {
		t.Fatalf("project agents must be added explicitly, got %d", len(boot.ProjectAgents))
	}
	agent, err := application.SaveProjectAgent(domain.ProjectAgentFromBlueprint(boot.CurrentWorkspace.ID, boot.Blueprints[0]))
	if err != nil {
		t.Fatal(err)
	}
	boot, err = application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	if len(boot.ProjectAgents) != 1 || boot.ProjectAgents[0].ID != agent.ID {
		t.Fatalf("expected one explicitly added project agent, got %#v", boot.ProjectAgents)
	}
	for _, agent := range boot.ProjectAgents {
		if agent.WorkspaceID == "" {
			t.Fatal("project agent missing workspace_id")
		}
	}
	if len(boot.Skills) == 0 {
		t.Fatal("expected seeded skills")
	}
	if boot.Companion == nil {
		t.Fatal("expected companion config")
	}
	if boot.ModelCatalog == nil {
		t.Fatal("expected model catalog")
	}
}

func TestCompanionInterventionDismissalIsReversibleAndWorkspaceScoped(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	firstRoot := t.TempDir()
	secondRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(firstRoot, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secondRoot, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	if err = application.SetWorkspaceBoundary(firstRoot); err != nil {
		t.Fatal(err)
	}
	if _, err = application.OpenWorkspace(firstRoot); err != nil {
		t.Fatal(err)
	}
	first, err := application.Bootstrap()
	if err != nil || len(first.CompanionInterventions) == 0 {
		t.Fatalf("initial interventions=%#v err=%v", first.CompanionInterventions, err)
	}
	var target domain.CompanionIntervention
	for _, item := range first.CompanionInterventions {
		if item.ID == "connections-empty" {
			target = item
			break
		}
	}
	if target.ID == "" {
		t.Fatalf("connection intervention is missing: %#v", first.CompanionInterventions)
	}
	if err = application.DismissCompanionIntervention(target.ID, target.OccurrenceKey); err != nil {
		t.Fatal(err)
	}
	hidden, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	if hidden.CompanionDismissedCount != 1 {
		t.Fatalf("dismissed count=%d", hidden.CompanionDismissedCount)
	}
	for _, item := range hidden.CompanionInterventions {
		if item.OccurrenceKey == target.OccurrenceKey {
			t.Fatalf("dismissed occurrence remains visible: %#v", item)
		}
	}
	now := time.Now().UTC()
	connection := domain.Connection{
		ID: "connected", Provider: domain.ProviderOllama, DisplayName: "Local", Status: domain.ConnectionConnected,
		CreatedAt: now, UpdatedAt: now,
	}
	if err = application.store.SaveConnection(context.Background(), connection); err != nil {
		t.Fatal(err)
	}
	resolved, err := application.Bootstrap()
	if err != nil || resolved.CompanionDismissedCount != 0 {
		t.Fatalf("resolved dismissal was not pruned: count=%d err=%v", resolved.CompanionDismissedCount, err)
	}
	// Speak TTL keeps soft stamps across transient clears; this test covers dismissal identity, not cooldown.
	wsID := ""
	if resolved.Companion != nil {
		wsID = resolved.Companion.WorkspaceID
	}
	if wsID == "" {
		t.Fatal("companion workspace id missing")
	}
	if err = application.store.SaveSetting(context.Background(), companionGateMemorySettingKey(wsID), "{}"); err != nil {
		t.Fatal(err)
	}
	connection.Status = domain.ConnectionUnknown
	connection.UpdatedAt = now.Add(time.Second)
	if err = application.store.SaveConnection(context.Background(), connection); err != nil {
		t.Fatal(err)
	}
	reappeared, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	foundAgain := false
	for _, item := range reappeared.CompanionInterventions {
		foundAgain = foundAgain || item.OccurrenceKey == target.OccurrenceKey
	}
	if !foundAgain {
		t.Fatalf("resolved problem did not reappear as a new occurrence: %#v", reappeared.CompanionInterventions)
	}
	if err = application.DismissCompanionIntervention(target.ID, target.OccurrenceKey); err != nil {
		t.Fatal(err)
	}
	if err = application.SetWorkspaceBoundary(secondRoot); err != nil {
		t.Fatal(err)
	}
	if _, err = application.OpenWorkspace(secondRoot); err != nil {
		t.Fatal(err)
	}
	second, err := application.Bootstrap()
	if err != nil || second.CompanionDismissedCount != 0 {
		t.Fatalf("dismissal leaked into second workspace: count=%d err=%v", second.CompanionDismissedCount, err)
	}
	if err = application.SetWorkspaceBoundary(firstRoot); err != nil {
		t.Fatal(err)
	}
	if _, err = application.OpenWorkspace(firstRoot); err != nil {
		t.Fatal(err)
	}
	if err = application.RestoreCompanionInterventions(); err != nil {
		t.Fatal(err)
	}
	restored, err := application.Bootstrap()
	if err != nil || restored.CompanionDismissedCount != 0 {
		t.Fatalf("restore count=%d err=%v", restored.CompanionDismissedCount, err)
	}
	found := false
	for _, item := range restored.CompanionInterventions {
		found = found || item.OccurrenceKey == target.OccurrenceKey
	}
	if !found {
		t.Fatalf("restored occurrence is missing: %#v", restored.CompanionInterventions)
	}
}

func TestMemoryCRUDIsOwnerAwareAndWorkspaceScoped(t *testing.T) {
	application := newTestApp(t)
	view, err := application.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	agent, err := application.SaveProjectAgent(domain.ProjectAgent{Name: "Memory owner", PrimaryModel: "model"})
	if err != nil {
		t.Fatal(err)
	}
	memory, err := application.SaveMemory(domain.MemoryRecord{
		Kind: domain.MemoryAgent, OwnerID: agent.ID, Content: "Use the project API", Source: "user", Confidence: 0.8,
	})
	if err != nil || memory.OwnerID != agent.ID {
		t.Fatalf("save owner memory=%#v err=%v", memory, err)
	}
	memory.Content = "Use the stable project API"
	memory.Confidence = 0.9
	updated, err := application.SaveMemory(memory)
	if err != nil || updated.CreatedAt != memory.CreatedAt {
		t.Fatalf("update memory=%#v err=%v", updated, err)
	}
	if err = application.DeleteMemory(memory.ID); err != nil {
		t.Fatal(err)
	}
	memories, err := application.store.ListMemories(context.Background(), view.Workspace.ID)
	if err != nil || len(memories) != 0 {
		t.Fatalf("deleted memory remains=%#v err=%v", memories, err)
	}
	foreign := domain.MemoryRecord{ID: "foreign-memory", WorkspaceID: "other-workspace", Kind: domain.MemoryProject, Content: "foreign", Source: "test", Confidence: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err = application.store.SaveMemory(context.Background(), foreign); err != nil {
		t.Fatal(err)
	}
	if err = application.DeleteMemory(foreign.ID); err == nil {
		t.Fatal("foreign workspace memory deletion must be rejected")
	}
	foreign.Content = "overwrite"
	foreign.WorkspaceID = view.Workspace.ID
	if _, err = application.SaveMemory(foreign); err == nil {
		t.Fatal("foreign workspace memory overwrite must be rejected")
	}
}

func TestContextInspectorShowsRuntimeLayersAndOnlyAmendsInputs(t *testing.T) {
	application := newTestApp(t)
	view, err := application.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	profile := domain.AgentProfile{ID: "agent", Name: "Inspector", SystemPrompt: "system instruction", Model: "test-model", MaxSteps: 2}
	run := domain.Run{
		ID: "run-inspector", AgentID: "runtime-agent", ProfileID: profile.ID, WorkspaceID: view.Workspace.ID,
		Task: "Inspect context", Status: domain.RunRunning, StartedAt: now,
		ConfigurationSnapshot: domain.NewRunConfigurationSnapshot(Version, profile, nil, now),
		ContextItems:          []domain.RunContextItem{{ID: "ctx", Kind: domain.ContextText, Label: "User note", Content: "attached", Size: 8}},
	}
	if err = application.store.SaveRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"tool": "read_file", "result": map[string]any{"content": "evidence"}})
	if err = application.store.Append(context.Background(), domain.Event{
		ID: "event-tool", RunID: run.ID, AgentID: run.AgentID, Type: domain.EventToolFinished,
		Step: 1, Actor: "agent", Data: payload, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	preview, err := application.RunContextInspector(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	categories := map[string]bool{}
	amendable := 0
	for _, item := range preview.Items {
		categories[item.Category] = true
		if item.Amendable {
			amendable++
			if item.ID != "ctx" {
				t.Fatalf("synthetic runtime layer became amendable: %#v", item)
			}
		}
	}
	for _, category := range []string{"System", "Agent", "Quest", "Retrieved context", "Tool results"} {
		if !categories[category] {
			t.Fatalf("missing context category %q: %#v", category, preview.Items)
		}
	}
	if amendable != 1 {
		t.Fatalf("amendable items=%d, want only the supplied context", amendable)
	}
}

func TestIDEObservationsAreBoundedReplaceableAndWorkspaceScoped(t *testing.T) {
	application := newTestApp(t)
	var err error
	firstRoot := t.TempDir()
	secondRoot := t.TempDir()
	if err = os.WriteFile(filepath.Join(firstRoot, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = application.OpenWorkspace(firstRoot); err != nil {
		t.Fatal(err)
	}
	exitCode := 1
	items, err := application.RecordIDEObservations(IDEObservationBatch{Kind: "diagnostic", Replace: true, Items: []domain.IDEObservation{{
		Source: "gopls", Level: "error", Summary: "undefined: handler", Detail: "api_key=must-not-persist", Path: filepath.Join(firstRoot, "main.go"), Line: 4,
	}}})
	if err != nil || len(items) != 1 || items[0].Path != "main.go" || strings.Contains(items[0].Detail, "must-not-persist") {
		t.Fatalf("diagnostic observations=%#v err=%v", items, err)
	}
	if _, err = application.RecordIDEObservations(IDEObservationBatch{Kind: "terminal", Items: []domain.IDEObservation{{
		Source: "Point · Квест", Level: "error", Summary: "tests failed", Detail: "FAIL auth", Command: "go test ./...", ExitCode: &exitCode,
	}}}); err != nil {
		t.Fatal(err)
	}
	boot, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	if len(boot.IDEObservations) != 2 {
		t.Fatalf("IDE observations=%#v", boot.IDEObservations)
	}
	foundDiagnosticIntervention, foundTerminalIntervention := false, false
	for _, item := range boot.CompanionInterventions {
		foundDiagnosticIntervention = foundDiagnosticIntervention || item.ID == "ide-diagnostics"
		foundTerminalIntervention = foundTerminalIntervention || strings.HasPrefix(item.ID, "ide-command-failed-")
	}
	if !foundDiagnosticIntervention || !foundTerminalIntervention {
		t.Fatalf("IDE interventions=%#v", boot.CompanionInterventions)
	}
	if _, err = application.RecordIDEObservations(IDEObservationBatch{Kind: "diagnostic", Items: []domain.IDEObservation{{
		Level: "error", Summary: "outside", Path: secondRoot,
	}}}); err == nil {
		t.Fatal("outside-workspace diagnostic path must be rejected")
	}
	if _, err = application.OpenWorkspace(secondRoot); err != nil {
		t.Fatal(err)
	}
	second, err := application.Bootstrap()
	if err != nil || len(second.IDEObservations) != 0 {
		t.Fatalf("cross-workspace observations=%#v err=%v", second.IDEObservations, err)
	}
	if _, err = application.OpenWorkspace(firstRoot); err != nil {
		t.Fatal(err)
	}
	if _, err = application.RecordIDEObservations(IDEObservationBatch{Kind: "diagnostic", Replace: true, Items: nil}); err != nil {
		t.Fatal(err)
	}
	cleared, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	if len(cleared.IDEObservations) != 1 || cleared.IDEObservations[0].Kind != "terminal" {
		t.Fatalf("diagnostic replacement did not preserve terminal history: %#v", cleared.IDEObservations)
	}
}

func TestBootstrapSurfacesLiveRunDiagnosticCompanionActions(t *testing.T) {
	application := newTestApp(t)
	var err error
	root := t.TempDir()
	if err = os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	view, err := application.OpenWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	boot, err := application.Bootstrap()
	if err != nil || len(boot.Blueprints) == 0 {
		t.Fatalf("bootstrap blueprints=%#v err=%v", boot.Blueprints, err)
	}
	agent := domain.ProjectAgentFromBlueprint(view.Workspace.ID, boot.Blueprints[0])
	agent.Name = "Live diagnostic agent"
	agent, err = application.SaveProjectAgent(agent)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	run := domain.Run{
		ID: "run-live-companion", AgentID: agent.ID, ProfileID: agent.ID, WorkspaceID: view.Workspace.ID,
		Task: "Verify live diagnostics", Provider: string(agent.Provider), Model: agent.PrimaryModel,
		Status: domain.RunWaiting, StartedAt: now.Add(-time.Minute),
		ConfigurationSnapshot: domain.RunConfigurationSnapshot{SchemaVersion: 2, Profile: domain.AgentProfile{
			ID: agent.ID, Name: agent.Name, Provider: agent.Provider, Model: agent.PrimaryModel,
			AllowedTools: []string{"run_command"}, ContextWindowTokens: 4_000, MaxDurationSeconds: 600,
		}},
	}
	if err = application.store.SaveRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	execution := domain.ExecutionInstance{
		ID: "execution-live-companion", WorkspaceID: view.Workspace.ID, ProjectAgentID: agent.ID,
		RunID: run.ID, Task: run.Task, Status: domain.RunWaiting, Snapshot: run.ConfigurationSnapshot, StartedAt: run.StartedAt,
	}
	if err = application.store.SaveExecution(context.Background(), execution); err != nil {
		t.Fatal(err)
	}
	failedResult, _ := json.Marshal(map[string]any{
		"tool": "run_command", "durationMs": 10,
		"result": map[string]any{"ok": false, "error": map[string]any{"code": "exit_nonzero", "message": "tests failed"}},
	})
	for index := 0; index < 2; index++ {
		if err = application.store.Append(context.Background(), domain.Event{
			ID: fmt.Sprintf("event-live-tool-%d", index), RunID: run.ID, AgentID: agent.ID, ExecutionID: execution.ID,
			Type: domain.EventToolFinished, Step: index + 1, Actor: "agent", Data: failedResult, CreatedAt: now.Add(time.Duration(index) * time.Second),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err = application.store.SaveApproval(context.Background(), domain.Approval{
		ID: "approval-live", RunID: run.ID, AgentID: agent.ID, ToolName: "run_command", Reason: "verification",
		Arguments: json.RawMessage(`{"command":"go test ./..."}`), Status: domain.ApprovalPending, CreatedAt: now.Add(-45 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	live, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	foundTool, foundApproval := false, false
	for _, item := range live.CompanionInterventions {
		switch item.ID {
		case "run-tool-failures-" + run.ID:
			foundTool = item.RelatedID == execution.ID && item.ActionKind == domain.CompanionInterventionMessageRun && item.ActionMessage != ""
		case "run-approval-" + run.ID:
			foundApproval = item.RelatedID == execution.ID && item.ActionKind == domain.CompanionInterventionOpenRun
		}
	}
	if !foundTool || !foundApproval {
		t.Fatalf("live Companion actions=%#v", live.CompanionInterventions)
	}
}

func TestBootstrapIsolatesProjectWorlds(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	firstRoot := t.TempDir()
	secondRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(firstRoot, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secondRoot, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())

	first, err := application.OpenWorkspace(firstRoot)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	firstRun := domain.Run{
		ID: "run-world-a", AgentID: "agent-a", ProfileID: "profile-a", WorkspaceID: first.Workspace.ID,
		Task: "secret task from world A", Provider: "ollama", Model: "test", Status: domain.RunCompleted,
		StartedAt: now, ToolsUsed: []string{}, ChangedFiles: []string{"secret.go"},
	}
	if err = application.store.SaveRun(context.Background(), firstRun); err != nil {
		t.Fatal(err)
	}
	if err = application.store.SavePatch(context.Background(), domain.PatchProposal{
		ID: "patch-world-a", RunID: firstRun.ID, Path: "secret.go", Status: "applied", CreatedAt: now,
		Original: "old", Proposed: "new", Diff: "secret diff",
	}); err != nil {
		t.Fatal(err)
	}
	if err = application.store.SaveWorkflowRun(context.Background(), domain.WorkflowRun{
		ID: "wfrun-world-a", WorkflowID: "wf-a", WorkspaceID: first.Workspace.ID, Task: "foreign workflow",
		Status: domain.RunCompleted, StartedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err = application.store.SaveChangeSet(context.Background(), domain.ChangeSet{
		ID: "changeset-world-a", WorkspaceID: first.Workspace.ID, ExecutionID: "exec-a",
		Title: "foreign set", Status: domain.ChangeSetPending, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	firstBoot, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	if len(firstBoot.Workspaces) != 1 || firstBoot.Workspaces[0].ID != first.Workspace.ID {
		t.Fatalf("first world workspaces=%#v", firstBoot.Workspaces)
	}
	if len(firstBoot.Runs) != 1 || firstBoot.Runs[0].ID != firstRun.ID || firstBoot.Runs[0].Task != firstRun.Task {
		t.Fatalf("first world runs=%#v", firstBoot.Runs)
	}
	if len(firstBoot.Changes) != 1 || firstBoot.Changes[0].ID != "patch-world-a" {
		t.Fatalf("first world changes=%#v", firstBoot.Changes)
	}
	if len(firstBoot.WorkflowRuns) != 1 || firstBoot.WorkflowRuns[0].ID != "wfrun-world-a" {
		t.Fatalf("first world workflow runs=%#v", firstBoot.WorkflowRuns)
	}

	second, err := application.OpenWorkspace(secondRoot)
	if err != nil {
		t.Fatal(err)
	}
	secondBoot, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	if len(secondBoot.Workspaces) != 1 || secondBoot.Workspaces[0].ID != second.Workspace.ID {
		t.Fatalf("second world leaked other folders: %#v", secondBoot.Workspaces)
	}
	if len(secondBoot.Runs) != 0 {
		t.Fatalf("runs leaked across worlds: %#v", secondBoot.Runs)
	}
	if len(secondBoot.Changes) != 0 {
		t.Fatalf("patches leaked across worlds: %#v", secondBoot.Changes)
	}
	if len(secondBoot.WorkflowRuns) != 0 {
		t.Fatalf("workflow runs leaked across worlds: %#v", secondBoot.WorkflowRuns)
	}
	if len(secondBoot.Blueprints) == 0 || len(secondBoot.Skills) == 0 {
		t.Fatal("global blueprints and skills must remain visible")
	}
	listed, err := application.Runs()
	if err != nil || len(listed) != 0 {
		t.Fatalf("Runs() leaked %#v err=%v", listed, err)
	}
	if _, err = application.RunDetails(firstRun.ID); err == nil {
		t.Fatal("expected RunDetails of another world to fail")
	}
	if _, err = application.WorkflowRunDetails("wfrun-world-a"); err == nil {
		t.Fatal("expected WorkflowRunDetails of another world to fail")
	}
	if _, err = application.RevertPatch("patch-world-a"); err == nil {
		t.Fatal("expected RevertPatch of another world to fail")
	}
	if _, err = application.Statistics(first.Workspace.ID); err == nil {
		t.Fatal("expected Statistics of another world to fail")
	}
	if _, err = application.ApplyChangeSet("changeset-world-a"); err == nil {
		t.Fatal("expected ApplyChangeSet of another world to fail")
	}
	if _, err = application.RejectChangeSet("changeset-world-a"); err == nil {
		t.Fatal("expected RejectChangeSet of another world to fail")
	}
	if _, err = application.RevertChangeSet("changeset-world-a"); err == nil {
		t.Fatal("expected RevertChangeSet of another world to fail")
	}
	cost := int64(999999)
	if _, err = application.RecordUsage(domain.UsageRecord{
		WorkspaceID: first.Workspace.ID, Provider: "ollama", Model: "test",
		InputTokens: 3, OutputTokens: 1, CostCents: &cost, Outcome: "ok",
	}); err == nil {
		t.Fatal("expected RecordUsage of another world to fail")
	}
	saved, err := application.RecordUsage(domain.UsageRecord{
		Provider: "ollama", Model: "test", InputTokens: 3, OutputTokens: 1, CostCents: &cost, Outcome: "ok",
	})
	if err != nil {
		t.Fatal(err)
	}
	if saved.WorkspaceID != second.Workspace.ID || saved.CostCents != nil {
		t.Fatalf("usage not sanitized: %#v", saved)
	}
}
