package app

import (
	"context"
	"local-agent-workbench/internal/textutil"
	"net"
	"net/url"
	"strings"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/environment"
	"local-agent-workbench/internal/orchestrator"
	"local-agent-workbench/internal/storage"
)

// saveMasterWorkOrderV2 turns the Master's structured task proposal into the
// single launch card. The model never supplies approval fields, workspace
// isolation or routing authority: those are derived from trusted local state.
func (a *App) saveMasterWorkOrderV2(ctx context.Context, proposal *domain.QuestProposal, sources []domain.SourceSnapshotRef, conversationID string, hire *orchestrator.AgentDraftProposal) (string, error) {
	if proposal == nil || proposal.Brief == nil {
		return "", nil
	}
	brief := *proposal.Brief
	cfg, err := a.masterConfig(ctx, proposal.WorkspaceID)
	if err != nil {
		return "", err
	}

	// Разговор Мастера ведёт одну работу, и карточка запуска у неё одна.
	// Идентификатор наряда шёл от предложения, а предложение у каждого хода
	// своё: стоило модели не назвать proposalId уточняемого квеста — и уточнение
	// приходило в ленту второй карточкой вместо новой версии первой. Продолжаем
	// открытый наряд беседы; новый идентификатор появляется только тогда, когда
	// продолжать нечего.
	orderID := "workorder-" + proposal.ID
	previousRoster := domain.AgentRosterPlan{}
	if existing, ok := a.openWorkOrderForConversationV2(ctx, conversationID); ok {
		orderID = existing.ID
		previousRoster = existing.Roster
	}

	order := domain.WorkOrder{
		ID: orderID, WorkspaceID: proposal.WorkspaceID,
		ConversationID: strings.TrimSpace(conversationID),
		Version:        1, State: "discussion", Goal: brief.Goal,
		Scope: append([]string(nil), brief.Scope...), OutOfScope: append([]string(nil), brief.OutOfScope...),
		OpenQuestions: append([]string(nil), brief.OpenQuestions...), Criteria: append([]domain.AcceptanceCriterion(nil), brief.Criteria...),
		Sources: append([]domain.SourceSnapshotRef(nil), sources...),
		Stack:   domain.StackPresetRef{ID: masterStackPresetV2(brief), Version: "1", Category: masterStackCategoryV2(brief), Source: "benchmark"},
		Routing: domain.ModelRoutingPolicy{
			Mode: "fixed", FixedConnectionID: cfg.ConnectionID, FixedModel: cfg.Model,
			FallbackMode: "auto", Certification: "experimental", Experimental: true,
			Adapter: domain.AdapterCapabilityManifest{Tools: true, StructuredOutput: true, ContextTokens: 32768, CostVisibility: "unknown"},
		},
		Budget: domain.BudgetEnvelope{
			Preset: "medium", Tokens: brief.Budget.Tokens, CostCents: brief.Budget.CostCents,
			ActiveSeconds: brief.Budget.ActiveSeconds, MaxParallel: brief.Budget.MaxParallel,
			MaxReplans: brief.Budget.MaxReplans, MaxAttempts: brief.Budget.MaxAttempts,
			MaxSteps: 64, MaxProjectAgents: brief.Budget.MaxProjectAgents,
		},
		Delivery: domain.DeliveryPolicy{ApplyMode: "automatic", CommitMode: "none", KeepPartialDays: 30},
	}
	if order.Stack.Category == "web" || order.Stack.Category == "api" {
		order.Delivery.KeepServicesRunning = true
		order.Delivery.ApplicationURL = "http://localhost:8080"
	}
	for _, decision := range brief.Decisions {
		text := strings.TrimSpace(decision.Topic + ": " + decision.Decision)
		if text != ":" {
			order.Assumptions = append(order.Assumptions, text)
		}
	}
	if len(order.OpenQuestions) > 2 {
		order.OpenQuestions = order.OpenQuestions[:2]
	}
	if brief.State == "ready" && len(order.OpenQuestions) == 0 {
		order.State = "ready"
	}
	if order.Budget.Tokens <= 0 {
		order.Budget.Tokens = 200000
	}
	if order.Budget.ActiveSeconds <= 0 {
		order.Budget.ActiveSeconds = 3600
	}
	if order.Budget.MaxParallel <= 0 {
		order.Budget.MaxParallel = 2
	}
	if order.Budget.MaxReplans <= 0 {
		order.Budget.MaxReplans = 6
	}
	if order.Budget.MaxAttempts <= 0 {
		order.Budget.MaxAttempts = 3
	}
	if order.Budget.MaxProjectAgents <= 0 {
		order.Budget.MaxProjectAgents = 2
	}

	a.mu.RLock()
	if a.currentWorkspace != nil && a.currentWorkspace.ID == proposal.WorkspaceID {
		order.Workspace = domain.WorkspacePlan{Mode: "existing", Path: a.currentWorkspace.Path}
	} else {
		order.Workspace = domain.WorkspacePlan{Mode: "managed"}
	}
	a.mu.RUnlock()
	if order.Workspace.Mode == "existing" && isCleanGitWorkspace(order.Workspace.Path) {
		order.Delivery.CommitMode = "squash"
	}
	order.Network = masterNetworkGrantsV2(brief, sources)
	order.Completion = masterCompletionProfileV2(order)
	order.Roster = a.masterRosterV2(ctx, order, proposal, previousRoster, hire)

	current, getErr := a.store.GetWorkOrderV2(ctx, order.ID)
	if getErr == nil {
		order.Version = current.Version + 1
		order.CreatedAt = current.CreatedAt
		if current.Workspace.Mode == "managed" {
			order.Workspace = current.Workspace
			order.WorkspaceID = current.WorkspaceID
		}
	} else if !storage.IsNotFound(getErr) {
		return "", getErr
	}
	saved, err := a.SaveWorkOrderV2(ctx, order)
	if err != nil {
		return "", err
	}
	a.dropStaleWorkOrdersForConversationV2(ctx, conversationID, saved.ID)
	return saved.ID, nil
}

