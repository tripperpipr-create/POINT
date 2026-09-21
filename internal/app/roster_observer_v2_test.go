package app

import (
	"context"
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
)

// rosterTestApp — проект с настроенным Мастером: наблюдателю нужен и ростер, и
// маршрут модели, иначе подбор нечем объяснить.
func rosterTestApp(t *testing.T, preset string) (*App, domain.Workspace) {
	t.Helper()
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { application.Shutdown(context.Background()) })
	world := openTestWorld(t, application)
	connection := saveTestConnection(t, application, "roster-observer", "openai", "Roster observer")
	teamPreference := 80
	if preset != "conductor" {
		teamPreference = 50
	}
	if err = application.store.SaveOrchestratorConfig(context.Background(), domain.OrchestratorConfig{
		ID: "master", WorkspaceID: world.ID, Preset: preset, ConnectionID: connection.ID,
		Provider: domain.ProviderOpenAI, ProviderPreset: "openai", Model: "gpt-test",
		PlanningDepth: 70, Parallelism: 50, ApprovalStrictness: 40, TeamPreference: teamPreference,
	}); err != nil {
		t.Fatal(err)
	}
	return application, world
}

func rosterTestAgent(t *testing.T, application *App, name, role, mission string) domain.ProjectAgent {
	t.Helper()
	agent, err := application.SaveProjectAgent(domain.ProjectAgent{
		Name: name, RoleDescription: role, Mission: mission,
		Provider: domain.ProviderOllama, BaseURL: "http://127.0.0.1:11434", PrimaryModel: "qwen2.5-coder:7b",
		AllowedTools: []string{"project_map", "list_files", "read_file", "search_code", "propose_patch", "run_command"},
		MaxSteps:     20, MaxDurationSeconds: 900, MaxOutputTokens: 4096, ContextWindowTokens: 32768,
	})
	if err != nil {
		t.Fatal(err)
	}
	return agent
}

func rosterTestProposal(workspaceID, id, goal string, subagents bool) domain.QuestProposal {
	return domain.QuestProposal{
		ID: id, WorkspaceID: workspaceID, TeamAgentIDs: []string{},
		Brief: &domain.TaskBrief{
			State: "ready", Goal: goal, ResultKind: "web",
			Scope:       []string{"composer create-project", "маршрут /health"},
			Criteria:    []domain.AcceptanceCriterion{{ID: "health", Kind: "manual", Text: "GET /health отвечает 200"}},
			Permissions: domain.TaskPermissions{WriteFiles: true, ExecuteCommands: true, ProvisionProjectAgents: subagents},
			Budget:      domain.TaskBudget{Tokens: 120000, ActiveSeconds: 1800, MaxParallel: 2, MaxReplans: 4, MaxAttempts: 2, MaxProjectAgents: 2},
		},
	}
}

