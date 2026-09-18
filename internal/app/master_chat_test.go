package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/orchestrator"
)

func TestMasterBriefingExposesBoundedProjectEvidence(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	root := t.TempDir()
	if err = os.MkdirAll(filepath.Join(root, "cmd", "api"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test/briefing\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "cmd", "api", "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	view, err := application.OpenWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	if err = application.store.SaveOrchestratorConfig(context.Background(), domain.OrchestratorConfig{
		ID: "master", WorkspaceID: view.Workspace.ID, Preset: "balanced", PlanningDepth: 50, Parallelism: 50, ApprovalStrictness: 50, TeamPreference: 50,
	}); err != nil {
		t.Fatal(err)
	}
	history, err := application.MasterHistory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	briefing := history.Briefing
	if briefing.IndexUpdatedAt.IsZero() || briefing.Files < 2 || len(briefing.Sources) == 0 {
		t.Fatalf("briefing lacks index provenance: %#v", briefing)
	}
	if !containsString(briefing.Entrypoints, "cmd/api/main.go") || !containsString(briefing.BuildCommands, "go build ./...") || !containsString(briefing.TestCommands, "go test ./...") {
		t.Fatalf("briefing lacks entrypoint/build facts: %#v", briefing)
	}
}

func TestMasterChatProposesQuestButNeverStartsIt(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())

	world := openTestWorld(t, application)
	ctx := context.Background()

	if err = application.store.SaveOrchestratorConfig(ctx, domain.OrchestratorConfig{
		ID: "master", WorkspaceID: world.ID, Preset: "balanced", Provider: domain.ProviderOllama,
		Model: "qwen", PlanningDepth: 50, Parallelism: 2, ApprovalStrictness: 50, TeamPreference: 50,
	}); err != nil {
		t.Fatal(err)
	}

	boot, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	if len(boot.Blueprints) == 0 {
		t.Fatal("нет чертежей — не из кого собирать отряд")
	}
	for _, blueprint := range boot.Blueprints[:min(3, len(boot.Blueprints))] {
		if _, err = application.SaveProjectAgent(domain.ProjectAgentFromBlueprint(world.ID, blueprint)); err != nil {
			t.Fatal(err)
		}
	}

	view, err := application.MasterChat(ctx, MasterChatRequest{Message: "Почини флаки-тесты в биллинге"})
	if err != nil {
		t.Fatal(err)
	}
	if view.Response.Proposal == nil {
		t.Fatal("на задачу мастер обязан предложить квест")
	}
	proposal := view.Response.Proposal
	if proposal.Status != "pending" {
		t.Fatalf("предложение обязано ждать решения, статус=%s", proposal.Status)
	}
	if len(proposal.TeamAgentIDs) == 0 {
		t.Fatal("мастер обязан подобрать отряд")
	}
	if !strings.Contains(proposal.Title, "Почини") {
		t.Fatalf("название квеста не отражает задачу: %q", proposal.Title)
	}
	// Обоснование уходит из разговора в предложение, а оттуда — в очередь
	// решений. Мастер здесь модель не звал, и жаловаться на неё не может: у
	// этого мира она настроена и исправна.
	if strings.Contains(proposal.Rationale, "недоступна") {
		t.Fatalf("предложение жалуется на модель, которую никто не звал: %q", proposal.Rationale)
	}

	// Предложение не должно становиться работой само — это тот же структурный
	// предохранитель, что и у компаньона, только с другой стороны.
	quests, err := application.store.ListQuests(ctx, world.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(quests) != 0 {
		t.Fatalf("мастер создал работу без решения человека: %+v", quests)
	}

	// Предложение обязано попасть в очередь решений — иначе оно потеряется.
	queue, err := application.Decisions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range queue.Items {
		if item.ID == proposal.ID && item.Kind == DecisionQuest {
			found = true
		}
	}
	if !found {
		t.Fatalf("предложение мастера не попало в очередь решений: %+v", queue.Items)
	}
}

func TestMasterChatAnswersQuestionsWithoutCreatingWork(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())

	world := openTestWorld(t, application)
	ctx := context.Background()
	if err = application.store.SaveOrchestratorConfig(ctx, domain.OrchestratorConfig{
		ID: "master", WorkspaceID: world.ID, Preset: "balanced", Provider: domain.ProviderOllama,
		Model: "qwen", PlanningDepth: 50, Parallelism: 2, ApprovalStrictness: 50, TeamPreference: 50,
	}); err != nil {
		t.Fatal(err)
	}

	boot, _ := application.Bootstrap()
	if len(boot.Blueprints) > 0 {
		if _, err = application.SaveProjectAgent(domain.ProjectAgentFromBlueprint(world.ID, boot.Blueprints[0])); err != nil {
			t.Fatal(err)
		}
	}

	view, err := application.MasterChat(ctx, MasterChatRequest{Message: "Что сейчас происходит в проекте?"})
	if err != nil {
		t.Fatal(err)
	}
	if view.Response.Proposal != nil {
		t.Fatal("на вопрос мастер не должен предлагать работу")
	}
	if strings.TrimSpace(view.Response.Reply) == "" {
		t.Fatal("мастер обязан ответить хоть что-то")
	}

	bareQuest, err := application.MasterChat(ctx, MasterChatRequest{Message: "Создай квест:"})
	if err != nil {
		t.Fatal(err)
	}
	if bareQuest.Response.Proposal != nil || !strings.Contains(bareQuest.Response.Reply, "Какую задачу") {
		t.Fatalf("пустая команда быстрого старта создала бессодержательный квест: %+v", bareQuest.Response)
	}
}

