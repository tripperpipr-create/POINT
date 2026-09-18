package companion_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/companion"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/flowruntime"
	"local-agent-workbench/internal/providers"
	"local-agent-workbench/internal/storage"
	"local-agent-workbench/internal/workspace"
)

type companionModel struct {
	content string
}

func (m companionModel) Stream(_ context.Context, _ providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	if err := emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: m.content}); err != nil {
		return err
	}
	return emit(providers.ModelEvent{Kind: providers.EventUsage, InputTokens: 120, OutputTokens: 45})
}

type recordingCompanionModel struct {
	contents []string
	requests []providers.ModelRequest
}

func (m *recordingCompanionModel) Stream(_ context.Context, request providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	index := len(m.requests)
	m.requests = append(m.requests, request)
	content := m.contents[min(index, len(m.contents)-1)]
	if err := emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: content}); err != nil {
		return err
	}
	return emit(providers.ModelEvent{Kind: providers.EventUsage, InputTokens: 80, OutputTokens: 20})
}

type companionProjectContext struct{}

func (companionProjectContext) ProjectMap(context.Context, int) (workspace.ProjectMap, error) {
	return workspace.ProjectMap{
		FilesByLanguage: map[string]int{"Go": 12}, TopDirectories: []string{"internal", "vscode-extension"},
		Symbols: []string{"Service", "CompanionChat"}, Status: workspace.IndexStatus{State: "ready", Files: 12, Symbols: 2},
	}, nil
}

func (companionProjectContext) SearchContextWithRelations(context.Context, string, int, int, bool) (workspace.ContextSearchResult, error) {
	return workspace.ContextSearchResult{
		Chunks: []workspace.RelevantChunk{{IndexedChunk: workspace.IndexedChunk{
			Path: "internal/companion/service.go", StartLine: 80, EndLine: 110, Language: "Go",
			Content: "func (s Service) Chat() { /* api_key=do-not-send-this-secret */ }",
		}, Score: 42}},
		RelatedFiles: []workspace.RelatedFile{{Path: "internal/companion/service_test.go", Relation: "test"}},
	}, nil
}