func rosterTestOrder(t *testing.T, application *App, proposal domain.QuestProposal, conversationID string) domain.WorkOrder {
	t.Helper()
	id, err := application.saveMasterWorkOrderV2(context.Background(), &proposal, nil, conversationID, nil)
	if err != nil {
		t.Fatal(err)
	}
	order, err := application.WorkOrderV2(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return order
}

// TestRosterObserverDraftsHireWhenProjectIsEmpty закрепляет замену хардкода: на
// пустом проекте карточка обязана предлагать исполнителя, названного по задаче,
// с согласием человека и только существующими инструментами.
func TestRosterObserverDraftsHireWhenProjectIsEmpty(t *testing.T) {
	application, world := rosterTestApp(t, "conductor")
	order := rosterTestOrder(t, application, rosterTestProposal(world.ID, "qp-empty", "Собрать backend API с /health", false), "conversation-empty")

	if len(order.Roster.Permanent) != 1 {
		t.Fatalf("пустой проект обязан дать ровно один черновик: %#v", order.Roster)
	}
	draft := order.Roster.Permanent[0]
	if !draft.Existing || draft.RequiresConsent || draft.BlueprintID != "" {
		t.Fatalf("selector draft must be a persisted ID without approval-time materialization: %#v", draft)
	}
	stored, err := application.store.GetProjectAgent(context.Background(), draft.ID)
	if err != nil || stored.Status != domain.ProjectAgentDraft || stored.RoleFamily != "developer" {
		t.Fatalf("persisted selector draft = %#v err=%v", stored, err)
	}
	if draft.Name == "" || draft.Role == "" || draft.Mission == "" {
		t.Fatalf("черновик без имени, роли или миссии домен не примет: %#v", draft)
	}
	catalog := map[string]bool{}
	for _, item := range domain.BuiltInToolCatalog() {
		catalog[item.Name] = true
	}
	for _, tool := range draft.RequiredTools {
		if !catalog[tool] {
			t.Fatalf("инструмент %q не существует — такой агент родится заблокированным", tool)
		}
	}
	if err := domain.ValidateWorkOrder(order); err != nil {
		t.Fatalf("наряд с подобранным ростером обязан проходить домен: %v", err)
	}
}

// TestRosterObserverPrefersExistingAgent: готовый исполнитель отменяет найм.
func TestRosterObserverPrefersExistingAgent(t *testing.T) {
	application, world := rosterTestApp(t, "dispatcher")
	agent := rosterTestAgent(t, application, "Backend", "Backend-разработчик", "Держит серверную часть проекта")
	order := rosterTestOrder(t, application, rosterTestProposal(world.ID, "qp-existing", "Собрать backend API с /health", false), "conversation-existing")

	if len(order.Roster.Permanent) != 1 || !order.Roster.Permanent[0].Existing || order.Roster.Permanent[0].ID != agent.ID {
		t.Fatalf("готовый агент обязан занять место в наряде без найма: %#v", order.Roster)
	}
	if err := domain.ValidateWorkOrder(order); err != nil {
		t.Fatal(err)
	}
}

// TestRosterObserverKeepsWithinAgentBudget — регрессия на скрытую поломку:
// состав подбирался по настройке оркестратора (до трёх при пресете «дирижёр»),
// а бюджет наряда по умолчанию равен двум. Домен отвергал такой ростер, и ход
// Мастера пропадал целиком после минут ожидания.
func TestRosterObserverKeepsWithinAgentBudget(t *testing.T) {
	application, world := rosterTestApp(t, "conductor")
	rosterTestAgent(t, application, "Backend", "Backend-разработчик", "Серверная часть")
	rosterTestAgent(t, application, "Frontend", "Frontend-разработчик", "Интерфейс")
	rosterTestAgent(t, application, "QA", "Инженер проверки", "Проверки и доказательства")

	order := rosterTestOrder(t, application, rosterTestProposal(world.ID, "qp-budget", "Собрать backend API с /health и проверить", false), "conversation-budget")

	if len(order.Roster.Permanent) > order.Budget.MaxProjectAgents {
		t.Fatalf("ростер вышел за бюджет проектных агентов: %d при бюджете %d", len(order.Roster.Permanent), order.Budget.MaxProjectAgents)
	}
	if err := domain.ValidateWorkOrder(order); err != nil {
		t.Fatalf("наряд обязан проходить домен при любом числе подходящих агентов: %v", err)
	}
}

// Narrow specialists are runtime children requested by the active parent, not
// top-level WorkOrder roster entries.
func TestSelectorDefersSubagentToApprovedParentRuntime(t *testing.T) {
	application, world := rosterTestApp(t, "dispatcher")
	rosterTestAgent(t, application, "Backend", "Backend-разработчик", "Держит серверную часть проекта")

	proposal := rosterTestProposal(world.ID, "qp-subagent", "Собрать backend API и покрыть тестами", true)
	proposal.Brief.Scope = append(proposal.Brief.Scope, "тесты на маршрут")
	order := rosterTestOrder(t, application, proposal, "conversation-subagent")

	if len(order.Roster.Temporary) != 0 {
		t.Fatalf("selector created a top-level temporary specialist: %#v", order.Roster)
	}
	if err := domain.ValidateWorkOrder(order); err != nil {
		t.Fatalf("наряд с субагентом обязан проходить домен: %v", err)
	}
}

// Без разрешения на новых исполнителей временных агентов не предлагают: право
// заводить их даёт человек, а не нехватка роли.
func TestRosterObserverSkipsSubagentWithoutPermission(t *testing.T) {
	application, world := rosterTestApp(t, "dispatcher")
	rosterTestAgent(t, application, "Backend", "Backend-разработчик", "Держит серверную часть проекта")

	proposal := rosterTestProposal(world.ID, "qp-no-subagent", "Собрать backend API и покрыть тестами", false)
	proposal.Brief.Scope = append(proposal.Brief.Scope, "тесты на маршрут")
	order := rosterTestOrder(t, application, proposal, "conversation-no-subagent")

	if len(order.Roster.Temporary) != 0 {
		t.Fatalf("без provisionProjectAgents временных исполнителей быть не должно: %#v", order.Roster)
	}
}

// A material brief change produces a new digest and retires the unapproved
// selector-only draft from the prior revision.
func TestSelectorReplacesSupersededDraftBetweenTurns(t *testing.T) {
	application, world := rosterTestApp(t, "conductor")
	first := rosterTestOrder(t, application, rosterTestProposal(world.ID, "qp-turn-one", "Собрать backend API с /health", false), "conversation-identity")
	second := rosterTestOrder(t, application, rosterTestProposal(world.ID, "qp-turn-two", "Собрать backend API с /health и логами", false), "conversation-identity")

	if len(first.Roster.Permanent) != 1 || len(second.Roster.Permanent) != 1 {
		t.Fatalf("оба хода обязаны дать один черновик: %#v / %#v", first.Roster, second.Roster)
	}
	if first.Roster.Permanent[0].ID == second.Roster.Permanent[0].ID {
		t.Fatalf("materially changed brief reused stale draft: %q", first.Roster.Permanent[0].ID)
	}
	if _, err := application.store.GetProjectAgent(context.Background(), first.Roster.Permanent[0].ID); err == nil {
		t.Fatal("superseded selector-only draft survived")
	}
}

// Выдуманное имя инструмента — единственный барьер между фантазией модели и
// агентом, который родится заблокированным: наряд заводит исполнителя в обход
// проверки профиля.
func TestFilterKnownToolsDropsInventedNames(t *testing.T) {
	application, _ := rosterTestApp(t, "dispatcher")
	tools := application.filterKnownTools(context.Background(), []string{"read_file", "symfony_console", "  ", "read_file", "run_command"})
	if strings.Join(tools, ",") != "read_file,run_command" {
		t.Fatalf("фильтр обязан оставить только существующие инструменты без повторов: %#v", tools)
	}
}

// Наблюдатель объясняет выбор словами задачи и называет причину, по которой
// подходящий агент остался вне наряда.
func TestObserveRosterExplainsSelection(t *testing.T) {
	application, world := rosterTestApp(t, "conductor")
	rosterTestAgent(t, application, "Backend", "Backend-разработчик", "Держит серверную часть проекта")
	rosterTestAgent(t, application, "Frontend", "Frontend-разработчик", "Интерфейс")
	rosterTestAgent(t, application, "QA", "Инженер проверки", "Проверки и доказательства")

	observation, err := application.ObserveRoster(context.Background(), RosterNeed{
		WorkspaceID: world.ID, Goal: "Собрать backend API с /health", MaxAgents: 1,
		RequiredTools: []string{"read_file", "propose_patch", "run_command"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(observation.Selected) != 1 {
		t.Fatalf("бюджет в одного исполнителя обязан давать одного выбранного: %#v", observation.Selected)
	}
	if len(observation.Considered) == 0 {
		t.Fatal("остальные агенты обязаны попасть в рассмотренные с причиной")
	}
	for _, candidate := range observation.Considered {
		if candidate.WhyNot == "" {
			t.Fatalf("у рассмотренного агента %q нет причины отказа", candidate.Name)
		}
	}
	if observation.Reason == "" {
		t.Fatal("усечение по бюджету обязано быть названо вслух")
	}
}
