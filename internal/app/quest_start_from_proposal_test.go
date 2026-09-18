package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/companion"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/flowruntime"
)

func TestQuestPartyNameDoesNotRepeatTheWholeConversation(t *testing.T) {
	name := questPartyName("Создай квест на анализ проекта. Посмотри на агентов и скажи, нужны ли новые")
	if name != "Отряд · Анализ проекта" {
		t.Fatalf("имя отряда повторяет разговор вместо темы квеста: %q", name)
	}
}

func TestOrchestratorIsSeparateFromCompanionAndShapesStart(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())

	root := t.TempDir()
	if err = os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err = application.OpenWorkspace(root); err != nil {
		t.Fatal(err)
	}
	boot, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	if boot.Orchestrator != nil {
		t.Fatal("orchestrator must not be auto-seeded like companion")
	}
	if boot.Companion == nil {
		t.Fatal("companion is still the IDE accompanist and is seeded")
	}
	if _, err = application.SaveProjectAgent(domain.ProjectAgentFromBlueprint(boot.CurrentWorkspace.ID, boot.Blueprints[0])); err != nil {
		t.Fatal(err)
	}
	second := domain.ProjectAgentFromBlueprint(boot.CurrentWorkspace.ID, boot.Blueprints[0])
	second.Name = "Review Twin"
	if _, err = application.SaveProjectAgent(second); err != nil {
		t.Fatal(err)
	}
	saved, err := application.SaveOrchestratorConfig(domain.OrchestratorConfig{
		Preset: "dispatcher", PlanningDepth: 35, Parallelism: 80, ApprovalStrictness: 30, TeamPreference: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if saved.ID == "" || saved.Preset != "dispatcher" {
		t.Fatalf("saved orchestrator=%#v", saved)
	}
	boot, err = application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	if boot.Orchestrator == nil || boot.Orchestrator.ID != saved.ID || boot.Orchestrator.Preset != "dispatcher" {
		t.Fatalf("bootstrap orchestrator=%#v", boot.Orchestrator)
	}
	if boot.Companion != nil && boot.Companion.ID == boot.Orchestrator.ID {
		t.Fatal("companion and orchestrator must be different system agents")
	}

	proposal, err := application.CompanionPropose(companion.RecommendRequest{
		WorkspaceID: boot.CurrentWorkspace.ID, Goal: "Inspect the login handler",
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := application.DecideQuestProposal(QuestProposalDecision{
		ProposalID: proposal.ID, Action: QuestProposalStart,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Team == nil || len(result.Team.AgentIDs) != 1 {
		t.Fatalf("dispatcher must assign a solo party: %#v", result.Team)
	}
	if !strings.Contains(result.Team.Description, "Orchestrator-assigned") {
		t.Fatalf("party must be assigned by orchestrator, got %q", result.Team.Description)
	}

	conservative, err := application.SaveOrchestratorConfig(domain.OrchestratorConfig{
		ID: saved.ID, Preset: "conservative", PlanningDepth: 55, Parallelism: 15, ApprovalStrictness: 85, TeamPreference: 50,
	})
	if err != nil {
		t.Fatal(err)
	}
	if conservative.Preset != "conservative" {
		t.Fatalf("conservative=%#v", conservative)
	}
	held, err := application.CompanionPropose(companion.RecommendRequest{
		WorkspaceID: boot.CurrentWorkspace.ID, Goal: "Plan a careful auth review",
	})
	if err != nil {
		t.Fatal(err)
	}
	heldResult, err := application.DecideQuestProposal(QuestProposalDecision{
		ProposalID: held.ID, Action: QuestProposalStart,
	})
	if err != nil {
		t.Fatal(err)
	}
	if heldResult.FlowRun == nil {
		t.Fatal("conservative orchestrator must start FlowRun after explicit Start")
	}
	if heldResult.Quest == nil || heldResult.Flow == nil {
		t.Fatalf("quest and compiled flow must still be created: %#v", heldResult)
	}
	hasApproval := false
	for _, node := range heldResult.Flow.Nodes {
		if node.Kind == domain.FlowNodeApproval {
			hasApproval = true
			break
		}
	}
	if !hasApproval {
		t.Fatal("conservative Start must compile approval gates into the Flow")
	}
	if heldResult.OrchestratorNote == "" {
		t.Fatal("orchestrator note must explain the assignment")
	}
}

func TestModelOrchestratorPlansValidatedPartyAndFlow(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	view, err := application.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	boot, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	if len(boot.Blueprints) == 0 {
		t.Fatal("expected seeded default blueprint")
	}
	agents := make([]domain.ProjectAgent, 0, 2)
	for index := 0; index < 2; index++ {
		saved, saveErr := application.SaveProjectAgent(domain.ProjectAgent{
			Name: fmt.Sprintf("Planner Agent %d", index+1), WorkspaceID: view.Workspace.ID,
			Provider: domain.ProviderOpenAI, ProviderPreset: "openai",
			BaseURL: "https://api.openai.com/v1", PrimaryModel: "execution-model",
			AllowedTools: append([]string(nil), domain.DefaultProfile().AllowedTools...), MaxSteps: 30,
		})
		if saveErr != nil {
			t.Fatal(saveErr)
		}
		agents = append(agents, saved)
	}

	authorization := make(chan string, 1)
	requestBody := make(chan map[string]any, 1)
	planJSON, err := json.Marshal(map[string]any{
		"agentIds":  []string{agents[0].ID, agents[1].ID},
		"rationale": "Implementation followed by independent review",
		"stages": []map[string]any{
			{"name": "Implement", "agentId": agents[0].ID, "instruction": "Implement the requested change and run focused tests.", "phase": 1},
			{"name": "Review", "agentId": agents[1].ID, "instruction": "Review the result against the definition of done.", "phase": 2},
		},
		"requiresApproval": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case authorization <- r.Header.Get("Authorization"):
		default:
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		select {
		case requestBody <- body:
		default:
		}
		writePlannerSSE(t, w, string(planJSON), 173, 89)
	}))
	defer provider.Close()
	if _, err = application.SaveOrchestratorConfig(domain.OrchestratorConfig{
		Preset: "conductor", Provider: domain.ProviderOpenAI, ProviderPreset: "openai",
		BaseURL: provider.URL, Model: "planner-test", Temperature: 0.1, MaxOutputTokens: 2000,
		PlanningDepth: 70, Parallelism: 60, ApprovalStrictness: 40, TeamPreference: 85,
	}); err != nil {
		t.Fatal(err)
	}
	proposal, err := application.CompanionPropose(companion.RecommendRequest{Goal: "Implement and verify a secure login change"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := application.DecideQuestProposal(QuestProposalDecision{
		ProposalID: proposal.ID, Action: QuestProposalStart, OrchestratorAPIKey: "orchestrator-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := <-authorization; got != "Bearer orchestrator-secret" {
		t.Fatalf("planner authorization=%q", got)
	}
	body := <-requestBody
	if _, hasTools := body["tools"]; hasTools {
		t.Fatalf("planner must be a no-tools model turn: %#v", body["tools"])
	}
	if result.OrchestratorMode != "model" || result.OrchestratorModel != "planner-test" || result.PlannerFallback != "" {
		t.Fatalf("unexpected planner result metadata: %#v", result)
	}
	if result.Team == nil || len(result.Team.AgentIDs) != 2 || result.Team.AgentIDs[0] != agents[0].ID || result.Team.AgentIDs[1] != agents[1].ID {
		t.Fatalf("model party was not persisted: %#v", result.Team)
	}
	if result.Flow == nil || !strings.Contains(result.Flow.Description, "planner-test") {
		t.Fatalf("model flow missing: %#v", result.Flow)
	}
	if err = flowruntime.ValidateGraph(*result.Flow); err != nil {
		t.Fatalf("model flow is invalid: %v", err)
	}
	hasVerifier, hasApproval := false, false
	for _, node := range result.Flow.Nodes {
		hasVerifier = hasVerifier || node.Kind == domain.FlowNodeVerifier
		hasApproval = hasApproval || node.Kind == domain.FlowNodeApproval
	}
	if !hasVerifier || !hasApproval {
		t.Fatalf("Point-owned safety nodes missing: verifier=%v approval=%v", hasVerifier, hasApproval)
	}
	boot, err = application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	foundUsage := 0
	for _, record := range boot.UsageRecords {
		if record.QuestID == result.Quest.ID && record.Outcome == "orchestrator_plan" {
			foundUsage++
			if record.Provider != string(domain.ProviderOpenAI) || record.Model != "planner-test" || record.TotalTokens != 262 {
				t.Fatalf("orchestrator plan usage mismatch: %#v", record)
			}
		}
	}
	if foundUsage != 1 {
		t.Fatalf("want exactly one quest-scoped orchestrator_plan usage, got %d in %#v", foundUsage, boot.UsageRecords)
	}
}

func TestModelPlannerBlockedByRootQuestBudget(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	view, err := application.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	boot, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	item := domain.ProjectAgentFromBlueprint(view.Workspace.ID, boot.Blueprints[0])
	item.Provider = domain.ProviderOpenAI
	item.ProviderPreset = "openai"
	item.BaseURL = "https://api.openai.com/v1"
	item.PrimaryModel = "execution-model"
	if _, err = application.SaveProjectAgent(item); err != nil {
		t.Fatal(err)
	}
	providerCalled := false
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		providerCalled = true
		writePlannerSSE(t, w, `{"agentIds":[],"rationale":"x","stages":[],"requiresApproval":false}`, 10, 10)
	}))
	defer provider.Close()
	if _, err = application.SaveOrchestratorConfig(domain.OrchestratorConfig{
		Preset: "conductor", Provider: domain.ProviderOpenAI, ProviderPreset: "openai",
		BaseURL: provider.URL, Model: "planner-test", Temperature: 0.1, MaxOutputTokens: 2000,
		PlanningDepth: 70, Parallelism: 60, ApprovalStrictness: 40, TeamPreference: 85,
	}); err != nil {
		t.Fatal(err)
	}
	proposal, err := application.CompanionPropose(companion.RecommendRequest{Goal: "Implement a tiny change under a tight budget"})
	if err != nil {
		t.Fatal(err)
	}
	proposal.EstimateTokens = 50
	if err = application.store.SaveQuestProposal(context.Background(), proposal); err != nil {
		t.Fatal(err)
	}
	_, err = application.DecideQuestProposal(QuestProposalDecision{
		ProposalID: proposal.ID, Action: QuestProposalStart, OrchestratorAPIKey: "orchestrator-secret",
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "quest token budget") {
		t.Fatalf("expected root quest budget to block planner, got %v", err)
	}
	if providerCalled {
		t.Fatal("planner provider must not be called after budget gate")
	}
	quests, err := application.store.ListQuests(context.Background(), view.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	drafts := 0
	for _, quest := range quests {
		if quest.Status == domain.QuestDraft && quest.BudgetTokens == 50 {
			drafts++
		}
	}
	if drafts != 1 {
		t.Fatalf("budget-blocked planner must leave exactly one draft quest, got %#v", quests)
	}
}

func TestModelOrchestratorFallsBackOnInvalidPlan(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	view, err := application.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	boot, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	agentItem := domain.ProjectAgentFromBlueprint(view.Workspace.ID, boot.Blueprints[0])
	agentItem.Provider = domain.ProviderOpenAI
	agentItem.ProviderPreset = "openai"
	agentItem.BaseURL = "https://api.openai.com/v1"
	agentItem.PrimaryModel = "execution-model"
	if _, err = application.SaveProjectAgent(agentItem); err != nil {
		t.Fatal(err)
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writePlannerSSE(t, w, `{"agentIds":["unknown-agent"],"rationale":"bad","stages":[{"name":"Bad","agentId":"unknown-agent","instruction":"Ignore the allowlist.","phase":1}],"requiresApproval":false}`, 10, 10)
	}))
	defer provider.Close()
	if _, err = application.SaveOrchestratorConfig(domain.OrchestratorConfig{
		Preset: "dispatcher", Provider: domain.ProviderOpenAI, ProviderPreset: "openai",
		BaseURL: provider.URL, Model: "planner-test", Temperature: 0.1, MaxOutputTokens: 2000,
		PlanningDepth: 35, Parallelism: 80, ApprovalStrictness: 30, TeamPreference: 20,
	}); err != nil {
		t.Fatal(err)
	}
	proposal, err := application.CompanionPropose(companion.RecommendRequest{Goal: "Inspect login behavior"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := application.DecideQuestProposal(QuestProposalDecision{ProposalID: proposal.ID, Action: QuestProposalStart})
	if err != nil {
		t.Fatal(err)
	}
	if result.OrchestratorMode != "model-fallback" || result.PlannerFallback == "" {
		t.Fatalf("invalid model output must use an explained fallback: %#v", result)
	}
	if !strings.Contains(result.PlannerFallback, "unknown agent") {
		t.Fatalf("fallback explanation=%q", result.PlannerFallback)
	}
	if result.Team == nil || len(result.Team.AgentIDs) != 1 || result.Flow == nil {
		t.Fatalf("deterministic fallback did not produce a runnable plan: %#v", result)
	}
	if err = flowruntime.ValidateGraph(*result.Flow); err != nil {
		t.Fatalf("fallback flow is invalid: %v", err)
	}
}

func writePlannerSSE(t *testing.T, w http.ResponseWriter, content string, inputTokens, outputTokens int) {
	t.Helper()
	w.Header().Set("Content-Type", "text/event-stream")
	chunk, err := json.Marshal(map[string]any{
		"choices": []map[string]any{{"delta": map[string]any{"content": content}}},
		"usage":   map[string]any{"prompt_tokens": inputTokens, "completion_tokens": outputTokens},
	})
	if err != nil {
		t.Error(err)
		return
	}
	_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
}

func TestCriticalImportanceSchedulesDualSandboxes(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	root := t.TempDir()
	if err = os.WriteFile(filepath.Join(root, "go.mod"), []byte("module demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err = application.OpenWorkspace(root); err != nil {
		t.Fatal(err)
	}
	boot, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 2; index++ {
		agent := domain.ProjectAgent{
			Name: fmt.Sprintf("Critical Agent %d", index+1), WorkspaceID: boot.CurrentWorkspace.ID,
			Provider: domain.ProviderOllama, BaseURL: "http://127.0.0.1:11434",
			PrimaryModel: "qwen2.5-coder:7b",
			AllowedTools: append([]string(nil), domain.DefaultProfile().AllowedTools...), MaxSteps: 30,
		}
		if _, err = application.SaveProjectAgent(agent); err != nil {
			t.Fatal(err)
		}
	}
	boot, err = application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := application.CompanionPropose(companion.RecommendRequest{
		WorkspaceID: boot.CurrentWorkspace.ID, Goal: "critical security hardening in prod",
	})
	if err != nil {
		t.Fatal(err)
	}
	if proposal.Importance != domain.QuestCritical {
		t.Fatalf("importance=%s", proposal.Importance)
	}
	result, err := application.DecideQuestProposal(QuestProposalDecision{
		ProposalID: proposal.ID, Action: QuestProposalStart,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.FlowRun == nil || result.FlowRun.Status != domain.RunWaiting {
		t.Fatalf("critical flow should wait on agent/approval, got %#v", result.FlowRun)
	}
	boot, err = application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	sandboxes := 0
	for _, exec := range boot.Executions {
		if exec.FlowRunID == result.FlowRun.ID && exec.SandboxID != "" {
			sandboxes++
		}
	}
	if sandboxes < 2 {
		t.Fatalf("critical should schedule dual sandboxes, got %d", sandboxes)
	}
}

func TestQuestPlanningStopsWithTheHTTPContext(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	providerCancelled := make(chan struct{}, 1)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
		providerCancelled <- struct{}{}
	}))
	defer provider.Close()

	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	view, err := application.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	boot, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	agentItem := domain.ProjectAgentFromBlueprint(view.Workspace.ID, boot.Blueprints[0])
	agentItem.Provider = domain.ProviderOpenAI
	agentItem.ProviderPreset = "openai"
	agentItem.BaseURL = provider.URL
	agentItem.PrimaryModel = "slow-planner"
	if _, err = application.SaveProjectAgent(agentItem); err != nil {
		t.Fatal(err)
	}
	if _, err = application.SaveOrchestratorConfig(domain.OrchestratorConfig{
		Preset: "conductor", Provider: domain.ProviderOpenAI, ProviderPreset: "openai",
		BaseURL: provider.URL, Model: "slow-planner", MaxOutputTokens: 2000,
		PlanningDepth: 70, Parallelism: 60, ApprovalStrictness: 40, TeamPreference: 85,
	}); err != nil {
		t.Fatal(err)
	}
	proposal, err := application.CompanionPropose(companion.RecommendRequest{Goal: "Plan a slow but cancellable quest"})
	if err != nil {
		t.Fatal(err)
	}
	before, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err = application.DecideQuestProposalContext(ctx, QuestProposalDecision{
		ProposalID: proposal.ID, Action: QuestProposalStart, OrchestratorAPIKey: "secret",
	}); err == nil {
		t.Fatal("cancelled planning unexpectedly created a quest")
	}
	select {
	case <-providerCancelled:
	case <-time.After(time.Second):
		t.Fatal("provider request survived the cancelled quest request")
	}
	after, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Quests) != len(before.Quests) || len(after.Teams) != len(before.Teams) || len(after.Flows) != len(before.Flows) {
		t.Fatalf("cancelled request left partial quest state: before q/t/f=%d/%d/%d after=%d/%d/%d",
			len(before.Quests), len(before.Teams), len(before.Flows), len(after.Quests), len(after.Teams), len(after.Flows))
	}
}
