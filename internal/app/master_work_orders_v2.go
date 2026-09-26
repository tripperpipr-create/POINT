package app

import (
	"context"
	"local-agent-workbench/internal/textutil"
	"net"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/environment"
	"local-agent-workbench/internal/orchestrator"
	"local-agent-workbench/internal/storage"
)

// saveMasterWorkOrderV2 turns the Master's structured task proposal into the
// single launch card. The model never supplies approval fields, workspace
// isolation or routing authority: those are derived from trusted local state.
func (a *App) saveMasterWorkOrderV2(ctx context.Context, proposal *domain.QuestProposal, sources []domain.SourceSnapshotRef, conversationID string, _ *orchestrator.AgentDraftProposal, apiKeys ...string) (string, error) {
	if proposal == nil || proposal.Brief == nil {
		return "", nil
	}
	brief := *proposal.Brief
	// A WorkOrder may be produced by the v2 Master path that used to keep its
	// proposal only in the response payload. Persist the source before linking
	// it so approval can atomically move that exact proposal to started.
	if strings.TrimSpace(proposal.Status) == "" {
		proposal.Status = "pending"
	}
	if proposal.CreatedAt.IsZero() {
		proposal.CreatedAt = time.Now().UTC()
	}
	durableProposal := *proposal
	// The WorkOrder accepts legacy Master briefs and normalizes them through
	// its own contract. Do not make proposal linkage depend on newer TaskBrief
	// validation; an existing validated brief is retained by COALESCE.
	durableProposal.Brief = nil
	if err := a.store.SaveQuestProposal(ctx, durableProposal); err != nil {
		return "", err
	}
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
	if existing, ok := a.openWorkOrderForConversationV2(ctx, conversationID); ok {
		orderID = existing.ID
	}

	order := domain.WorkOrder{
		ID: orderID, ProposalID: proposal.ID, WorkspaceID: proposal.WorkspaceID,
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
		order.State = "staffing"
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
	order.Setup = masterSetupPlanV2(order.Stack.ID, brief)
	order.Network = masterNetworkGrantsV2(brief, sources, order.Setup, masterToolchainsV2(brief, order.Workspace))
	order.Completion = masterCompletionProfileV2(order)
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
	selection := agentSelectionResult{}
	if order.State == "staffing" {
		apiKey := ""
		if len(apiKeys) > 0 {
			apiKey = apiKeys[0]
		}
		selection, err = a.selectAgentsForWorkOrder(ctx, order, cfg, apiKey)
		if err != nil {
			return "", err
		}
		order.Roster = a.rosterFromSelection(ctx, selection)
		if len(selection.Drafts) == 0 && len(selection.AgentIDs) > 0 {
			order.State = "ready"
		}
	} else {
		// Selection is intentionally delayed until the brief is decision-complete.
		order.Roster = domain.AgentRosterPlan{}
	}
	saved, err := a.SaveWorkOrderV2(ctx, order)
	if err != nil {
		return "", err
	}
	if order.State == "ready" || order.State == "staffing" {
		if err = a.store.ReplaceAgentSelectionBindings(ctx, saved.ID, saved.ConversationID, saved.WorkspaceID, selection.Digest, saved.Version, selection.AgentIDs); err != nil {
			return "", err
		}
		kind := "agent_selection_completed"
		if order.State == "staffing" {
			kind = "agent_selection_needs_creation"
		}
		_ = a.store.SaveAgentLifecycleEvent(ctx, domain.AgentLifecycleEvent{
			ID: domain.NewID("agentlife"), WorkspaceID: saved.WorkspaceID, WorkOrderID: saved.ID,
			Kind: kind, Detail: map[string]any{
				"selectionDigest": selection.Digest, "revision": saved.Version, "agentIds": selection.AgentIDs,
				"draftCount": len(selection.Drafts), "state": order.State,
				"model": cfg.Model, "fallbackReason": selection.Fallback, "validation": "accepted",
			}, CreatedAt: saved.UpdatedAt,
		})
	}
	a.dropStaleWorkOrdersForConversationV2(ctx, conversationID, saved.ID)
	return saved.ID, nil
}

func (a *App) rosterFromAgentIDs(ctx context.Context, ids []string) domain.AgentRosterPlan {
	plan := domain.AgentRosterPlan{AgentIDs: append([]string(nil), ids...)}
	for _, id := range ids {
		agent, err := a.store.GetProjectAgent(ctx, id)
		if err != nil {
			continue
		}
		plan.Permanent = append(plan.Permanent, domain.AgentDraft{
			ID: agent.ID, BlueprintID: agent.BlueprintID, Existing: true,
			Name: agent.Name, Role: agent.RoleDescription, Mission: agent.Mission,
			RequiredTools: append([]string(nil), agent.AllowedTools...),
		})
	}
	return plan
}

func (a *App) rosterFromSelection(ctx context.Context, selection agentSelectionResult) domain.AgentRosterPlan {
	plan := a.rosterFromAgentIDs(ctx, selection.AgentIDs)
	plan.Permanent = append(plan.Permanent, selection.Drafts...)
	return plan
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
	if order.Stack.ID == "php-symfony-7" {
		add("dependency_audit", "composer validate --no-check-publish")
		add("cli_smoke", "php bin/console about")
		return domain.CompletionProfile{ID: "php-symfony-7", Version: "1", Checks: checks}
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
	if masterBriefMentionsSymfony7(brief) {
		return "php-symfony-7"
	}
	return "recommended-" + masterStackCategoryV2(brief)
}

func masterBriefMentionsSymfony7(brief domain.TaskBrief) bool {
	parts := []string{brief.Goal, brief.ResultKind}
	parts = append(parts, brief.Scope...)
	parts = append(parts, brief.OutOfScope...)
	for _, decision := range brief.Decisions {
		parts = append(parts, decision.Topic, decision.Decision)
	}
	text := strings.ToLower(strings.Join(parts, " "))
	return strings.Contains(text, "symfony") && (strings.Contains(text, "symfony 7") || strings.Contains(text, "symfony7") || strings.Contains(text, "7.x"))
}

func masterSetupPlanV2(stackID string, brief domain.TaskBrief) domain.SetupPlan {
	if stackID != "php-symfony-7" {
		return domain.SetupPlan{}
	}
	plan := domain.SetupPlan{
		ID: "php-symfony-7", Version: "1",
		Commands: []domain.SetupCommand{
			{Command: `composer create-project symfony/skeleton:"7.*" . --no-interaction --prefer-dist`, TimeoutSeconds: 900},
		},
		ExpectedPaths: []string{"composer.json", "vendor/autoload.php", "bin/console"},
		OwnedPaths:    []string{"composer.json", "composer.lock", ".env", ".env.local", "bin", "vendor"},
	}
	requested := strings.ToLower(strings.Join(append([]string{brief.Goal}, brief.Scope...), " "))
	excluded := strings.ToLower(strings.Join(brief.OutOfScope, " "))
	if (strings.Contains(requested, "api platform") || strings.Contains(requested, "api-platform")) &&
		!strings.Contains(excluded, "api platform") && !strings.Contains(excluded, "api-platform") {
		plan.Commands = append(plan.Commands, domain.SetupCommand{Command: "composer require api-platform/core --no-interaction --prefer-dist", TimeoutSeconds: 900})
	}
	if (strings.Contains(requested, "orm") || strings.Contains(requested, "doctrine")) &&
		!strings.Contains(excluded, "orm") && !strings.Contains(excluded, "doctrine") {
		plan.Commands = append(plan.Commands, domain.SetupCommand{Command: "composer require symfony/orm-pack --no-interaction --prefer-dist", TimeoutSeconds: 900})
	}
	return plan
}

// Symfony packages are discovered on Packagist, GitHub dist archives travel
// through api.github.com and codeload.github.com, and Flex fetches recipes
// from raw.githubusercontent.com.
func composerDistributionHostsV2() []string {
	return []string{"repo.packagist.org", "packagist.org", "github.com", "api.github.com", "codeload.github.com", "raw.githubusercontent.com"}
}

func masterNetworkGrantsV2(brief domain.TaskBrief, sources []domain.SourceSnapshotRef, setup domain.SetupPlan, toolchains []string) []domain.NetworkGrant {
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
	// A package registry of the chosen language is part of the approved stack:
	// without it the planner "works around" the missing network by hand-writing
	// what a standard library already does (a PostgreSQL wire protocol instead
	// of pgx). The grant is shown on the card like any other.
	for _, toolchain := range toolchains {
		for _, host := range environment.RegistryHosts(toolchain) {
			key := net.JoinHostPort(host, "443")
			if _, ok := grants[key]; !ok {
				grants[key] = "Загрузка зависимостей: " + masterToolchainLabelV2(toolchain)
			}
		}
	}
	if setup.ID == "php-symfony-7" {
		for _, host := range composerDistributionHostsV2() {
			grants[net.JoinHostPort(host, "443")] = "Загрузка утверждённых Composer-зависимостей Symfony"
		}
	}
	result := make([]domain.NetworkGrant, 0, len(grants))
	for host, purpose := range grants {
		result = append(result, domain.NetworkGrant{Host: host, Purpose: purpose})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Host < result[j].Host })
	return result
}

// masterToolchainKeywordsV2 recognises the language a brief commits to. Only
// whole words count, so "javascript" is not Java and "golang.org" is not Go.
var masterToolchainKeywordsV2 = map[string][]string{
	"go":     {"go", "golang", "go.mod", "go.sum"},
	"node":   {"node", "node.js", "nodejs", "npm", "typescript", "javascript", "express", "nestjs", "next.js"},
	"python": {"python", "pip", "django", "fastapi", "flask", "pyproject.toml", "requirements.txt"},
	"php":    {"php", "composer", "laravel", "symfony"},
	"rust":   {"rust", "cargo"},
	"java":   {"java", "maven", "gradle", "kotlin"},
	"dotnet": {"dotnet", ".net", "c#", "asp.net"},
}

// masterToolchainsV2 combines the manifests already in the workspace with the
// language named in the approved scope, decisions and criteria. Out-of-scope
// text is ignored: "без Python-скриптов" must not open PyPI.
func masterToolchainsV2(brief domain.TaskBrief, workspace domain.WorkspacePlan) []string {
	found := map[string]bool{}
	if workspace.Mode == "existing" {
		for _, toolchain := range environment.ManifestToolchains(workspace.Path) {
			found[toolchain] = true
		}
	}
	parts := []string{brief.Goal}
	parts = append(parts, brief.Scope...)
	for _, decision := range brief.Decisions {
		parts = append(parts, decision.Topic, decision.Decision)
	}
	for _, criterion := range brief.Criteria {
		parts = append(parts, criterion.Text, string(criterion.Arguments))
	}
	words := map[string]bool{}
	for _, word := range strings.FieldsFunc(strings.ToLower(strings.Join(parts, " ")), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '.' && r != '#'
	}) {
		words[strings.TrimRight(word, ".")] = true
	}
	for toolchain, keywords := range masterToolchainKeywordsV2 {
		for _, keyword := range keywords {
			if words[keyword] {
				found[toolchain] = true
				break
			}
		}
	}
	result := make([]string, 0, len(found))
	for toolchain := range found {
		result = append(result, toolchain)
	}
	sort.Strings(result)
	return result
}

func masterToolchainLabelV2(toolchain string) string {
	switch toolchain {
	case "go":
		return "Go-модули"
	case "node":
		return "npm-пакеты"
	case "python":
		return "пакеты Python"
	case "php":
		return "Composer-пакеты"
	case "rust":
		return "crates Rust"
	case "java":
		return "Maven-артефакты"
	case "dotnet":
		return "NuGet-пакеты"
	default:
		return toolchain
	}
}
