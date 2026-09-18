package storage_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/storage"
)

func TestOrchestratorConfigIDCannotCrossWorkspaceBoundary(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "orch.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	first := domain.OrchestratorConfig{ID: "orch-shared", WorkspaceID: "ws-a", Preset: "conductor", PlanningDepth: 70, CreatedAt: now, UpdatedAt: now}
	if err = store.SaveOrchestratorConfig(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := first
	second.WorkspaceID = "ws-b"
	second.Preset = "dispatcher"
	if err = store.SaveOrchestratorConfig(ctx, second); err == nil {
		t.Fatal("cross-workspace orchestrator config overwrite must be rejected")
	}
	loaded, err := store.GetOrchestratorConfig(ctx, "ws-a")
	if err != nil || loaded.Preset != "conductor" {
		t.Fatalf("original orchestrator changed: %#v err=%v", loaded, err)
	}
}

func TestParallelMergeLineageAndSupersessionPersistAtomically(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "merge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	source := domain.ChangeSet{
		ID: "set-source", WorkspaceID: "ws", ExecutionID: "exec-source", Kind: "execution",
		Title: "source", Status: domain.ChangeSetSuperseded, SupersededBy: "set-merge", CreatedAt: now, UpdatedAt: now,
	}
	merge := domain.ChangeSet{
		ID: "set-merge", WorkspaceID: "ws", ExecutionID: "exec-merge", Kind: "merge",
		Title: "merge", Status: domain.ChangeSetPending, DependsOn: []string{"set-root"}, Supersedes: []string{source.ID},
		CreatedAt: now, UpdatedAt: now,
	}
	record := domain.SandboxRecord{
		ID: "sandbox-merge", WorkspaceID: "ws", ExecutionID: "exec-merge", Kind: "merge-copy", Path: "sandbox",
		ParentSandboxIDs: []string{"sandbox-a", "sandbox-b"}, ParentExecutionIDs: []string{"exec-a", "exec-b"},
		BaselinePath: "baseline", BaselineChangeSetIDs: []string{merge.ID}, CreatedAt: now,
	}
	execution := domain.ExecutionInstance{
		ID: "exec-merge", WorkspaceID: "ws", ProjectAgentID: "agent", SandboxID: record.ID,
		Task: "merge", Status: domain.RunPending, StartedAt: now,
	}
	if err = store.SaveParallelMergeExecution(ctx, execution, record, &merge, []domain.ChangeSet{source}); err != nil {
		t.Fatal(err)
	}
	loadedRecord, err := store.GetSandbox(ctx, record.ID)
	if err != nil || len(loadedRecord.ParentExecutionIDs) != 2 || len(loadedRecord.BaselineChangeSetIDs) != 1 || loadedRecord.BaselineChangeSetIDs[0] != merge.ID {
		t.Fatalf("sandbox=%#v err=%v", loadedRecord, err)
	}
	loadedMerge, err := store.GetChangeSet(ctx, merge.ID)
	if err != nil || loadedMerge.Kind != "merge" || len(loadedMerge.Supersedes) != 1 || loadedMerge.DependsOn[0] != "set-root" {
		t.Fatalf("merge=%#v err=%v", loadedMerge, err)
	}
	loadedSource, err := store.GetChangeSet(ctx, source.ID)
	if err != nil || loadedSource.Status != domain.ChangeSetSuperseded || loadedSource.SupersededBy != merge.ID {
		t.Fatalf("source=%#v err=%v", loadedSource, err)
	}
}