func TestCompanionProposalDoesNotAutoMutate(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "companion.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_ = store.SaveWorkspace(context.Background(), domain.Workspace{ID: "ws", Path: t.TempDir(), Name: "demo"})
	_ = store.SaveBlueprint(context.Background(), domain.AgentBlueprint{
		ID: "bp", Name: "Scout", SystemPrompt: "scout", Provider: domain.ProviderOllama, PrimaryModel: "llama",
		AllowedTools: []string{"read_file"}, MaxSteps: 3, MaxDurationSeconds: 30, ApprovalMode: domain.ApprovalSafe,
	})
	_, _ = store.EnsureProjectAgentsForWorkspace(context.Background(), "ws")

	svc := companion.Service{Store: store}
	response, err := svc.Chat(context.Background(), companion.ChatRequest{
		WorkspaceID: "ws", Message: "Добавь логирование в модуль auth",
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Proposal == nil {
		t.Fatal("expected proposal")
	}
	if response.Proposal.Status != "pending" {
		t.Fatalf("proposal should stay pending, got %s", response.Proposal.Status)
	}
	quests, err := store.ListQuests(context.Background(), "ws")
	if err != nil {
		t.Fatal(err)
	}
	if len(quests) != 0 {
		t.Fatalf("companion must not create quests without explicit Start, got %d", len(quests))
	}
}

func TestExplicitFlowRequestCreatesReviewableDraftWithoutCallingModel(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "companion-flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	workspaceID := "ws-flow"
	now := time.Now().UTC()
	if err = store.SaveWorkspace(ctx, domain.Workspace{ID: workspaceID, Path: t.TempDir(), Name: "flow"}); err != nil {
		t.Fatal(err)
	}
	for _, agent := range []domain.ProjectAgent{
		{
			ID: "backend", WorkspaceID: workspaceID, Name: "Backend", RoleDescription: "Backend auth implementer",
			SystemPrompt: "implement", Provider: domain.ProviderOllama, PrimaryModel: "local", AllowedTools: []string{"read_file"},
			CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: "reviewer", WorkspaceID: workspaceID, Name: "Security reviewer", RoleDescription: "Auth security review",
			SystemPrompt: "review", Provider: domain.ProviderOllama, PrimaryModel: "local", AllowedTools: []string{"read_file"},
			CreatedAt: now, UpdatedAt: now,
		},
	} {
		if err = store.SaveProjectAgent(ctx, agent); err != nil {
			t.Fatal(err)
		}
	}
	if err = store.SaveCompanionConfig(ctx, domain.CompanionConfig{
		ID: "companion-flow", WorkspaceID: workspaceID, Preset: "balanced", Provider: domain.ProviderOllama,
		Model: "configured-model", Criticality: 50, Creativity: 50, Verbosity: 50, Initiative: 50,
		QuestionStrictness: 70, RiskTolerance: 30, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	model := &recordingCompanionModel{contents: []string{`{"reply":"must not run","level":"suggestion","questions":[],"proposal":null}`}}
	svc := companion.Service{Store: store, ModelFactory: func(providers.Config) (providers.Model, error) { return model, nil }}

	response, err := svc.Chat(ctx, companion.ChatRequest{WorkspaceID: workspaceID, Message: "Create an important Flow for auth review"})
	if err != nil {
		t.Fatal(err)
	}
	if response.ActionProposal == nil || response.Proposal != nil {
		t.Fatalf("typed action proposal=%#v quest proposal=%#v", response.ActionProposal, response.Proposal)
	}
	proposal := response.ActionProposal
	if proposal.Kind != domain.CompanionActionCreateFlow || proposal.Status != "pending" || proposal.Flow == nil {
		t.Fatalf("action proposal=%#v", proposal)
	}
	if err = flowruntime.ValidateGraph(*proposal.Flow); err != nil {
		t.Fatalf("invalid proposed flow: %v", err)
	}
	if len(model.requests) != 0 {
		t.Fatalf("explicit Hub mutation must use the typed deterministic draft path, model requests=%d", len(model.requests))
	}
	flows, err := store.ListFlows(ctx, workspaceID)
	if err != nil || len(flows) != 0 {
		t.Fatalf("flow was persisted before confirmation: %#v err=%v", flows, err)
	}
	proposals, err := store.ListCompanionActionProposals(ctx, workspaceID)
	if err != nil || len(proposals) != 1 || proposals[0].ID != proposal.ID {
		t.Fatalf("durable action proposals=%#v err=%v", proposals, err)
	}
	messages, err := store.ListCompanionMessages(ctx, workspaceID, 10)
	if err != nil || len(messages) != 2 || messages[1].ActionProposalID != proposal.ID {
		t.Fatalf("action provenance messages=%#v err=%v", messages, err)
	}
}

func TestExplicitFlowRequestWithoutAgentsAsksForAgent(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "companion-flow-no-agents.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err = store.SaveWorkspace(ctx, domain.Workspace{ID: "ws-empty", Path: t.TempDir(), Name: "empty"}); err != nil {
		t.Fatal(err)
	}
	response, err := (companion.Service{Store: store}).Chat(ctx, companion.ChatRequest{WorkspaceID: "ws-empty", Message: "Create a Flow"})
	if err != nil {
		t.Fatal(err)
	}
	if response.ActionProposal != nil || len(response.Questions) != 1 {
		t.Fatalf("response=%#v", response)
	}
	proposals, err := store.ListCompanionActionProposals(ctx, "ws-empty")
	if err != nil || len(proposals) != 0 {
		t.Fatalf("unexpected proposals=%#v err=%v", proposals, err)
	}
}

func TestProviderBackedCompanionPersistsValidatedProposalAndUsage(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "companion-model.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	workspaceID := "ws-model"
	if err = store.SaveWorkspace(ctx, domain.Workspace{ID: workspaceID, Path: t.TempDir(), Name: "demo"}); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveProjectAgent(ctx, domain.ProjectAgent{
		ID: "project-agent", WorkspaceID: workspaceID, Name: "Builder", SystemPrompt: "Build carefully",
		Provider: domain.ProviderOllama, PrimaryModel: "test", AllowedTools: []string{"read_file"},
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	config := domain.CompanionConfig{
		ID: "companion-model", WorkspaceID: workspaceID, Preset: "balanced",
		Provider: domain.ProviderOpenAI, ProviderPreset: "openai", BaseURL: "https://example.test/v1", Model: "planner",
		Temperature: 0.2, MaxOutputTokens: 1200, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err = store.SaveCompanionConfig(ctx, config); err != nil {
		t.Fatal(err)
	}
	modelJSON := `{"reply":"Предлагаю безопасный план.","level":"warning","questions":[],"proposal":{"title":"Усилить auth","rationale":"Нужно закрыть риск","unknowns":["Поддерживаемые клиенты"],"objectives":["Сохранить API"],"constraints":[],"definitionOfDone":["Проверки пройдены"],"importance":"important"}}`
	var captured providers.Config
	svc := companion.Service{
		Store: store,
		ModelFactory: func(cfg providers.Config) (providers.Model, error) {
			captured = cfg
			return companionModel{content: modelJSON}, nil
		},
	}
	response, err := svc.Chat(ctx, companion.ChatRequest{WorkspaceID: workspaceID, Message: "Спланируй auth", APIKey: "transient-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if response.Mode != "model" || response.Provider != string(domain.ProviderOpenAI) || response.Model != "planner" || response.Proposal == nil {
		t.Fatalf("model response=%#v", response)
	}
	if captured.APIKey != "transient-secret" || captured.BaseURL != config.BaseURL || captured.Kind != config.Provider {
		t.Fatalf("provider config=%#v", captured)
	}
	if response.Proposal.Status != "pending" || response.Proposal.Importance != domain.QuestImportant || len(response.Proposal.TeamAgentIDs) != 1 {
		t.Fatalf("proposal validation=%#v", response.Proposal)
	}
	if !strings.Contains(strings.Join(response.Proposal.Constraints, " "), "Change Set") {
		t.Fatalf("mandatory sandbox constraint missing: %#v", response.Proposal.Constraints)
	}
	quests, err := store.ListQuests(ctx, workspaceID)
	if err != nil || len(quests) != 0 {
		t.Fatalf("model companion mutated quests: %#v err=%v", quests, err)
	}
	usage, err := store.ListUsageRecords(ctx, workspaceID, 10)
	if err != nil || len(usage) != 1 {
		t.Fatalf("companion usage=%#v err=%v", usage, err)
	}
	if usage[0].ProjectAgentID != config.ID || usage[0].TotalTokens != 165 || usage[0].Outcome != "companion_model" {
		t.Fatalf("companion usage correlation=%#v", usage[0])
	}
	restored, err := store.GetCompanionConfig(ctx, workspaceID)
	if err != nil || restored.Provider != config.Provider || restored.Model != config.Model || restored.MaxOutputTokens != 1200 {
		t.Fatalf("companion model config=%#v err=%v", restored, err)
	}
}

func TestProviderBackedCompanionFallsBackOnInvalidModelJSON(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "companion-fallback.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	workspaceID := "ws-fallback"
	_ = store.SaveWorkspace(ctx, domain.Workspace{ID: workspaceID, Path: t.TempDir(), Name: "demo"})
	_ = store.SaveCompanionConfig(ctx, domain.CompanionConfig{
		ID: "companion-fallback", WorkspaceID: workspaceID, Preset: "balanced", Provider: domain.ProviderOllama,
		BaseURL: "http://127.0.0.1:11434", Model: "planner", Temperature: 0.2, MaxOutputTokens: 1200,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	svc := companion.Service{Store: store, ModelFactory: func(providers.Config) (providers.Model, error) {
		return companionModel{content: "not-json"}, nil
	}}
	response, err := svc.Chat(ctx, companion.ChatRequest{WorkspaceID: workspaceID, Message: "Добавь логирование"})
	if err != nil {
		t.Fatal(err)
	}
	if response.Mode != "deterministic" || response.Proposal == nil || response.FallbackReason == "" {
		t.Fatalf("fallback response=%#v", response)
	}
	usage, err := store.ListUsageRecords(ctx, workspaceID, 10)
	if err != nil || len(usage) != 1 || usage[0].Outcome != "companion_model_invalid" {
		t.Fatalf("invalid response usage=%#v err=%v", usage, err)
	}
}

func TestProviderBackedCompanionAcceptsUnknownJSONFields(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "companion-extra-json.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	workspaceID := "ws-extra-json"
	_ = store.SaveWorkspace(ctx, domain.Workspace{ID: workspaceID, Path: t.TempDir(), Name: "demo"})
	_ = store.SaveCompanionConfig(ctx, domain.CompanionConfig{
		ID: "companion-extra", WorkspaceID: workspaceID, Preset: "balanced", Provider: domain.ProviderOllama,
		BaseURL: "http://127.0.0.1:11434", Model: "planner", Temperature: 0.2, MaxOutputTokens: 1200,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	svc := companion.Service{Store: store, ModelFactory: func(providers.Config) (providers.Model, error) {
		return companionModel{content: `{"reply":"Keep the model answer","level":"suggestion","questions":[],"proposal":null,"tone":"calm"}`}, nil
	}}
	response, err := svc.Chat(ctx, companion.ChatRequest{WorkspaceID: workspaceID, Message: "Что не так в auth.go?"})
	if err != nil {
		t.Fatal(err)
	}
	if response.Mode != "model" || response.Reply != "Keep the model answer" || response.FallbackReason != "" {
		t.Fatalf("extra JSON fields should not fall back: %#v", response)
	}
}

func TestDeterministicCompanionContinuesFromHistory(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "companion-followup.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	workspaceID := "ws-followup"
	now := time.Now().UTC()
	if err = store.SaveWorkspace(ctx, domain.Workspace{ID: workspaceID, Path: t.TempDir(), Name: "followup"}); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveProjectAgent(ctx, domain.ProjectAgent{
		ID: "fixer", WorkspaceID: workspaceID, Name: "Fixer", RoleDescription: "Debug",
		SystemPrompt: "fix", Provider: domain.ProviderOllama, PrimaryModel: "local", AllowedTools: []string{"read_file"},
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	svc := companion.Service{Store: store}
	first, err := svc.Chat(ctx, companion.ChatRequest{
		WorkspaceID: workspaceID,
		Message:     "Какие ошибки сейчас?",
		Focus:       companion.ChatFocus{File: "auth.go", Line: 40, Run: "go test ./..."},
	})
	if err != nil || first.Mode != "deterministic" || first.Reply == "" {
		t.Fatalf("first deterministic reply=%#v err=%v", first, err)
	}
	follow, err := svc.Chat(ctx, companion.ChatRequest{
		WorkspaceID: workspaceID,
		Message:     "Продолжи подробнее",
		Focus:       companion.ChatFocus{File: "auth.go", Line: 40, Snippet: "func Login() {}"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if follow.Proposal != nil || !strings.Contains(follow.Reply, "Продолжаю предыдущий ответ") || !strings.Contains(follow.Reply, first.Reply[:min(40, len(first.Reply))]) {
		t.Fatalf("follow-up ignored history: %#v", follow)
	}
	if !strings.Contains(follow.Reply, "auth.go:40") || !strings.Contains(follow.Reply, "func Login()") {
		t.Fatalf("follow-up lost live focus: %q", follow.Reply)
	}
}

func TestCompanionPersistsHistoryAndGroundsLaterTurns(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "companion-history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	workspaceID := "ws-history"
	now := time.Now().UTC()
	if err = store.SaveWorkspace(ctx, domain.Workspace{ID: workspaceID, Path: t.TempDir(), Name: "history"}); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveProjectAgent(ctx, domain.ProjectAgent{
		ID: "architect", WorkspaceID: workspaceID, Name: "Architect", RoleDescription: "Architecture reviewer",
		SystemPrompt: "plan", Provider: domain.ProviderOllama, PrimaryModel: "local", AllowedTools: []string{"read_file"},
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveMemory(ctx, domain.MemoryRecord{
		ID: "memory", WorkspaceID: workspaceID, Kind: domain.MemoryProject, Content: "Companion must preserve the public API. token=memory-secret-value",
		Source: "architecture decision", Confidence: 0.9, Pinned: true, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	cfg, _ := companion.PresetDefaults("balanced")
	cfg.ID, cfg.WorkspaceID = "companion-history", workspaceID
	cfg.Provider, cfg.ProviderPreset, cfg.BaseURL, cfg.Model = domain.ProviderOpenAI, "openai", "https://example.test/v1", "planner"
	cfg.Temperature, cfg.MaxOutputTokens, cfg.CreatedAt, cfg.UpdatedAt = 0.2, 1200, now, now
	if err = store.SaveCompanionConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	model := &recordingCompanionModel{contents: []string{
		`{"reply":"First grounded answer","level":"suggestion","questions":["Confirm the public API scope?"],"proposal":null}`,
		`{"reply":"Second grounded answer","level":"suggestion","questions":[],"proposal":null}`,
	}}
	svc := companion.Service{
		Store: store, ProjectContext: companionProjectContext{},
		ModelFactory: func(providers.Config) (providers.Model, error) { return model, nil },
	}
	first, err := svc.Chat(ctx, companion.ChatRequest{WorkspaceID: workspaceID, Message: "Inspect Companion chat context"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Reply != "First grounded answer" || !containsFact(first.FactsUsed, "codeContext=internal/companion/service.go") {
		t.Fatalf("first response=%#v", first)
	}
	if _, err = svc.Chat(ctx, companion.ChatRequest{WorkspaceID: workspaceID, Message: "Continue from the previous answer"}); err != nil {
		t.Fatal(err)
	}
	messages, err := store.ListCompanionMessages(ctx, workspaceID, 20)
	if err != nil || len(messages) != 4 {
		t.Fatalf("history=%#v err=%v", messages, err)
	}
	if messages[0].Role != "user" || messages[1].Role != "assistant" || messages[2].Role != "user" || messages[3].Role != "assistant" {
		t.Fatalf("history order=%#v", messages)
	}
	if messages[1].Provider != string(domain.ProviderOpenAI) || messages[1].Model != "planner" || messages[1].Mode != "model" {
		t.Fatalf("assistant provenance=%#v", messages[1])
	}
	if len(messages[1].Questions) != 1 || messages[1].Questions[0] != "Confirm the public API scope?" {
		t.Fatalf("assistant questions were not persisted=%#v", messages[1].Questions)
	}
	if messages[1].UsageRecordID == "" || messages[1].InputTokens != 80 || messages[1].OutputTokens != 20 || messages[1].TotalTokens != 100 || messages[1].LatencyMs < 0 {
		t.Fatalf("assistant usage trace=%#v", messages[1])
	}
	if !containsFact(messages[1].FactsUsed, "codeContext=internal/companion/service.go") {
		t.Fatalf("assistant facts=%#v", messages[1].FactsUsed)
	}
	if len(model.requests) != 2 {
		t.Fatalf("model requests=%d", len(model.requests))
	}
	encoded, err := json.Marshal(model.requests[1].Messages)
	if err != nil {
		t.Fatal(err)
	}
	prompt := string(encoded)
	for _, expected := range []string{"First grounded answer", "Inspect Companion chat context", "internal/companion/service.go", "[REDACTED]"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("second request missing %q: %s", expected, prompt)
		}
	}
	if strings.Contains(prompt, "memory-secret-value") || strings.Contains(prompt, "do-not-send-this-secret") {
		t.Fatalf("secret leaked into model context: %s", prompt)
	}
}

func TestDeterministicCompanionExplainsUsageWithoutCreatingQuest(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "companion-usage.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	workspaceID := "ws-usage"
	now := time.Now().UTC()
	currentStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	if err = store.SaveWorkspace(ctx, domain.Workspace{ID: workspaceID, Path: t.TempDir(), Name: "usage"}); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveProjectAgent(ctx, domain.ProjectAgent{
		ID: "architect", WorkspaceID: workspaceID, Name: "Architect", RoleDescription: "Architecture",
		SystemPrompt: "analyze", Provider: domain.ProviderOllama, PrimaryModel: "local", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	knownCost := int64(25)
	for _, record := range []domain.UsageRecord{
		{ID: "current-main", WorkspaceID: workspaceID, ProjectAgentID: "architect", Provider: "ollama", Model: "big", TotalTokens: 9000, Outcome: "completed", CreatedAt: now},
		{ID: "current-secondary", WorkspaceID: workspaceID, ProjectAgentID: "architect", Provider: "ollama", Model: "small", TotalTokens: 1000, CostCents: &knownCost, Outcome: "completed", CreatedAt: now.Add(-time.Minute)},
		{ID: "previous", WorkspaceID: workspaceID, ProjectAgentID: "architect", Provider: "ollama", Model: "small", TotalTokens: 5000, CostCents: &knownCost, Outcome: "completed", CreatedAt: currentStart.Add(-time.Hour)},
	} {
		if err = store.InsertUsageRecord(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	response, err := (companion.Service{Store: store}).Chat(ctx, companion.ChatRequest{
		WorkspaceID: workspaceID, Message: "Почему вырос расход токенов?",
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Proposal != nil || response.Mode != "deterministic" || response.Level != "warning" {
		t.Fatalf("usage response routing=%#v", response)
	}
	for _, expected := range []string{"10000", "5000", "+100%", "ollama/big", "стоимость не сообщена"} {
		if !strings.Contains(response.Reply, expected) {
			t.Fatalf("usage reply missing %q: %s", expected, response.Reply)
		}
	}
	if !containsFact(response.FactsUsed, "usageCurrentMonthRecords=2 tokens=10000") || !containsFact(response.FactsUsed, "usageTopModel=ollama/big tokens=9000") {
		t.Fatalf("usage facts=%#v", response.FactsUsed)
	}
	proposals, err := store.ListQuestProposals(ctx, workspaceID)
	if err != nil || len(proposals) != 0 {
		t.Fatalf("usage question created quest proposals=%#v err=%v", proposals, err)
	}
}

func TestCompanionPresetDrivesSemanticTeamFlowAndVerification(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "companion-planning.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	workspaceID := "ws-planning"
	now := time.Now().UTC()
	_ = store.SaveWorkspace(ctx, domain.Workspace{ID: workspaceID, Path: t.TempDir(), Name: "planning"})
	for _, agent := range []domain.ProjectAgent{
		{ID: "frontend", WorkspaceID: workspaceID, Name: "Frontend", RoleDescription: "UI developer", SystemPrompt: "ui", Provider: domain.ProviderOllama, PrimaryModel: "local", CreatedAt: now, UpdatedAt: now},
		{ID: "security", WorkspaceID: workspaceID, Name: "Security", RoleDescription: "Security auth backend reviewer", SystemPrompt: "security", Provider: domain.ProviderOllama, PrimaryModel: "local", TasksCompleted: 10, SuccessCount: 9, CreatedAt: now, UpdatedAt: now},
	} {
		if err = store.SaveProjectAgent(ctx, agent); err != nil {
			t.Fatal(err)
		}
	}
	flow := domain.FlowGraph{
		ID: "auth-flow", WorkspaceID: workspaceID, Name: "Security auth review", Description: "Verify authentication changes",
		Nodes: []domain.FlowNode{{ID: "input", Kind: domain.FlowNodeInput, Name: "Input"}, {ID: "output", Kind: domain.FlowNodeOutput, Name: "Output"}},
		Edges: []domain.FlowEdge{{ID: "edge", From: "input", To: "output"}}, CreatedAt: now, UpdatedAt: now,
	}
	if err = store.SaveFlow(ctx, flow); err != nil {
		t.Fatal(err)
	}
	cfg, ok := companion.PresetDefaults("cautious")
	if !ok {
		t.Fatal("cautious preset missing")
	}
	cfg.ID, cfg.WorkspaceID, cfg.CreatedAt, cfg.UpdatedAt = "companion-cautious", workspaceID, now, now
	if err = store.SaveCompanionConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	proposal, err := (companion.Service{Store: store}).ProposeQuest(ctx, companion.RecommendRequest{
		WorkspaceID: workspaceID, Goal: "Critical security auth hardening in production",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(proposal.TeamAgentIDs) == 0 || proposal.TeamAgentIDs[0] != "security" {
		t.Fatalf("semantic team=%#v", proposal.TeamAgentIDs)
	}
	if proposal.FlowID != flow.ID {
		t.Fatalf("flow=%q want %q", proposal.FlowID, flow.ID)
	}
	if proposal.Importance != domain.QuestCritical || proposal.EstimateTokens <= 0 {
		t.Fatalf("importance/estimate=%s/%d", proposal.Importance, proposal.EstimateTokens)
	}
	if !strings.Contains(strings.ToLower(strings.Join(proposal.Constraints, " ")), "rollback") {
		t.Fatalf("cautious constraints=%#v", proposal.Constraints)
	}
	if !strings.Contains(strings.ToLower(strings.Join(proposal.DefinitionOfDone, " ")), "тест") {
		t.Fatalf("criticality DoD=%#v", proposal.DefinitionOfDone)
	}
}

func containsFact(facts []string, needle string) bool {
	for _, fact := range facts {
		if strings.Contains(fact, needle) {
			return true
		}
	}
	return false
}

func TestCompanionStudioPresetsAreSupported(t *testing.T) {
	tests := map[string]struct {
		criticality int
		initiative  int
	}{
		"technical-lead":     {criticality: 65, initiative: 65},
		"critical-architect": {criticality: 90, initiative: 55},
		"mentor":             {criticality: 55, initiative: 60},
		"product-engineer":   {criticality: 55, initiative: 85},
		"custom":             {criticality: 50, initiative: 50},
	}
	for preset, expected := range tests {
		cfg, ok := companion.PresetDefaults(preset)
		if !ok || cfg.Preset != preset || cfg.Criticality != expected.criticality || cfg.Initiative != expected.initiative {
			t.Fatalf("preset %q = %#v, supported=%t", preset, cfg, ok)
		}
	}
}

type chunkedCompanionModel struct {
	chunks []string
}

func (m chunkedCompanionModel) Stream(_ context.Context, _ providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	for _, chunk := range m.chunks {
		if err := emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: chunk}); err != nil {
			return err
		}
	}
	return emit(providers.ModelEvent{Kind: providers.EventUsage, InputTokens: 10, OutputTokens: 20})
}

// Модель, которая генерирует, пока её не остановят.
type blockingCompanionModel struct {
	started  chan struct{}
	finished chan error
}

func (m *blockingCompanionModel) Stream(ctx context.Context, _ providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	close(m.started)
	if err := emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "начал отвечать"}); err != nil {
		return err
	}
	// Ждём либо отмены, либо срабатывания страховки: живая модель здесь
	// продолжала бы писать токены и тратить деньги.
	select {
	case <-ctx.Done():
		m.finished <- ctx.Err()
		return ctx.Err()
	case <-time.After(5 * time.Second):
		m.finished <- nil
		return nil
	}
}
