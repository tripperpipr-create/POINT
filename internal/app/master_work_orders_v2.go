package app

import (
	"context"
	"local-agent-workbench/internal/textutil"
	"net"
	"net/url"
	"path/filepath"
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
	if existing, ok := a.openWorkOrderForConversationV2(ctx, proposal.WorkspaceID, conversationID); ok {
		orderID = existing.ID
	}

	order := domain.WorkOrder{
		ID: orderID, ProposalID: proposal.ID, WorkspaceID: proposal.WorkspaceID,
		ConversationID: strings.TrimSpace(conversationID),
		Version:        1, State: "discussion", Goal: brief.Goal,
		SourceRequest: brief.SourceRequest, Clarifications: append([]string(nil), brief.Clarifications...),
		Scope: append([]string(nil), brief.Scope...), OutOfScope: append([]string(nil), brief.OutOfScope...),
		OpenQuestions: append([]string(nil), brief.OpenQuestions...), Criteria: append([]domain.AcceptanceCriterion(nil), brief.Criteria...),
		Sources: append([]domain.SourceSnapshotRef(nil), sources...),
		Stack:   domain.StackPresetRef{ID: masterStackPresetV2(brief), Version: "1", Category: masterStackCategoryV2(brief), Source: "benchmark"},
		Routing: domain.ModelRoutingPolicy{
			Mode: "fixed", FixedConnectionID: cfg.ConnectionID, FixedModel: cfg.Model,
			FallbackMode: "auto", Certification: "experimental", Experimental: true,
			Adapter: domain.AdapterCapabilityManifest{Tools: true, StructuredOutput: true, ContextTokens: masterRouteContextTokensV2(cfg.Model), CostVisibility: "unknown"},
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
	if scope, ok := ctx.Value(masterScopeKey{}).(masterScope); ok && scope.Workspace.ID == proposal.WorkspaceID {
		order.Workspace = domain.WorkspacePlan{Mode: "existing", Path: scope.Workspace.Path}
	}
	// Ручной приёмки у изменений проекта нет (propose_brief её отклоняет); если
	// ручной критерий всё же дошёл, он становится допущением, а наряд без
	// исполняемой проверки остаётся в обсуждении.
	order.Criteria, order.Assumptions, order.OpenQuestions, order.State = withoutManualCriteriaV2(brief, order)
	if order.Workspace.Mode == "existing" {
		order.Sandbox = environment.Analyze(order.Workspace.Path, order.WorkspaceID).Runtime
	}
	order.Dependencies = environment.DependencyPlanFor(order.Workspace.Path, order.Criteria)
	dependencyToolchains := []string{}
	if order.Dependencies != nil {
		if order.Sandbox.Toolchains == nil {
			order.Sandbox.Toolchains = map[string]string{}
		}
		for _, project := range order.Dependencies.Projects {
			detected := environment.Analyze(filepath.Join(order.Workspace.Path, filepath.FromSlash(project.Cwd)), order.WorkspaceID).Runtime
			for toolchain, version := range detected.Toolchains {
				dependencyToolchains = appendUniqueStrings(dependencyToolchains, toolchain)
				if _, exists := order.Sandbox.Toolchains[toolchain]; !exists {
					order.Sandbox.Toolchains[toolchain] = version
				}
			}
		}
	}
	order.Setup = masterSetupPlanV2(order.Stack.ID, brief)
	order.Network = masterNetworkGrantsV2(brief, sources, order.Setup, appendUniqueStrings(masterToolchainsV2(brief, order.Workspace), dependencyToolchains...))
	order.Completion = masterCompletionProfileV2(order)
	// Чтение текущей версии и запись следующей — под одним замком с фоновым
	// подбором (work_order_staffing_v2.go), иначе обе взяли бы одну версию.
	a.staffing.save.Lock()
	defer a.staffing.save.Unlock()
	current, getErr := a.store.GetWorkOrderV2(ctx, order.ID)
	if getErr == nil {
		order.Version = current.Version + 1
		order.CreatedAt = current.CreatedAt
		// A user-selected runtime belongs to the reviewable WorkOrder version.
		// A later Master turn must not silently reset it to auto detection.
		if len(current.Sandbox.Toolchains) > 0 {
			order.Sandbox = current.Sandbox
		}
		if current.Workspace.Mode == "managed" {
			order.Workspace = current.Workspace
			order.WorkspaceID = current.WorkspaceID
		}
	} else if !storage.IsNotFound(getErr) {
		return "", getErr
	}
	// Ветку и коммит решает git-агент: Мастер о git не думает.
	var previous *domain.WorkOrder
	if getErr == nil {
		previous = &current
	}
	apiKey := ""
	if len(apiKeys) > 0 {
		apiKey = apiKeys[0]
	}
	if order.State == "staffing" {
		// Состав и ветку подбирает фоновая задача после хода (TODO Q15). До
		// неё наряд несёт прежний git-план с выбором человека.
		order.Roster = domain.AgentRosterPlan{Selecting: true}
		if previous != nil && previous.Git != nil {
			plan := *previous.Git
			order.Git, order.Delivery.CommitMode = &plan, previous.Delivery.CommitMode
		}
	} else {
		// Selection is intentionally delayed until the brief is decision-complete.
		order.Git, order.Delivery.CommitMode = a.masterWorkOrderGitV2(ctx, order, previous, cfg, apiKey)
		order.Roster = domain.AgentRosterPlan{}
	}
	saved, err := a.SaveWorkOrderV2(ctx, order)
	if err != nil {
		return "", err
	}
	a.dropStaleWorkOrdersForConversationV2(ctx, saved.WorkspaceID, conversationID, saved.ID)
	if saved.Roster.Selecting {
		a.startWorkOrderStaffingV2(saved.ID, cfg, apiKey)
	}
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
func (a *App) openWorkOrderForConversationV2(ctx context.Context, workspaceID, conversationID string) (domain.WorkOrder, bool) {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return domain.WorkOrder{}, false
	}
	orders, err := a.store.ListWorkOrdersForConversationV2(ctx, workspaceID, conversationID)
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
func (a *App) dropStaleWorkOrdersForConversationV2(ctx context.Context, workspaceID, conversationID, keepID string) {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return
	}
	orders, err := a.store.ListWorkOrdersForConversationV2(ctx, workspaceID, conversationID)
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

// masterRouteContextTokensV2 берёт окно маршрута из справочника моделей.
// Прежние жёсткие 32768 записывали в карточку окно, которого у Qwen3 (131072)
// нет, и расходились с тем, что исполнитель получает на самом деле.
func masterRouteContextTokensV2(model string) int {
	if known := domain.DefaultContextWindow(model); known > 0 {
		return known
	}
	return legacyAgentContextWindow
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

func withoutManualCriteriaV2(brief domain.TaskBrief, order domain.WorkOrder) ([]domain.AcceptanceCriterion, []string, []string, string) {
	criteria, assumptions, questions, state := order.Criteria, order.Assumptions, order.OpenQuestions, order.State
	if brief.ResultKind != "workspace_change" && brief.ResultKind != "hub_tool" {
		return criteria, assumptions, questions, state
	}
	kept := make([]domain.AcceptanceCriterion, 0, len(criteria))
	for _, criterion := range criteria {
		if criterion.Kind == "manual" {
			assumptions = append(assumptions, "Проверьте сами после квеста: "+strings.TrimSpace(criterion.Text))
			continue
		}
		kept = append(kept, criterion)
	}
	if len(kept) == 0 && len(criteria) > 0 {
		questions = append(questions, "Как квесту самому проверить результат: какой тест или команда это докажет?")
		state = "discussion"
	}
	return kept, assumptions, questions, state
}
