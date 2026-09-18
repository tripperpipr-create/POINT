package companion

import (
	"context"
	"fmt"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/flowruntime"
)

func (s Service) deterministicChat(ctx context.Context, cfg domain.CompanionConfig, message string, projectContext gatheredContext, history []domain.CompanionMessage) (ChatResponse, error) {
	lower := strings.ToLower(message)
	response := ChatResponse{Level: "suggestion", Mode: "deterministic", FactsUsed: projectContext.Facts}
	intent := classifyCompanionIntent(lower)
	lastAssistant := lastCompanionByRole(history, "assistant")
	if lastAssistant.ProposalID != "" && isCompanionAffirmative(lower) {
		response.Reply = "Предложение уже в ленте. Запуск только после Start на карточке — я сам его не запускаю."
		response.Questions = mergeCompanionQuestions(response.Questions, ideFollowUpQuestions(message, projectContext.Focus))
		return response, nil
	}
	if isCompanionFollowUp(lower) && strings.TrimSpace(lastAssistant.Content) != "" && intent != companionIntentPlanning && intent != companionIntentUsage {
		response.Reply = continueDeterministicReply(lastAssistant, projectContext)
		response.Questions = mergeCompanionQuestions(response.Questions, ideFollowUpQuestions(message, projectContext.Focus))
		return response, nil
	}
	if intent == companionIntentPlanning && shouldAskQuestion(lower, cfg.QuestionStrictness) {
		response.Questions = append(response.Questions, "Какой главный outcome вы хотите подтвердить до старта?")
		response.Reply = "Сначала уточню архитектурную/outcome неоднозначность, затем предложу квест."
		return response, nil
	}
	response.Level = interventionLevel(lower, cfg)
	if intent == companionIntentUsage {
		response.Level = usageAnalysisLevel(projectContext.Usage)
		response.Reply = deterministicUsageReply(projectContext.Usage, cfg.Verbosity)
		return response, nil
	}
	if reply, ok := pointIDEFeatureReply(lower); ok && intent != companionIntentPlanning {
		response.Reply = reply
		return response, nil
	}
	if intent != companionIntentPlanning && (isIDEProblemRequest(lower) || isHereRequest(lower)) {
		response.Level, response.Reply = deterministicIDEReply(projectContext.IDE, projectContext.Focus, cfg.Verbosity)
		response.Questions = mergeCompanionQuestions(response.Questions, ideFollowUpQuestions(message, projectContext.Focus))
		return response, nil
	}
	if intent != companionIntentPlanning {
		response.Reply = deterministicStatusReply(projectContext.Facts, projectContext.Focus, cfg.Verbosity)
		response.Questions = mergeCompanionQuestions(response.Questions, ideFollowUpQuestions(message, projectContext.Focus))
		return response, nil
	}
	proposal, err := s.proposeQuest(ctx, RecommendRequest{WorkspaceID: cfg.WorkspaceID, Goal: message}, cfg)
	if err != nil {
		return ChatResponse{}, err
	}
	if isIDEProblemRequest(lower) || isHereRequest(lower) {
		groundQuestProposalInIDE(&proposal, projectContext.IDE, projectContext.Focus)
		if err = s.Store.SaveQuestProposal(ctx, proposal); err != nil {
			return ChatResponse{}, err
		}
	}
	response.Proposal = &proposal
	response.Reply = fmt.Sprintf("Предлагаю квест «%s» с %d этапами и отрядом из %d агентов. Запуск — только после вашего подтверждения.", proposal.Title, len(proposal.Objectives), len(proposal.TeamAgentIDs))
	if cfg.Verbosity >= 70 {
		response.Reply += " План учитывает текущие квесты, исполнения, память, индекс проекта и статистику использования."
	}
	if cfg.AutoAct {
		response.Reply += " Auto-act настроен, но Companion по-прежнему не выполняет мутации без явной команды."
	}
	response.Questions = mergeCompanionQuestions(response.Questions, ideFollowUpQuestions(message, projectContext.Focus))
	return response, nil
}

