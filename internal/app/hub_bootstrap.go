// Первичная выдача состояния Гильдии интерфейсу.
//
// Один ответ собирает чертежи, проектных агентов, отряды, квесты, навыки и
// нерешённые предложения: интерфейс рисует Чертог с одного запроса.
package app

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"local-agent-workbench/internal/companion"
	"local-agent-workbench/internal/connections"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/sandbox"
	"local-agent-workbench/internal/servers"
)

func (a *App) loadHubBootstrap(ctx context.Context, workspaceID string) (HubBootstrap, error) {
	hub := HubBootstrap{Sandbox: a.sandboxCapabilities()}
	blueprints, err := a.store.ListBlueprints(ctx)
	if err != nil {
		return hub, err
	}
	if blueprints == nil {
		blueprints = []domain.AgentBlueprint{}
	}
	hub.Blueprints = blueprints
	skills, err := a.store.ListSkills(ctx)
	if err != nil {
		return hub, err
	}
	if len(skills) == 0 {
		if err = a.seedDefaultSkills(ctx); err != nil {
			return hub, err
		}
		skills, err = a.store.ListSkills(ctx)
		if err != nil {
			return hub, err
		}
	}
	hub.Skills = skills
	connectionsList, err := a.store.ListConnections(ctx)
	if err != nil {
		return hub, err
	}
	hub.Connections = connectionsList
	hub.ModelCatalog = connections.DefaultModelCatalog()
	hub.ModelEvidence, err = a.store.ListModelCapabilityEvidence(ctx, "", "", 200)
	if err != nil {
		return hub, err
	}
	serverProfiles, err := a.store.ListServerProfiles(ctx)
	if err != nil {
		return hub, err
	}
	hub.ServerProfiles = serverProfiles
	dbConnections, err := a.store.ListDBConnections(ctx, workspaceID)
	if err != nil {
		return hub, err
	}
	hub.DBConnections = dbConnections
	if workspaceID == "" {
		return hub, nil
	}
	routing, routingErr := a.store.GetWorkspaceModelRouting(ctx, workspaceID)
	if errors.Is(routingErr, sql.ErrNoRows) {
		routing = domain.WorkspaceModelRouting{WorkspaceID: workspaceID}
	} else if routingErr != nil {
		return hub, routingErr
	}
	hub.ModelRouting, err = a.workspaceModelRoutingView(routing)
	if err != nil {
		return hub, err
	}
	agents, err := a.store.ListProjectAgents(ctx, workspaceID)
	if err != nil {
		return hub, err
	}
	if agents == nil {
		agents = []domain.ProjectAgent{}
	}
	hub.ProjectAgents = visibleProjectAgents(agents)
	hub.ProjectSkills, err = a.store.ListProjectSkills(ctx, workspaceID)
	if err != nil {
		return hub, err
	}
	hub.Teams, err = a.store.ListTeams(ctx, workspaceID)
	if err != nil {
		return hub, err
	}
	hub.Quests, err = a.store.ListQuests(ctx, workspaceID)
	if err != nil {
		return hub, err
	}
	hub.ModelCandidates, err = a.modelCandidates(ctx)
	if err != nil {
		return hub, err
	}
	hub.AgentPrepChains, err = a.store.ListAgentPrepChains(ctx, workspaceID)
	if err != nil {
		return hub, err
	}
	hub.TeamEvents, err = a.store.ListTeamEvents(ctx, workspaceID, "", "", false, 500)
	if err != nil {
		return hub, err
	}
	hub.Flows, err = a.store.ListFlows(ctx, workspaceID)
	if err != nil {
		return hub, err
	}
	hub.FlowRuns, err = a.store.ListFlowRuns(ctx, workspaceID, 50)
	if err != nil {
		return hub, err
	}
	hub.Executions, err = a.store.ListExecutions(ctx, workspaceID, 50)
	if err != nil {
		return hub, err
	}
	hub.ChangeSets, err = a.store.ListChangeSets(ctx, workspaceID)
	if err != nil {
		return hub, err
	}
	hub.Memories, err = a.store.ListMemories(ctx, workspaceID)
	if err != nil {
		return hub, err
	}
	hub.UsageRecords, err = a.store.ListUsageRecords(ctx, workspaceID, 100)
	if err != nil {
		return hub, err
	}
	hub.BudgetReservations, err = a.store.ListBudgetReservations(ctx, workspaceID, 500)
	if err != nil {
		return hub, err
	}
	hub.ModelPricingProfiles, err = a.store.ListModelPricingProfiles(ctx, workspaceID)
	if err != nil {
		return hub, err
	}
	hub.LearningSignals, err = a.store.ListLearningSignals(ctx, workspaceID, 200)
	if err != nil {
		return hub, err
	}
	hub.SkillOutcomes, err = a.store.ListSkillOutcomes(ctx, workspaceID, 500)
	if err != nil {
		return hub, err
	}
	allSkillOutcomes, err := a.store.ListAllSkillOutcomes(ctx, 1000)
	if err != nil {
		return hub, err
	}
	hub.SkillCuration = buildSkillCuration(hub.Skills, hub.ProjectAgents, hub.Blueprints, hub.ProjectSkills, allSkillOutcomes, time.Now().UTC())
	_ = a.queueCuratorMergeProposals(ctx, workspaceID)
	hub.CompanionMessages, err = a.store.ListCompanionMessages(ctx, workspaceID, 80)
	if err != nil {
		return hub, err
	}
	hub.QuestProposals, err = a.store.ListQuestProposals(ctx, workspaceID)
	if err != nil {
		return hub, err
	}
	hub.QuestProposals = trimResolvedProposals(hub.QuestProposals)
	hub.CompanionActionProposals, err = a.store.ListCompanionActionProposals(ctx, workspaceID)
	if err != nil {
		return hub, err
	}
	hub.IDEObservations, err = a.store.ListIDEObservations(ctx, workspaceID, 100)
	if err != nil {
		return hub, err
	}
	svc := companion.Service{Store: a.store}
	cfg, ensureErr := svc.EnsureConfig(ctx, workspaceID)
	if ensureErr != nil {
		return hub, ensureErr
	}
	hub.Companion = &cfg
	if orch, orchErr := a.store.GetOrchestratorConfig(ctx, workspaceID); orchErr == nil {
		hub.Orchestrator = &orch
	} else if !errors.Is(orchErr, sql.ErrNoRows) {
		return hub, orchErr
	}
	allUsage, err := a.store.ListUsageRecords(ctx, workspaceID, 5000)
	if err != nil {
		return hub, err
	}
	budget, err := a.loadHubBudgetSettings(ctx, workspaceID)
	if err != nil {
		return hub, err
	}
	now := time.Now().UTC()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	budgetInterventions := companion.BuildBudgetInterventions(companion.BudgetSnapshot{
		DailyLimitCents: budget.DailyCents, DailyUsedCents: a.usageCostCents(allUsage, dayStart),
		MonthlyLimitCents: budget.MonthlyCents, MonthlyUsedCents: a.usageCostCents(allUsage, monthStart), HardStop: budget.HardStop,
	})
	workspaceRuns, err := a.store.ListRunsForWorkspace(ctx, workspaceID, 500)
	if err != nil {
		return hub, err
	}
	linkedRuns := make(map[string]domain.RunStatus, len(hub.Executions))
	for _, execution := range hub.Executions {
		if execution.RunID != "" {
			linkedRuns[execution.RunID] = execution.Status
		}
	}
	diagnosticRuns := make([]domain.Run, 0, min(20, len(workspaceRuns)))
	selectedRuns := map[string]bool{}
	for _, run := range workspaceRuns {
		status, linked := linkedRuns[run.ID]
		if linked && (status == domain.RunPending || status == domain.RunRunning || status == domain.RunWaiting || status == domain.RunPaused) {
			diagnosticRuns = append(diagnosticRuns, run)
			selectedRuns[run.ID] = true
		}
	}
	for _, run := range workspaceRuns {
		if len(diagnosticRuns) >= 20 {
			break
		}
		if _, linked := linkedRuns[run.ID]; linked && !selectedRuns[run.ID] {
			diagnosticRuns = append(diagnosticRuns, run)
			selectedRuns[run.ID] = true
		}
	}
	companionRunDiagnostics, err := a.recentRunDiagnostics(ctx, diagnosticRuns, len(diagnosticRuns))
	if err != nil {
		return hub, err
	}
	focus := companion.LatestObservationFocus(hub.IDEObservations)
	interventions := companion.MergeInterventionsWithObservations(
		hub.IDEObservations,
		companion.BuildInterventions(cfg, hub.ProjectAgents, hub.Executions, hub.ChangeSets, hub.UsageRecords, hub.Connections),
		budgetInterventions,
		companion.BuildExecutionInterventions(cfg, hub.Executions, workspaceRuns, now),
		companion.BuildRunDiagnosticInterventions(cfg, hub.Executions, companionRunDiagnostics),
		companion.BuildIDEInterventionsGated(hub.IDEObservations, cfg, companion.InterveneContext{Now: now, FocusPath: focus}),
	)
	visible, dismissedCount, err := a.finalizeCompanionInterventions(ctx, workspaceID, cfg, hub.IDEObservations, interventions, now, focus)
	if err != nil {
		return hub, err
	}
	hub.CompanionInterventions, hub.CompanionDismissedCount = visible, dismissedCount
	return hub, nil
}

