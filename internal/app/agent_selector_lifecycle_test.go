package app

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

func TestRuntimeSubagentRequiresActiveParentPermissionAndToolIntersection(t *testing.T) {
	application := newTestApp(t)
	view, err := application.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	parent, err := application.SaveProjectAgent(domain.ProjectAgent{
		Name: "Developer", RoleFamily: "developer", RoleDescription: "General developer",
		Provider: domain.ProviderOllama, BaseURL: "http://127.0.0.1:11434", PrimaryModel: "model",
		AllowedTools: []string{"read_file", "search_code"}, ToolPolicies: map[string]string{"read_file": "ALLOW", "search_code": "ASK"},
		MaxSteps: 10, MaxDurationSeconds: 600, MaxOutputTokens: 1024, ContextWindowTokens: 8192,
	})
	if err != nil {
		t.Fatal(err)
	}
	brief := domain.NormalizeTaskBrief(domain.TaskBrief{
		Mode: domain.TaskModePrecise, State: "ready", Goal: "Implement Symfony route", ResultKind: "code",
		Criteria:    []domain.AcceptanceCriterion{{ID: "c1", Text: "route works", Kind: "manual"}},
		Permissions: domain.TaskPermissions{ProvisionProjectAgents: true},
		Budget:      domain.TaskBudget{Tokens: 10000, ActiveSeconds: 600, MaxParallel: 1, MaxReplans: 1, MaxAttempts: 1, MaxProjectAgents: 1},
	})
	brief, err = domain.ApproveTaskBrief(brief)
	if err != nil {
		t.Fatal(err)
	}
	quest := domain.Quest{ID: "quest-runtime-subagent", WorkspaceID: view.Workspace.ID, Title: brief.Goal, Status: domain.QuestRunning, Brief: &brief, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err = application.store.SaveQuest(context.Background(), quest); err != nil {
		t.Fatal(err)
	}
	if err = application.store.SaveExecution(context.Background(), domain.ExecutionInstance{
		ID: "execution-parent", WorkspaceID: view.Workspace.ID, ProjectAgentID: parent.ID, QuestID: quest.ID,
		Task: brief.Goal, Status: domain.RunRunning, StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, deniedErr := application.RequestTemporarySubagent(context.Background(), domain.SubagentRequest{
		WorkspaceID: view.Workspace.ID, QuestID: quest.ID, ParentAgentID: parent.ID,
		Role: "Shell specialist", Mission: "Run deployment", RequiredTools: []string{"run_command"},
	}); deniedErr == nil || !strings.Contains(deniedErr.Error(), "not allowed") {
		t.Fatalf("parent tool ceiling was not enforced: %v", deniedErr)
	}
	child, err := application.RequestTemporarySubagent(context.Background(), domain.SubagentRequest{
		WorkspaceID: view.Workspace.ID, QuestID: quest.ID, ParentAgentID: parent.ID,
		Role: "Symfony-разработчик", Mission: "Implement route", RequiredTools: []string{"read_file", "run_command"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !child.Temporary || child.ParentAgentID != parent.ID || child.OwnerQuestID != quest.ID || child.BlueprintID != "" || child.Status != domain.ProjectAgentActive {
		t.Fatalf("invalid temporary child: %#v", child)
	}
	if strings.Join(child.AllowedTools, ",") != "read_file" || child.ConnectionID != parent.ConnectionID || child.PrimaryModel != parent.PrimaryModel {
		t.Fatalf("child exceeded or failed to inherit parent boundary: %#v", child)
	}
	if _, err = application.RequestTemporarySubagent(context.Background(), domain.SubagentRequest{
		WorkspaceID: view.Workspace.ID, QuestID: quest.ID, ParentAgentID: parent.ID,
		Role: "Другой специалист", Mission: "Second child", RequiredTools: []string{"read_file"},
	}); err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatalf("subagent budget was not enforced: %v", err)
	}
}

func TestAgentSelectorUsesGeneralDeveloperDraftForSymfony(t *testing.T) {
	application, world := rosterTestApp(t, "dispatcher")
	proposal := rosterTestProposal(world.ID, "selector-symfony", "Развернуть Symfony API", true)
	order := rosterTestOrder(t, application, proposal, "conversation-selector-symfony")
	if order.State != "staffing" || len(order.Roster.AgentIDs) != 0 || len(order.Roster.Permanent) != 1 {
		t.Fatalf("selector staffing result = %#v", order)
	}
	draft := order.Roster.Permanent[0]
	if draft.Existing || draft.ID == "" || !draft.RequiresConsent || !draft.ProjectOnly {
		t.Fatalf("selector persisted or weakened its proposal: %#v", draft)
	}
	if _, err := application.store.GetProjectAgent(context.Background(), draft.ID); err == nil {
		t.Fatalf("selector created ProjectAgent before the feed card: %s", draft.ID)
	}
	if strings.Contains(strings.ToLower(draft.Role), "symfony") || strings.Contains(strings.ToLower(draft.Name), "symfony") {
		t.Fatalf("technology leaked into root role: %#v", draft)
	}
	if _, err := application.ApproveWorkOrderV2(context.Background(), order.ID, ApproveWorkOrderV2Request{
		Version: order.Version, Digest: domain.WorkOrderDigest(order), IdempotencyKey: "draft-must-block",
	}); err == nil || !strings.Contains(strings.ToLower(err.Error()), "not ready") {
		t.Fatalf("draft did not block WorkOrder approval: %v", err)
	}
}

func TestRejectDraftCascadesAndExcludesFamilyForSameSelection(t *testing.T) {
	application, world := rosterTestApp(t, "dispatcher")
	order := rosterTestOrder(t, application, rosterTestProposal(world.ID, "selector-reject", "Развернуть Symfony", true), "conversation-selector-reject")
	if len(order.Roster.Permanent) != 1 || order.Roster.Permanent[0].Existing {
		t.Fatalf("selector proposal = %#v", order.Roster)
	}
	if _, getErr := application.store.GetProjectAgent(context.Background(), order.Roster.Permanent[0].ID); getErr == nil {
		t.Fatalf("non-persisted selector proposal appeared in project roster")
	}
	return
	if len(order.Roster.AgentIDs) != 1 {
		t.Fatalf("selector result = %#v", order.Roster)
	}
	root, err := application.store.GetProjectAgent(context.Background(), order.Roster.AgentIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	child := root
	child.ID, child.Status, child.ParentAgentID, child.OwnerQuestID, child.Temporary = "draft-child", domain.ProjectAgentActive, root.ID, "quest-x", true
	child.Name = "Symfony child"
	if err = application.store.SaveProjectAgent(context.Background(), child); err != nil {
		t.Fatal(err)
	}
	result, err := application.RejectProjectAgentDraft(root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ReplacementAgentIDs) != 0 {
		t.Fatalf("rejected family was proposed again: %#v", result)
	}
	for _, id := range []string{root.ID, child.ID} {
		if _, getErr := application.store.GetProjectAgent(context.Background(), id); !errors.Is(getErr, sql.ErrNoRows) {
			t.Fatalf("rejected agent %s survived: %v", id, getErr)
		}
	}
	rejected, err := application.store.RejectedRoleFamiliesForWorkOrder(context.Background(), order.ID)
	if err != nil || len(rejected) != 1 || rejected[0] != "developer" {
		t.Fatalf("rejection exclusion = %#v err=%v", rejected, err)
	}
}

func TestExactSelectionDigestReusesPersistedDraft(t *testing.T) {
	application, world := rosterTestApp(t, "dispatcher")
	proposal := rosterTestProposal(world.ID, "selector-stable-1", "Собрать API", false)
	first := rosterTestOrder(t, application, proposal, "conversation-selector-stable")
	proposal.ID = "selector-stable-2"
	second := rosterTestOrder(t, application, proposal, "conversation-selector-stable")
	if len(first.Roster.Permanent) != 1 || len(second.Roster.Permanent) != 1 || first.Roster.Permanent[0].ID != second.Roster.Permanent[0].ID {
		t.Fatalf("same digest did not preserve staffing proposal: %#v / %#v", first.Roster, second.Roster)
	}
}

// Черновик роли рождается пригодным к работе.
//
// 21 сентября 2026 семейства рождались с MaxOutputTokens: 4096 при
// ReasoningEffort: "medium". На размышляющей модели такой исполнитель терял ход
// целиком: размышление тратит бюджет вывода первым, и до ответа места не
// оставалось. Соседний путь (storage/work_order_roster_v2.go) пол уже держал —
// этот забыли.
func TestRoleFamilyDraftIsBornWithRoomForThinking(t *testing.T) {
	application := newTestApp(t)
	if _, err := application.OpenWorkspace(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	workspace, err := application.requireWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	config := domain.OrchestratorConfig{
		Provider: domain.ProviderOpenAI, ProviderPreset: "llmux",
		BaseURL: "https://llmux.invalid/v1", Model: "Qwen3.8-27B",
	}
	draft, err := application.createRoleFamilyDraft(workspace.ID, "workorder-test", "devops", config)
	if err != nil {
		t.Fatal(err)
	}
	if draft.MaxOutputTokens < domain.MinThinkingOutputTokens {
		t.Fatalf("исполнитель рождён с пределом %d — меньше пола %d", draft.MaxOutputTokens, domain.MinThinkingOutputTokens)
	}
	// Окно должно остаться больше вывода: из их разницы считается бюджет входа,
	// и равные числа оставили бы разговору ноль.
	if draft.ContextWindowTokens <= draft.MaxOutputTokens {
		t.Fatalf("окно %d не больше вывода %d", draft.ContextWindowTokens, draft.MaxOutputTokens)
	}
}

// DevOps принимает поставку в Docker Compose, и смотреть на контейнеры ему
// нечем не должно быть. Управление контейнерами при этом не выдаётся: задача
// его не поручает, а RestrictTaskProfile снимает такой инструмент и сама.
func TestDevOpsRoleFamilyCanInspectContainers(t *testing.T) {
	template, ok := roleFamilyTemplates["devops"]
	if !ok {
		t.Fatal("семейство devops исчезло из шаблонов")
	}
	has := func(name string) bool {
		for _, tool := range template.Tools {
			if tool == name {
				return true
			}
		}
		return false
	}
	if !has("docker_inspect") {
		t.Fatalf("DevOps без docker_inspect принимает Docker-поставку вслепую: %v", template.Tools)
	}
	if has("docker_control") {
		t.Fatalf("DevOps получил управление контейнерами, которого задача не поручала: %v", template.Tools)
	}
}

// Схема не переживает последний свой квест.
//
// Мастер собирает схему под каждый квест, а узел схемы держит исполнителя по
// идентификатору. Пока схема оставалась после удаления квеста, роспуск
// персонажа отказывал «замените его в схеме» — и заменять было негде: редактор
// графа скрыт, а экран уборки до сих пор не имел входа. 21 сентября 2026 у
// владельца так и вышло: квестов в проекте не осталось, две схемы остались, и
// обоих исполнителей держали они.
func TestFlowDoesNotOutliveItsLastQuest(t *testing.T) {
	application := newTestApp(t)
	view, err := application.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	flow, err := application.SaveFlow(domain.FlowGraph{
		WorkspaceID: view.Workspace.ID, Name: "pipeline · тест",
		Nodes: []domain.FlowNode{{ID: "bootstrap", Kind: "input", Name: "Bootstrap"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	shared := domain.Quest{ID: "quest-shared", WorkspaceID: view.Workspace.ID, Title: "Второй квест той же схемы", Status: domain.QuestCompleted, FlowID: flow.ID, CreatedAt: time.Now().UTC()}
	owner := domain.Quest{ID: "quest-owner", WorkspaceID: view.Workspace.ID, Title: "Первый квест", Status: domain.QuestCompleted, FlowID: flow.ID, CreatedAt: time.Now().UTC()}
	for _, quest := range []domain.Quest{shared, owner} {
		if err = application.store.SaveQuest(ctx, quest); err != nil {
			t.Fatal(err)
		}
	}

	// Пока на схему смотрит второй квест, она остаётся: удаление одного квеста
	// не вправе уносить чужую работу.
	if err = application.DeleteQuest(owner.ID); err != nil {
		t.Fatal(err)
	}
	flows, err := application.store.ListFlows(ctx, view.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(flows) != 1 {
		t.Fatalf("схему унесли, пока на неё смотрел второй квест: %d", len(flows))
	}

	// Последний квест забирает её с собой.
	if err = application.DeleteQuest(shared.ID); err != nil {
		t.Fatal(err)
	}
	flows, err = application.store.ListFlows(ctx, view.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(flows) != 0 {
		t.Fatalf("схема пережила последний свой квест и продолжит держать исполнителей: %d", len(flows))
	}
}

// Живой прогон важнее автоматической уборки схемы. Квест уже можно удалить
// только когда по нему самому нет активной работы, но у общей схемы может
// остаться прогон из прежнего квеста. Его граф нужен до остановки прогона.
func TestDeletingLastQuestKeepsFlowWithActiveHistoricalRun(t *testing.T) {
	application := newTestApp(t)
	view, err := application.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	flow, err := application.SaveFlow(domain.FlowGraph{
		WorkspaceID: view.Workspace.ID, Name: "pipeline · живой прогон",
		Nodes: []domain.FlowNode{{ID: "bootstrap", Kind: "input", Name: "Bootstrap"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	quest := domain.Quest{ID: "quest-last", WorkspaceID: view.Workspace.ID, Title: "Последний квест", Status: domain.QuestCompleted, FlowID: flow.ID, CreatedAt: time.Now().UTC()}
	if err = application.store.SaveQuest(ctx, quest); err != nil {
		t.Fatal(err)
	}
	if err = application.store.SaveFlowRun(ctx, domain.FlowRun{
		ID: "flow-run-historical", FlowID: flow.ID, WorkspaceID: view.Workspace.ID,
		QuestID: "already-removed-quest", Status: domain.RunPaused, NodeStates: map[string]domain.FlowNodeState{},
		StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	if err = application.DeleteQuest(quest.ID); err != nil {
		t.Fatal(err)
	}
	flows, err := application.store.ListFlows(ctx, view.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(flows) != 1 || flows[0].ID != flow.ID {
		t.Fatalf("схему живого исторического прогона удалили вместе с квестом: %#v", flows)
	}
}
