package companion_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/companion"
	"local-agent-workbench/internal/diagnostics"
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

func TestCompanionChatEmitsGatherProgress(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "companion-progress.db"))
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

	type stepEvent struct{ step, status string }
	var events []stepEvent
	svc := companion.Service{Store: store, ProjectContext: companionProjectContext{}}
	_, err = svc.Chat(context.Background(), companion.ChatRequest{
		WorkspaceID: "ws",
		Message:     "Что сейчас в проекте?",
		OnProgress: func(step, status string) {
			events = append(events, stepEvent{step: step, status: status})
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := ""
	for _, event := range events {
		joined += event.step + ":" + event.status + ";"
	}
	for _, need := range []string{"gather:running", "roster:running", "memory:running", "gather:done", "local:running", "local:done"} {
		if !strings.Contains(joined, need) {
			t.Fatalf("missing progress step %q in %q", need, joined)
		}
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

func TestModelPromptIncludesCurrentIDEFocus(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "companion-focus-prompt.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	workspaceID := "ws-focus-prompt"
	now := time.Now().UTC()
	if err = store.SaveWorkspace(ctx, domain.Workspace{ID: workspaceID, Path: t.TempDir(), Name: "focus-prompt"}); err != nil {
		t.Fatal(err)
	}
	cfg, _ := companion.PresetDefaults("balanced")
	cfg.ID, cfg.WorkspaceID = "companion-focus-prompt", workspaceID
	cfg.Provider, cfg.ProviderPreset, cfg.BaseURL, cfg.Model = domain.ProviderOpenAI, "openai", "https://example.test/v1", "planner"
	cfg.Temperature, cfg.MaxOutputTokens, cfg.CreatedAt, cfg.UpdatedAt = 0.2, 1200, now, now
	if err = store.SaveCompanionConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	model := &recordingCompanionModel{contents: []string{`{"reply":"Saw the open file","level":"suggestion","questions":[],"proposal":null}`}}
	svc := companion.Service{Store: store, ModelFactory: func(providers.Config) (providers.Model, error) { return model, nil }}
	if _, err = svc.Chat(ctx, companion.ChatRequest{
		WorkspaceID: workspaceID,
		Message:     "Что не так здесь?",
		Focus:       companion.ChatFocus{File: "auth.go", Line: 40, Language: "go", Snippet: "func Login() {}"},
	}); err != nil {
		t.Fatal(err)
	}
	if len(model.requests) != 1 {
		t.Fatalf("model requests=%d", len(model.requests))
	}
	encoded, err := json.Marshal(model.requests[0].Messages)
	if err != nil {
		t.Fatal(err)
	}
	prompt := string(encoded)
	for _, expected := range []string{"CURRENT IDE FOCUS", "auth.go", "40", "func Login()"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("model prompt missing %q: %s", expected, prompt)
		}
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

func TestCompanionInterventionsArePrioritizedAndNonBlocking(t *testing.T) {
	cfg, _ := companion.PresetDefaults("cautious")
	interventions := companion.BuildInterventions(
		cfg,
		nil,
		[]domain.ExecutionInstance{{ID: "waiting", Status: domain.RunWaiting}, {ID: "failed-1", Status: domain.RunFailed}, {ID: "failed-2", Status: domain.RunFailed}, {ID: "failed-3", Status: domain.RunInterrupted}},
		[]domain.ChangeSet{{ID: "conflict", Title: "Auth changes", Status: domain.ChangeSetConflict}},
		nil,
		nil,
	)
	if len(interventions) < 4 {
		t.Fatalf("interventions=%#v", interventions)
	}
	if interventions[0].Level != "critical" || interventions[0].RelatedID != "conflict" {
		t.Fatalf("critical intervention must be first: %#v", interventions)
	}
	for _, intervention := range interventions {
		if intervention.Level != "suggestion" && intervention.Level != "warning" && intervention.Level != "critical" {
			t.Fatalf("invalid intervention level: %#v", intervention)
		}
		if len(intervention.OccurrenceKey) != 16 {
			t.Fatalf("intervention occurrence key=%q", intervention.OccurrenceKey)
		}
	}
}

func TestUsageFailureInterventionProbesSelectedCompanionConnection(t *testing.T) {
	cfg, _ := companion.PresetDefaults("cautious")
	cfg.Provider = domain.ProviderOpenAI
	cfg.ProviderPreset = "llmux"
	cfg.BaseURL = "https://llmux.example.test/v1/"
	usage := []domain.UsageRecord{
		{ID: "1", Outcome: "error"}, {ID: "2", Outcome: "failed"},
		{ID: "3", Outcome: "ok"}, {ID: "4", Outcome: "ok"}, {ID: "5", Outcome: "ok"},
	}
	connections := []domain.Connection{
		{ID: "other-openai", Provider: domain.ProviderOpenAI, PresetID: "openai", BaseURL: "https://api.openai.com/v1", Status: domain.ConnectionConnected},
		{ID: "selected-llmux", Provider: domain.ProviderOpenAI, PresetID: "llmux", BaseURL: "https://llmux.example.test/v1", Status: domain.ConnectionError},
	}
	interventions := companion.BuildInterventions(cfg, []domain.ProjectAgent{{ID: "agent"}}, nil, nil, usage, connections)
	for _, intervention := range interventions {
		if intervention.ID != "usage-failure-rate" {
			continue
		}
		if intervention.ActionKind != domain.CompanionInterventionProbeConnection || intervention.RelatedID != "selected-llmux" || intervention.ActionLabel != "Проверить связь" {
			t.Fatalf("usage probe action=%#v", intervention)
		}
		withoutSelected := companion.BuildInterventions(cfg, []domain.ProjectAgent{{ID: "agent"}}, nil, nil, usage, connections[:1])
		for _, fallback := range withoutSelected {
			if fallback.ID == "usage-failure-rate" && (fallback.ActionKind != "" || fallback.RelatedID != "" || fallback.ActionLabel != "Открыть подключения") {
				t.Fatalf("usage probe selected the wrong gateway: %#v", fallback)
			}
		}
		return
	}
	t.Fatalf("usage failure intervention is missing: %#v", interventions)
}

func TestUsageFailureInterventionClearsAfterThreeSuccessfulCalls(t *testing.T) {
	now := time.Now().UTC()
	usage := []domain.UsageRecord{
		{Outcome: "completed", CreatedAt: now},
		{Outcome: "success", CreatedAt: now.Add(-time.Second)},
		{Outcome: "ok", CreatedAt: now.Add(-2 * time.Second)},
		{Outcome: "failed", CreatedAt: now.Add(-3 * time.Second)},
		{Outcome: "failed", CreatedAt: now.Add(-4 * time.Second)},
		{Outcome: "failed", CreatedAt: now.Add(-5 * time.Second)},
	}
	items := companion.BuildInterventions(domain.CompanionConfig{}, []domain.ProjectAgent{{ID: "agent"}}, nil, nil, usage, nil)
	for _, item := range items {
		if item.ID == "usage-failure-rate" {
			t.Fatalf("recovered provider must not keep a stale failure-rate warning: %#v", item)
		}
	}
}

func TestUsageFailureOccurrenceKeyIsStableAcrossCounterChanges(t *testing.T) {
	first := domain.CompanionIntervention{ID: "usage-failure-rate", Level: "warning", Title: "Высокая доля", Detail: "3 из 7"}
	second := domain.CompanionIntervention{ID: "usage-failure-rate", Level: "warning", Title: "Высокая доля", Detail: "3 из 8"}
	firstKey := companion.InterventionOccurrenceKey(first, nil)
	secondKey := companion.InterventionOccurrenceKey(second, nil)
	if firstKey != secondKey || len(firstKey) != 16 {
		t.Fatalf("aggregate warning key must be stable and valid: first=%q second=%q", firstKey, secondKey)
	}
}

func TestDismissedInterventionOnlyHidesTheObservedOccurrence(t *testing.T) {
	items := make([]domain.CompanionIntervention, 0, 10)
	for index := 0; index < 10; index++ {
		items = append(items, domain.CompanionIntervention{
			ID: fmt.Sprintf("risk-%d", index), Level: "suggestion", Title: "Risk", Detail: fmt.Sprintf("evidence=%d", index),
		})
	}
	merged := companion.MergeInterventions(items)
	if len(merged) != 10 || merged[0].OccurrenceKey == "" {
		t.Fatalf("merged interventions=%#v", merged)
	}
	visible, hidden := companion.VisibleInterventions(merged, map[string]bool{merged[0].OccurrenceKey: true}, 8)
	if hidden != 1 || len(visible) != 8 || visible[0].ID == merged[0].ID || visible[7].ID != merged[8].ID {
		t.Fatalf("visible=%#v hidden=%d", visible, hidden)
	}
	changed := companion.MergeInterventions([]domain.CompanionIntervention{{ID: merged[0].ID, Level: "warning", Title: "Risk", Detail: "new evidence"}})
	if changed[0].OccurrenceKey == merged[0].OccurrenceKey {
		t.Fatalf("changed evidence reused occurrence key %q", changed[0].OccurrenceKey)
	}
	visible, hidden = companion.VisibleInterventions(changed, map[string]bool{merged[0].OccurrenceKey: true}, 8)
	if hidden != 0 || len(visible) != 1 {
		t.Fatalf("changed occurrence stayed hidden: %#v hidden=%d", visible, hidden)
	}
}

func TestCompanionBudgetAndExecutionInterventions(t *testing.T) {
	cfg, _ := companion.PresetDefaults("cautious")
	now := time.Now().UTC()
	budget := companion.BuildBudgetInterventions(companion.BudgetSnapshot{
		DailyLimitCents: 100, DailyUsedCents: 82, MonthlyLimitCents: 1000, MonthlyUsedCents: 1000, HardStop: true,
	})
	if len(budget) != 2 || budget[0].Level != "warning" || budget[1].Level != "critical" || !strings.Contains(budget[1].Detail, "политикой бюджета") {
		t.Fatalf("budget interventions=%#v", budget)
	}
	run := domain.Run{
		ID: "run-risk", WorkspaceID: "ws", Status: domain.RunRunning, StartedAt: now.Add(-9 * time.Minute),
		ChangedFiles:          []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l"},
		ContextItems:          []domain.RunContextItem{{ID: "context", Size: 13_000, TokenEstimate: 3_250}},
		ConfigurationSnapshot: domain.RunConfigurationSnapshot{Profile: domain.AgentProfile{ContextWindowTokens: 4_000, MaxDurationSeconds: 600}},
	}
	execution := domain.ExecutionInstance{ID: "execution-risk", RunID: run.ID, Status: domain.RunRunning}
	items := companion.BuildExecutionInterventions(cfg, []domain.ExecutionInstance{execution}, []domain.Run{run}, now)
	if len(items) != 3 {
		t.Fatalf("execution interventions=%#v", items)
	}
	merged := companion.MergeInterventions(budget, items)
	if len(merged) != 5 || merged[0].Level != "critical" {
		t.Fatalf("merged interventions=%#v", merged)
	}
}

func TestCompanionRunDiagnosticInterventionsOfferExplicitActions(t *testing.T) {
	cfg, _ := companion.PresetDefaults("cautious")
	execution := domain.ExecutionInstance{ID: "execution-live", RunID: "run-live", Status: domain.RunWaiting}
	diag := diagnostics.RunDiagnostics{
		RunID: "run-live", Health: diagnostics.HealthAttention,
		Tools:      diagnostics.ToolMetrics{Failed: 2, Items: []diagnostics.ToolMetric{{Name: "run_command", Failed: 2}}},
		Approvals:  diagnostics.ApprovalMetrics{Pending: 1, MaxWaitMs: 45_000},
		Context:    diagnostics.ContextMetrics{Compactions: 2, PeakInputTokens: 900, InputBudgetTokens: 1_000},
		Retrieval:  diagnostics.RetrievalMetrics{TruncatedSearches: 1},
		Completion: diagnostics.CompletionMetrics{RevisionRequests: 1},
		Model:      diagnostics.ModelMetrics{Retries: 2},
	}
	items := companion.BuildRunDiagnosticInterventions(cfg, []domain.ExecutionInstance{execution}, []diagnostics.RunDiagnostics{diag})
	if len(items) != 6 {
		t.Fatalf("diagnostic interventions=%#v", items)
	}
	byID := map[string]domain.CompanionIntervention{}
	for _, item := range items {
		byID[item.ID] = item
	}
	approval := byID["run-approval-run-live"]
	if approval.ActionKind != domain.CompanionInterventionOpenRun || approval.RelatedID != execution.ID {
		t.Fatalf("approval intervention=%#v", approval)
	}
	toolFailure := byID["run-tool-failures-run-live"]
	if toolFailure.ActionKind != domain.CompanionInterventionMessageRun || toolFailure.ActionMessage == "" || !strings.Contains(toolFailure.Detail, "run_command") {
		t.Fatalf("tool intervention=%#v", toolFailure)
	}
	merged := companion.MergeInterventions(items)
	if len(merged) != len(items) || merged[0].Level != "warning" || merged[0].OccurrenceKey == "" {
		t.Fatalf("merged diagnostic interventions=%#v", merged)
	}
}

func TestCompanionIDEInterventionsPrepareReviewedFixQuests(t *testing.T) {
	exitCode := 1
	items := companion.BuildIDEInterventions([]domain.IDEObservation{
		{ID: "diag-1", Kind: "diagnostic", Level: "error", Path: "main.go", Line: 12, Summary: "undefined: handler"},
		{ID: "terminal-1", Kind: "terminal", Level: "error", Command: "go test ./...", ExitCode: &exitCode, Detail: "FAIL auth"},
	})
	if len(items) != 2 {
		t.Fatalf("IDE interventions=%#v", items)
	}
	diagnosticsItem := items[0]
	if diagnosticsItem.ActionKind != domain.CompanionInterventionPrompt || diagnosticsItem.RelatedID != "diag-1" || diagnosticsItem.RelatedPath != "main.go" || diagnosticsItem.RelatedLine != 12 || !strings.Contains(diagnosticsItem.ActionMessage, "Problems") {
		t.Fatalf("diagnostic action=%#v", diagnosticsItem)
	}
	terminalItem := items[1]
	if terminalItem.ActionKind != domain.CompanionInterventionPrompt || terminalItem.RelatedID != "terminal-1" || !strings.Contains(terminalItem.ActionMessage, "go test ./...") {
		t.Fatalf("terminal action=%#v", terminalItem)
	}
}

func TestDiagnosticsRelatedPathPrefersFocus(t *testing.T) {
	now := time.Now().UTC()
	obs := []domain.IDEObservation{
		{ID: "d1", Kind: "diagnostic", Level: "error", Path: "a.go", Line: 1, Summary: "first", ObservedAt: now, FirstSeen: now, LastSeen: now, Count: 1},
		{ID: "d2", Kind: "diagnostic", Level: "error", Path: "b.go", Line: 9, Summary: "focused", ObservedAt: now, FirstSeen: now, LastSeen: now, Count: 1},
	}
	items := companion.BuildIDEInterventionsGated(obs, domain.CompanionConfig{Initiative: 55, QuestionStrictness: 40}, companion.InterveneContext{Now: now, FocusPath: "b.go"})
	if len(items) == 0 || items[0].ID != "ide-diagnostics" {
		t.Fatalf("expected diagnostics: %#v", items)
	}
	if items[0].RelatedPath != "b.go" || items[0].RelatedLine != 9 || items[0].RelatedID != "d2" {
		t.Fatalf("RelatedPath must prefer focus match: %#v", items[0])
	}
}

func TestSCMInterventionCopyIsHonest(t *testing.T) {
	now := time.Now().UTC()
	obs := []domain.IDEObservation{{
		ID: "s1", Kind: "scm", Level: "warning", Path: "pkg/x.go",
		Summary:    "9 в git · behind 1 · main",
		Detail:     "changed=5; staged=2; untracked=2; unsaved=1; branch=main; ahead=0; behind=1",
		ObservedAt: now, LastSeen: now,
	}}
	items := companion.BuildIDEInterventionsGated(obs, domain.CompanionConfig{Initiative: 80, QuestionStrictness: 40}, companion.InterveneContext{Now: now})
	if len(items) == 0 || items[0].ID != "ide-scm-dirty" {
		t.Fatalf("expected scm intervention: %#v", items)
	}
	item := items[0]
	if item.Level != "warning" {
		t.Fatalf("large/behind scm should be warning: %#v", item)
	}
	if !strings.Contains(item.Title, "отставание") && !strings.Contains(item.Title, "незакоммиченные") {
		t.Fatalf("title must describe git state honestly: %#v", item)
	}
	if !strings.Contains(item.Detail, "изменено 5") || !strings.Contains(item.Detail, "несохранённых буферов 1") {
		t.Fatalf("detail must separate git vs unsaved: %#v", item)
	}
	if item.RelatedPath != "pkg/x.go" {
		t.Fatalf("RelatedPath=%q", item.RelatedPath)
	}
	legacy := companion.BuildIDEInterventionsGated([]domain.IDEObservation{{
		ID: "legacy", Kind: "scm", Level: "info", Detail: "dirty=3; branch=dev", ObservedAt: now, LastSeen: now,
	}}, domain.CompanionConfig{Initiative: 80, QuestionStrictness: 40}, companion.InterveneContext{Now: now})
	if len(legacy) == 0 || !strings.Contains(legacy[0].Title, "несохранённые") {
		t.Fatalf("legacy dirty= should map to unsaved buffers: %#v", legacy)
	}
}

func TestCompanionExplainsIDESignalsAndGroundsFixQuest(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "companion-ide.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	workspaceID := "ws-ide"
	now := time.Now().UTC()
	if err = store.SaveWorkspace(ctx, domain.Workspace{ID: workspaceID, Path: t.TempDir(), Name: "ide"}); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveProjectAgent(ctx, domain.ProjectAgent{
		ID: "fixer", WorkspaceID: workspaceID, Name: "Fixer", RoleDescription: "Debug and test failures",
		SystemPrompt: "fix", Provider: domain.ProviderOllama, PrimaryModel: "local", AllowedTools: []string{"read_file", "run_command"},
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	exitCode := 1
	if err = store.SaveIDEObservationBatch(ctx, workspaceID, "diagnostic", true, []domain.IDEObservation{{
		ID: "diag", WorkspaceID: workspaceID, Kind: "diagnostic", Source: "gopls", Level: "error",
		Summary: "undefined: handler", Path: "main.go", Line: 12, ObservedAt: now,
	}}); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveIDEObservationBatch(ctx, workspaceID, "terminal", false, []domain.IDEObservation{{
		ID: "terminal", WorkspaceID: workspaceID, Kind: "terminal", Source: "Point", Level: "error",
		Summary: "tests failed", Detail: "FAIL auth", Command: "go test ./...", ExitCode: &exitCode, ObservedAt: now.Add(time.Second),
	}}); err != nil {
		t.Fatal(err)
	}
	svc := companion.Service{Store: store}
	status, err := svc.Chat(ctx, companion.ChatRequest{WorkspaceID: workspaceID, Message: "Какие ошибки сейчас?"})
	if err != nil {
		t.Fatal(err)
	}
	if status.Proposal != nil || !strings.Contains(status.Reply, "main.go:12") || !strings.Contains(status.Reply, "go test ./...") {
		t.Fatalf("IDE status response=%#v", status)
	}
	fix, err := svc.Chat(ctx, companion.ChatRequest{WorkspaceID: workspaceID, Message: "Исправь текущие ошибки"})
	if err != nil {
		t.Fatal(err)
	}
	if fix.Proposal == nil || fix.Proposal.Importance != domain.QuestImportant {
		t.Fatalf("fix response=%#v", fix)
	}
	joinedObjectives := strings.Join(fix.Proposal.Objectives, " ")
	joinedDoD := strings.Join(fix.Proposal.DefinitionOfDone, " ")
	if !strings.Contains(joinedObjectives, "main.go:12") || !strings.Contains(joinedObjectives, "go test ./...") || !strings.Contains(joinedDoD, "Problems") || !strings.Contains(joinedDoD, "кодом 0") {
		t.Fatalf("IDE-grounded proposal=%#v", fix.Proposal)
	}
	quests, err := store.ListQuests(ctx, workspaceID)
	if err != nil || len(quests) != 0 {
		t.Fatalf("fix proposal started work without confirmation: %#v err=%v", quests, err)
	}
}

func TestCompanionGroundsOrdinaryChatInLiveFocus(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "companion-focus.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	workspaceID := "ws-focus"
	now := time.Now().UTC()
	if err = store.SaveWorkspace(ctx, domain.Workspace{ID: workspaceID, Path: t.TempDir(), Name: "focus"}); err != nil {
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
	status, err := svc.Chat(ctx, companion.ChatRequest{
		WorkspaceID: workspaceID,
		Message:     "Что здесь не так?",
		Focus: companion.ChatFocus{
			File: "auth.go", Line: 40, Language: "go", Dirty: true, Diagnostics: 1,
			Run: "go test ./...", Snippet: "func Login() {\n\treturn nil\n}",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if status.Proposal != nil || !strings.Contains(status.Reply, "auth.go:40") || !strings.Contains(status.Reply, "go test ./...") {
		t.Fatalf("focus status response=%#v", status)
	}
	if !containsFact(status.FactsUsed, "ideFocus=auth.go:40") || !containsFact(status.FactsUsed, "ideRun=go test ./...") {
		t.Fatalf("focus facts=%#v", status.FactsUsed)
	}
	joinedQuestions := strings.Join(status.Questions, " ")
	if !strings.Contains(joinedQuestions, "Исправь ошибки в auth.go:40") || strings.Contains(joinedQuestions, "Разбери цель запуска") {
		t.Fatalf("focus follow-ups=%#v", status.Questions)
	}
	here, err := svc.Chat(ctx, companion.ChatRequest{
		WorkspaceID: workspaceID,
		Message:     "Посмотри этот код",
		Focus:       companion.ChatFocus{File: "auth.go", Line: 40, Selection: true, Snippet: "func Login() {}"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if here.Proposal != nil || !strings.Contains(here.Reply, "auth.go:40") || !strings.Contains(here.Reply, "выделение") {
		t.Fatalf("selection focus response=%#v", here)
	}
	if err = store.SaveIDEObservationBatch(ctx, workspaceID, "diagnostic", true, []domain.IDEObservation{
		{ID: "other", WorkspaceID: workspaceID, Kind: "diagnostic", Source: "gopls", Level: "error", Summary: "unused import", Path: "other.go", Line: 2, ObservedAt: now},
		{ID: "auth", WorkspaceID: workspaceID, Kind: "diagnostic", Source: "gopls", Level: "error", Summary: "undefined: token", Path: "auth.go", Line: 40, ObservedAt: now},
	}); err != nil {
		t.Fatal(err)
	}
	priority, err := svc.Chat(ctx, companion.ChatRequest{
		WorkspaceID: workspaceID,
		Message:     "Какие ошибки сейчас?",
		Focus:       companion.ChatFocus{File: "auth.go", Line: 40},
	})
	if err != nil {
		t.Fatal(err)
	}
	authAt, otherAt := strings.Index(priority.Reply, "auth.go:40"), strings.Index(priority.Reply, "other.go:2")
	if authAt < 0 || otherAt < 0 || authAt > otherAt {
		t.Fatalf("focused diagnostic was not first: %q", priority.Reply)
	}
	fix, err := svc.Chat(ctx, companion.ChatRequest{
		WorkspaceID: workspaceID,
		Message:     "Исправь этот файл",
		Focus:       companion.ChatFocus{File: "auth.go", Line: 40, Failure: "go test ./..."},
	})
	if err != nil {
		t.Fatal(err)
	}
	if fix.Proposal == nil {
		t.Fatalf("focus fix response=%#v", fix)
	}
	joined := strings.Join(fix.Proposal.Objectives, " ")
	if !strings.Contains(joined, "auth.go:40") || !strings.Contains(joined, "go test ./...") {
		t.Fatalf("focus-grounded proposal=%#v", fix.Proposal)
	}
	quests, err := store.ListQuests(ctx, workspaceID)
	if err != nil || len(quests) != 0 {
		t.Fatalf("focus fix started work: %#v err=%v", quests, err)
	}
}

func TestCompanionFocusKeepsDotDotFilenameAndDropsParentPaths(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "companion-path.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	workspaceID := "ws-path"
	if err = store.SaveWorkspace(ctx, domain.Workspace{ID: workspaceID, Path: t.TempDir(), Name: "path"}); err != nil {
		t.Fatal(err)
	}
	svc := companion.Service{Store: store}
	kept, err := svc.Chat(ctx, companion.ChatRequest{
		WorkspaceID: workspaceID,
		Message:     "Что здесь не так?",
		Focus:       companion.ChatFocus{File: "notes..md", Line: 2, Snippet: "TODO: check auth"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(kept.Reply, "notes..md") {
		t.Fatalf("filename with dots was stripped: %q", kept.Reply)
	}
	escaped, err := svc.Chat(ctx, companion.ChatRequest{
		WorkspaceID: workspaceID,
		Message:     "Что здесь не так?",
		Focus:       companion.ChatFocus{File: "../secret.go", Line: 1, Snippet: "package secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(escaped.Reply, "secret.go") {
		t.Fatalf("parent path leaked into reply: %q", escaped.Reply)
	}
	if !strings.Contains(escaped.Reply, "фрагмент") {
		t.Fatalf("snippet focus was dropped with parent path: %q", escaped.Reply)
	}
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

func TestPreferLeanGather(t *testing.T) {
	focus := companion.ChatFocus{File: "internal/companion/service.go", Line: 10, Snippet: "func Chat()"}
	if !companion.PreferLeanGather("Что не так в этом файле?", focus) {
		t.Fatal("expected lean for focused file question")
	}
	if companion.PreferLeanGather("Покажи статус гильдии", focus) {
		t.Fatal("guild status must force full gather")
	}
	if companion.PreferLeanGather("Сколько токенов потратили?", focus) {
		t.Fatal("usage must force full gather")
	}
	if companion.PreferLeanGather("Создай агента reviewer", focus) {
		t.Fatal("agent creation must force full gather")
	}
	if companion.PreferLeanGather("Что не так?", companion.ChatFocus{}) {
		t.Fatal("empty focus must not lean")
	}
}

func TestExtractStreamingReply(t *testing.T) {
	cases := []struct {
		name   string
		raw    string
		want   string
		wantOK bool
	}{
		{name: "empty", raw: "", wantOK: false},
		{name: "before key", raw: `{"level":"suggestion"`, wantOK: false},
		{name: "partial reply", raw: `{"reply":"Привет`, want: "Привет", wantOK: true},
		{name: "complete reply", raw: `{"reply":"Готово","level":"suggestion","questions":[],"proposal":null}`, want: "Готово", wantOK: true},
		{name: "escaped newline", raw: `{"reply":"строка1\nстрока2`, want: "строка1\nстрока2", wantOK: true},
		{name: "escaped quote", raw: `{"reply":"скажи \"да\"`, want: `скажи "да"`, wantOK: true},
		{name: "fenced partial", raw: "```json\n{\"reply\":\"Код", want: "Код", wantOK: true},
		{name: "fenced complete", raw: "```json\n{\"reply\":\"Ок\",\"proposal\":null}\n```", want: "Ок", wantOK: true},
		{name: "unicode escape", raw: `{"reply":"\u041f\u0440\u0438\u0432\u0435\u0442`, want: "Привет", wantOK: true},
		{name: "extra fields before", raw: `{"level":"warning","reply":"внимание`, want: "внимание", wantOK: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := companion.ExtractStreamingReply(tc.raw)
			if ok != tc.wantOK {
				t.Fatalf("ok=%v want %v (got %q)", ok, tc.wantOK, got)
			}
			if got != tc.want {
				t.Fatalf("reply=%q want %q", got, tc.want)
			}
		})
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

func TestCompanionChatEmitsGrowingDeltas(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "companion-delta.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_ = store.SaveWorkspace(context.Background(), domain.Workspace{ID: "ws", Path: t.TempDir(), Name: "demo"})
	now := time.Now().UTC()
	cfg := domain.CompanionConfig{
		ID: "companion-ws", WorkspaceID: "ws", Preset: "balanced",
		Provider: domain.ProviderOllama, BaseURL: "http://127.0.0.1:11434", Model: "planner",
		Temperature: 0.2, MaxOutputTokens: 2048, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.SaveCompanionConfig(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	envelope := `{"reply":"Разбор файла","level":"suggestion","questions":[],"proposal":null}`
	chunks := []string{envelope[:12], envelope[12:25], envelope[25:]}
	var deltas []string
	svc := companion.Service{
		Store:          store,
		ProjectContext: companionProjectContext{},
		ModelFactory: func(providers.Config) (providers.Model, error) {
			return chunkedCompanionModel{chunks: chunks}, nil
		},
	}
	response, err := svc.Chat(context.Background(), companion.ChatRequest{
		WorkspaceID: "ws",
		Message:     "Что в текущем файле?",
		Focus:       companion.ChatFocus{File: "main.go", Line: 10},
		OnDelta: func(reply string) {
			deltas = append(deltas, reply)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Reply != "Разбор файла" {
		t.Fatalf("reply=%q", response.Reply)
	}
	if len(deltas) == 0 {
		t.Fatal("expected streaming deltas")
	}
	for i := 1; i < len(deltas); i++ {
		if len(deltas[i]) <= len(deltas[i-1]) {
			t.Fatalf("deltas must grow: %#v", deltas)
		}
	}
	if deltas[len(deltas)-1] != "Разбор файла" {
		t.Fatalf("last delta=%q", deltas[len(deltas)-1])
	}
}

// Человек видит, что именно ушло модели.
//
// В панели компаньона под каждым ответом раскрывается «Сведения»: режим,
// провайдер, модель и список фактов проекта. Когда список пуст, там прямо
// написано «Факты проекта для этого ответа не использовались» — это утверждение
// о приватности, и оно обязано быть верным.
//
// Держится оно на том, что каждый путь ответа кладёт в FactsUsed собранный
// контекст. Путей несколько (детерминированный, через модель, откат после сбоя
// модели), они написаны по отдельности, и забыть поле в новом — значит сказать
// человеку «ничего не отправляли» при отправленном коде.
func TestCompanionReportsGatheredFactsOnEveryPath(t *testing.T) {
	setup := func(t *testing.T, withModel bool, message string) companion.ChatResponse {
		t.Helper()
		store, err := storage.Open(filepath.Join(t.TempDir(), "facts.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		ctx := context.Background()
		workspaceID := "ws-facts"
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
			ID: "companion-facts", WorkspaceID: workspaceID, Preset: "balanced",
			CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		}
		if withModel {
			config.Provider = domain.ProviderOpenAI
			config.ProviderPreset = "openai"
			config.BaseURL = "https://example.test/v1"
			config.Model = "planner"
		}
		if err = store.SaveCompanionConfig(ctx, config); err != nil {
			t.Fatal(err)
		}
		svc := companion.Service{Store: store}
		if withModel {
			svc.ModelFactory = func(providers.Config) (providers.Model, error) {
				return companionModel{content: `{"reply":"Предлагаю безопасный план.","level":"warning","questions":[],"proposal":{"title":"Усилить auth","rationale":"Нужно закрыть риск","unknowns":["Поддерживаемые клиенты"],"objectives":["Сохранить API"],"constraints":[],"definitionOfDone":["Проверки пройдены"],"importance":"important"}}`}, nil
			}
		}
		response, err := svc.Chat(ctx, companion.ChatRequest{
			WorkspaceID: workspaceID, Message: message, APIKey: "transient-secret",
			// Фокус IDE — то, что человек считает своим кодом: имя файла и
			// выделение попадают в контекст, значит обязаны попасть и в отчёт.
			Focus: companion.ChatFocus{File: "internal/app/app.go", Line: 42, Snippet: "func main() {}"},
		})
		if err != nil {
			t.Fatal(err)
		}
		return response
	}

	for _, item := range []struct {
		имя       string
		сМоделью  bool
		сообщение string
		режим     string
	}{
		{"детерминированный путь", false, "Что не так в файле?", "deterministic"},
		{"путь через модель", true, "Спланируй auth", "model"},
	} {
		t.Run(item.имя, func(t *testing.T) {
			response := setup(t, item.сМоделью, item.сообщение)
			// Подтест обязан доказать, каким путём он пошёл: без этого «путь через
			// модель» тихо сворачивал в детерминированный и ничего не проверял.
			if response.Mode != item.режим {
				t.Fatalf("ожидался режим %q, получен %q — подтест проверяет не тот путь", item.режим, response.Mode)
			}
			if len(response.FactsUsed) == 0 {
				t.Fatalf("ответ не сообщает ни одного факта — человеку будет сказано, что контекст не использовался")
			}
			// Защита от холостого хода: список обязан называть именно то, что
			// пришло из фокуса, а не любой посторонний факт.
			joined := strings.Join(response.FactsUsed, " ")
			if !strings.Contains(joined, "ideFocus=") {
				t.Errorf("в отчёте нет фокуса IDE, хотя он был отправлен: %v", response.FactsUsed)
			}
		})
	}
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

// «Стоп» действительно останавливает работу модели.
//
// Кнопка ⏹ в панели компаньона обрывает запрос расширения к ядру
// (companionChatAbort.abort()), ядро передаёт контекст запроса дальше —
// r.Context() → CompanionChat → chatWithModel → model.Stream(ctx, …). Если
// однажды в этой цепочке появится context.Background() — частая правка «чтобы
// отмена не мешала», — интерфейс будет показывать «остановлено», а модель
// продолжит генерировать и тратить деньги.
//
// Проверяем поведение: отменяем контекст вызова и требуем, чтобы отмена дошла
// до самой модели.
func TestCompanionChatCancellationReachesTheModel(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "cancel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	workspaceID := "ws-cancel"
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
	if err = store.SaveCompanionConfig(ctx, domain.CompanionConfig{
		ID: "companion-cancel", WorkspaceID: workspaceID, Preset: "balanced",
		Provider: domain.ProviderOpenAI, ProviderPreset: "openai", BaseURL: "https://example.test/v1", Model: "planner",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	model := &blockingCompanionModel{started: make(chan struct{}), finished: make(chan error, 1)}
	svc := companion.Service{
		Store:        store,
		ModelFactory: func(providers.Config) (providers.Model, error) { return model, nil },
	}

	callCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		_, chatErr := svc.Chat(callCtx, companion.ChatRequest{
			WorkspaceID: workspaceID, Message: "Спланируй auth", APIKey: "transient-secret",
		})
		done <- chatErr
	}()

	// Защита от холостого хода: модель обязана начать работу, иначе отмена
	// «сработает» просто потому, что звать было некого.
	select {
	case <-model.started:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("модель не была вызвана — проверка отмены прошла бы вхолостую")
	}

	cancel()

	select {
	case reason := <-model.finished:
		if reason == nil {
			t.Fatal("модель досчитала до конца: отмена до неё не дошла, генерация и расход продолжались бы")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("модель не заметила отмены за отведённое время")
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Chat не вернулся после отмены")
	}
}