func permanentProjectAgents(agents []domain.ProjectAgent) []domain.ProjectAgent {
	result := make([]domain.ProjectAgent, 0, len(agents))
	for _, agent := range agents {
		if !agent.Temporary && (agent.Status == "" || agent.Status == domain.ProjectAgentActive) {
			result = append(result, agent)
		}
	}
	return result
}

func visibleProjectAgents(agents []domain.ProjectAgent) []domain.ProjectAgent {
	result := make([]domain.ProjectAgent, 0, len(agents))
	for _, agent := range agents {
		if !agent.Temporary {
			result = append(result, agent)
		}
	}
	return result
}

// bootstrapResolvedProposals — сколько решённых предложений уезжает клиенту.
const bootstrapResolvedProposals = 50

// trimResolvedProposals убирает из состояния мира то, что там уже не нужно.
//
// Открытые предложения отдаются все: по ним считается очередь решений и рисуются
// карточки на разбор, и старое нерешённое не должно пропасть с экрана. Решённые
// нужны только разговору с Мастером — он показывает строкой «квест запущен» или
// «предложение отклонено» у той реплики, что его создала, а реплик в разговоре
// видно шестьдесят.
//
// Разница не умозрительная. Каждая задача, сказанная Мастеру, создаёт
// предложение, решённое остаётся строкой в базе навсегда, а состояние мира
// запрашивается часто. Замер: шестьдесят ходов — это 38 КиБ предложений в
// каждом bootstrap, и дальше только больше.
//
// Список приходит от новых к старым, поэтому под предел попадают свежие.
func trimResolvedProposals(proposals []domain.QuestProposal) []domain.QuestProposal {
	trimmed := make([]domain.QuestProposal, 0, len(proposals))
	resolved := 0
	for _, proposal := range proposals {
		if proposal.Status == "pending" || proposal.Status == "modified" {
			trimmed = append(trimmed, proposal)
			continue
		}
		if resolved >= bootstrapResolvedProposals {
			continue
		}
		resolved++
		trimmed = append(trimmed, proposal)
	}
	return trimmed
}