// openWorkOrderForConversationV2 отдаёт наряд беседы, который ещё обсуждают.
// Список идёт от свежих к старым, поэтому продолжается последний открытый.
func (a *App) openWorkOrderForConversationV2(ctx context.Context, conversationID string) (domain.WorkOrder, bool) {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return domain.WorkOrder{}, false
	}
	orders, err := a.store.ListWorkOrdersForConversationV2(ctx, conversationID)
	if err != nil {
		return domain.WorkOrder{}, false
	}
	for _, order := range orders {
		if isOpenWorkOrderV2(order) {
			return order, true
		}
	}
	return domain.WorkOrder{}, false
}

// Открытый наряд — тот, который ещё обсуждают: человек его не утверждал и за
// ним не стоит квест. Утверждённый наряд — запись состоявшегося запуска, её
// следующий ход разговора не переписывает и не убирает.
func isOpenWorkOrderV2(order domain.WorkOrder) bool {
	if order.State == "approved" || order.ApprovedDigest != "" {
		return false
	}
	return order.Runtime == nil || strings.TrimSpace(order.Runtime.QuestID) == ""
}

// dropStaleWorkOrdersForConversationV2 убирает открытые наряды беседы, которые
// остались от прежних ходов до этой правки. Они ничем не заняты, и человеку
// доставался выбор между черновиком и его же уточнением. Ошибка уборки не
// отменяет сохранённый наряд: лишняя карточка — не повод потерять свежую.
func (a *App) dropStaleWorkOrdersForConversationV2(ctx context.Context, conversationID, keepID string) {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return
	}
	orders, err := a.store.ListWorkOrdersForConversationV2(ctx, conversationID)
	if err != nil {
		return
	}
	for _, order := range orders {
		if order.ID == keepID || !isOpenWorkOrderV2(order) {
			continue
		}
		_ = a.store.DeleteWorkOrderV2(ctx, order.WorkspaceID, order.ID)
	}
}