func TestMasterCreatesReviewableAgentAndTeamDraftsInsteadOfFakeCodingQuests(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())

	world := openTestWorld(t, application)
	ctx := context.Background()
	if err = application.store.SaveOrchestratorConfig(ctx, domain.OrchestratorConfig{
		ID: "master", WorkspaceID: world.ID, Preset: "balanced", Provider: domain.ProviderOllama,
		Model: "qwen", PlanningDepth: 50, Parallelism: 2, ApprovalStrictness: 50, TeamPreference: 50,
	}); err != nil {
		t.Fatal(err)
	}

	agentDraft, err := application.MasterChat(ctx, MasterChatRequest{Message: "Создай backend-агента для API"})
	if err != nil {
		t.Fatal(err)
	}
	if agentDraft.Response.Proposal != nil {
		t.Fatalf("создание агента превратилось в coding-квест: %+v", agentDraft.Response.Proposal)
	}
	if agentDraft.Response.ActionProposal == nil || agentDraft.Response.ActionProposal.Kind != domain.CompanionActionCreateAgent {
		t.Fatalf("Мастер не подготовил проверяемый черновик агента: %+v", agentDraft.Response)
	}
	if agentDraft.Response.ActionProposal.Agent == nil || agentDraft.Response.ActionProposal.Agent.BlueprintID == "" {
		t.Fatalf("черновик агента не основан на Blueprint: %+v", agentDraft.Response.ActionProposal)
	}
	if agentDraft.Response.ActionProposal.ContinuationPrompt != "" {
		t.Fatalf("прямая просьба создать агента выдумала задачу для продолжения: %+v", agentDraft.Response.ActionProposal)
	}
	reloadedAgentDraft, err := application.MasterHistory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	linkedAgentDraft := false
	for _, message := range reloadedAgentDraft.History {
		if message.ActionProposalID == agentDraft.Response.ActionProposal.ID {
			linkedAgentDraft = true
			break
		}
	}
	if !linkedAgentDraft {
		t.Fatal("история Мастера потеряла ссылку на черновик агента")
	}
	created, err := application.DecideCompanionAction(CompanionActionDecision{
		ProposalID: agentDraft.Response.ActionProposal.ID, Action: CompanionActionApply,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Agent == nil || created.Agent.ID == "" {
		t.Fatalf("явное подтверждение не создало агента: %+v", created)
	}

	teamDraft, err := application.MasterChat(ctx, MasterChatRequest{Message: "Подбери отряд для миграции базы данных"})
	if err != nil {
		t.Fatal(err)
	}
	if teamDraft.Response.Proposal != nil {
		t.Fatalf("выбор отряда превратился в coding-квест: %+v", teamDraft.Response.Proposal)
	}
	if teamDraft.Response.ActionProposal == nil || teamDraft.Response.ActionProposal.Kind != domain.CompanionActionCreateTeam {
		t.Fatalf("Мастер не подготовил проверяемый черновик отряда: %+v", teamDraft.Response)
	}
	if teamDraft.Response.ActionProposal.Team == nil || len(teamDraft.Response.ActionProposal.Team.AgentIDs) == 0 || len(teamDraft.Response.Party) == 0 {
		t.Fatalf("в черновике не виден выбранный состав: %+v", teamDraft.Response)
	}
	reloadedTeamDraft, err := application.MasterHistory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	linkedTeamDraft := false
	for _, message := range reloadedTeamDraft.History {
		if message.ActionProposalID == teamDraft.Response.ActionProposal.ID {
			linkedTeamDraft = true
			break
		}
	}
	if !linkedTeamDraft {
		t.Fatal("история Мастера потеряла ссылку на черновик отряда")
	}

	proposals, err := application.store.ListQuestProposals(ctx, world.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(proposals) != 0 {
		t.Fatalf("создание сущностей Hub оставило лишние предложения квестов: %+v", proposals)
	}
}

func TestMasterContinuesExactTeamRequestAfterCreatingTheFirstAgent(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())

	world := openTestWorld(t, application)
	ctx := context.Background()
	if err = application.store.SaveOrchestratorConfig(ctx, domain.OrchestratorConfig{
		ID: "master", WorkspaceID: world.ID, Preset: "balanced", Provider: domain.ProviderOllama,
		Model: "qwen", PlanningDepth: 50, Parallelism: 2, ApprovalStrictness: 50, TeamPreference: 50,
	}); err != nil {
		t.Fatal(err)
	}

	const request = "Подбери отряд для миграции платежей"
	first, err := application.MasterChat(ctx, MasterChatRequest{Message: request})
	if err != nil {
		t.Fatal(err)
	}
	action := first.Response.ActionProposal
	if action == nil || action.Kind != domain.CompanionActionCreateAgent {
		t.Fatalf("при пустом ростере сначала ожидался агент: %+v", first.Response)
	}
	if action.ContinuationPrompt != request || action.ContinuationLabel != "ПРОДОЛЖИТЬ ПОДБОР ОТРЯДА" {
		t.Fatalf("просьба об отряде потерялась в промежуточном агенте: %+v", action)
	}
	if _, err = application.DecideCompanionAction(CompanionActionDecision{ProposalID: action.ID, Action: CompanionActionApply}); err != nil {
		t.Fatal(err)
	}

	continued, err := application.MasterChat(ctx, MasterChatRequest{Message: action.ContinuationPrompt})
	if err != nil {
		t.Fatal(err)
	}
	if continued.Response.ActionProposal == nil || continued.Response.ActionProposal.Kind != domain.CompanionActionCreateTeam {
		t.Fatalf("после первого агента продолжился не подбор исходного отряда: %+v", continued.Response)
	}
}

// Перезагрузка панели не должна раздевать разговор.
//
// Основания ответа, уточняющие вопросы и ссылка на предложенный квест жили
// только в ответе на текущий ход. Стоило переоткрыть Хаб — и история
// поднималась голыми репликами: «квест предложен и ждёт вашего решения» без
// самого предложения, чипы вопросов исчезали, а на чём это основано, приходилось
// спрашивать заново.
func TestMasterHistoryKeepsFactsQuestionsAndProposal(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())

	world := openTestWorld(t, application)
	ctx := context.Background()
	if err = application.store.SaveOrchestratorConfig(ctx, domain.OrchestratorConfig{
		ID: "master", WorkspaceID: world.ID, Preset: "balanced", Provider: domain.ProviderOllama,
		Model: "qwen", PlanningDepth: 50, Parallelism: 2, ApprovalStrictness: 50, TeamPreference: 50,
	}); err != nil {
		t.Fatal(err)
	}
	boot, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	for _, blueprint := range boot.Blueprints[:min(2, len(boot.Blueprints))] {
		if _, err = application.SaveProjectAgent(domain.ProjectAgentFromBlueprint(world.ID, blueprint)); err != nil {
			t.Fatal(err)
		}
	}

	// Короткая задача: на неё мастер и отряд подберёт, и уточнение спросит.
	live, err := application.MasterChat(ctx, MasterChatRequest{Message: "Почини тесты"})
	if err != nil {
		t.Fatal(err)
	}
	if live.Response.Proposal == nil {
		t.Fatal("на задачу мастер обязан предложить квест")
	}
	if len(live.Response.Facts) == 0 || len(live.Response.Questions) == 0 {
		t.Fatalf("ход без оснований или уточнений нечего и проверять: %+v", live.Response)
	}
	// Основания не должны спорить сами с собой: предел отряда из политики может
	// быть больше ростера, и числом он читался как состав, которого не бывает.
	roster := len(live.Response.Party)
	for _, fact := range live.Response.Facts {
		if !strings.HasPrefix(fact, "отряд") {
			continue
		}
		if !strings.Contains(fact, "по политике") {
			t.Fatalf("предел из политики выдан за собранный состав: %q при отряде из %d", fact, roster)
		}
	}

	// Второе открытие раздела — ровно то, что делает панель после перезапуска.
	reloaded, err := application.MasterHistory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var reply *domain.CompanionMessage
	for index := range reloaded.History {
		if reloaded.History[index].Role == "assistant" {
			reply = &reloaded.History[index]
		}
	}
	if reply == nil {
		t.Fatal("в поднятой истории нет ответа мастера")
	}
	if len(reply.FactsUsed) != len(live.Response.Facts) {
		t.Fatalf("основания не пережили перезагрузку: было %v, стало %v", live.Response.Facts, reply.FactsUsed)
	}
	if len(reply.Questions) != len(live.Response.Questions) {
		t.Fatalf("уточняющие вопросы не пережили перезагрузку: было %v, стало %v", live.Response.Questions, reply.Questions)
	}
	// По этой ссылке интерфейс и достаёт карточку квеста из bootstrap: без неё
	// реплика обещает решение, которого в разговоре уже не найти.
	if reply.ProposalID != live.Response.Proposal.ID {
		t.Fatalf("реплика потеряла ссылку на предложение: %q вместо %q", reply.ProposalID, live.Response.Proposal.ID)
	}
	if reply.Mode != live.Response.Mode {
		t.Fatalf("режим ответа разошёлся: %q вместо %q", reply.Mode, live.Response.Mode)
	}
}