func (a *App) seedDefaultSkills(ctx context.Context) error {
	now := time.Now().UTC()
	defaults := []domain.SkillDefinition{
		{
			ID: "skill-code-review", Name: "Code Review", Description: "Review diffs for correctness and risks",
			Instructions:    "Если доступен git_diff, начни с него. Затем search_text или search_code по изменённым символам и читай только нужные файлы через read_file. Для каждого замечания укажи путь, причину и сценарий отказа. Не вызывай propose_patch и не утверждай, что проблема уже исправлена.",
			RequiredTools:   []string{"read_file", "search_text"},
			PermissionDelta: map[string]domain.ToolPolicy{"read_file": domain.ToolPolicyAllow}, CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: "skill-test-runner", Name: "Test Runner", Description: "Run project tests as verification",
			Instructions:    "Найди, как в проекте запускают тесты (go test, npm test, Makefile). Запусти узкий релевантный набор через run_command. Интерпретируй exit code и вывод; не скрывай падения и пропущенные проверки.",
			RequiredTools:   []string{"run_command"},
			PermissionDelta: map[string]domain.ToolPolicy{"run_command": domain.ToolPolicyAsk}, CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: "skill-refactor", Name: "Safe Refactor", Description: "Small scoped refactors with verification",
			Instructions:    "Сначала search_code или read_file по затронутым символам. Меняй через propose_patch минимальным diff. После принятия проверь поведение узким тестом или сборкой через run_command, если он разрешён.",
			RequiredTools:   []string{"propose_patch", "read_file"},
			PermissionDelta: map[string]domain.ToolPolicy{"propose_patch": domain.ToolPolicyAsk}, CreatedAt: now, UpdatedAt: now,
		},
	}
	for _, skill := range defaults {
		if err := a.store.SaveSkill(ctx, skill); err != nil {
			return err
		}
	}
	return nil
}

