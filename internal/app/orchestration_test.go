package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/companion"
	"local-agent-workbench/internal/domain"
)

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