// Разговоры мастера и компаньона делят таблицу, но не диалог: чужие реплики в
// своём окне — ровно та каша, из-за которой раздел «Мастер» открывал компаньона.
func TestMasterAndCompanionHistoriesDoNotMix(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())

	world := openTestWorld(t, application)
	ctx := context.Background()

	if err = application.store.SaveOrchestratorConfig(ctx, domain.OrchestratorConfig{
		ID: "master", WorkspaceID: world.ID, Preset: "balanced", Provider: domain.ProviderOllama,
		Model: "qwen", PlanningDepth: 50, Parallelism: 2, ApprovalStrictness: 50, TeamPreference: 50,
	}); err != nil {
		t.Fatal(err)
	}
	if err = application.store.SaveCompanionMessage(ctx, domain.CompanionMessage{
		ID: "cm-1", WorkspaceID: world.ID, Role: "user", Content: "реплика компаньону",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = application.MasterChat(ctx, MasterChatRequest{Message: "Что по проекту?"}); err != nil {
		t.Fatal(err)
	}

	master, err := application.MasterHistory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range master.History {
		if strings.Contains(message.Content, "реплика компаньону") {
			t.Fatal("реплика компаньона просочилась в диалог мастера")
		}
	}
	companion, err := application.store.ListCompanionMessages(ctx, world.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range companion {
		if message.Speaker == "master" {
			t.Fatal("реплика мастера просочилась в диалог компаньона")
		}
	}
	if len(companion) != 1 {
		t.Fatalf("у компаньона должна остаться одна своя реплика, получено %d", len(companion))
	}
}

// Чат целиком принадлежит Мастеру. Пока диспетчер не настроен, разговаривать
// не с кем — но раздел обязан открыться и объяснить, чего не хватает, а не
// упасть с непонятной ошибкой.
func TestMasterChatRequiresConfiguredMaster(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())

	openTestWorld(t, application)
	ctx := context.Background()

	view, err := application.MasterHistory(ctx)
	if err != nil {
		t.Fatalf("раздел обязан открыться и без настройки: %v", err)
	}
	if view.Configured {
		t.Fatal("мастер не настроен, но раздел утверждает обратное")
	}
	if view.History == nil {
		t.Fatal("история должна быть пустым списком, а не nil: клиент рендерит её напрямую")
	}

	// Ход без настроенного диспетчера ничего не делает, но и не выглядит сбоем:
	// раздел объясняет, чего не хватает, вместо красного «request_failed».
	attempt, err := application.MasterChat(ctx, MasterChatRequest{Message: "Почини тесты"})
	if err != nil {
		t.Fatalf("ненастроенный мастер обязан отвечать состоянием, а не ошибкой: %v", err)
	}
	if attempt.Configured {
		t.Fatal("мастер не настроен, но ход утверждает обратное")
	}
	if strings.TrimSpace(attempt.Response.Reply) != "" {
		t.Fatalf("без настройки не может быть ответа мастера: %+v", attempt.Response)
	}

	quests, err := application.store.ListQuestProposals(ctx, application.currentWorldID())
	if err != nil {
		t.Fatal(err)
	}
	if len(quests) != 0 {
		t.Fatalf("ненастроенный мастер создал предложение: %+v", quests)
	}
}

// Мастер обязан судить о годности агента теми же правилами, что и его карточка.
// Иначе диспетчер собирает отряд из тех, кого форма уже признала
// неработоспособными, и человек узнаёт об этом из провала квеста.
func TestMasterRefusesToProposeWithAnIncapableParty(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())

	world := openTestWorld(t, application)
	ctx := context.Background()
	if err = application.store.SaveOrchestratorConfig(ctx, domain.OrchestratorConfig{
		ID: "master", WorkspaceID: world.ID, Preset: "balanced", Provider: domain.ProviderOllama,
		Model: "qwen", PlanningDepth: 50, Parallelism: 2, ApprovalStrictness: 50, TeamPreference: 50,
	}); err != nil {
		t.Fatal(err)
	}

	// Агент правит файлы, но подтвердить результат не может — карточка такую
	// настройку блокирует, значит и Мастер не должен на неё рассчитывать.
	broken := domain.ProjectAgent{
		ID: "a-broken", WorkspaceID: world.ID, Name: "Кузнец", RoleDescription: "правит тесты",
		Provider: domain.ProviderOllama, PrimaryModel: "qwen",
		AllowedTools: []string{"read_file", "propose_patch"}, MaxSteps: 30,
	}
	if _, err = application.SaveProjectAgent(broken); err != nil {
		t.Fatal(err)
	}

	view, err := application.MasterChat(ctx, MasterChatRequest{Message: "Почини тесты в биллинге"})
	if err != nil {
		t.Fatal(err)
	}
	if view.Response.Proposal != nil {
		t.Fatal("с полностью неработоспособным отрядом квест предлагать нельзя")
	}
	if len(view.Response.Party) == 0 {
		t.Fatal("мастер обязан показать, кого он рассматривал")
	}
	if len(view.Response.Party[0].Blocking) == 0 {
		t.Fatalf("причина непригодности агента не названа: %+v", view.Response.Party[0])
	}
	if !strings.Contains(view.Response.Reply, "не сможет довести работу до конца") {
		t.Fatalf("отказ не объяснён человеку: %q", view.Response.Reply)
	}

	proposals, err := application.store.ListQuestProposals(ctx, world.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(proposals) != 0 {
		t.Fatalf("создано заведомо провальное предложение: %+v", proposals)
	}
}

// Пустой ростер не должен превращать разговор в петлю.
//
// Раньше на любое сообщение — включая встречный вопрос самого Мастера «какой
// агент нужен под эту задачу?» — приходила одна и та же строка «создайте
// первого в Гильдии». Выйти из этого изнутри было нельзя.
func TestMasterWorksWithAnEmptyRoster(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())

	world := openTestWorld(t, application)
	ctx := context.Background()
	if err = application.store.SaveOrchestratorConfig(ctx, domain.OrchestratorConfig{
		ID: "master", WorkspaceID: world.ID, Preset: "balanced", Provider: domain.ProviderOllama,
		Model: "qwen", PlanningDepth: 50, Parallelism: 2, ApprovalStrictness: 50, TeamPreference: 50,
	}); err != nil {
		t.Fatal(err)
	}

	// Ни одного агента: ростер пуст.
	agents, err := application.store.ListProjectAgents(ctx, world.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 0 {
		t.Fatalf("ростер должен быть пуст, найдено %d", len(agents))
	}

	work, err := application.MasterChat(ctx, MasterChatRequest{Message: "Сделай ревью проекта"})
	if err != nil {
		t.Fatal(err)
	}
	if work.Response.Hire == nil {
		t.Fatal("без агентов Мастер обязан предложить, кого нанять")
	}
	if strings.TrimSpace(work.Response.Hire.Name) == "" {
		t.Fatalf("предложение найма без имени роли: %+v", work.Response.Hire)
	}
	if strings.TrimSpace(work.Response.Hire.Why) == "" {
		t.Fatal("выбор роли обязан быть объяснён")
	}
	if !strings.Contains(work.Response.Reply, work.Response.Hire.Name) {
		t.Fatalf("ответ не называет предложенную роль: %q", work.Response.Reply)
	}
	if work.Response.ActionProposal == nil || work.Response.ActionProposal.ContinuationPrompt != "Сделай ревью проекта" {
		t.Fatalf("черновик агента потерял исходную задачу для продолжения: %+v", work.Response.ActionProposal)
	}
	if work.Response.ActionProposal.ContinuationLabel != "ПРОДОЛЖИТЬ ЗАДАЧУ" {
		t.Fatalf("неверная подпись продолжения задачи: %+v", work.Response.ActionProposal)
	}
	snapshot, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	persistedContinuation := ""
	for _, action := range snapshot.CompanionActionProposals {
		if action.ID == work.Response.ActionProposal.ID {
			persistedContinuation = action.ContinuationPrompt
			break
		}
	}
	if persistedContinuation != "Сделай ревью проекта" {
		t.Fatalf("исходная задача не пережила reload: %q", persistedContinuation)
	}

	// Другое сообщение — другой ответ. Одинаковая строка на всё и была петлёй.
	question, err := application.MasterChat(ctx, MasterChatRequest{Message: "Какой агент нужен под эту задачу?"})
	if err != nil {
		t.Fatal(err)
	}
	if question.Response.Reply == work.Response.Reply {
		t.Fatalf("на разные сообщения пришёл один и тот же ответ: %q", question.Response.Reply)
	}
	if question.Response.Hire == nil {
		t.Fatal("на прямой вопрос о роли Мастер обязан её назвать")
	}

	// Пустой ростер не делает Мастера односложным.
	//
	// Ветка найма стояла до разбора намерения и перехватывала всё подряд:
	// «что ты умеешь», «что сейчас», «кто есть» и даже «привет» получали одно и
	// то же предложение нанять — причём чертёж подбирался по тексту приветствия,
	// а ответ уверял, что подобран «под то, что вы описали».
	for _, probe := range []struct {
		message string
		expect  string
	}{
		{"что ты умеешь?", "Я диспетчер"},
		{"что сейчас?", "Активных квестов"},
		{"кто есть в ростере?", "Ростер пуст"},
	} {
		answer, askErr := application.MasterChat(ctx, MasterChatRequest{Message: probe.message})
		if askErr != nil {
			t.Fatal(askErr)
		}
		if !strings.Contains(answer.Response.Reply, probe.expect) {
			t.Fatalf("на «%s» пришёл не свой ответ: %q", probe.message, answer.Response.Reply)
		}
		if answer.Response.Hire != nil {
			t.Fatalf("на «%s» вместо ответа предложен найм: %+v", probe.message, answer.Response.Hire)
		}
	}

	// А приветствие без названной задачи не выдумывает, будто задача была.
	greeting, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer greeting.Shutdown(context.Background())
	freshWorld := openTestWorld(t, greeting)
	if err = greeting.store.SaveOrchestratorConfig(ctx, domain.OrchestratorConfig{
		ID: "master", WorkspaceID: freshWorld.ID, Preset: "balanced", Provider: domain.ProviderOllama,
		Model: "qwen", PlanningDepth: 50, Parallelism: 2, ApprovalStrictness: 50, TeamPreference: 50,
	}); err != nil {
		t.Fatal(err)
	}
	hello, err := greeting.MasterChat(ctx, MasterChatRequest{Message: "привет"})
	if err != nil {
		t.Fatal(err)
	}
	if hello.Response.Hire != nil {
		t.Fatalf("на «привет» подобран чертёж по слову «привет»: %+v", hello.Response.Hire)
	}
	if strings.Contains(hello.Response.Reply, "вы описали") {
		t.Fatalf("ответ уверяет, что задачу описали, хотя её не было: %q", hello.Response.Reply)
	}

	// Работа при этом не создаётся: нанимать и запускать — решение человека.
	proposals, err := application.store.ListQuestProposals(ctx, world.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(proposals) != 0 {
		t.Fatalf("без агентов предложение квеста создаваться не должно: %+v", proposals)
	}
}

// Диспетчер должен уметь больше одного действия.
//
// Раньше всё, что не похоже на задачу, получало одну и ту же строку про размер
// ростера: спросить о состоянии дел или о составе отряда было нельзя, и чат
// выглядел как окно с единственной кнопкой.
func TestMasterAnswersStatusRosterAndHelp(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())

	world := openTestWorld(t, application)
	ctx := context.Background()
	if err = application.store.SaveOrchestratorConfig(ctx, domain.OrchestratorConfig{
		ID: "master", WorkspaceID: world.ID, Preset: "balanced", Provider: domain.ProviderOllama,
		Model: "qwen", PlanningDepth: 50, Parallelism: 2, ApprovalStrictness: 50, TeamPreference: 50,
	}); err != nil {
		t.Fatal(err)
	}
	boot, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	for _, blueprint := range boot.Blueprints[:min(2, len(boot.Blueprints))] {
		if _, err = application.SaveProjectAgent(domain.ProjectAgentFromBlueprint(world.ID, blueprint)); err != nil {
			t.Fatal(err)
		}
	}

	help, err := application.MasterChat(ctx, MasterChatRequest{Message: "что ты умеешь?"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(help.Response.Reply, "что сейчас") {
		t.Fatalf("справка обязана называть, о чём можно спросить: %q", help.Response.Reply)
	}

	status, err := application.MasterChat(ctx, MasterChatRequest{Message: "что сейчас?"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status.Response.Reply, "Активных квестов") {
		t.Fatalf("сводка обязана приводить числа: %q", status.Response.Reply)
	}
	if status.Response.Reply == help.Response.Reply {
		t.Fatal("разные вопросы получили один ответ")
	}

	roster, err := application.MasterChat(ctx, MasterChatRequest{Message: "кто есть в ростере?"})
	if err != nil {
		t.Fatal(err)
	}
	if len(roster.Response.Party) == 0 {
		t.Fatal("на вопрос о составе Мастер обязан перечислить агентов")
	}
	// Ростер перечисляется без задачи, а значит и без оценки соответствия ей.
	// Ноль в этом поле интерфейс рисовал как выставленную оценку.
	for _, member := range roster.Response.Party {
		if member.Score != nil {
			t.Fatalf("агент %q получил оценку соответствия задаче, которой не было: %d", member.Name, *member.Score)
		}
	}
	if !strings.Contains(roster.Response.Reply, "Готовы к квесту") {
		t.Fatalf("состав без готовности бесполезен: %q", roster.Response.Reply)
	}

	// Ни один из этих вопросов не создаёт работу.
	proposals, err := application.store.ListQuestProposals(ctx, world.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(proposals) != 0 {
		t.Fatalf("вопрос не должен создавать предложение квеста: %+v", proposals)
	}
}

// Хвост разговора обязан признаваться хвостом.
//
// История отдаётся последними репликами, и на длинном разговоре лента
// начиналась с середины без единого знака: первая показанная реплика читалась
// как первая вообще. Это то же различие между «этого не было» и «этого здесь не
// показано», ради которого в остальном интерфейсе разведены пустота и незнание.
func TestMasterHistorySaysWhenItIsOnlyTheTail(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())

	world := openTestWorld(t, application)
	ctx := context.Background()
	if err = application.store.SaveOrchestratorConfig(ctx, domain.OrchestratorConfig{
		ID: "master", WorkspaceID: world.ID, Preset: "balanced", Provider: domain.ProviderOllama,
		Model: "qwen", PlanningDepth: 50, Parallelism: 2, ApprovalStrictness: 50, TeamPreference: 50,
	}); err != nil {
		t.Fatal(err)
	}

	// Короткий разговор хвостом не является.
	short, err := application.MasterHistory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if short.Truncated {
		t.Fatal("пустая переписка объявлена обрезанной")
	}

	// Ровно предел — тоже полный разговор, а не хвост: иначе пометка врала бы
	// про пропавшее начало, которого нет.
	for index := 0; index < masterHistoryLimit; index++ {
		if err = application.store.SaveCompanionMessage(ctx, domain.CompanionMessage{
			ID: fmt.Sprintf("mm-%03d", index), WorkspaceID: world.ID, Speaker: "master",
			Role: "user", Content: fmt.Sprintf("реплика %d", index), CreatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	exact, err := application.MasterHistory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if exact.Truncated {
		t.Fatalf("разговор ровно в предел объявлен обрезанным: %d реплик", len(exact.History))
	}
	if len(exact.History) != masterHistoryLimit {
		t.Fatalf("показано %d реплик вместо %d", len(exact.History), masterHistoryLimit)
	}

	// Одна лишняя — и начало ушло за кадр.
	if err = application.store.SaveCompanionMessage(ctx, domain.CompanionMessage{
		ID: "mm-999", WorkspaceID: world.ID, Speaker: "master", Role: "user",
		Content: "лишняя реплика", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	long, err := application.MasterHistory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !long.Truncated {
		t.Fatal("начало разговора потерялось молча")
	}
	if len(long.History) != masterHistoryLimit {
		t.Fatalf("предел не соблюдён: показана %d реплика", len(long.History))
	}
	// Показывается именно хвост: последняя реплика на месте, первая — нет.
	if long.History[len(long.History)-1].Content != "лишняя реплика" {
		t.Fatalf("последняя реплика не показана: %q", long.History[len(long.History)-1].Content)
	}
	for _, message := range long.History {
		if message.Content == "реплика 0" {
			t.Fatal("вместо хвоста показано начало разговора")
		}
	}
}

// Мастер помнит, что сам же и предложил.
//
// Реплика, которая не команда и не вопрос, чаще всего продолжает предыдущий ход:
// «давай», «ок», «а можно без него?». Пока ответ на такую реплику был один на
// все случаи, сразу после предложенного квеста человек получал приглашение
// описать задачу — разговор обрывался ровно там, где ему отвечали. А описав
// вторую задачу, он получал второе предложение и ни слова о том, что первое
// всё ещё ждёт: очередь росла, и заметить это можно было только по счётчику.
func TestMasterRemembersTheProposalItJustMade(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())

	world := openTestWorld(t, application)
	ctx := context.Background()
	if err = application.store.SaveOrchestratorConfig(ctx, domain.OrchestratorConfig{
		ID: "master", WorkspaceID: world.ID, Preset: "balanced", Provider: domain.ProviderOllama,
		Model: "qwen", PlanningDepth: 50, Parallelism: 2, ApprovalStrictness: 50, TeamPreference: 50,
	}); err != nil {
		t.Fatal(err)
	}
	boot, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	for _, blueprint := range boot.Blueprints[:min(2, len(boot.Blueprints))] {
		if _, err = application.SaveProjectAgent(domain.ProjectAgentFromBlueprint(world.ID, blueprint)); err != nil {
			t.Fatal(err)
		}
	}

	first, err := application.MasterChat(ctx, MasterChatRequest{Message: "Почини флаки-тест оплаты"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Response.Proposal == nil {
		t.Fatal("на задачу мастер обязан предложить квест")
	}

	// Ответ Мастеру, а не новая задача.
	reply, err := application.MasterChat(ctx, MasterChatRequest{Message: "давай"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reply.Response.Reply, first.Response.Proposal.Title) {
		t.Fatalf("мастер забыл собственное предложение: %q", reply.Response.Reply)
	}
	if strings.Contains(reply.Response.Reply, "Опишите задачу") {
		t.Fatalf("вместо ответа на реплику — приглашение начать сначала: %q", reply.Response.Reply)
	}

	// Вторая задача: предложение создаётся, но о первом сказано.
	second, err := application.MasterChat(ctx, MasterChatRequest{Message: "Добавь метрики в вебхук"})
	if err != nil {
		t.Fatal(err)
	}
	if second.Response.Proposal == nil {
		t.Fatal("вторая задача осталась без предложения")
	}
	if second.Response.Proposal.ID == first.Response.Proposal.ID {
		t.Fatal("вторая задача переписала первое предложение вместо своего")
	}
	if !strings.Contains(second.Response.Reply, first.Response.Proposal.Title) {
		t.Fatalf("о ждущем предложении не сказано, очередь растёт молча: %q", second.Response.Reply)
	}

	// Решённое предложение назад не зовёт.
	if _, err = application.DecideQuestProposal(QuestProposalDecision{
		ProposalID: second.Response.Proposal.ID, Action: QuestProposalIgnore,
	}); err != nil {
		t.Fatal(err)
	}
	after, err := application.MasterChat(ctx, MasterChatRequest{Message: "ну ладно"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(after.Response.Reply, second.Response.Proposal.Title) {
		t.Fatalf("мастер напоминает об отклонённом предложении: %q", after.Response.Reply)
	}
}

// Задача не тонет в собственных словах.
//
// «Статус», «состояние», «команды» — обычные слова предметной области, но они же
// открывают сводку и справку Мастера. Пока упоминание било глагол, «Добавь
// команды в CLI» получало рассказ о том, что Мастер умеет, а «Обнови статус
// заказа» — отчёт об очереди: просьба исчезала, и догадаться, каким словом ты
// себе помешал, было нельзя.
func TestMasterHearsTaskEvenWhenItMentionsStatusOrCommands(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())

	world := openTestWorld(t, application)
	ctx := context.Background()
	if err = application.store.SaveOrchestratorConfig(ctx, domain.OrchestratorConfig{
		ID: "master", WorkspaceID: world.ID, Preset: "balanced", Provider: domain.ProviderOllama,
		Model: "qwen", PlanningDepth: 50, Parallelism: 2, ApprovalStrictness: 50, TeamPreference: 50,
	}); err != nil {
		t.Fatal(err)
	}
	boot, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	for _, blueprint := range boot.Blueprints[:min(2, len(boot.Blueprints))] {
		if _, err = application.SaveProjectAgent(domain.ProjectAgentFromBlueprint(world.ID, blueprint)); err != nil {
			t.Fatal(err)
		}
	}

	for _, task := range []string{"Добавь команды в CLI", "Обнови статус заказа в админке"} {
		view, chatErr := application.MasterChat(ctx, MasterChatRequest{Message: task})
		if chatErr != nil {
			t.Fatal(chatErr)
		}
		if view.Response.Proposal == nil {
			t.Fatalf("задача %q осталась без предложения квеста, ответ: %q", task, view.Response.Reply)
		}
	}

	// Обратная сторона правила: вопрос без глагола-задачи по-прежнему вопрос,
	// и работой он не становится.
	view, err := application.MasterChat(ctx, MasterChatRequest{Message: "какой сейчас статус?"})
	if err != nil {
		t.Fatal(err)
	}
	if view.Response.Proposal != nil {
		t.Fatalf("вопрос о статусе превратился в работу: %+v", view.Response.Proposal)
	}
}

// Та же задача второй раз — то же предложение.
//
// Задачу повторяют по-разному: не заметили ответа, вернулись к разговору,
// нажали не туда. Каждый повтор клал в очередь решений ещё одно предложение с
// тем же названием, и Мастер сообщал о «предыдущем предложении» с заголовком,
// который сам же только что назвал, — это читалось как сбой, а очередь росла
// на ровном месте.
func TestMasterDoesNotProposeTheSameTaskTwice(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())

	world := openTestWorld(t, application)
	ctx := context.Background()
	if err = application.store.SaveOrchestratorConfig(ctx, domain.OrchestratorConfig{
		ID: "master", WorkspaceID: world.ID, Preset: "balanced", Provider: domain.ProviderOllama,
		Model: "qwen", PlanningDepth: 50, Parallelism: 2, ApprovalStrictness: 50, TeamPreference: 50,
	}); err != nil {
		t.Fatal(err)
	}
	boot, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	for _, blueprint := range boot.Blueprints[:min(2, len(boot.Blueprints))] {
		if _, err = application.SaveProjectAgent(domain.ProjectAgentFromBlueprint(world.ID, blueprint)); err != nil {
			t.Fatal(err)
		}
	}

	const task = "Почини флаки-тест оплаты подписки"
	first, err := application.MasterChat(ctx, MasterChatRequest{Message: task})
	if err != nil {
		t.Fatal(err)
	}
	if first.Response.Proposal == nil {
		t.Fatal("на задачу мастер обязан предложить квест")
	}

	// Повтор — в том числе с хвостовой точкой: это та же задача.
	repeat, err := application.MasterChat(ctx, MasterChatRequest{Message: task + "."})
	if err != nil {
		t.Fatal(err)
	}
	if repeat.Response.Proposal == nil || repeat.Response.Proposal.ID != first.Response.Proposal.ID {
		t.Fatalf("повтор задачи дал другое предложение: %+v", repeat.Response.Proposal)
	}
	if !strings.Contains(repeat.Response.Reply, "та же задача") {
		t.Fatalf("повтор не назван повтором: %q", repeat.Response.Reply)
	}

	proposals, err := application.store.ListQuestProposals(ctx, world.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(proposals) != 1 {
		t.Fatalf("из одной задачи вышло %d предложений", len(proposals))
	}

	// Другая задача — по-прежнему своё предложение.
	other, err := application.MasterChat(ctx, MasterChatRequest{Message: "Добавь метрики в вебхук"})
	if err != nil {
		t.Fatal(err)
	}
	if other.Response.Proposal == nil || other.Response.Proposal.ID == first.Response.Proposal.ID {
		t.Fatalf("новая задача не получила своего предложения: %+v", other.Response.Proposal)
	}
}

// Обещание вернуться к задаче Мастер сдерживает.
//
// На пустой ростер он отвечает «наймите его, и я сразу предложу квест», на
// неработоспособный отряд — «поправьте их настройку, и я предложу квест».
// Человек уходит, делает, что просили, и возвращается со словом, которое само по
// себе не задача: «нанял», «поправил». Раньше в ответ приходило «Опишите
// задачу» — при том, что задача записана двумя репликами выше в этой же
// переписке, и обещание было дано там же.
func TestMasterReturnsToTheTaskItPromisedToResume(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())

	world := openTestWorld(t, application)
	ctx := context.Background()
	if err = application.store.SaveOrchestratorConfig(ctx, domain.OrchestratorConfig{
		ID: "master", WorkspaceID: world.ID, Preset: "balanced", Provider: domain.ProviderOllama,
		Model: "qwen", PlanningDepth: 50, Parallelism: 2, ApprovalStrictness: 50, TeamPreference: 50,
	}); err != nil {
		t.Fatal(err)
	}

	// Ростер пуст: Мастер предлагает найм и обещает вернуться к задаче.
	const task = "Почини флаки-тест оплаты подписки"
	empty, err := application.MasterChat(ctx, MasterChatRequest{Message: task})
	if err != nil {
		t.Fatal(err)
	}
	if empty.Response.Hire == nil || empty.Response.Proposal != nil {
		t.Fatalf("ожидался найм без предложения: %+v", empty.Response)
	}
	if !strings.Contains(empty.Response.Reply, "предложу квест") {
		t.Fatalf("обещание не дано, проверять нечего: %q", empty.Response.Reply)
	}

	// Человек нанял агента и вернулся со словом, которое задачей не является.
	boot, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = application.SaveProjectAgent(domain.ProjectAgentFromBlueprint(world.ID, boot.Blueprints[0])); err != nil {
		t.Fatal(err)
	}
	resumed, err := application.MasterChat(ctx, MasterChatRequest{Message: "нанял"})
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Response.Proposal == nil {
		t.Fatalf("обещание не сдержано — вместо предложения: %q", resumed.Response.Reply)
	}
	if !strings.Contains(resumed.Response.Proposal.Title, "Почини") {
		t.Fatalf("предложение не про ту задачу: %q", resumed.Response.Proposal.Title)
	}
	if !strings.Contains(resumed.Response.Reply, "Возвращаюсь к задаче") {
		t.Fatalf("отряд собран молча, будто по слову «нанял»: %q", resumed.Response.Reply)
	}

	// Дальше разговор не должен предлагать то же самое на каждую реплику:
	// предложение уже ждёт решения, и об этом Мастер и говорит.
	next, err := application.MasterChat(ctx, MasterChatRequest{Message: "спасибо"})
	if err != nil {
		t.Fatal(err)
	}
	if next.Response.Proposal != nil {
		t.Fatalf("на «спасибо» создано ещё одно предложение: %q", next.Response.Proposal.Title)
	}
}

// Отказ по неработоспособному отряду ведёт туда, где его чинят.
//
// «Открыть карточку агента и добавить недостающее умение?» стояло подсказкой, а
// подсказка уходит Мастеру следующей репликой: действие в интерфейсе,
// отправленное в чат. В ответ приходил тот же отказ — в мире-то ничего не
// поменялось. Чинят агента в гильдии, туда и должна вести кнопка.
func TestMasterRefusalOffersTheGuildInsteadOfTalkingToItself(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())

	world := openTestWorld(t, application)
	ctx := context.Background()
	if err = application.store.SaveOrchestratorConfig(ctx, domain.OrchestratorConfig{
		ID: "master", WorkspaceID: world.ID, Preset: "balanced", Provider: domain.ProviderOllama,
		Model: "qwen", PlanningDepth: 50, Parallelism: 2, ApprovalStrictness: 50, TeamPreference: 50,
	}); err != nil {
		t.Fatal(err)
	}
	boot, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	// Единственный агент мира без лимита ходов: ни одного шага он не выполнит.
	blocked := domain.ProjectAgentFromBlueprint(world.ID, boot.Blueprints[0])
	blocked.ID = ""
	blocked.MaxSteps = 0
	if _, err = application.SaveProjectAgent(blocked); err != nil {
		t.Fatal(err)
	}

	view, err := application.MasterChat(ctx, MasterChatRequest{Message: "Почини флаки-тест оплаты"})
	if err != nil {
		t.Fatal(err)
	}
	if view.Response.Proposal != nil {
		t.Fatalf("отряд неработоспособен, а квест всё равно предложен: %q", view.Response.Proposal.Title)
	}
	if len(view.Response.Party) == 0 || len(view.Response.Party[0].Blocking) == 0 {
		t.Fatalf("отказ не назвал, кто и почему не сможет: %+v", view.Response.Party)
	}
	for _, suggestion := range view.Response.Questions {
		answer, sendErr := application.MasterChat(ctx, MasterChatRequest{Message: suggestion})
		if sendErr != nil {
			t.Fatal(sendErr)
		}
		if answer.Response.Reply == view.Response.Reply {
			t.Fatalf("подсказка «%s» вернула тот же отказ — разговор ходит по кругу", suggestion)
		}
	}
	guild := false
	for _, action := range view.Response.Actions {
		if action.Tab == "agents" {
			guild = true
		}
	}
	if !guild {
		t.Fatalf("из отказа некуда пойти чинить агента: %+v", view.Response.Actions)
	}
}

// Подсказка — то, что можно сказать Мастеру, а не то, о чём он спрашивает.
//
// Чип с подсказкой подставляется в поле ввода человека и уходит следующей
// репликой — значит Мастер получает её в свой адрес. Пока подсказками были его
// собственные вопросы, разговор ходил по кругу: «Что нужно сделать в проекте?»
// возвращалось ему и отвечалось той же строкой, под которой этот чип и стоял.
// А «Что поменять в предложении?» вдобавок обещало то, чего он не умеет:
// править предложение из разговора нечем.
func TestMasterSuggestionsDoNotLoopBackToTheSameAnswer(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())

	world := openTestWorld(t, application)
	ctx := context.Background()
	if err = application.store.SaveOrchestratorConfig(ctx, domain.OrchestratorConfig{
		ID: "master", WorkspaceID: world.ID, Preset: "balanced", Provider: domain.ProviderOllama,
		Model: "qwen", PlanningDepth: 50, Parallelism: 2, ApprovalStrictness: 50, TeamPreference: 50,
	}); err != nil {
		t.Fatal(err)
	}
	boot, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	for _, blueprint := range boot.Blueprints[:min(2, len(boot.Blueprints))] {
		if _, err = application.SaveProjectAgent(domain.ProjectAgentFromBlueprint(world.ID, blueprint)); err != nil {
			t.Fatal(err)
		}
	}

	// Проверяем оба разговора, где подсказки и появляются: пустой и такой, где
	// предложение уже ждёт решения.
	for _, opening := range []string{"привет", "Почини флаки-тест оплаты"} {
		first, chatErr := application.MasterChat(ctx, MasterChatRequest{Message: opening})
		if chatErr != nil {
			t.Fatal(chatErr)
		}
		for _, suggestion := range first.Response.Questions {
			answer, sendErr := application.MasterChat(ctx, MasterChatRequest{Message: suggestion})
			if sendErr != nil {
				t.Fatal(sendErr)
			}
			if answer.Response.Reply == first.Response.Reply {
				t.Fatalf("после «%s» подсказка «%s» вернула тот же ответ — разговор ходит по кругу",
					opening, suggestion)
			}
			if strings.TrimSpace(answer.Response.Reply) == "" {
				t.Fatalf("подсказка «%s» осталась без ответа", suggestion)
			}
		}
	}
}

// Числа в одном ответе сходятся между собой.
//
// Сводка и кнопки перехода спрашивали снимок мира каждая для себя. Снимок — это
// вся очередь решений целиком, так что двойная работа заметна; но хуже, что два
// снимка берутся в разные мгновения, а между ними агент успевает попросить
// разрешения. Один ответ мог сказать «ждут решения: 3» и тут же подписать кнопку
// «разобрать очередь (2)» — числа, спорящие сами с собой в двух строках подряд.
func TestMasterAnswerUsesOneSnapshotOfTheWorld(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())

	world := openTestWorld(t, application)
	ctx := context.Background()
	cfg := domain.OrchestratorConfig{
		ID: "master", WorkspaceID: world.ID, Preset: "balanced", Provider: domain.ProviderOllama,
		Model: "qwen", PlanningDepth: 50, Parallelism: 2, ApprovalStrictness: 50, TeamPreference: 50,
	}
	if err = application.store.SaveOrchestratorConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	boot, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	for _, blueprint := range boot.Blueprints[:min(2, len(boot.Blueprints))] {
		if _, err = application.SaveProjectAgent(domain.ProjectAgentFromBlueprint(world.ID, blueprint)); err != nil {
			t.Fatal(err)
		}
	}

	// Мир, меняющийся между запросами: каждый следующий снимок насчитывает на
	// одно ожидающее решение больше.
	taken := 0
	service := orchestrator.ChatService{
		Store: application.store,
		NewID: domain.NewID,
		Situation: func(context.Context, string) (orchestrator.Situation, error) {
			taken++
			return orchestrator.Situation{WaitingDecisions: taken}, nil
		},
	}
	response, err := service.Chat(ctx, orchestrator.ChatRequest{
		WorkspaceID: world.ID, Message: "что сейчас?", Config: cfg,
	})
	if err != nil {
		t.Fatal(err)
	}
	if taken != 1 {
		t.Fatalf("снимков мира на один ход: %d", taken)
	}
	if !strings.Contains(response.Reply, "Ждут вашего решения: 1") {
		t.Fatalf("сводка не назвала число из снимка: %q", response.Reply)
	}
	found := false
	for _, action := range response.Actions {
		if strings.Contains(action.Label, "очередь") {
			found = true
			if !strings.Contains(action.Label, "(1)") {
				t.Fatalf("кнопка спорит со сводкой: %q при %q", action.Label, response.Reply)
			}
		}
	}
	if !found {
		t.Fatalf("ожидающая очередь не дала кнопки перехода: %+v", response.Actions)
	}
}

// Неполученный снимок мира — не спокойный мир.
//
// Очередь решений, наборы изменений и выполняющиеся узлы Мастер берёт одним
// снимком. Отказ снимка возвращался теми же нулями, что и тишина, и на «что
// сейчас?» приходило уверенное «ничего не ждёт вашего решения» — при том, что
// счётчик в шапке приходит другим запросом и мог в это же время показывать
// очередь. Спокойствие, которого никто не проверял, хуже отсутствия сводки.
func TestMasterDoesNotPromiseCalmItCouldNotCheck(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())

	world := openTestWorld(t, application)
	ctx := context.Background()
	cfg := domain.OrchestratorConfig{
		ID: "master", WorkspaceID: world.ID, Preset: "balanced", Provider: domain.ProviderOllama,
		Model: "qwen", PlanningDepth: 50, Parallelism: 2, ApprovalStrictness: 50, TeamPreference: 50,
	}
	if err = application.store.SaveOrchestratorConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	// Пустой ростер отвечает раньше и о другом — до сводки разговор бы не дошёл.
	boot, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	for _, blueprint := range boot.Blueprints[:min(2, len(boot.Blueprints))] {
		if _, err = application.SaveProjectAgent(domain.ProjectAgentFromBlueprint(world.ID, blueprint)); err != nil {
			t.Fatal(err)
		}
	}

	// Хранилище настоящее — ломается ровно то, что и в жизни: сведение картины
	// мира. Всё остальное в ответе считается отдельно и остаётся правдой.
	service := orchestrator.ChatService{
		Store: application.store,
		NewID: domain.NewID,
		Situation: func(context.Context, string) (orchestrator.Situation, error) {
			return orchestrator.Situation{}, errors.New("хранилище занято")
		},
	}
	response, err := service.Chat(ctx, orchestrator.ChatRequest{
		WorkspaceID: world.ID, Message: "что сейчас?", Config: cfg,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(response.Reply, "Ничего не ждёт") {
		t.Fatalf("мастер обещает тишину, которую не проверял: %q", response.Reply)
	}
	if !strings.Contains(response.Reply, "не удалось") {
		t.Fatalf("отказ снимка обязан быть назван: %q", response.Reply)
	}
	// То, что посчитано мимо снимка, теряться не должно: разговор без чисел
	// вообще — плата больше, чем сам отказ.
	if !strings.Contains(response.Reply, "Активных квестов") {
		t.Fatalf("надёжные числа пропали вместе с ненадёжными: %q", response.Reply)
	}
	// Кнопка «разобрать очередь (0)» звала бы в пустоту с выдуманным числом.
	for _, action := range response.Actions {
		if strings.Contains(action.Label, "очередь") {
			t.Fatalf("действие построено на неполученном снимке: %+v", action)
		}
	}
}