type HubBootstrap struct {
	Blueprints               []domain.AgentBlueprint          `json:"blueprints"`
	ProjectAgents            []domain.ProjectAgent            `json:"projectAgents"`
	Skills                   []domain.SkillDefinition         `json:"skills,omitempty"`
	ProjectSkills            []domain.ProjectSkillInstance    `json:"projectSkills,omitempty"`
	Teams                    []domain.Team                    `json:"teams,omitempty"`
	Quests                   []domain.Quest                   `json:"quests,omitempty"`
	Flows                    []domain.FlowGraph               `json:"flows,omitempty"`
	FlowRuns                 []domain.FlowRun                 `json:"flowRuns,omitempty"`
	Executions               []domain.ExecutionInstance       `json:"executions,omitempty"`
	ChangeSets               []domain.ChangeSet               `json:"changeSets,omitempty"`
	Memories                 []domain.MemoryRecord            `json:"memories,omitempty"`
	Connections              []domain.Connection              `json:"connections,omitempty"`
	ModelRouting             WorkspaceModelRoutingView        `json:"modelRouting"`
	ServerProfiles           []servers.Profile                `json:"serverProfiles,omitempty"`
	DBConnections            []domain.DBConnection            `json:"dbConnections,omitempty"`
	UsageRecords             []domain.UsageRecord             `json:"usageRecords,omitempty"`
	BudgetReservations       []domain.BudgetReservation       `json:"budgetReservations,omitempty"`
	ModelPricingProfiles     []domain.ModelPricingProfile     `json:"modelPricingProfiles,omitempty"`
	LearningSignals          []domain.LearningSignal          `json:"learningSignals,omitempty"`
	SkillOutcomes            []domain.SkillOutcome            `json:"skillOutcomes,omitempty"`
	SkillCuration            []domain.SkillCurationSuggestion `json:"skillCuration,omitempty"`
	Companion                *domain.CompanionConfig          `json:"companion,omitempty"`
	Orchestrator             *domain.OrchestratorConfig       `json:"orchestrator,omitempty"`
	CompanionMessages        []domain.CompanionMessage        `json:"companionMessages"`
	CompanionInterventions   []domain.CompanionIntervention   `json:"companionInterventions,omitempty"`
	CompanionDismissedCount  int                              `json:"companionDismissedCount,omitempty"`
	CompanionActionProposals []domain.CompanionActionProposal `json:"companionActionProposals,omitempty"`
	IDEObservations          []domain.IDEObservation          `json:"ideObservations,omitempty"`
	QuestProposals           []domain.QuestProposal           `json:"questProposals,omitempty"`
	ModelCatalog             []connections.ModelMeta          `json:"modelCatalog,omitempty"`
	ModelCandidates          []domain.ModelCandidate          `json:"modelCandidates,omitempty"`
	ModelEvidence            []domain.ModelCapabilityEvidence `json:"modelEvidence,omitempty"`
	AgentPrepChains          []domain.AgentPrepChain          `json:"agentPrepChains,omitempty"`
	TeamEvents               []domain.TeamEvent               `json:"teamEvents,omitempty"`
	Sandbox                  sandbox.Capabilities             `json:"sandbox"`
}