// masterCompletionProfileV2 proposes only checks that can actually be executed
// on the delivered revision. A profile of names nobody runs is how `completed`
// stops meaning anything, so every command here comes from a manifest that
// exists in the approved workspace or from the delivery policy itself. The user
// edits the profile in the launch card, and the approval digest freezes it.
func masterCompletionProfileV2(order domain.WorkOrder) domain.CompletionProfile {
	category := strings.ToLower(strings.TrimSpace(order.Stack.Category))
	checks := []domain.CompletionCheck{{Kind: domain.CompletionCheckAcceptance}}
	seen := map[string]bool{domain.CompletionCheckAcceptance: true}
	add := func(kind, command string) {
		if command == "" || seen[kind] {
			return
		}
		seen[kind] = true
		checks = append(checks, domain.CompletionCheck{Kind: kind, Command: command})
	}
	if path := strings.TrimSpace(order.Workspace.Path); path != "" {
		plan := environment.Analyze(path, order.WorkspaceID)
		for _, item := range plan.Commands {
			kind := "build"
			if item.ProvidesVerification || item.ID == "tests" {
				kind = "automated_tests"
			}
			add(kind, environmentCommandTextV2(item))
		}
	}
	if order.Delivery.KeepServicesRunning {
		add("service_start", "docker compose up -d --wait")
		if url := strings.TrimSpace(order.Delivery.ApplicationURL); url != "" {
			add("health", "curl -fsS "+url)
		}
	}
	return domain.CompletionProfile{ID: "mvp-" + textutil.FirstNonEmpty(category, "general"), Version: "1", Checks: checks}
}

func environmentCommandTextV2(item domain.EnvironmentCommand) string {
	parts := make([]string, 0, len(item.Arguments)+1)
	for _, part := range append([]string{item.Program}, item.Arguments...) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if strings.ContainsAny(part, " \t") {
			part = `"` + part + `"`
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, " ")
}

// masterRosterV2 отдаёт состав исполнителей наблюдателю ростера. Раньше здесь
// стоял перенос предложенного состава и один и тот же черновик на всякий пустой
// проект — карточка обещала исполнителя, которого никто не подбирал.
//
// previous — ростер открытой карточки этой беседы: неподтверждённый черновик
// обязан сохранить свой идентификатор между ходами, иначе согласие человека
// сошлётся на исчезнувший черновик.
func (a *App) masterRosterV2(ctx context.Context, order domain.WorkOrder, proposal *domain.QuestProposal, previous domain.AgentRosterPlan, hire *orchestrator.AgentDraftProposal) domain.AgentRosterPlan {
	observation, err := a.ObserveRoster(ctx, rosterNeedFromOrder(order, proposal))
	if err != nil {
		return a.safeMinimumRosterV2(ctx, previous)
	}
	plan := a.applyModelDraftV2(ctx, observation.RosterPlan(previous), hire)
	if validateRosterPlanV2(plan, order.Budget) != nil || len(plan.Permanent) == 0 {
		// Негодный ростер стоит человеку всего хода: сохранение наряда проверяет
		// его доменными правилами и поднимает ошибку наружу, а не в карточку.
		return a.safeMinimumRosterV2(ctx, previous)
	}
	return plan
}

// applyModelDraftV2 принимает уточнение модели поверх собранного черновика.
// Модель называет специалиста словами задачи — «архитектор платежей» вместо
// «Backend-разработчик», — но не трогает ни идентификатор черновика, ни
// согласие, ни выбор существующего исполнителя: подбор от её послушности не
// зависит, и молчание модели ничего не стоит.
func (a *App) applyModelDraftV2(ctx context.Context, plan domain.AgentRosterPlan, hire *orchestrator.AgentDraftProposal) domain.AgentRosterPlan {
	if hire == nil {
		return plan
	}
	for index, draft := range plan.Permanent {
		if draft.Existing {
			continue
		}
		draft.Name = hire.Name
		draft.Role = hire.Role
		draft.Mission = hire.Mission
		if tools := a.filterKnownTools(ctx, appendUniqueStrings(hire.RequiredTools, draft.RequiredTools...)); len(tools) > 0 {
			draft.RequiredTools = tools
		}
		plan.Permanent[index] = draft
		break
	}
	return plan
}