func TestCompanionConfigIDCannotCrossWorkspaceBoundary(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	first := domain.CompanionConfig{ID: "companion-shared", WorkspaceID: "ws-a", Configured: true, Preset: "balanced", CreatedAt: now, UpdatedAt: now}
	if err = store.SaveCompanionConfig(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := first
	second.WorkspaceID = "ws-b"
	second.Preset = "creative"
	if err = store.SaveCompanionConfig(ctx, second); err == nil {
		t.Fatal("cross-workspace companion config overwrite must be rejected")
	}
	loaded, err := store.GetCompanionConfig(ctx, "ws-a")
	if err != nil || loaded.Preset != "balanced" || !loaded.Configured {
		t.Fatalf("original config changed: %#v err=%v", loaded, err)
	}
}

func TestActorModelFallbacksAndSelectionBreakdownRoundTrip(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "actor-targets.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	now := time.Now().UTC()

	profile := domain.AgentProfile{
		ID: "profile-target", Name: "Target", Model: "deployment", ConnectionID: "conn-target",
		Provider: domain.ProviderAzureOpenAI, ProviderPreset: "azure-openai", BaseURL: "https://resource.invalid/openai/deployments/deployment",
		APIVersion: "2025-04-01-preview", MaxSteps: 20, CreatedAt: now, UpdatedAt: now,
	}
	if err = store.SaveProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	profiles, err := store.ListProfiles(ctx)
	if err != nil || len(profiles) != 1 || profiles[0].ConnectionID != profile.ConnectionID || profiles[0].APIVersion != profile.APIVersion {
		t.Fatalf("profile target did not round-trip: %#v err=%v", profiles, err)
	}

	companionConfig := domain.CompanionConfig{
		ID: "companion-target", WorkspaceID: "ws", Preset: "balanced", ConnectionID: profile.ConnectionID,
		Provider: profile.Provider, ProviderPreset: profile.ProviderPreset, BaseURL: profile.BaseURL, APIVersion: profile.APIVersion,
		Model: profile.Model, CreatedAt: now, UpdatedAt: now,
	}
	if err = store.SaveCompanionConfig(ctx, companionConfig); err != nil {
		t.Fatal(err)
	}
	loadedCompanion, err := store.GetCompanionConfig(ctx, "ws")
	if err != nil || loadedCompanion.ConnectionID != profile.ConnectionID || loadedCompanion.APIVersion != profile.APIVersion {
		t.Fatalf("companion target did not round-trip: %#v err=%v", loadedCompanion, err)
	}

	masterConfig := domain.OrchestratorConfig{
		ID: "master-target", WorkspaceID: "ws", Preset: "conductor", ConnectionID: profile.ConnectionID,
		Provider: profile.Provider, ProviderPreset: profile.ProviderPreset, BaseURL: profile.BaseURL, APIVersion: profile.APIVersion,
		Model: profile.Model, CreatedAt: now, UpdatedAt: now,
	}
	if err = store.SaveOrchestratorConfig(ctx, masterConfig); err != nil {
		t.Fatal(err)
	}
	loadedMaster, err := store.GetOrchestratorConfig(ctx, "ws")
	if err != nil || loadedMaster.ConnectionID != profile.ConnectionID || loadedMaster.APIVersion != profile.APIVersion {
		t.Fatalf("master target did not round-trip: %#v err=%v", loadedMaster, err)
	}

	proposal := domain.QuestProposal{
		ID: "proposal-selection", WorkspaceID: "ws", Title: "Explain selection", Task: "audit",
		Importance: domain.QuestNormal, Status: "draft", CreatedAt: now,
		SelectionBreakdown: []domain.AgentSelectionBreakdown{{
			AgentID: "agent-a", RoleFit: 40, Evidence: 20, Verification: 10, Diversity: 5,
			LoadPenalty: 3, LatencyPenalty: 2, CostPenalty: 1, Total: 69,
			Matched: []string{"audit", "verification"}, ConfirmedSuccesses: 4, Attempts: 5,
		}},
	}
	if err = store.SaveQuestProposal(ctx, proposal); err != nil {
		t.Fatal(err)
	}
	proposals, err := store.ListQuestProposals(ctx, "ws")
	if err != nil || len(proposals) != 1 || len(proposals[0].SelectionBreakdown) != 1 || proposals[0].SelectionBreakdown[0].Total != 69 || len(proposals[0].SelectionBreakdown[0].Matched) != 2 {
		t.Fatalf("selection breakdown did not round-trip: %#v err=%v", proposals, err)
	}
}

func TestCompanionMessageProvenanceAndScopedHistoryDeletion(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	for _, message := range []domain.CompanionMessage{
		{ID: "a", WorkspaceID: "ws-a", Role: "assistant", Content: "answer", Mode: "model", Provider: "openai-compatible", Model: "planner", FactsUsed: []string{"codeContext=main.go:1-3"}, UsageRecordID: "usage-a", InputTokens: 10, OutputTokens: 5, TotalTokens: 15, LatencyMs: 42, CreatedAt: now},
		{ID: "master-a", WorkspaceID: "ws-a", Speaker: "master", Role: "assistant", Content: "keep master", CreatedAt: now.Add(time.Nanosecond)},
		{ID: "log-a", WorkspaceID: "ws-a", Speaker: "companion-log", Role: "assistant", Content: "keep log", CreatedAt: now.Add(2 * time.Nanosecond)},
		{ID: "b", WorkspaceID: "ws-b", Role: "user", Content: "keep", CreatedAt: now},
	} {
		if err = store.SaveCompanionMessage(ctx, message); err != nil {
			t.Fatal(err)
		}
	}
	messages, err := store.ListCompanionMessages(ctx, "ws-a", 10)
	if err != nil || len(messages) != 1 || messages[0].UsageRecordID != "usage-a" || messages[0].TotalTokens != 15 || len(messages[0].FactsUsed) != 1 {
		t.Fatalf("provenance=%#v err=%v", messages, err)
	}
	if err = store.DeleteCompanionMessages(ctx, "ws-a"); err != nil {
		t.Fatal(err)
	}
	removed, _ := store.ListCompanionMessages(ctx, "ws-a", 10)
	master, _ := store.ListChatMessages(ctx, "ws-a", "master", 10)
	logChat, _ := store.ListChatMessages(ctx, "ws-a", "companion-log", 10)
	kept, _ := store.ListCompanionMessages(ctx, "ws-b", 10)
	if len(removed) != 0 || len(master) != 1 || len(logChat) != 1 || len(kept) != 1 || kept[0].Content != "keep" {
		t.Fatalf("scoped deletion removed=%#v master=%#v log=%#v kept=%#v", removed, master, logChat, kept)
	}
}

func TestVersionedMigrationsAndProfileBridge(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()

	versions, err := store.MigrationVersions(context.Background())
	if err != nil {
		t.Fatalf("versions: %v", err)
	}
	if len(versions) < 2 || versions[0] != 1 || versions[1] != 2 {
		t.Fatalf("expected migrations 1,2 got %v", versions)
	}

	profile := domain.AgentProfile{
		ID: "profile-a", Name: "Analyst", RoleDescription: "reads code", SystemPrompt: "be careful",
		Provider: domain.ProviderOllama, Model: "llama3", AllowedTools: []string{"read_file"},
		MaxSteps: 5, MaxDurationSeconds: 60, ApprovalMode: domain.ApprovalSafe,
	}
	// Хранится чертёж: зеркала «профиль → чертёж» больше нет, профиль выводится
	// из чертежа на лету (см. App.storedProfiles).
	if err = store.SaveBlueprint(context.Background(), domain.BlueprintFromProfile(profile)); err != nil {
		t.Fatalf("save blueprint: %v", err)
	}
	blueprints, err := store.ListBlueprints(context.Background())
	if err != nil {
		t.Fatalf("list blueprints: %v", err)
	}
	found := false
	for _, bp := range blueprints {
		if bp.ID == profile.ID && bp.PrimaryModel == profile.Model {
			found = true
		}
	}
	if !found {
		t.Fatalf("чертёж не сохранился")
	}

	ws := domain.Workspace{ID: "ws-1", Path: dir, Name: "demo"}
	if err = store.SaveWorkspace(context.Background(), ws); err != nil {
		t.Fatalf("save workspace: %v", err)
	}
	agents, err := store.EnsureProjectAgentsForWorkspace(context.Background(), ws.ID)
	if err != nil {
		t.Fatalf("ensure agents: %v", err)
	}
	if len(agents) == 0 {
		t.Fatalf("expected project agents")
	}
	for _, agent := range agents {
		if agent.WorkspaceID != ws.ID {
			t.Fatalf("project agent missing workspace_id")
		}
	}
}

// Оценка ответа принадлежит реплике, а не машине, на которой её поставили.
//
// У компаньона отметки лежат в состоянии рабочей области расширения: переставил
// IDE — и «не помогло» исчезло вместе с причиной, по которой ответ считали
// плохим. У Мастера оценка ценнее вдвойне: по ней видно, какие постановки задач
// человек принимает, а какие переделывает.
func TestChatMessageFeedbackIsSetAndCleared(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(filepath.Join(t.TempDir(), "hub.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	if err = store.SaveCompanionMessage(ctx, domain.CompanionMessage{
		ID: "ma-1", WorkspaceID: "ws", Speaker: "master", Role: "assistant", Content: "answer", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	read := func() domain.CompanionMessage {
		messages, listErr := store.ListChatMessages(ctx, "ws", "master", 10)
		if listErr != nil || len(messages) != 1 {
			t.Fatalf("reply lost: %#v err=%v", messages, listErr)
		}
		return messages[0]
	}
	if got := read().Feedback; got != "" {
		t.Fatalf("свежая реплика уже оценена: %q", got)
	}
	if err = store.SetChatMessageFeedback(ctx, "ws", "ma-1", "down"); err != nil {
		t.Fatal(err)
	}
	if got := read().Feedback; got != "down" {
		t.Fatalf("оценка не сохранилась: %q", got)
	}
	// Передумать можно: пустая строка снимает отметку, а не ставит третью.
	if err = store.SetChatMessageFeedback(ctx, "ws", "ma-1", ""); err != nil {
		t.Fatal(err)
	}
	if got := read().Feedback; got != "" {
		t.Fatalf("отметка не снялась: %q", got)
	}
	// Чужая оценка не проходит: колонка хранит только «up», «down» и пустоту.
	if err = store.SetChatMessageFeedback(ctx, "ws", "ma-1", "любопытно"); err != nil {
		t.Fatal(err)
	}
	if got := read().Feedback; got != "" {
		t.Fatalf("в оценку попало произвольное слово: %q", got)
	}
	// Чужая рабочая область не задета.
	if err = store.SetChatMessageFeedback(ctx, "ws-other", "ma-1", "up"); err != nil {
		t.Fatal(err)
	}
	if got := read().Feedback; got != "" {
		t.Fatalf("оценка поставлена из чужой рабочей области: %q", got)
	}
}