func pointIDEFeatureReply(lower string) (string, bool) {
	mentionsProduct := strings.Contains(lower, "point") || strings.Contains(lower, "ide") || strings.Contains(lower, "иде") || strings.Contains(lower, "редактор") || strings.Contains(lower, "в ней") || strings.Contains(lower, "у тебя")
	if !mentionsProduct {
		return "", false
	}
	switch {
	case strings.Contains(lower, "ssh") || strings.Contains(lower, "сервер") || strings.Contains(lower, "remote"):
		return "Да. В Point IDE есть встроенная работа по SSH: сохранённые профили серверов, проверка подключения, SSH-терминал, просмотр удалённых каталогов и безопасный предпросмотр файлов. Откройте правое окно «SSH» или выполните команду «Point: Подключиться к серверу».", true
	case strings.Contains(lower, "баз") || strings.Contains(lower, "database") || strings.Contains(lower, "sql") || strings.Contains(lower, "postgres") || strings.Contains(lower, "mysql") || strings.Contains(lower, "sqlite"):
		return "Да. Окно «Базы данных» в правой панели поддерживает профили SQLite, PostgreSQL и MySQL, проверку подключения, просмотр схемы и SQL-запросы; рискованные операции требуют подтверждения.", true
	case strings.Contains(lower, "git") || strings.Contains(lower, "репозитор") || strings.Contains(lower, "commit") || strings.Contains(lower, "клонир"):
		return "В Point IDE встроен полный основной Git-сценарий: клонирование проекта, рабочее дерево и diff, commit, rollback, pull, push, blame и «Летопись Git» с историей. Быстрый вход — правое окно «Git».", true
	case strings.Contains(lower, "терминал") || strings.Contains(lower, "terminal") || strings.Contains(lower, "запуск") || strings.Contains(lower, "debug"):
		return "В Point IDE есть терминальные каналы с выбором shell/REPL, Run Anything, задачи, конфигурации запуска и отладка. Откройте правое окно «Терминал» или нажмите Alt+F12.", true
	case strings.Contains(lower, "проект") || strings.Contains(lower, "возможност") || strings.Contains(lower, "умеет") || strings.Contains(lower, "функц"):
		return "Point — самостоятельная IDE на базе редактора Code-OSS. Она объединяет работу с кодом и файлами, несколько проектов, терминал и запуск, Git, базы данных, SSH, Docker и Agent Hub. Основные инструменты вынесены в правые окна «Помощник Point», «Терминал», «Базы данных», «SSH» и «Git».", true
	default:
		return "", false
	}
}

func (s Service) proposeFlowAction(ctx context.Context, cfg domain.CompanionConfig, message string, projectContext gatheredContext) (ChatResponse, error) {
	response := ChatResponse{
		Level: interventionLevel(strings.ToLower(message), cfg), Mode: "deterministic", FactsUsed: projectContext.Facts,
	}
	agents, err := s.Store.ListProjectAgents(ctx, cfg.WorkspaceID)
	if err != nil {
		return ChatResponse{}, err
	}
	if len(agents) == 0 {
		response.Reply = "Для рабочего флоу нужен хотя бы один проектный агент. Сначала создайте или добавьте агента в текущий проект."
		response.Questions = []string{"Какого первого агента добавить в этот флоу?"}
		return response, nil
	}
	team := selectTeam(agents, message, 2)
	primary := team[0]
	reviewer := primary
	if len(team) > 1 {
		reviewer = team[1]
	}
	importance := domain.QuestNormal
	lower := strings.ToLower(message)
	if strings.Contains(lower, "critical") || strings.Contains(lower, "критич") || strings.Contains(lower, "security") || strings.Contains(lower, "безопас") || strings.Contains(lower, "prod") {
		importance = domain.QuestCritical
	} else if strings.Contains(lower, "important") || strings.Contains(lower, "важн") || strings.Contains(lower, "review") || strings.Contains(lower, "ревью") {
		importance = domain.QuestImportant
	}
	flow := flowruntime.ImportanceTemplate(importance, primary, reviewer)
	flow.WorkspaceID = cfg.WorkspaceID
	flow.Name = companionFlowName(message)
	flow.Description = fmt.Sprintf("Companion draft from an explicit user request; %s safety template.", importance)
	for index := range flow.Nodes {
		flow.Nodes[index].PositionX = float64(index%5) * 220
		flow.Nodes[index].PositionY = float64(index/5) * 150
	}
	now := time.Now().UTC()
	proposal := domain.CompanionActionProposal{
		ID: domain.NewID("companionaction"), WorkspaceID: cfg.WorkspaceID, Kind: domain.CompanionActionCreateFlow,
		Title:     "Создать Flow · " + flow.Name,
		Rationale: fmt.Sprintf("Подготовлен %s-шаблон с %d узлами и %d связями. Создание произойдёт только после подтверждения.", importance, len(flow.Nodes), len(flow.Edges)),
		Flow:      &flow, Status: "pending", CreatedAt: now, UpdatedAt: now,
	}
	if err = s.Store.SaveCompanionActionProposal(ctx, proposal); err != nil {
		return ChatResponse{}, err
	}
	response.ActionProposal = &proposal
	response.Reply = fmt.Sprintf("Подготовил черновик Flow «%s»: %d узлов, %d связей. Проверьте граф и подтвердите создание.", flow.Name, len(flow.Nodes), len(flow.Edges))
	return response, nil
}