// safeMinimumRosterV2 — то, чем карточка обходится, когда подбор не состоялся:
// один черновик исполнителя с согласием человека и только теми инструментами,
// которые в этой сборке существуют.
func (a *App) safeMinimumRosterV2(ctx context.Context, previous domain.AgentRosterPlan) domain.AgentRosterPlan {
	name, role := rosterRoleNaming("")
	draft := domain.AgentDraft{
		ID: domain.NewID("agentdraft"), Name: name, Role: role,
		Mission:         rosterMission(domain.RoleRequirement{}),
		RequiredTools:   a.filterKnownTools(ctx, []string{"project_map", "search_code", "list_files", "read_file", "propose_patch", "run_command", "git_diff"}),
		RequiresConsent: true,
	}
	return domain.AgentRosterPlan{Permanent: []domain.AgentDraft{carryRosterDraftIdentity(draft, previous)}}
}

// rosterNeedFromOrder переводит наряд в сводку требований. Права выводятся из
// разрешений задания: право писать и запускать команды — это и есть инструменты,
// без которых исполнителю нечем закрыть критерий.
func rosterNeedFromOrder(order domain.WorkOrder, proposal *domain.QuestProposal) RosterNeed {
	need := RosterNeed{
		WorkspaceID:   order.WorkspaceID,
		Goal:          order.Goal,
		Scope:         append([]string(nil), order.Scope...),
		StackCategory: order.Stack.Category,
		MaxAgents:     order.Budget.MaxProjectAgents,
		RequiredTools: []string{"list_files", "read_file", "search_code"},
	}
	for _, criterion := range order.Criteria {
		need.Criteria = append(need.Criteria, criterion.Text)
	}
	if proposal != nil {
		need.ProposedIDs = append([]string(nil), proposal.TeamAgentIDs...)
		if proposal.Brief != nil {
			need.Mode = string(proposal.Brief.Mode)
			need.AllowSubagents = proposal.Brief.Permissions.ProvisionProjectAgents
			if proposal.Brief.Permissions.WriteFiles {
				need.RequiredTools = append(need.RequiredTools, "propose_patch")
			}
			if proposal.Brief.Permissions.ExecuteCommands {
				need.RequiredTools = append(need.RequiredTools, "run_command")
			}
		}
	}
	return need
}

func masterStackCategoryV2(brief domain.TaskBrief) string {
	switch strings.ToLower(strings.TrimSpace(brief.ResultKind)) {
	case "cli", "command", "script":
		return "cli"
	case "data", "analysis":
		return "data"
	case "desktop", "mobile":
		return "desktop-mobile"
	default:
		return "web"
	}
}

func masterStackPresetV2(brief domain.TaskBrief) string {
	return "recommended-" + masterStackCategoryV2(brief)
}

func masterNetworkGrantsV2(brief domain.TaskBrief, sources []domain.SourceSnapshotRef) []domain.NetworkGrant {
	grants := map[string]string{}
	for _, value := range brief.Permissions.NetworkHosts {
		host := strings.ToLower(strings.TrimSpace(value))
		if host != "" {
			if _, _, err := net.SplitHostPort(host); err != nil {
				host += ":443"
			}
			grants[host] = "Доступ, необходимый для выполнения утверждённого задания"
		}
	}
	for _, source := range sources {
		parsed, err := url.Parse(source.Locator)
		if err == nil && parsed.Scheme == "https" && parsed.Hostname() != "" {
			grants[net.JoinHostPort(strings.ToLower(parsed.Hostname()), "443")] = "Обновление или проверка утверждённого источника"
		}
	}
	result := make([]domain.NetworkGrant, 0, len(grants))
	for host, purpose := range grants {
		result = append(result, domain.NetworkGrant{Host: host, Purpose: purpose})
	}
	return result
}
