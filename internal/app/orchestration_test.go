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
	"local-agent-workbench/internal/sandbox"
)

func TestQuestPartyNameDoesNotRepeatTheWholeConversation(t *testing.T) {
	name := questPartyName("Создай квест на анализ проекта. Посмотри на агентов и скажи, нужны ли новые")
	if name != "Отряд · Анализ проекта" {
		t.Fatalf("имя отряда повторяет разговор вместо темы квеста: %q", name)
	}
}

func TestFlowCreatesOnlyAgentNodeChildQuestsAndReusesChildForRetry(t *testing.T) {
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
	agent := domain.ProjectAgentFromBlueprint(view.Workspace.ID, boot.Blueprints[0])
	agent.Provider, agent.ProviderPreset, agent.PrimaryModel = domain.ProviderOllama, "ollama", "qwen2.5-coder:7b"
	agent.BaseURL = "http://127.0.0.1:11434"
	agent, err = application.SaveProjectAgent(agent)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := application.SaveQuest(domain.Quest{Title: "Ship feature", Status: domain.QuestActive, BudgetTokens: 50000})
	if err != nil {
		t.Fatal(err)
	}
	flow, err := application.SaveFlow(domain.FlowGraph{
		Name: "Retry flow",
		Nodes: []domain.FlowNode{
			{ID: "input", Kind: domain.FlowNodeInput},
			{ID: "work", Kind: domain.FlowNodeAgent, Name: "Implement", AgentID: agent.ID, FailurePolicy: domain.FlowFailurePolicy{Mode: "retry", MaxRetries: 1}},
			{ID: "output", Kind: domain.FlowNodeOutput},
		},
		Edges: []domain.FlowEdge{{ID: "a", From: "input", To: "work"}, {ID: "b", From: "work", To: "output"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	flowRun, err := application.StartFlowRun(flow.ID, parent.ID, map[string]any{"task": parent.Title})
	if err != nil {
		t.Fatal(err)
	}
	quests, err := application.store.ListQuests(context.Background(), view.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	var child domain.Quest
	for _, quest := range quests {
		if quest.FlowRunID == flowRun.ID {
			if child.ID != "" {
				t.Fatalf("control nodes unexpectedly created child quests: %#v", quests)
			}
			child = quest
		}
	}
	if child.ParentID != parent.ID || child.FlowNodeID != "work" || child.AssignedAgentID != agent.ID || child.Status != domain.QuestActive {
		t.Fatalf("child quest links=%#v", child)
	}
	executions, err := application.store.ListExecutions(context.Background(), view.Workspace.ID, 20)
	if err != nil || len(executions) != 1 || executions[0].QuestID != child.ID {
		t.Fatalf("first attempt=%#v err=%v", executions, err)
	}
	first := executions[0]
	first.Status, first.Error = domain.RunFailed, "temporary provider outage"
	if err = application.store.SaveExecution(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	recoveredRun, recovered, err := application.recoverFlowNodeFailure(flowRun.ID, "work", first)
	if err != nil || !recovered {
		t.Fatalf("recovered=%v err=%v", recovered, err)
	}
	if err = application.scheduleFlowAgentExecutionsFromRun(recoveredRun); err != nil {
		t.Fatal(err)
	}
	executions, err = application.store.ListExecutions(context.Background(), view.Workspace.ID, 20)
	if err != nil || len(executions) != 2 {
		t.Fatalf("retry attempts=%#v err=%v", executions, err)
	}
	for _, execution := range executions {
		if execution.QuestID != child.ID || execution.Snapshot.SchemaVersion != 3 {
			t.Fatalf("retry escaped child/snapshot invariant: %#v", execution)
		}
	}
}

func TestFlowFallbackCreatesFreshExecutionWithReadyExplicitAgent(t *testing.T) {
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
	boot, _ := application.Bootstrap()
	primary := domain.ProjectAgentFromBlueprint(view.Workspace.ID, boot.Blueprints[0])
	primary.Provider, primary.ProviderPreset, primary.PrimaryModel = domain.ProviderOllama, "ollama", "qwen2.5-coder:7b"
	primary.BaseURL = "http://127.0.0.1:11434"
	primary.Name = "Primary"
	primary, err = application.SaveProjectAgent(primary)
	if err != nil {
		t.Fatal(err)
	}
	fallback := domain.ProjectAgentFromBlueprint(view.Workspace.ID, boot.Blueprints[0])
	fallback.Provider, fallback.ProviderPreset, fallback.PrimaryModel = domain.ProviderOllama, "ollama", "qwen2.5-coder:7b"
	fallback.BaseURL = "http://127.0.0.1:11434"
	fallback.Name = "Fallback"
	fallback, err = application.SaveProjectAgent(fallback)
	if err != nil {
		t.Fatal(err)
	}
	parent, _ := application.SaveQuest(domain.Quest{Title: "Fallback", Status: domain.QuestActive})
	flow, err := application.SaveFlow(domain.FlowGraph{
		Name: "Fallback flow",
		Nodes: []domain.FlowNode{
			{ID: "input", Kind: domain.FlowNodeInput},
			{ID: "work", Kind: domain.FlowNodeAgent, AgentID: primary.ID, FailurePolicy: domain.FlowFailurePolicy{Mode: "fallback_agent", FallbackAgentID: fallback.ID}},
			{ID: "output", Kind: domain.FlowNodeOutput},
		},
		Edges: []domain.FlowEdge{{From: "input", To: "work"}, {From: "work", To: "output"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	flowRun, err := application.StartFlowRun(flow.ID, parent.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	executions, _ := application.store.ListExecutions(context.Background(), view.Workspace.ID, 20)
	first := executions[0]
	first.Status, first.Error = domain.RunFailed, "temporary transport error"
	_ = application.store.SaveExecution(context.Background(), first)
	recoveredRun, recovered, err := application.recoverFlowNodeFailure(flowRun.ID, "work", first)
	if err != nil || !recovered {
		t.Fatalf("recovered=%v err=%v", recovered, err)
	}
	if err = application.scheduleFlowAgentExecutionsFromRun(recoveredRun); err != nil {
		t.Fatal(err)
	}
	executions, _ = application.store.ListExecutions(context.Background(), view.Workspace.ID, 20)
	foundFallback := false
	for _, execution := range executions {
		foundFallback = foundFallback || execution.ProjectAgentID == fallback.ID
	}
	if !foundFallback {
		t.Fatalf("fallback execution missing: %#v", executions)
	}
}

func TestBootstrapReportsTruthfulFilteredCopyBoundary(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	boot, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	if boot.Sandbox.Backend != "filtered-copy" || !boot.Sandbox.LiveWorkspaceIsolation {
		t.Fatalf("sandbox capability missing from bootstrap: %#v", boot.Sandbox)
	}
	if boot.Sandbox.ProcessIsolation || boot.Sandbox.NetworkIsolation || boot.Sandbox.StrongOSBoundary {
		t.Fatalf("bootstrap overclaimed filtered-copy isolation: %#v", boot.Sandbox)
	}
}

func TestDecideQuestProposalStartCreatesQuestFlowAndSandboxes(t *testing.T) {
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
	if len(boot.Blueprints) == 0 {
		t.Fatal("expected a blueprint catalog")
	}
	if _, err = application.SaveProjectAgent(domain.ProjectAgentFromBlueprint(boot.CurrentWorkspace.ID, boot.Blueprints[0])); err != nil {
		t.Fatal(err)
	}
	boot, err = application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}

	proposal, err := application.CompanionPropose(companion.RecommendRequest{
		WorkspaceID: boot.CurrentWorkspace.ID, Goal: "Implement Google OAuth important review",
	})
	if err != nil {
		t.Fatal(err)
	}
	if proposal.Importance != domain.QuestImportant {
		t.Fatalf("importance=%s", proposal.Importance)
	}

	result, err := application.DecideQuestProposal(QuestProposalDecision{
		ProposalID: proposal.ID, Action: QuestProposalStart,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Quest == nil || result.Quest.Status != domain.QuestActive {
		t.Fatalf("quest=%#v", result.Quest)
	}
	if result.Flow == nil || len(result.Flow.Nodes) < 3 {
		t.Fatalf("flow=%#v", result.Flow)
	}
	if result.FlowRun == nil {
		t.Fatal("expected flow run")
	}
	boot, err = application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	matched := 0
	for _, exec := range boot.Executions {
		if exec.FlowRunID != result.FlowRun.ID {
			continue
		}
		matched++
		if exec.SandboxID == "" {
			t.Fatalf("execution missing sandbox: %#v", exec)
		}
		if exec.FlowRunID != result.FlowRun.ID {
			t.Fatalf("flow run link missing: %#v", exec)
		}
	}
	if matched < 1 {
		t.Fatalf("expected >=1 sandboxed execution for important primary, got %d", matched)
	}
	// Reviewer is gated until primary execution completes.
	reviewerScheduled := 0
	for _, exec := range boot.Executions {
		if exec.FlowRunID == result.FlowRun.ID && strings.Contains(exec.Task, "Reviewer") {
			reviewerScheduled++
		}
	}
	if reviewerScheduled != 0 {
		t.Fatalf("reviewer must wait for primary completion, got %d", reviewerScheduled)
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

func TestDecideQuestProposalIgnore(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	root := t.TempDir()
	if err = os.WriteFile(filepath.Join(root, "README.md"), []byte("# demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err = application.OpenWorkspace(root); err != nil {
		t.Fatal(err)
	}
	boot, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := application.CompanionPropose(companion.RecommendRequest{
		WorkspaceID: boot.CurrentWorkspace.ID, Goal: "tiny chore",
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := application.DecideQuestProposal(QuestProposalDecision{
		ProposalID: proposal.ID, Action: QuestProposalIgnore,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Proposal.Status != "ignored" {
		t.Fatalf("status=%s", result.Proposal.Status)
	}
	if result.Quest != nil {
		t.Fatal("ignore must not create quest")
	}
}

// Одно предложение — один квест.
//
// Запуск не повторяет старое, а делает второе: свой квест, свой отряд, свой Flow
// и свой прогон — те же агенты выходят на ту же задачу и тратят бюджет дважды.
// Между нажатием и ответом успевает пройти заметное время, и второе нажатие в
// этот промежуток — обычное человеческое действие. Кнопку прячет и интерфейс,
// но запрет обязан жить здесь: маршрут открыт всем клиентам, а гонку двух
// нажатий экран не разрешает.
func TestDecideQuestProposalStartRefusesSecondStart(t *testing.T) {
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
	if _, err = application.SaveProjectAgent(domain.ProjectAgentFromBlueprint(boot.CurrentWorkspace.ID, boot.Blueprints[0])); err != nil {
		t.Fatal(err)
	}
	proposal, err := application.CompanionPropose(companion.RecommendRequest{
		WorkspaceID: boot.CurrentWorkspace.ID, Goal: "Починить флаки-тест оплаты",
	})
	if err != nil {
		t.Fatal(err)
	}

	first, err := application.DecideQuestProposal(QuestProposalDecision{
		ProposalID: proposal.ID, Action: QuestProposalStart,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Quest == nil {
		t.Fatal("первый запуск обязан создать квест")
	}

	if _, err = application.DecideQuestProposal(QuestProposalDecision{
		ProposalID: proposal.ID, Action: QuestProposalStart,
	}); err == nil {
		t.Fatal("повторный запуск принят — из одного предложения вышло два квеста")
	}

	quests, err := application.store.ListQuests(context.Background(), boot.CurrentWorkspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	started := 0
	for _, quest := range quests {
		if quest.ParentID == "" {
			started++
		}
	}
	if started != 1 {
		t.Fatalf("квестов верхнего уровня %d, а предложение было одно", started)
	}
}

func TestQuestProposalFullEditPersistsAndUsesSelectedFlow(t *testing.T) {
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
	view, err := application.OpenWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	boot, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	agent, err := application.SaveProjectAgent(domain.ProjectAgentFromBlueprint(view.Workspace.ID, boot.Blueprints[0]))
	if err != nil {
		t.Fatal(err)
	}
	flow, err := application.SaveFlow(domain.FlowGraph{
		WorkspaceID: view.Workspace.ID, Name: "Authentication delivery", Description: "Implement and verify auth changes",
		Nodes: []domain.FlowNode{
			{ID: "input", Kind: domain.FlowNodeInput, Name: "Input"},
			{ID: "agent", Kind: domain.FlowNodeAgent, Name: "Implement", AgentID: agent.ID},
			{ID: "output", Kind: domain.FlowNodeOutput, Name: "Output"},
		},
		Edges: []domain.FlowEdge{{ID: "one", From: "input", To: "agent"}, {ID: "two", From: "agent", To: "output"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := application.CompanionPropose(companion.RecommendRequest{Goal: "Implement authentication"})
	if err != nil {
		t.Fatal(err)
	}
	modified, err := application.DecideQuestProposal(QuestProposalDecision{
		ProposalID: proposal.ID, Action: QuestProposalModify, Title: "Harden authentication",
		Objectives: []string{"Implement token rotation", "Verify compatibility"}, Constraints: []string{"No network access"},
		DefinitionOfDone: []string{"Tests pass", "Reviewer approves"}, TeamAgentIDs: []string{agent.ID},
		FlowID: flow.ID, Importance: domain.QuestImportant,
	})
	if err != nil {
		t.Fatal(err)
	}
	if modified.Proposal.Title != "Harden authentication" || modified.Proposal.FlowID != flow.ID || len(modified.Proposal.Objectives) != 2 {
		t.Fatalf("modified proposal=%#v", modified.Proposal)
	}
	if !strings.Contains(strings.ToLower(strings.Join(modified.Proposal.Constraints, " ")), "change set") {
		t.Fatalf("mandatory change-set constraint missing: %#v", modified.Proposal.Constraints)
	}
	boot, err = application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	var persisted *domain.QuestProposal
	for index := range boot.QuestProposals {
		if boot.QuestProposals[index].ID == proposal.ID {
			persisted = &boot.QuestProposals[index]
			break
		}
	}
	if persisted == nil || persisted.Title != "Harden authentication" || persisted.FlowID != flow.ID || len(persisted.DefinitionOfDone) != 2 {
		t.Fatalf("persisted proposal=%#v", persisted)
	}
	// Пустой выбор в редакторе означает «авто», а не «оставь прежний отряд».
	// Webview всегда отправляет массив checkbox-ов, включая пустой.
	cleared, err := application.DecideQuestProposal(QuestProposalDecision{
		ProposalID: proposal.ID, Action: QuestProposalModify, TeamAgentIDs: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cleared.Proposal.TeamAgentIDs) != 0 {
		t.Fatalf("пустой ручной выбор не вернул автоматический подбор: %#v", cleared.Proposal.TeamAgentIDs)
	}
	started, err := application.DecideQuestProposal(QuestProposalDecision{ProposalID: proposal.ID, Action: QuestProposalStart})
	if err != nil {
		t.Fatal(err)
	}
	if started.Flow == nil || started.Flow.ID != flow.ID || started.Quest == nil || started.Quest.FlowID != flow.ID {
		t.Fatalf("selected flow was not used: %#v", started)
	}
}

func TestQuestProposalManualTeamSurvivesSaveBeforeLaterStart(t *testing.T) {
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
	view, err := application.OpenWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	boot, err := application.Bootstrap()
	if err != nil || len(boot.Blueprints) == 0 {
		t.Fatalf("catalog unavailable: %v", err)
	}
	firstDraft := domain.ProjectAgentFromBlueprint(view.Workspace.ID, boot.Blueprints[0])
	firstDraft.Name = "Первый"
	first, err := application.SaveProjectAgent(firstDraft)
	if err != nil {
		t.Fatal(err)
	}
	secondDraft := domain.ProjectAgentFromBlueprint(view.Workspace.ID, boot.Blueprints[0])
	secondDraft.Name = "Выбранный"
	second, err := application.SaveProjectAgent(secondDraft)
	if err != nil {
		t.Fatal(err)
	}
	if err = application.store.SaveOrchestratorConfig(context.Background(), domain.OrchestratorConfig{
		ID: "master", WorkspaceID: view.Workspace.ID, Preset: "balanced",
		PlanningDepth: 50, Parallelism: 50, ApprovalStrictness: 50, TeamPreference: 50,
	}); err != nil {
		t.Fatal(err)
	}
	proposal := domain.QuestProposal{
		ID: domain.NewID("qp"), WorkspaceID: view.Workspace.ID, Title: "Проверить ручной отряд",
		Task: "Проверить ручной отряд", Objectives: []string{"Выполнить задачу"},
		DefinitionOfDone: []string{"Проверка проходит"}, TeamAgentIDs: []string{first.ID, second.ID},
		Importance: domain.QuestNormal, Status: "pending", CreatedAt: time.Now().UTC(),
	}
	if err = application.store.SaveQuestProposal(context.Background(), proposal); err != nil {
		t.Fatal(err)
	}

	modified, err := application.DecideQuestProposal(QuestProposalDecision{
		ProposalID: proposal.ID, Action: QuestProposalModify, TeamAgentIDs: []string{second.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !modified.Proposal.TeamAgentIDsLocked || len(modified.Proposal.TeamAgentIDs) != 1 || modified.Proposal.TeamAgentIDs[0] != second.ID {
		t.Fatalf("ручной состав не зафиксирован: %#v", modified.Proposal)
	}

	// Так запускает интерфейс после отдельного Save: только proposalId и Start,
	// без повторной отправки полей уже закрытого редактора.
	started, err := application.DecideQuestProposal(QuestProposalDecision{
		ProposalID: proposal.ID, Action: QuestProposalStart,
	})
	if err != nil {
		t.Fatal(err)
	}
	if started.Team == nil || len(started.Team.AgentIDs) != 1 || started.Team.AgentIDs[0] != second.ID {
		t.Fatalf("Start переиграл сохранённый человеком отряд: %#v", started.Team)
	}
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

func TestProviderCredentialRequirementUsesSelectedPreset(t *testing.T) {
	if providerNeedsAPIKey(domain.ProviderOpenAI, "lm-studio") {
		t.Fatal("LM Studio must not require an API key")
	}
	if !providerNeedsAPIKey(domain.ProviderOpenAI, "openai") {
		t.Fatal("OpenAI cloud preset must require an API key")
	}
	if providerNeedsAPIKey(domain.ProviderOllama, "") {
		t.Fatal("Ollama must not require an API key")
	}
	if !providerNeedsAPIKey(domain.ProviderOpenAI, "") {
		t.Fatal("unknown OpenAI-compatible preset must default to requiring a key")
	}
}

func TestFlowRescheduleReusesRememberedOrchestratorAPIKey(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	started := make(chan string, 2)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case started <- r.Header.Get("Authorization"):
		default:
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"done\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer provider.Close()

	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	if _, err = application.OpenWorkspace(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	agentItem, err := application.SaveProjectAgent(domain.ProjectAgent{
		Name: "Cloud", Provider: domain.ProviderOpenAI, ProviderPreset: "openai",
		BaseURL: provider.URL, PrimaryModel: "test-model", MaxSteps: 2, MaxDurationSeconds: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	flow, err := application.SaveFlow(domain.FlowGraph{
		Name: "Two stages",
		Nodes: []domain.FlowNode{
			{ID: "input", Kind: domain.FlowNodeInput},
			{ID: "first", Kind: domain.FlowNodeAgent, AgentID: agentItem.ID},
			{ID: "second", Kind: domain.FlowNodeAgent, AgentID: agentItem.ID},
			{ID: "output", Kind: domain.FlowNodeOutput},
		},
		Edges: []domain.FlowEdge{
			{From: "input", To: "first"}, {From: "first", To: "second"}, {From: "second", To: "output"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	quest, err := application.SaveQuest(domain.Quest{Title: "Reuse key", Status: domain.QuestActive, BudgetTokens: 20000})
	if err != nil {
		t.Fatal(err)
	}
	runtime := flowruntime.Runtime{Store: application.store}
	flowRun, err := runtime.Start(context.Background(), flowruntime.StartRequest{
		FlowID: flow.ID, WorkspaceID: quest.WorkspaceID, QuestID: quest.ID,
		Input: map[string]any{"task": quest.Title},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = application.scheduleFlowAgentExecutions(quest, flow, flowRun, "orchestrator-secret"); err != nil {
		t.Fatal(err)
	}
	select {
	case auth := <-started:
		if auth != "Bearer orchestrator-secret" {
			t.Fatalf("first auth=%q", auth)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("first stage did not start")
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		current, getErr := application.store.GetFlowRun(context.Background(), flowRun.ID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if state := current.NodeStates["second"]; state.Status != "" {
			if reason, _ := state.Output["waitReason"].(string); reason == "waiting_api_key" {
				t.Fatalf("second stage lost orchestrator credential: %#v", state.Output)
			}
			if state.Output["executionId"] != nil || state.Status == "running" || state.Status == "completed" {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	current, _ := application.store.GetFlowRun(context.Background(), flowRun.ID)
	t.Fatalf("second stage did not auto-start: %#v", current.NodeStates)
}

func TestLaunchPendingCloudExecutionUsesProvidedCredential(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	authorization := make(chan string, 1)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case authorization <- r.Header.Get("Authorization"):
		default:
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Cloud execution completed.\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer provider.Close()

	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	if _, err = application.OpenWorkspace(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	projectAgent, err := application.SaveProjectAgent(domain.ProjectAgent{
		Name:               "Cloud Agent",
		Provider:           domain.ProviderOpenAI,
		ProviderPreset:     "openai",
		BaseURL:            provider.URL,
		PrimaryModel:       "test-model",
		MaxSteps:           2,
		MaxDurationSeconds: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	execution, err := application.StartSandboxedExecution(projectAgent.ID, "Explain the result.", "")
	if err != nil {
		t.Fatal(err)
	}
	run, err := application.LaunchPendingExecution(execution.ID, "secret-from-keychain")
	if err != nil {
		t.Fatal(err)
	}

	select {
	case header := <-authorization:
		if header != "Bearer secret-from-keychain" {
			t.Fatalf("authorization=%q", header)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("provider did not receive the cloud execution")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		details, detailsErr := application.RunDetails(run.ID)
		if detailsErr != nil {
			t.Fatal(detailsErr)
		}
		if details.Run.Status == domain.RunCompleted {
			return
		}
		if details.Run.Status == domain.RunFailed {
			t.Fatalf("run failed: %s", details.Run.Error)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("cloud execution did not complete")
}

func TestCompletedFlowAgentPersistsExecutionIDAndSeedsNextSandbox(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Stage complete.\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer provider.Close()
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	root := t.TempDir()
	if err = os.WriteFile(filepath.Join(root, "state.txt"), []byte("live"), 0o644); err != nil {
		t.Fatal(err)
	}
	view, err := application.OpenWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	makeAgent := func(name string) domain.ProjectAgent {
		agent, saveErr := application.SaveProjectAgent(domain.ProjectAgent{
			Name: name, Provider: domain.ProviderOpenAI, ProviderPreset: "openai", BaseURL: provider.URL,
			PrimaryModel: "test-model", MaxSteps: 2, MaxDurationSeconds: 5,
		})
		if saveErr != nil {
			t.Fatal(saveErr)
		}
		return agent
	}
	firstAgent := makeAgent("First")
	secondAgent := makeAgent("Second")
	flow, err := application.SaveFlow(domain.FlowGraph{
		Name: "Sequential lineage",
		Nodes: []domain.FlowNode{
			{ID: "input", Kind: domain.FlowNodeInput},
			{ID: "first", Kind: domain.FlowNodeAgent, AgentID: firstAgent.ID},
			{ID: "second", Kind: domain.FlowNodeAgent, AgentID: secondAgent.ID},
			{ID: "output", Kind: domain.FlowNodeOutput},
		},
		Edges: []domain.FlowEdge{{From: "input", To: "first"}, {From: "first", To: "second"}, {From: "second", To: "output"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	flowRun, err := application.StartFlowRun(flow.ID, "", map[string]any{"task": "lineage"})
	if err != nil {
		t.Fatal(err)
	}
	executions, err := application.store.ListExecutions(context.Background(), view.Workspace.ID, 20)
	if err != nil || len(executions) != 1 {
		t.Fatalf("initial executions=%#v err=%v", executions, err)
	}
	parent := executions[0]
	parentSandbox, err := application.store.GetSandbox(context.Background(), parent.SandboxID)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(parentSandbox.Path, "state.txt"), []byte("stage-one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err = application.LaunchPendingExecution(parent.ID, "flow-secret"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(4 * time.Second)
	var child domain.ExecutionInstance
	for time.Now().Before(deadline) {
		executions, err = application.store.ListExecutions(context.Background(), view.Workspace.ID, 20)
		if err != nil {
			t.Fatal(err)
		}
		for _, execution := range executions {
			if execution.FlowNodeID == "second" {
				child = execution
				break
			}
		}
		if child.ID != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if child.ID == "" {
		t.Fatalf("child execution missing all=%#v", executions)
	}
	reloaded, err := application.store.GetFlowRun(context.Background(), flowRun.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.NodeStates["first"].Output["executionId"] != parent.ID {
		t.Fatalf("first output=%#v parent=%s", reloaded.NodeStates["first"].Output, parent.ID)
	}
	if reloaded.NodeStates["second"].Output["seedExecutionId"] != parent.ID || reloaded.NodeStates["second"].Output["sandboxLineage"] != "inherited" {
		t.Fatalf("second lineage=%#v", reloaded.NodeStates["second"].Output)
	}
	if reason, _ := reloaded.NodeStates["second"].Output["waitReason"].(string); reason == "waiting_api_key" {
		t.Fatalf("second stage should reuse the launched flow credential, waitReason=%q", reason)
	}
	childSandbox, err := application.store.GetSandbox(context.Background(), child.SandboxID)
	if err != nil {
		t.Fatal(err)
	}
	if childSandbox.ParentExecutionID != parent.ID || childSandbox.BaselinePath == "" {
		t.Fatalf("child sandbox=%#v", childSandbox)
	}
	seeded, err := os.ReadFile(filepath.Join(childSandbox.Path, "state.txt"))
	if err != nil || string(seeded) != "stage-one" {
		t.Fatalf("seeded state=%q err=%v", seeded, err)
	}
	live, err := os.ReadFile(filepath.Join(root, "state.txt"))
	if err != nil || string(live) != "live" {
		t.Fatalf("live state=%q err=%v", live, err)
	}
}

func TestFlowNodeContextIncludesQuestAndUpstreamResult(t *testing.T) {
	flow := domain.FlowGraph{
		Nodes: []domain.FlowNode{
			{ID: "primary", Name: "Primary implementation", AgentID: "agent-a"},
			{ID: "reviewer", Name: "Reviewer", AgentID: "agent-b"},
		},
		Edges: []domain.FlowEdge{{ID: "edge", From: "primary", To: "reviewer"}},
	}
	run := domain.FlowRun{ID: "flowrun-1", NodeStates: map[string]domain.FlowNodeState{
		"primary": {Status: "completed", Output: map[string]any{"executionId": "exec-a", "result": "implemented OAuth", "status": domain.RunCompleted}},
	}}
	quest := domain.Quest{
		ID: "quest", Title: "OAuth", Objectives: []string{"callback"}, DefinitionOfDone: []string{"tests pass"},
	}
	inputs := flowNodeContext(quest, flow, run, "reviewer", []domain.ChangeSet{{
		ID: "changes-primary", ExecutionID: "exec-a", Title: "Primary changes", Status: domain.ChangeSetPending,
		Items: []domain.ChangeItem{{
			Path: "internal/auth.go", Kind: "modify",
			Diff: "@@ -1 +1 @@\n-token := old\n+token := rotated\n+api_key=sk-12345678901234567890",
		}},
	}})
	if len(inputs) != 2 {
		t.Fatalf("inputs=%#v", inputs)
	}
	if inputs[1].Label != "Передача от · Primary implementation" {
		t.Fatalf("handoff label=%q", inputs[1].Label)
	}
	combined := inputs[0].Content + inputs[1].Content
	if !strings.Contains(combined, "tests pass") || !strings.Contains(combined, "implemented OAuth") {
		t.Fatalf("context=%s", combined)
	}
	if !strings.Contains(combined, `"kind":"agent_handoff"`) || !strings.Contains(combined, "agent-a") {
		t.Fatalf("structured handoff missing: %s", combined)
	}
	if !strings.Contains(combined, `"changeSets"`) || !strings.Contains(combined, "internal/auth.go") || !strings.Contains(combined, "@@ -1 +1 @@") {
		t.Fatalf("reviewable Change Set diff missing from handoff: %s", combined)
	}
	if strings.Contains(combined, "sk-12345678901234567890") || !strings.Contains(combined, "[REDACTED]") {
		t.Fatalf("Change Set handoff did not redact a likely secret: %s", combined)
	}
	if !strings.Contains(inputs[0].Content, `"currentNode":"Reviewer"`) {
		t.Fatalf("coordination role missing: %s", inputs[0].Content)
	}
}

func TestFlowNodeContextTraversesJoinToEveryCompletedAgent(t *testing.T) {
	flow := domain.FlowGraph{
		Nodes: []domain.FlowNode{
			{ID: "a", Kind: domain.FlowNodeAgent, Name: "Implementation", AgentID: "agent-a"},
			{ID: "b", Kind: domain.FlowNodeAgent, Name: "Tests", AgentID: "agent-b"},
			{ID: "join", Kind: domain.FlowNodeJoin, Name: "Join"},
			{ID: "review", Kind: domain.FlowNodeAgent, Name: "Review", AgentID: "agent-c"},
		},
		Edges: []domain.FlowEdge{{From: "a", To: "join"}, {From: "b", To: "join"}, {From: "join", To: "review"}},
	}
	run := domain.FlowRun{ID: "flowrun", NodeStates: map[string]domain.FlowNodeState{
		"a":    {Status: "completed", Output: map[string]any{"executionId": "exec-a", "result": "implemented"}},
		"b":    {Status: "completed", Output: map[string]any{"executionId": "exec-b", "result": "tested"}},
		"join": {Status: "completed", Output: map[string]any{"joined": true}},
	}}
	inputs := flowNodeContext(domain.Quest{ID: "quest", Title: "Feature"}, flow, run, "review", []domain.ChangeSet{
		{ID: "set-a", ExecutionID: "exec-a", Status: domain.ChangeSetPending, Items: []domain.ChangeItem{{Path: "feature.go", Diff: "+feature"}}},
		{ID: "set-b", ExecutionID: "exec-b", Status: domain.ChangeSetPending, Items: []domain.ChangeItem{{Path: "feature_test.go", Diff: "+test"}}},
	})
	if len(inputs) != 3 {
		t.Fatalf("inputs=%#v", inputs)
	}
	combined := inputs[1].Content + inputs[2].Content
	for _, expected := range []string{"exec-a", "exec-b", "feature.go", "feature_test.go", "implemented", "tested"} {
		if !strings.Contains(combined, expected) {
			t.Fatalf("join handoff missing %q: %s", expected, combined)
		}
	}
	if got := completedUpstreamExecutionIDs(flow, run, "review"); len(got) != 2 {
		t.Fatalf("parallel upstream executions=%v", got)
	}
}

func TestSequentialExecutionSandboxAndChangeSetLineage(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	root := t.TempDir()
	filePath := filepath.Join(root, "state.txt")
	if err = os.WriteFile(filePath, []byte("live"), 0o644); err != nil {
		t.Fatal(err)
	}
	workspaceRecord, err := application.OpenWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	boot, err := application.Bootstrap()
	if err != nil || len(boot.Blueprints) == 0 {
		t.Fatalf("bootstrap blueprints=%d err=%v", len(boot.Blueprints), err)
	}
	firstAgent := domain.ProjectAgentFromBlueprint(workspaceRecord.Workspace.ID, boot.Blueprints[0])
	firstAgent.Name = "Implementer"
	firstAgent, err = application.SaveProjectAgent(firstAgent)
	if err != nil {
		t.Fatal(err)
	}
	secondAgent := domain.ProjectAgentFromBlueprint(workspaceRecord.Workspace.ID, boot.Blueprints[0])
	secondAgent.Name = "Reviewer"
	secondAgent, err = application.SaveProjectAgent(secondAgent)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := application.StartSandboxedExecution(firstAgent.ID, "stage one", "")
	if err != nil {
		t.Fatal(err)
	}
	parentSandbox, err := application.store.GetSandbox(context.Background(), parent.SandboxID)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(parentSandbox.Path, "state.txt"), []byte("stage-one"), 0o644); err != nil {
		t.Fatal(err)
	}
	parent.Status = domain.RunCompleted
	now := time.Now().UTC()
	parent.FinishedAt = &now
	if err = application.store.SaveExecution(context.Background(), parent); err != nil {
		t.Fatal(err)
	}
	parentSet, err := application.BuildChangeSet(parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	child, err := application.startSandboxedExecution(secondAgent.ID, "stage two", "", parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	childSandbox, err := application.store.GetSandbox(context.Background(), child.SandboxID)
	if err != nil {
		t.Fatal(err)
	}
	seeded, err := os.ReadFile(filepath.Join(childSandbox.Path, "state.txt"))
	if err != nil || string(seeded) != "stage-one" {
		t.Fatalf("child seed=%q err=%v", seeded, err)
	}
	if err = os.WriteFile(filepath.Join(childSandbox.Path, "state.txt"), []byte("stage-two"), 0o644); err != nil {
		t.Fatal(err)
	}
	child.Status = domain.RunCompleted
	child.FinishedAt = &now
	if err = application.store.SaveExecution(context.Background(), child); err != nil {
		t.Fatal(err)
	}
	childSet, err := application.BuildChangeSet(child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(childSet.DependsOn) != 1 || childSet.DependsOn[0] != parentSet.ID {
		t.Fatalf("child dependencies=%v parent=%s", childSet.DependsOn, parentSet.ID)
	}
	if len(childSet.Items) != 1 || childSet.Items[0].OriginalContent != "stage-one" || childSet.Items[0].ProposedContent != "stage-two" {
		t.Fatalf("child incremental set=%#v", childSet.Items)
	}
	if _, err = application.ApplyChangeSet(childSet.ID); err == nil || !strings.Contains(err.Error(), "prerequisite") {
		t.Fatalf("dependent set applied before parent: %v", err)
	}
	if _, err = application.ApplyChangeSet(parentSet.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = application.ApplyChangeSet(childSet.ID); err != nil {
		t.Fatal(err)
	}
	live, err := os.ReadFile(filePath)
	if err != nil || string(live) != "stage-two" {
		t.Fatalf("applied chain=%q err=%v", live, err)
	}
	if _, err = application.RevertChangeSet(parentSet.ID); err == nil || !strings.Contains(err.Error(), "dependent") {
		t.Fatalf("parent reverted before child: %v", err)
	}
	if _, err = application.RevertChangeSet(childSet.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = application.RevertChangeSet(parentSet.ID); err != nil {
		t.Fatal(err)
	}
	live, err = os.ReadFile(filePath)
	if err != nil || string(live) != "live" {
		t.Fatalf("reverted chain=%q err=%v", live, err)
	}
}

func TestParallelSandboxMergeCreatesAggregateChangeSet(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	root := t.TempDir()
	for path, content := range map[string]string{"a.txt": "a-base", "b.txt": "b-base"} {
		if err = os.WriteFile(filepath.Join(root, path), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	view, err := application.OpenWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	makeAgent := func(name string) domain.ProjectAgent {
		agent, saveErr := application.SaveProjectAgent(domain.ProjectAgent{
			Name: name, Provider: domain.ProviderOpenAI, ProviderPreset: "openai", PrimaryModel: "test-model",
			MaxSteps: 2, MaxDurationSeconds: 5,
		})
		if saveErr != nil {
			t.Fatal(saveErr)
		}
		return agent
	}
	branchAgent := makeAgent("Branch")
	mergeAgent := makeAgent("Integrator")
	branchA, err := application.StartSandboxedExecution(branchAgent.ID, "Branch A", "")
	if err != nil {
		t.Fatal(err)
	}
	branchB, err := application.StartSandboxedExecution(branchAgent.ID, "Branch B", "")
	if err != nil {
		t.Fatal(err)
	}
	for index, branch := range []*domain.ExecutionInstance{&branchA, &branchB} {
		record, loadErr := application.store.GetSandbox(context.Background(), branch.SandboxID)
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		path, content := "a.txt", "a-branch"
		if index == 1 {
			path, content = "b.txt", "b-branch"
		}
		if err = os.WriteFile(filepath.Join(record.Path, path), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		branch.Status, branch.FinishedAt = domain.RunCompleted, &now
		if err = application.store.SaveExecution(context.Background(), *branch); err != nil {
			t.Fatal(err)
		}
	}
	setA, err := application.BuildChangeSet(branchA.ID)
	if err != nil {
		t.Fatal(err)
	}
	setB, err := application.BuildChangeSet(branchB.ID)
	if err != nil {
		t.Fatal(err)
	}
	mergedExec, merged, mergeSet, err := application.startMergedSandboxedExecution(
		mergeAgent.ID, "Integrate", "", "flow-run", "integrator", []string{branchA.ID, branchB.ID}, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if mergedExec.ID == "" || len(merged.Conflicts) != 0 || mergeSet == nil || mergeSet.Kind != "merge" {
		t.Fatalf("exec=%#v merged=%#v mergeSet=%#v", mergedExec, merged, mergeSet)
	}
	if len(mergeSet.Supersedes) != 2 || len(mergeSet.Items) != 2 {
		t.Fatalf("aggregate merge set=%#v", mergeSet)
	}
	for _, sourceID := range []string{setA.ID, setB.ID} {
		source, loadErr := application.store.GetChangeSet(context.Background(), sourceID)
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		if source.Status != domain.ChangeSetSuperseded || source.SupersededBy != mergeSet.ID {
			t.Fatalf("source set not superseded: %#v", source)
		}
		if _, applyErr := application.ApplyChangeSet(source.ID); applyErr == nil {
			t.Fatalf("superseded source %s remained independently applicable", source.ID)
		}
	}
	mergedRecord, err := application.store.GetSandbox(context.Background(), mergedExec.SandboxID)
	if err != nil {
		t.Fatal(err)
	}
	if len(mergedRecord.ParentExecutionIDs) != 2 || len(mergedRecord.BaselineChangeSetIDs) != 1 || mergedRecord.BaselineChangeSetIDs[0] != mergeSet.ID {
		t.Fatalf("merged lineage=%#v", mergedRecord)
	}
	for path, expected := range map[string]string{"a.txt": "a-branch", "b.txt": "b-branch"} {
		content, readErr := os.ReadFile(filepath.Join(mergedRecord.Path, path))
		if readErr != nil || string(content) != expected {
			t.Fatalf("merged %s=%q err=%v", path, content, readErr)
		}
	}
	if _, err = application.ApplyChangeSet(mergeSet.ID); err != nil {
		t.Fatal(err)
	}
	for path, expected := range map[string]string{"a.txt": "a-branch", "b.txt": "b-branch"} {
		content, readErr := os.ReadFile(filepath.Join(root, path))
		if readErr != nil || string(content) != expected {
			t.Fatalf("live %s=%q err=%v workspace=%s", path, content, readErr, view.Workspace.ID)
		}
	}
	if err = os.WriteFile(filepath.Join(mergedRecord.Path, "a.txt"), []byte("integrated"), 0o644); err != nil {
		t.Fatal(err)
	}
	childSet, err := application.BuildChangeSet(mergedExec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(childSet.DependsOn) != 1 || childSet.DependsOn[0] != mergeSet.ID || len(childSet.Items) != 1 {
		t.Fatalf("post-merge child set=%#v", childSet)
	}
}

func TestParallelFlowWaitsForExplicitSandboxMergeResolution(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	root := t.TempDir()
	if err = os.WriteFile(filepath.Join(root, "shared.txt"), []byte("base"), 0o644); err != nil {
		t.Fatal(err)
	}
	view, err := application.OpenWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	makeAgent := func(name string) domain.ProjectAgent {
		agent, saveErr := application.SaveProjectAgent(domain.ProjectAgent{
			Name: name, Provider: domain.ProviderOpenAI, ProviderPreset: "openai", PrimaryModel: "test-model",
			MaxSteps: 2, MaxDurationSeconds: 5,
		})
		if saveErr != nil {
			t.Fatal(saveErr)
		}
		return agent
	}
	agentA, agentB, integrator := makeAgent("A"), makeAgent("B"), makeAgent("Integrator")
	flow, err := application.SaveFlow(domain.FlowGraph{
		Name: "Parallel merge resolution",
		Nodes: []domain.FlowNode{
			{ID: "input", Kind: domain.FlowNodeInput},
			{ID: "parallel", Kind: domain.FlowNodeParallel},
			{ID: "a", Kind: domain.FlowNodeAgent, AgentID: agentA.ID},
			{ID: "b", Kind: domain.FlowNodeAgent, AgentID: agentB.ID},
			{ID: "join", Kind: domain.FlowNodeJoin},
			{ID: "integrate", Kind: domain.FlowNodeAgent, AgentID: integrator.ID},
			{ID: "output", Kind: domain.FlowNodeOutput},
		},
		Edges: []domain.FlowEdge{
			{From: "input", To: "parallel"}, {From: "parallel", To: "a"}, {From: "parallel", To: "b"},
			{From: "a", To: "join"}, {From: "b", To: "join"}, {From: "join", To: "integrate"}, {From: "integrate", To: "output"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	flowRun, err := application.StartFlowRun(flow.ID, "", map[string]any{"task": "merge"})
	if err != nil {
		t.Fatal(err)
	}
	executions, err := application.store.ListExecutions(context.Background(), view.Workspace.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	byNode := map[string]domain.ExecutionInstance{}
	for _, execution := range executions {
		byNode[execution.FlowNodeID] = execution
	}
	if byNode["a"].ID == "" || byNode["b"].ID == "" {
		t.Fatalf("parallel executions=%#v", executions)
	}
	runtime := flowruntime.Runtime{Store: application.store}
	current := flowRun
	for _, nodeID := range []string{"a", "b"} {
		execution := byNode[nodeID]
		record, loadErr := application.store.GetSandbox(context.Background(), execution.SandboxID)
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		if err = os.WriteFile(filepath.Join(record.Path, "shared.txt"), []byte("from-"+nodeID), 0o644); err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		execution.Status, execution.FinishedAt = domain.RunCompleted, &now
		if err = application.store.SaveExecution(context.Background(), execution); err != nil {
			t.Fatal(err)
		}
		if _, err = application.BuildChangeSet(execution.ID); err != nil {
			t.Fatal(err)
		}
		current, err = runtime.CompleteAgentNode(context.Background(), flowRun.ID, nodeID, true, map[string]any{
			"executionId": execution.ID, "result": "done " + nodeID,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = application.scheduleFlowAgentExecutionsFromRun(current); err != nil {
		t.Fatal(err)
	}
	waiting, err := application.store.GetFlowRun(context.Background(), flowRun.ID)
	if err != nil {
		t.Fatal(err)
	}
	state := waiting.NodeStates["integrate"]
	conflictCount := 0
	switch value := state.Output["mergeConflictCount"].(type) {
	case int:
		conflictCount = value
	case float64:
		conflictCount = int(value)
	}
	if state.Output["waitReason"] != "sandbox_merge_conflict" || conflictCount != 1 {
		t.Fatalf("merge conflict state=%#v", state)
	}
	if _, exists := state.Output["executionId"]; exists {
		t.Fatalf("integrator execution created before conflict resolution: %#v", state.Output)
	}
	resolved, err := application.ResolveFlowSandboxMerge(flowRun.ID, "integrate", sandbox.MergeResolution{
		Path: "shared.txt", Strategy: "use_parent", ExecutionID: byNode["b"].ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	state = resolved.NodeStates["integrate"]
	if state.Output["sandboxLineage"] != "merged_parallel_join" || state.Output["executionId"] == nil {
		t.Fatalf("resolved lineage=%#v", state.Output)
	}
	integratorExec, err := application.findExecution(fmt.Sprint(state.Output["executionId"]))
	if err != nil {
		t.Fatal(err)
	}
	integratorSandbox, err := application.store.GetSandbox(context.Background(), integratorExec.SandboxID)
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(integratorSandbox.Path, "shared.txt"))
	if err != nil || string(content) != "from-b" {
		t.Fatalf("resolved sandbox=%q err=%v", content, err)
	}
}

func TestParallelRootBranchesReuseOneImmutableFlowSnapshot(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	root := t.TempDir()
	path := filepath.Join(root, "state.txt")
	if err = os.WriteFile(path, []byte("flow-start"), 0o644); err != nil {
		t.Fatal(err)
	}
	view, err := application.OpenWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := application.SaveProjectAgent(domain.ProjectAgent{
		Name: "Branch", Provider: domain.ProviderOpenAI, ProviderPreset: "openai", PrimaryModel: "test-model",
		MaxSteps: 2, MaxDurationSeconds: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := application.startSandboxedExecutionWithSeed(agent.ID, "A", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	first.FlowRunID = "flow-run"
	first.FlowNodeID = "a"
	if err = application.store.SaveExecution(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("user-edited-during-flow"), 0o644); err != nil {
		t.Fatal(err)
	}
	flowRun := domain.FlowRun{ID: "flow-run", WorkspaceID: view.Workspace.ID}
	seed := application.rootFlowSeedPath(flowRun)
	if seed == "" {
		t.Fatal("flow root seed was not found")
	}
	second, err := application.startSandboxedExecutionWithSeed(agent.ID, "B", "", "", seed)
	if err != nil {
		t.Fatal(err)
	}
	secondRecord, err := application.store.GetSandbox(context.Background(), second.SandboxID)
	if err != nil {
		t.Fatal(err)
	}
	for _, snapshotPath := range []string{secondRecord.Path, secondRecord.BaselinePath} {
		content, readErr := os.ReadFile(filepath.Join(snapshotPath, "state.txt"))
		if readErr != nil || string(content) != "flow-start" {
			t.Fatalf("branch forked from live drift: %s=%q err=%v", snapshotPath, content, readErr)
		}
	}
}

func TestResumeActiveFlowReusesInterruptedExecution(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	workspaceRoot := t.TempDir()
	view, err := application.OpenWorkspace(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	projectAgent, err := application.SaveProjectAgent(domain.ProjectAgent{
		Name: "Cloud Agent", Provider: domain.ProviderOpenAI, ProviderPreset: "openai",
		BaseURL: "https://api.openai.com/v1", PrimaryModel: "test-model", MaxSteps: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	flow, err := application.SaveFlow(domain.FlowGraph{
		Name: "Resume flow",
		Nodes: []domain.FlowNode{
			{ID: "input", Kind: domain.FlowNodeInput},
			{ID: "agent", Kind: domain.FlowNodeAgent, AgentID: projectAgent.ID},
			{ID: "output", Kind: domain.FlowNodeOutput},
		},
		Edges: []domain.FlowEdge{{ID: "e1", From: "input", To: "agent"}, {ID: "e2", From: "agent", To: "output"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	flowRun, err := application.StartFlowRun(flow.ID, "", map[string]any{"task": "resume"})
	if err != nil {
		t.Fatal(err)
	}
	executions, err := application.store.ListExecutions(context.Background(), view.Workspace.ID, 20)
	if err != nil || len(executions) != 1 {
		t.Fatalf("executions=%#v err=%v", executions, err)
	}
	stale := executions[0]
	stale.Status = domain.RunRunning
	stale.RunID = "stale-run"
	if err = application.store.SaveExecution(context.Background(), stale); err != nil {
		t.Fatal(err)
	}
	if err = application.ResumeActiveFlowRuns(view.Workspace.ID); err != nil {
		t.Fatal(err)
	}
	executions, err = application.store.ListExecutions(context.Background(), view.Workspace.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(executions) != 1 || executions[0].ID != stale.ID || executions[0].Status != domain.RunInterrupted {
		t.Fatalf("executions=%#v", executions)
	}
	reloaded, err := application.store.GetFlowRun(context.Background(), flowRun.ID)
	if err != nil || reloaded.NodeStates["agent"].Output["executionId"] != stale.ID {
		t.Fatalf("flowRun=%#v err=%v", reloaded, err)
	}
}

func TestToolFlowNodeCreatesSandboxedSingleToolExecution(t *testing.T) {
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
	projectAgent, err := application.SaveProjectAgent(domain.ProjectAgent{
		Name: "Tool Agent", Provider: domain.ProviderOpenAI, ProviderPreset: "openai",
		BaseURL: "https://api.openai.com/v1", PrimaryModel: "test-model", MaxSteps: 20,
		AllowedTools: []string{"list_files"},
	})
	if err != nil {
		t.Fatal(err)
	}
	flow, err := application.SaveFlow(domain.FlowGraph{
		Name: "Tool flow",
		Nodes: []domain.FlowNode{
			{ID: "input", Kind: domain.FlowNodeInput},
			{ID: "tool", Kind: domain.FlowNodeTool, AgentID: projectAgent.ID, ToolName: "list_files", Config: map[string]any{"arguments": map[string]any{"maxDepth": 2}}},
			{ID: "output", Kind: domain.FlowNodeOutput},
		},
		Edges: []domain.FlowEdge{{ID: "e1", From: "input", To: "tool"}, {ID: "e2", From: "tool", To: "output"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	flowRun, err := application.StartFlowRun(flow.ID, "", map[string]any{"task": "inspect"})
	if err != nil {
		t.Fatal(err)
	}
	executions, err := application.store.ListExecutions(context.Background(), view.Workspace.ID, 20)
	if err != nil || len(executions) != 1 {
		t.Fatalf("executions=%#v err=%v", executions, err)
	}
	if executions[0].FlowRunID != flowRun.ID || executions[0].FlowNodeID != "tool" ||
		executions[0].SandboxID == "" || executions[0].Status != domain.RunCompleted || executions[0].Task != "Tool: list_files" {
		t.Fatalf("execution=%#v", executions[0])
	}
	if flowRun.Status != domain.RunCompleted || flowRun.NodeStates["tool"].Status != "completed" {
		t.Fatalf("deterministic tool did not complete the flow: %#v", flowRun)
	}
}

func TestBoundedLoopSchedulesFreshToolExecutionPerIteration(t *testing.T) {
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
	projectAgent, err := application.SaveProjectAgent(domain.ProjectAgent{
		Name: "Loop Tool Agent", Provider: domain.ProviderOpenAI, ProviderPreset: "openai",
		BaseURL: "https://api.openai.com/v1", PrimaryModel: "test-model", MaxSteps: 20,
		AllowedTools: []string{"list_files"},
	})
	if err != nil {
		t.Fatal(err)
	}
	flow, err := application.SaveFlow(domain.FlowGraph{
		Name: "Bounded tool loop",
		Nodes: []domain.FlowNode{
			{ID: "input", Kind: domain.FlowNodeInput},
			{ID: "loop", Kind: domain.FlowNodeLoop, Config: map[string]any{"maxIterations": 2}},
			{ID: "tool", Kind: domain.FlowNodeTool, AgentID: projectAgent.ID, ToolName: "list_files", Config: map[string]any{"arguments": map[string]any{"maxDepth": 1}}},
			{ID: "output", Kind: domain.FlowNodeOutput},
		},
		Edges: []domain.FlowEdge{
			{ID: "entry", From: "input", To: "loop"},
			{ID: "continue", From: "loop", To: "tool", Condition: "continue"},
			{ID: "back", From: "tool", To: "loop"},
			{ID: "done", From: "loop", To: "output", Condition: "done"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	flowRun, err := application.StartFlowRun(flow.ID, "", map[string]any{"task": "inspect twice"})
	if err != nil {
		t.Fatal(err)
	}
	if flowRun.Status != domain.RunCompleted || flowRun.NodeStates["loop"].Attempts != 3 || flowRun.NodeStates["tool"].Attempts != 2 {
		t.Fatalf("bounded loop did not complete deterministically: %#v", flowRun)
	}
	executions, err := application.store.ListExecutions(context.Background(), view.Workspace.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(executions) != 2 {
		t.Fatalf("expected one fresh sandboxed execution per iteration, got %#v", executions)
	}
	for _, execution := range executions {
		if execution.FlowRunID != flowRun.ID || execution.FlowNodeID != "tool" || execution.SandboxID == "" || execution.Status != domain.RunCompleted {
			t.Fatalf("invalid loop execution: %#v", execution)
		}
	}
}

func TestFlowFailureFinalizesQuestAndSkipsScheduling(t *testing.T) {
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
	primary, err := application.SaveProjectAgent(domain.ProjectAgent{
		Name: "Primary", Provider: domain.ProviderOllama, ProviderPreset: "ollama", PrimaryModel: "test", MaxSteps: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	reviewer, err := application.SaveProjectAgent(domain.ProjectAgent{
		Name: "Reviewer", Provider: domain.ProviderOllama, ProviderPreset: "ollama", PrimaryModel: "test", MaxSteps: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	flow, err := application.SaveFlow(domain.FlowGraph{
		Name: "Parallel fail",
		Nodes: []domain.FlowNode{
			{ID: "input", Kind: domain.FlowNodeInput},
			{ID: "fork", Kind: domain.FlowNodeParallel},
			{ID: "a", Kind: domain.FlowNodeAgent, Name: "A", AgentID: primary.ID},
			{ID: "b", Kind: domain.FlowNodeAgent, Name: "B", AgentID: reviewer.ID},
			{ID: "join", Kind: domain.FlowNodeJoin},
			{ID: "output", Kind: domain.FlowNodeOutput},
		},
		Edges: []domain.FlowEdge{
			{ID: "e1", From: "input", To: "fork"},
			{ID: "e2", From: "fork", To: "a"},
			{ID: "e3", From: "fork", To: "b"},
			{ID: "e4", From: "a", To: "join"},
			{ID: "e5", From: "b", To: "join"},
			{ID: "e6", From: "join", To: "output"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	quest := domain.Quest{
		ID: domain.NewID("quest"), WorkspaceID: view.Workspace.ID, Title: "Ship",
		Status: domain.QuestActive, FlowID: flow.ID, CreatedAt: now, UpdatedAt: now,
	}
	if err = application.store.SaveQuest(context.Background(), quest); err != nil {
		t.Fatal(err)
	}
	flowRun, err := application.StartFlowRun(flow.ID, quest.ID, map[string]any{"task": "parallel"})
	if err != nil {
		t.Fatal(err)
	}
	pending := domain.ExecutionInstance{
		ID: domain.NewID("exec"), WorkspaceID: view.Workspace.ID, ProjectAgentID: reviewer.ID,
		QuestID: quest.ID, FlowRunID: flowRun.ID, FlowNodeID: "b", Task: "B",
		Status: domain.RunPending, StartedAt: now,
	}
	if err = application.store.SaveExecution(context.Background(), pending); err != nil {
		t.Fatal(err)
	}

	runtime := flowruntime.Runtime{Store: application.store}
	flowRun, err = runtime.CompleteAgentNode(context.Background(), flowRun.ID, "a", false, map[string]any{
		"error": "branch A failed", "status": domain.RunFailed,
	})
	if err != nil {
		t.Fatal(err)
	}
	if flowRun.Status != domain.RunFailed {
		t.Fatalf("flow status=%s", flowRun.Status)
	}
	application.cancelSiblingFlowExecutions(flowRun.ID, "")
	application.finalizeQuestAfterFlow(quest.ID, false)

	quests, err := application.store.ListQuests(context.Background(), view.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	var savedQuest domain.Quest
	for _, item := range quests {
		if item.ID == quest.ID {
			savedQuest = item
			break
		}
	}
	if savedQuest.ID == "" || savedQuest.Status != domain.QuestFailed {
		t.Fatalf("quest=%#v", savedQuest)
	}
	exec, err := application.findExecution(pending.ID)
	if err != nil {
		t.Fatal(err)
	}
	if exec.Status != domain.RunCancelled {
		t.Fatalf("sibling execution status=%s", exec.Status)
	}
	if err = application.scheduleFlowAgentExecutionsFromRun(flowRun); err != nil {
		t.Fatal(err)
	}
}

// Исполняющий агент читает задачу, а не рассказ о том, как его выбирали.
//
// Описание квеста уходит в контекст агента. Пока задаче не было места в
// предложении, туда попадал Rationale — «пресет conductor · отряд 1 · движком
// Point». Проверяем оба случая: с задачей и без неё (предложения, созданные до
// появления поля, обязаны сохранить прежнее поведение, а не остаться пустыми).
func TestQuestDescriptionCarriesTheTask(t *testing.T) {
	task := "почини вебхук биллинга: 500 на повторной доставке"
	withTask := domain.QuestProposal{
		Title: "Починить вебхук биллинга", Task: task,
		Rationale: "пресет conductor · отряд 1 · движком Point",
	}
	if got := questDescription(withTask); got != task {
		t.Fatalf("в описание квеста ушла не задача: %q", got)
	}

	legacy := domain.QuestProposal{
		Title:     "Починить вебхук биллинга",
		Rationale: "пресет conductor · отряд 1 · движком Point",
	}
	if got := questDescription(legacy); got != legacy.Rationale {
		t.Fatalf("старое предложение осталось без описания: %q", got)
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

func TestSaveProjectAgentRejectsCursorCLI(t *testing.T) {
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
	_, err = application.SaveProjectAgent(domain.ProjectAgent{
		Name: "Interactive Cursor", Provider: domain.ProviderCursor, ProviderPreset: "cursor",
		PrimaryModel: "auto", AllowedTools: []string{"read_file", "propose_patch"}, MaxSteps: 8,
	})
	if err == nil || (!strings.Contains(err.Error(), "removed") && !strings.Contains(err.Error(), "CLI")) {
		t.Fatalf("expected Cursor CLI rejection, got %v", err)
	}
}
