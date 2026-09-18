package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/url"
	"os"
	"reflect"
	"slices"
	"strings"
	"time"

	"local-agent-workbench/internal/changesets"
	"local-agent-workbench/internal/companion"
	"local-agent-workbench/internal/connections"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/egress"
	"local-agent-workbench/internal/flowruntime"
	"local-agent-workbench/internal/observability"
	"local-agent-workbench/internal/policy"
	"local-agent-workbench/internal/sandbox"
	"local-agent-workbench/internal/security"
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
	hub.ProjectAgents = permanentProjectAgents(agents)
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

// agentName приводит имя персонажа к тому, чем оно обязано быть: одной непустой
// строкой. Живая проба показала, что ядро принимало и «», и «   », и имя с
// переводом строки внутри — в ростере это карточки, которые нельзя ни отличить
// друг от друга, ни назвать в отряде. Интерфейс пустое имя не пропускает, но он
// не единственный клиент, а гарантия нужна одна на всех.
func agentName(raw string) (string, error) {
	name := strings.TrimSpace(strings.Join(strings.Fields(raw), " "))
	if name == "" {
		return "", errors.New("укажите имя агента")
	}
	return name, nil
}

// checkProviderURL — одно правило адреса модели на компаньона, мастера и агента.
// Раньше их было два: компаньон и мастер требовали абсолютный http/https без
// учётных данных внутри, а агент не требовал ничего — и принимал
// «http://admin:s3cret@example.com/v1». Пароль в адресе оседает в конфиге, в
// хронике и в тексте ошибок, а исполняет запросы к модели как раз агент.
func checkProviderURL(subject, raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fmt.Errorf("%s base URL must be an absolute HTTP or HTTPS URL", subject)
	}
	if parsed.User != nil {
		return fmt.Errorf("credentials in the %s provider URL are not allowed", subject)
	}
	return nil
}

// checkAgentLimits предъявляет агенту границы, которые конструктор агента и так
// объявляет полями формы (min/max), но ядро до сих пор не проверяло: живая проба
// сохраняла температуру 99, температуру −5 и −10 ходов. Ноль в лимитах остаётся
// разрешённым: это «не задано», и agent_capability так и говорит человеку.
func checkAgentLimits(provider domain.ProviderKind, baseURL string, approvalMode domain.ApprovalMode, temperature float64, maxOutputTokens, maxSteps, maxDurationSeconds int) error {
	if provider != "" && domain.IsAgentCLIProvider(provider) {
		return fmt.Errorf("agent CLI provider %q is removed; use an HTTP API provider", provider)
	}
	if provider != "" && !domain.IsHTTPAPIProvider(provider) {
		return fmt.Errorf("agent provider %q is not supported", provider)
	}
	if baseURL != "" {
		if err := checkProviderURL("agent", baseURL); err != nil {
			return err
		}
	}
	// Неизвестный режим подтверждений тих и потому опасен: он не равен
	// ApprovalAlways, значит движок молча считает его «спрашивать только про
	// опасное» — из непонятного значения выходит послабление. Legacy-профили это
	// проверяли давно, проектные агенты — нет.
	switch approvalMode {
	case "", domain.ApprovalSafe, domain.ApprovalAlways:
	default:
		return fmt.Errorf("agent approval mode %q is not supported", approvalMode)
	}
	if temperature < 0 || temperature > 2 {
		return errors.New("agent temperature must be between 0 and 2")
	}
	if maxOutputTokens < 0 || maxOutputTokens > 131072 {
		return errors.New("agent max output tokens must be between 0 and 131072")
	}
	if maxSteps < 0 || maxSteps > 100 {
		return errors.New("agent max steps must be between 0 and 100")
	}
	if maxDurationSeconds < 0 {
		return errors.New("agent max duration must not be negative")
	}
	return nil
}

func (a *App) SaveBlueprint(blueprint domain.AgentBlueprint) (domain.AgentBlueprint, error) {
	now := time.Now().UTC()
	name, err := agentName(blueprint.Name)
	if err != nil {
		return domain.AgentBlueprint{}, err
	}
	blueprint.Name = name
	blueprintProfile := domain.ProfileFromBlueprint(blueprint)
	normalizeRuntimeProfileDefaults(&blueprintProfile)
	blueprint.ProviderPreset = blueprintProfile.ProviderPreset
	blueprint.BaseURL = blueprintProfile.BaseURL
	blueprint.MaxOutputTokens = blueprintProfile.MaxOutputTokens
	blueprint.ContextWindowTokens = blueprintProfile.ContextWindowTokens
	blueprint.MaxSteps = blueprintProfile.MaxSteps
	blueprint.MaxDurationSeconds = blueprintProfile.MaxDurationSeconds
	blueprint.ApprovalMode = blueprintProfile.ApprovalMode
	if strings.TrimSpace(blueprint.ConnectionID) != "" {
		connection, resolveErr := a.ResolveConnection(ConnectionRef{ConnectionID: blueprint.ConnectionID, ProviderPreset: blueprint.ProviderPreset, Provider: blueprint.Provider, Label: "blueprint «" + blueprint.Name + "»"})
		if resolveErr != nil {
			return domain.AgentBlueprint{}, resolveErr
		}
		blueprint.Provider, blueprint.ProviderPreset, blueprint.BaseURL = connection.Provider, connection.PresetID, connection.BaseURL
		if strings.TrimSpace(blueprint.PrimaryModel) == "" && strings.TrimSpace(connection.DefaultModel) != "" {
			blueprint.PrimaryModel = connection.DefaultModel
		}
	}
	if blueprint.PrimaryModel == "" {
		blueprint.PrimaryModel = "auto"
	}
	blueprint.BaseURL = strings.TrimRight(strings.TrimSpace(blueprint.BaseURL), "/")
	if err := checkAgentLimits(blueprint.Provider, blueprint.BaseURL, blueprint.ApprovalMode, blueprint.Temperature,
		blueprint.MaxOutputTokens, blueprint.MaxSteps, blueprint.MaxDurationSeconds); err != nil {
		return domain.AgentBlueprint{}, err
	}
	if _, err := egress.CompileToolPolicies(blueprint.ToolPolicies); err != nil {
		return domain.AgentBlueprint{}, fmt.Errorf("invalid controlled egress policy: %w", err)
	}
	if err := a.validateActorDefinition(context.Background(), domain.ProfileFromBlueprint(blueprint), blueprint.SkillIDs); err != nil {
		return domain.AgentBlueprint{}, err
	}
	if blueprint.ID == "" {
		blueprint.ID = domain.NewID("blueprint")
		blueprint.CreatedAt = now
	}
	if blueprint.CreatedAt.IsZero() {
		blueprint.CreatedAt = now
	}
	blueprint.UpdatedAt = now
	if err := a.store.SaveBlueprint(context.Background(), blueprint); err != nil {
		return domain.AgentBlueprint{}, err
	}
	return blueprint, nil
}

func (a *App) SaveProjectAgent(agent domain.ProjectAgent) (domain.ProjectAgent, error) {
	now := time.Now().UTC()
	existingSkillIDs := []string{}
	ws, err := a.requireWorkspace()
	if err != nil {
		return domain.ProjectAgent{}, err
	}
	name, err := agentName(agent.Name)
	if err != nil {
		return domain.ProjectAgent{}, err
	}
	agent.Name = name
	agentProfile := domain.ProfileFromProjectAgent(agent)
	normalizeRuntimeProfileDefaults(&agentProfile)
	agent.ProviderPreset = agentProfile.ProviderPreset
	agent.BaseURL = agentProfile.BaseURL
	agent.MaxOutputTokens = agentProfile.MaxOutputTokens
	agent.ContextWindowTokens = agentProfile.ContextWindowTokens
	agent.MaxSteps = agentProfile.MaxSteps
	agent.MaxDurationSeconds = agentProfile.MaxDurationSeconds
	agent.ApprovalMode = agentProfile.ApprovalMode
	if strings.TrimSpace(agent.ConnectionID) != "" {
		connection, resolveErr := a.ResolveConnection(ConnectionRef{ConnectionID: agent.ConnectionID, ProviderPreset: agent.ProviderPreset, Provider: agent.Provider, Label: "агента «" + agent.Name + "»"})
		if resolveErr != nil {
			return domain.ProjectAgent{}, resolveErr
		}
		agent.Provider, agent.ProviderPreset, agent.BaseURL = connection.Provider, connection.PresetID, connection.BaseURL
		if strings.TrimSpace(agent.PrimaryModel) == "" && strings.TrimSpace(connection.DefaultModel) != "" {
			agent.PrimaryModel = connection.DefaultModel
		}
	}
	agent.BaseURL = strings.TrimRight(strings.TrimSpace(agent.BaseURL), "/")
	if err := checkAgentLimits(agent.Provider, agent.BaseURL, agent.ApprovalMode, agent.Temperature,
		agent.MaxOutputTokens, agent.MaxSteps, agent.MaxDurationSeconds); err != nil {
		return domain.ProjectAgent{}, err
	}
	if _, err := egress.CompileToolPolicies(agent.ToolPolicies); err != nil {
		return domain.ProjectAgent{}, fmt.Errorf("invalid controlled egress policy: %w", err)
	}
	if agent.WorkspaceID == "" {
		agent.WorkspaceID = ws.ID
	}
	if agent.WorkspaceID != ws.ID {
		return domain.ProjectAgent{}, errors.New("project agent belongs to another workspace")
	}
	if agent.ID == "" {
		agent.ID = domain.NewID("projectagent")
		agent.CreatedAt = now
	} else if existing, getErr := a.store.GetProjectAgent(context.Background(), agent.ID); getErr == nil {
		if existing.WorkspaceID != ws.ID {
			return domain.ProjectAgent{}, errors.New("project agent belongs to another workspace")
		}
		// Progress counters are server-owned; constructor edits must not wipe them.
		agent.Experience = existing.Experience
		agent.Level = existing.Level
		agent.TasksCompleted = existing.TasksCompleted
		agent.SuccessCount = existing.SuccessCount
		agent.CreatedAt = existing.CreatedAt
		existingSkillIDs = append(existingSkillIDs, existing.SkillIDs...)
	}
	if err = a.rejectNewDeprecatedSkills(context.Background(), agent.SkillIDs, existingSkillIDs); err != nil {
		return domain.ProjectAgent{}, err
	}
	if err = a.validateActorDefinition(context.Background(), domain.ProfileFromProjectAgent(agent), agent.SkillIDs); err != nil {
		return domain.ProjectAgent{}, err
	}
	if agent.CreatedAt.IsZero() {
		agent.CreatedAt = now
	}
	agent.UpdatedAt = now
	if agent.Level == 0 {
		agent.Level = 1
	}
	if err := a.store.SaveProjectAgent(context.Background(), agent); err != nil {
		return domain.ProjectAgent{}, err
	}
	if !agent.Temporary {
		_ = a.reconcileUserAgentPrepChains(context.Background(), agent)
	}
	return agent, nil
}

func (a *App) rejectNewDeprecatedSkills(ctx context.Context, requested, alreadyAssigned []string) error {
	skills, err := a.store.ListSkills(ctx)
	if err != nil {
		return err
	}
	deprecated := map[string]string{}
	for _, skill := range skills {
		if curatorDeprecated(skill) {
			deprecated[skill.ID] = skill.Name
		}
	}
	for _, id := range requested {
		if name, blocked := deprecated[id]; blocked && !slices.Contains(alreadyAssigned, id) {
			return fmt.Errorf("skill %q is deprecated and cannot be newly assigned", name)
		}
	}
	return nil
}

// DeleteBlueprint убирает класс из списка найма.
//
// Конструктор, сохраняя персонажа «с нуля», заводит и класс под него. Пока
// распустить персонажа было нечем, это не бросалось в глаза; теперь три круга
// «создал — распустил» оставляют в списке найма три класса от несуществующих
// персонажей, и убрать их нечем. Класс, которым кто-то пользуется, не удаляем и
// говорим, кто именно держит.
func (a *App) DeleteBlueprint(blueprintID string) error {
	ctx := context.Background()
	if _, err := a.store.GetBlueprint(ctx, blueprintID); err != nil {
		return err
	}
	// Чертёж глобален, а персонажи живут в проектах. Проверять занятость только
	// в открытом проекте — значит разрешить удалить класс из-под персонажа
	// соседнего проекта; заодно удаление перестаёт требовать открытого проекта,
	// как и было у профилей.
	agents, err := a.store.ListAllProjectAgents(ctx)
	if err != nil {
		return err
	}
	for _, agent := range agents {
		if agent.BlueprintID == blueprintID {
			return fmt.Errorf("класс занят персонажем %q — распустите его или смените класс", agent.Name)
		}
	}
	return a.store.DeleteBlueprint(ctx, blueprintID)
}

// DeleteProjectAgent распускает персонажа из ростера.
//
// Раньше кнопка «Распустить» слала deleteProfile: маршрут удаления был только у
// legacy-профилей, а у проектных агентов — никакого. Ядро отвечало 204, панель
// обновлялась, и персонаж оставался на месте. Отказ, выданный за успех, хуже
// отсутствия кнопки.
//
// Отказываем там, где роспуск оставил бы после себя ссылку в пустоту, и всегда
// называем, что именно держит — как это делает DeleteProfile со сценариями.
func (a *App) DeleteProjectAgent(projectAgentID string) error {
	agent, err := a.currentProjectAgent(projectAgentID)
	if err != nil {
		return err
	}
	ctx := context.Background()
	executions, err := a.store.ListExecutions(ctx, agent.WorkspaceID, 0)
	if err != nil {
		return err
	}
	for _, execution := range executions {
		if execution.ProjectAgentID != agent.ID {
			continue
		}
		switch execution.Status {
		case domain.RunPending, domain.RunRunning, domain.RunPaused, domain.RunWaiting:
			return errors.New("персонаж занят в запуске — остановите запуск и повторите")
		}
	}
	teams, err := a.store.ListTeams(ctx, agent.WorkspaceID)
	if err != nil {
		return err
	}
	for _, team := range teams {
		for _, member := range team.AgentIDs {
			if member == agent.ID {
				// Отказ обязан называть не только причину, но и место, где её снимают:
				// состав отряда править негде, а распустить отряд можно в «Отрядах».
				return fmt.Errorf("персонаж состоит в отряде %q — распустите отряд в разделе «Отряды»", team.Name)
			}
		}
	}
	// Узел Flow держит агента по идентификатору, и на запуске узел просто не
	// найдёт карточку: наружу выйдет сырая ошибка хранилища посреди работы, без
	// связи с давним роспуском. Отказ на месте роспуска называет схему.
	flows, err := a.store.ListFlows(ctx, agent.WorkspaceID)
	if err != nil {
		return err
	}
	for _, flow := range flows {
		for _, node := range flow.Nodes {
			if node.AgentID == agent.ID {
				return fmt.Errorf("персонаж стоит в узле %q схемы %q — замените его в схеме", node.Name, flow.Name)
			}
		}
	}
	return a.store.DeleteProjectAgent(ctx, agent.ID)
}

func (a *App) ApplyBlueprintToProjectAgent(projectAgentID string) (domain.ProjectAgent, error) {
	agent, err := a.currentProjectAgent(projectAgentID)
	if err != nil {
		return domain.ProjectAgent{}, err
	}
	blueprint, err := a.store.GetBlueprint(context.Background(), agent.BlueprintID)
	if err != nil {
		return domain.ProjectAgent{}, err
	}
	updated := domain.ProjectAgentFromBlueprint(agent.WorkspaceID, blueprint)
	updated.ID = agent.ID
	updated.Experience = agent.Experience
	updated.Level = agent.Level
	updated.TasksCompleted = agent.TasksCompleted
	updated.SuccessCount = agent.SuccessCount
	updated.ProjectRules = append([]string(nil), agent.ProjectRules...)
	updated.CreatedAt = agent.CreatedAt
	return a.SaveProjectAgent(updated)
}

func (a *App) UpdateBlueprintFromProjectAgent(projectAgentID string) (domain.AgentBlueprint, error) {
	agent, err := a.currentProjectAgent(projectAgentID)
	if err != nil {
		return domain.AgentBlueprint{}, err
	}
	blueprint, err := a.store.GetBlueprint(context.Background(), agent.BlueprintID)
	if err != nil {
		return domain.AgentBlueprint{}, err
	}
	blueprint.Name = agent.Name
	blueprint.RoleDescription = agent.RoleDescription
	blueprint.Personality = agent.Personality
	blueprint.Mission = agent.Mission
	blueprint.SystemPrompt = agent.SystemPrompt
	blueprint.Goals = append([]string(nil), agent.Goals...)
	blueprint.Rules = append([]string(nil), agent.Rules...)
	blueprint.Constraints = append([]string(nil), agent.Constraints...)
	blueprint.SkillIDs = append([]string(nil), agent.SkillIDs...)
	blueprint.AllowedTools = append([]string(nil), agent.AllowedTools...)
	blueprint.ToolPolicies = cloneMap(agent.ToolPolicies)
	blueprint.ConnectionID = agent.ConnectionID
	blueprint.Provider = agent.Provider
	blueprint.ProviderPreset = agent.ProviderPreset
	blueprint.BaseURL = agent.BaseURL
	blueprint.PrimaryModel = agent.PrimaryModel
	blueprint.FallbackModels = append([]string(nil), agent.FallbackModels...)
	blueprint.Temperature = agent.Temperature
	blueprint.MaxOutputTokens = agent.MaxOutputTokens
	blueprint.ContextWindowTokens = agent.ContextWindowTokens
	blueprint.ReasoningEffort = agent.ReasoningEffort
	blueprint.MaxSteps = agent.MaxSteps
	blueprint.MaxDurationSeconds = agent.MaxDurationSeconds
	blueprint.ApprovalMode = agent.ApprovalMode
	return a.SaveBlueprint(blueprint)
}

type AgentBlueprintDiffField struct {
	Key            string `json:"key"`
	Label          string `json:"label"`
	AgentValue     any    `json:"agentValue"`
	BlueprintValue any    `json:"blueprintValue"`
}

type AgentBlueprintDiff struct {
	ProjectAgentID string                    `json:"projectAgentId"`
	BlueprintID    string                    `json:"blueprintId"`
	AgentName      string                    `json:"agentName"`
	BlueprintName  string                    `json:"blueprintName"`
	Fields         []AgentBlueprintDiffField `json:"fields"`
	ProjectOnly    map[string]any            `json:"projectOnly,omitempty"`
	HasChanges     bool                      `json:"hasChanges"`
}

func (a *App) DiffProjectAgentBlueprint(projectAgentID string) (AgentBlueprintDiff, error) {
	agent, err := a.currentProjectAgent(projectAgentID)
	if err != nil {
		return AgentBlueprintDiff{}, err
	}
	blueprint, err := a.store.GetBlueprint(context.Background(), agent.BlueprintID)
	if err != nil {
		return AgentBlueprintDiff{}, err
	}
	diff := AgentBlueprintDiff{
		ProjectAgentID: agent.ID, BlueprintID: blueprint.ID, AgentName: agent.Name, BlueprintName: blueprint.Name,
		Fields: []AgentBlueprintDiffField{}, ProjectOnly: map[string]any{"projectRules": append([]string(nil), agent.ProjectRules...)},
	}
	add := func(key, label string, agentValue, blueprintValue any) {
		if !reflect.DeepEqual(agentValue, blueprintValue) {
			diff.Fields = append(diff.Fields, AgentBlueprintDiffField{Key: key, Label: label, AgentValue: agentValue, BlueprintValue: blueprintValue})
		}
	}
	add("name", "Имя", agent.Name, blueprint.Name)
	add("roleDescription", "Роль", agent.RoleDescription, blueprint.RoleDescription)
	add("personality", "Personality", agent.Personality, blueprint.Personality)
	add("mission", "Миссия", agent.Mission, blueprint.Mission)
	add("systemPrompt", "Инструкции", agent.SystemPrompt, blueprint.SystemPrompt)
	add("goals", "Цели", agent.Goals, blueprint.Goals)
	add("rules", "Правила", agent.Rules, blueprint.Rules)
	add("constraints", "Ограничения", agent.Constraints, blueprint.Constraints)
	add("skillIds", "Skills", agent.SkillIDs, blueprint.SkillIDs)
	add("allowedTools", "Tools", agent.AllowedTools, blueprint.AllowedTools)
	add("toolPolicies", "Tool policy", agent.ToolPolicies, blueprint.ToolPolicies)
	add("connectionId", "Подключение", agent.ConnectionID, blueprint.ConnectionID)
	add("provider", "Provider", agent.Provider, blueprint.Provider)
	add("providerPreset", "Provider preset", agent.ProviderPreset, blueprint.ProviderPreset)
	add("baseUrl", "Base URL", agent.BaseURL, blueprint.BaseURL)
	add("primaryModel", "Primary model", agent.PrimaryModel, blueprint.PrimaryModel)
	add("fallbackModels", "Fallback models", agent.FallbackModels, blueprint.FallbackModels)
	add("temperature", "Температура", agent.Temperature, blueprint.Temperature)
	add("maxOutputTokens", "Макс. output tokens", agent.MaxOutputTokens, blueprint.MaxOutputTokens)
	add("contextWindowTokens", "Окно контекста", agent.ContextWindowTokens, blueprint.ContextWindowTokens)
	add("reasoningEffort", "Reasoning", agent.ReasoningEffort, blueprint.ReasoningEffort)
	add("maxSteps", "Макс. шагов", agent.MaxSteps, blueprint.MaxSteps)
	add("maxDurationSeconds", "Тайм-аут", agent.MaxDurationSeconds, blueprint.MaxDurationSeconds)
	add("approvalMode", "Подтверждения", agent.ApprovalMode, blueprint.ApprovalMode)
	diff.HasChanges = len(diff.Fields) > 0
	return diff, nil
}

func (a *App) currentProjectAgent(projectAgentID string) (domain.ProjectAgent, error) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return domain.ProjectAgent{}, err
	}
	agent, err := a.store.GetProjectAgent(context.Background(), projectAgentID)
	if err != nil {
		return domain.ProjectAgent{}, err
	}
	if agent.WorkspaceID != ws.ID {
		return domain.ProjectAgent{}, errors.New("project agent belongs to another workspace")
	}
	return agent, nil
}

func (a *App) SaveSkill(skill domain.SkillDefinition) (domain.SkillDefinition, error) {
	skill.Name = strings.TrimSpace(skill.Name)
	skill.Description = strings.TrimSpace(skill.Description)
	skill.Instructions = strings.TrimSpace(skill.Instructions)
	if skill.Name == "" || len([]rune(skill.Name)) > 120 {
		return domain.SkillDefinition{}, errors.New("skill name is required and must not exceed 120 characters")
	}
	if len([]rune(skill.Description)) > 4096 {
		return domain.SkillDefinition{}, errors.New("skill description exceeds 4096 characters")
	}
	if skill.Instructions == "" || len([]rune(skill.Instructions)) > 32768 {
		return domain.SkillDefinition{}, errors.New("skill instructions are required and must not exceed 32768 characters")
	}
	var err error
	if skill.References, err = normalizeSkillList("references", skill.References, 32, 4096); err != nil {
		return domain.SkillDefinition{}, err
	}
	if skill.Scripts, err = normalizeSkillList("scripts", skill.Scripts, 32, 4096); err != nil {
		return domain.SkillDefinition{}, err
	}
	if skill.RequiredTools, err = normalizeSkillList("required tools", skill.RequiredTools, 32, 200); err != nil {
		return domain.SkillDefinition{}, err
	}
	if err = a.validateSkillRequiredTools(skill.RequiredTools); err != nil {
		return domain.SkillDefinition{}, err
	}
	if skill.PermissionDelta, err = normalizeSkillPermissionDelta(skill.PermissionDelta); err != nil {
		return domain.SkillDefinition{}, err
	}
	permissionKeys := make([]string, 0, len(skill.PermissionDelta))
	for key := range skill.PermissionDelta {
		if key != "network" && !strings.HasPrefix(strings.ToLower(key), "network:") {
			permissionKeys = append(permissionKeys, key)
		}
	}
	if err = a.validateSkillRequiredTools(permissionKeys); err != nil {
		return domain.SkillDefinition{}, err
	}
	if skill.Configuration == nil {
		skill.Configuration = map[string]any{}
	}
	if encoded, encodeErr := json.Marshal(skill.Configuration); encodeErr != nil || len(encoded) > 64*1024 {
		return domain.SkillDefinition{}, errors.New("skill configuration must be valid JSON not exceeding 64 KiB")
	}
	existing, err := a.store.ListSkills(context.Background())
	if err != nil {
		return domain.SkillDefinition{}, err
	}
	var previous *domain.SkillDefinition
	for index := range existing {
		item := existing[index]
		if skill.ID != "" && item.ID == skill.ID {
			previous = &item
			continue
		}
		if strings.EqualFold(strings.TrimSpace(item.Name), skill.Name) {
			return domain.SkillDefinition{}, fmt.Errorf("skill named %q already exists", skill.Name)
		}
	}
	now := time.Now().UTC()
	if skill.ID == "" {
		skill.ID = domain.NewID("skill")
		skill.CreatedAt = now
	} else if previous != nil {
		skill.CreatedAt = previous.CreatedAt
	}
	if skill.CreatedAt.IsZero() {
		skill.CreatedAt = now
	}
	skill.UpdatedAt = now
	if err := a.store.SaveSkill(context.Background(), skill); err != nil {
		return domain.SkillDefinition{}, err
	}
	return skill, nil
}

func (a *App) validateSkillRequiredTools(tools []string) error {
	known := map[string]bool{}
	for _, item := range domain.BuiltInToolCatalog() {
		known[item.Name] = true
	}
	customTools, err := a.store.ListCustomTools(context.Background())
	if err != nil {
		return err
	}
	for _, tool := range customTools {
		known[tool.ID] = true
	}
	for _, tool := range tools {
		if !known[tool] {
			return fmt.Errorf("skill references unknown tool %q", tool)
		}
	}
	return nil
}

func normalizeSkillPermissionDelta(values map[string]domain.ToolPolicy) (map[string]domain.ToolPolicy, error) {
	if len(values) == 0 {
		return map[string]domain.ToolPolicy{}, nil
	}
	if len(values) > 32 {
		return nil, errors.New("skill permission delta exceeds 32 entries")
	}
	result := make(map[string]domain.ToolPolicy, len(values))
	for key, value := range values {
		key = strings.TrimSpace(key)
		value = domain.ToolPolicy(strings.ToUpper(strings.TrimSpace(string(value))))
		if key == "" || len([]rune(key)) > 200 || (value != domain.ToolPolicyAllow && value != domain.ToolPolicyAsk && value != domain.ToolPolicyDeny) {
			return nil, errors.New("skill permission delta contains an invalid entry")
		}
		result[key] = value
	}
	return result, nil
}

func normalizeSkillList(label string, values []string, maxItems, maxRunes int) ([]string, error) {
	if len(values) > maxItems {
		return nil, fmt.Errorf("skill %s exceed %d entries", label, maxItems)
	}
	result := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if len([]rune(value)) > maxRunes {
			return nil, fmt.Errorf("skill %s entry exceeds %d characters", label, maxRunes)
		}
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result, nil
}

func (a *App) EquipSkill(workspaceID, skillID string, configuration map[string]any) (domain.ProjectSkillInstance, error) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return domain.ProjectSkillInstance{}, err
	}
	if workspaceID != "" && workspaceID != ws.ID {
		return domain.ProjectSkillInstance{}, errors.New("project skill belongs to another workspace")
	}
	workspaceID = ws.ID
	skillID = strings.TrimSpace(skillID)
	if skillID == "" {
		return domain.ProjectSkillInstance{}, errors.New("skill id is required")
	}
	skills, err := a.store.ListSkills(context.Background())
	if err != nil {
		return domain.ProjectSkillInstance{}, err
	}
	found := false
	for _, skill := range skills {
		if skill.ID == skillID {
			if curatorDeprecated(skill) {
				return domain.ProjectSkillInstance{}, fmt.Errorf("skill %q is deprecated and cannot be equipped", skill.Name)
			}
			found = true
			break
		}
	}
	if !found {
		return domain.ProjectSkillInstance{}, fmt.Errorf("skill %q not found", skillID)
	}
	existing, err := a.store.ListProjectSkills(context.Background(), workspaceID)
	if err != nil {
		return domain.ProjectSkillInstance{}, err
	}
	for _, instance := range existing {
		if instance.SkillID == skillID && instance.Enabled {
			return instance, nil
		}
	}
	now := time.Now().UTC()
	instance := domain.ProjectSkillInstance{
		ID: domain.NewID("projectskill"), WorkspaceID: workspaceID, SkillID: skillID,
		Configuration: configuration, Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	if err := a.store.SaveProjectSkill(context.Background(), instance); err != nil {
		return domain.ProjectSkillInstance{}, err
	}
	return instance, nil
}

type SkillEquipPreview struct {
	Skill           domain.SkillDefinition       `json:"skill"`
	PermissionDelta map[string]domain.ToolPolicy `json:"permissionDelta"`
	RequiredTools   []string                     `json:"requiredTools,omitempty"`
	AlreadyEquipped bool                         `json:"alreadyEquipped"`
	Instance        *domain.ProjectSkillInstance `json:"instance,omitempty"`
}

func (a *App) PreviewSkillEquip(workspaceID, skillID string) (SkillEquipPreview, error) {
	if workspaceID == "" {
		ws, err := a.requireWorkspace()
		if err != nil {
			return SkillEquipPreview{}, err
		}
		workspaceID = ws.ID
	}
	skills, err := a.store.ListSkills(context.Background())
	if err != nil {
		return SkillEquipPreview{}, err
	}
	var skill *domain.SkillDefinition
	for index := range skills {
		if skills[index].ID == skillID {
			skill = &skills[index]
			break
		}
	}
	if skill == nil {
		return SkillEquipPreview{}, fmt.Errorf("skill %s not found", skillID)
	}
	if curatorDeprecated(*skill) {
		return SkillEquipPreview{}, fmt.Errorf("skill %q is deprecated and cannot be equipped", skill.Name)
	}
	preview := SkillEquipPreview{
		Skill: *skill, PermissionDelta: skill.PermissionDelta, RequiredTools: append([]string(nil), skill.RequiredTools...),
	}
	equipped, err := a.store.ListProjectSkills(context.Background(), workspaceID)
	if err != nil {
		return SkillEquipPreview{}, err
	}
	for index := range equipped {
		if equipped[index].SkillID == skillID && equipped[index].Enabled {
			preview.AlreadyEquipped = true
			instance := equipped[index]
			preview.Instance = &instance
			break
		}
	}
	return preview, nil
}

func (a *App) SaveTeam(team domain.Team) (domain.Team, error) {
	now := time.Now().UTC()
	ws, err := a.requireWorkspace()
	if err != nil {
		return domain.Team{}, err
	}
	if team.WorkspaceID != "" && team.WorkspaceID != ws.ID {
		return domain.Team{}, errors.New("team belongs to another workspace")
	}
	team.WorkspaceID = ws.ID
	if strings.TrimSpace(team.Name) == "" || len([]rune(team.Name)) > 120 {
		return domain.Team{}, errors.New("team name is required and must not exceed 120 characters")
	}
	if len([]rune(team.Description)) > 4096 {
		return domain.Team{}, errors.New("team description exceeds 4096 characters")
	}
	if err = a.validateCompanionTeamDraft(team); err != nil {
		return domain.Team{}, err
	}
	if team.ID == "" {
		team.ID = domain.NewID("team")
		team.CreatedAt = now
	}
	if team.CreatedAt.IsZero() {
		team.CreatedAt = now
	}
	team.UpdatedAt = now
	if err := a.store.SaveTeam(context.Background(), team); err != nil {
		return domain.Team{}, err
	}
	return team, nil
}

// DeleteTeam распускает отряд проекта.
//
// Отряд под квест собирает сам оркестратор, и переживает он и завершение квеста,
// и его удаление. Убрать отряд было негде: роспуск персонажа отказывал словами
// «сначала уберите его оттуда», а «оттуда» не открывалось ни одной кнопкой —
// отказ звал сделать невозможное.
//
// Держит отряд только незакрытый квест: у завершённого, проваленного и
// отменённого отряд — уже история, и она остаётся в статистике по идентификатору
// даже после роспуска.
func (a *App) DeleteTeam(teamID string) error {
	teamID = strings.TrimSpace(teamID)
	if teamID == "" {
		return errors.New("не указан отряд")
	}
	ws, err := a.requireWorkspace()
	if err != nil {
		return err
	}
	ctx := context.Background()
	quests, err := a.store.ListQuests(ctx, ws.ID)
	if err != nil {
		return err
	}
	for _, quest := range quests {
		if quest.TeamID != teamID {
			continue
		}
		switch quest.Status {
		case domain.QuestCompleted, domain.QuestNeedsReview, domain.QuestBlocked, domain.QuestFailed, domain.QuestCancelled:
			continue
		}
		return fmt.Errorf("отряд занят квестом %q — закройте или удалите квест", quest.Title)
	}
	return a.store.DeleteTeam(ctx, ws.ID, teamID)
}

func (a *App) SaveQuest(quest domain.Quest) (domain.Quest, error) {
	now := time.Now().UTC()
	if quest.WorkspaceID == "" {
		ws, err := a.requireWorkspace()
		if err != nil {
			return domain.Quest{}, err
		}
		quest.WorkspaceID = ws.ID
	}
	if quest.ID == "" {
		quest.ID = domain.NewID("quest")
		quest.CreatedAt = now
	}
	if quest.CreatedAt.IsZero() {
		quest.CreatedAt = now
	}
	if quest.Status == "" {
		quest.Status = domain.QuestDraft
	}
	if quest.Importance == "" {
		quest.Importance = domain.QuestNormal
	}
	if err := a.guardTaskQuestUpdate(context.Background(), &quest); err != nil {
		return domain.Quest{}, err
	}
	quest.UpdatedAt = now
	if err := a.store.SaveQuest(context.Background(), quest); err != nil {
		return domain.Quest{}, err
	}
	return quest, nil
}

// DeleteQuest убирает квест из списка проекта.
//
// Кнопки удаления у квеста не было вовсе: черновик, поставленный по ошибке, и
// отменённый квест оставались в разделе навсегда. Список, из которого нельзя
// ничего убрать, со временем перестаёт показывать работу проекта.
//
// Удаляем только то, за чем никто не стоит, и всегда называем, что именно
// держит квест, — так же, как это делает роспуск персонажа. Хроника запусков
// остаётся: она доказательство сделанного, и интерфейс показывает её разделом
// «запуски без квеста».
func (a *App) DeleteQuest(questID string) error {
	questID = strings.TrimSpace(questID)
	if questID == "" {
		return errors.New("не указан квест")
	}
	ws, err := a.requireWorkspace()
	if err != nil {
		return err
	}
	ctx := context.Background()
	quests, err := a.store.ListQuests(ctx, ws.ID)
	if err != nil {
		return err
	}
	var target *domain.Quest
	for i := range quests {
		if quests[i].ID == questID {
			target = &quests[i]
			break
		}
	}
	if target == nil {
		return errors.New("квест не найден в открытом проекте")
	}
	for _, quest := range quests {
		if quest.ParentID == questID {
			return fmt.Errorf("у квеста есть подквест %q — сначала удалите его", quest.Title)
		}
	}
	executions, err := a.store.ListExecutions(ctx, ws.ID, 500)
	if err != nil {
		return err
	}
	for _, execution := range executions {
		if execution.QuestID != questID {
			continue
		}
		switch execution.Status {
		case domain.RunPending, domain.RunRunning, domain.RunPaused, domain.RunWaiting:
			return errors.New("по квесту идёт запуск — остановите его и повторите")
		}
	}
	flowRuns, err := a.store.ListFlowRuns(ctx, ws.ID, 500)
	if err != nil {
		return err
	}
	for _, run := range flowRuns {
		if run.QuestID != questID {
			continue
		}
		switch run.Status {
		case domain.RunPending, domain.RunRunning, domain.RunPaused, domain.RunWaiting:
			return errors.New("схема квеста ещё выполняется — остановите прогон и повторите")
		}
	}
	// Набор правок без своего квеста некому ни принять, ни откатить: карточка с
	// кнопками решения живёт у квеста. Удаление незакрытого набора оставило бы
	// правки в подвешенном состоянии, поэтому сначала решение, потом удаление.
	changeSets, err := a.store.ListChangeSets(ctx, ws.ID)
	if err != nil {
		return err
	}
	for _, set := range changeSets {
		if set.QuestID != questID {
			continue
		}
		switch set.Status {
		case domain.ChangeSetPending, domain.ChangeSetApproved, domain.ChangeSetConflict:
			return fmt.Errorf("набор правок %q ещё ждёт решения — примените или отклоните его", set.Title)
		}
	}
	return a.store.DeleteQuest(ctx, ws.ID, questID)
}

// DeleteFlow убирает схему проекта.
//
// Без этого действия любой отработавший квест держал своих исполнителей вечно:
// Мастер создаёт схему под квест, узлы схемы называют агента по идентификатору,
// а удаление агента отказом отправляет «заменить его в схеме». Заменять было
// негде — схему нельзя было ни удалить, ни закрыть, — и персонаж оставался в
// ростере навсегда вместе со схемой, которой больше никто не пользуется.
//
// Отказы здесь те же, что у правки графа: живой прогон и незакрытый квест. Оба
// названы вместе с местом, где их снимают.
func (a *App) DeleteFlow(flowID string) error {
	flowID = strings.TrimSpace(flowID)
	if flowID == "" {
		return errors.New("не указана схема")
	}
	ws, err := a.requireWorkspace()
	if err != nil {
		return err
	}
	ctx := context.Background()
	flow, err := a.store.GetFlow(ctx, flowID)
	if err != nil {
		return err
	}
	if flow.WorkspaceID != ws.ID {
		return errors.New("схема принадлежит другому проекту")
	}
	if err = flowHasActiveRun(a.store, flowID); err != nil {
		return err
	}
	quests, err := a.store.ListQuests(ctx, ws.ID)
	if err != nil {
		return err
	}
	for _, quest := range quests {
		if quest.FlowID != flowID {
			continue
		}
		switch quest.Status {
		case domain.QuestCompleted, domain.QuestNeedsReview, domain.QuestBlocked, domain.QuestFailed, domain.QuestCancelled:
			continue
		}
		return fmt.Errorf("схему ведёт квест %q — закройте или удалите квест", quest.Title)
	}
	return a.store.DeleteFlow(ctx, ws.ID, flowID)
}

func (a *App) SaveFlow(flow domain.FlowGraph) (domain.FlowGraph, error) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return domain.FlowGraph{}, err
	}
	if flow.WorkspaceID != "" && flow.WorkspaceID != ws.ID {
		return domain.FlowGraph{}, errors.New("flow workspace does not match the open project")
	}
	flow.WorkspaceID = ws.ID
	if flow.ID != "" {
		if err := flowHasActiveRun(a.store, flow.ID); err != nil {
			return domain.FlowGraph{}, err
		}
	}
	now := time.Now().UTC()
	if err := flowruntime.ValidateGraph(flow); err != nil {
		return domain.FlowGraph{}, err
	}
	agents, err := a.store.ListProjectAgents(context.Background(), flow.WorkspaceID)
	if err != nil {
		return domain.FlowGraph{}, err
	}
	agentByID := make(map[string]domain.ProjectAgent, len(agents))
	for _, agent := range agents {
		agentByID[agent.ID] = agent
	}
	for _, node := range flow.Nodes {
		if node.Kind != domain.FlowNodeAgent && node.Kind != domain.FlowNodeTool {
			continue
		}
		agent, ok := agentByID[node.AgentID]
		if !ok {
			return domain.FlowGraph{}, fmt.Errorf("flow node %q references unknown project agent %q", node.ID, node.AgentID)
		}
		if node.Kind == domain.FlowNodeTool && !policy.ProfileGrants(domain.ProfileFromProjectAgent(agent)).Allows(node.ToolName) {
			return domain.FlowGraph{}, fmt.Errorf("tool node %q uses tool %q not allowed by agent %q", node.ID, node.ToolName, agent.Name)
		}
		if node.Kind == domain.FlowNodeTool {
			decision := policy.Engine{TrustedCustomTool: a.trustedCustomTool}.Evaluate(domain.ProfileFromProjectAgent(agent), node.ToolName)
			if decision.Risk != domain.ToolRiskLow || decision.RequiresApproval || decision.Policy != domain.ToolPolicyAllow {
				return domain.FlowGraph{}, fmt.Errorf("tool node %q requires an explicitly allowed low-risk tool; %q needs an agent or approval workflow", node.ID, node.ToolName)
			}
		}
		if fallbackID := strings.TrimSpace(node.FailurePolicy.FallbackAgentID); fallbackID != "" {
			fallback, exists := agentByID[fallbackID]
			if !exists {
				return domain.FlowGraph{}, fmt.Errorf("flow node %q references unknown fallback agent %q", node.ID, fallbackID)
			}
			if fallback.ID == agent.ID {
				return domain.FlowGraph{}, fmt.Errorf("flow node %q fallback agent must differ from its primary agent", node.ID)
			}
		}
	}
	if flow.ID == "" {
		flow.ID = domain.NewID("flow")
		flow.CreatedAt = now
	}
	if flow.CreatedAt.IsZero() {
		flow.CreatedAt = now
	}
	flow.UpdatedAt = now
	if err := a.store.SaveFlow(context.Background(), flow); err != nil {
		return domain.FlowGraph{}, err
	}
	return flow, nil
}

func (a *App) StartFlowRun(flowID, questID string, input map[string]any) (domain.FlowRun, error) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return domain.FlowRun{}, err
	}
	flow, err := a.store.GetFlow(context.Background(), flowID)
	if err != nil {
		return domain.FlowRun{}, err
	}
	if flow.WorkspaceID != ws.ID {
		return domain.FlowRun{}, errors.New("flow belongs to another workspace")
	}
	if err = a.requireProjectAgentsReady(context.Background(), ws.ID, flowProjectAgentIDs(flow)); err != nil {
		return domain.FlowRun{}, err
	}
	runtime := flowruntime.Runtime{Store: a.store}
	run, err := runtime.Start(context.Background(), flowruntime.StartRequest{
		FlowID: flowID, WorkspaceID: ws.ID, QuestID: questID, Input: input,
	})
	if err != nil {
		return domain.FlowRun{}, err
	}
	quest := domain.Quest{ID: questID, WorkspaceID: ws.ID, Title: flow.Name, FlowID: flow.ID, Importance: domain.QuestNormal}
	if questID != "" {
		if quests, listErr := a.store.ListQuests(context.Background(), ws.ID); listErr == nil {
			for _, item := range quests {
				if item.ID == questID {
					quest = item
					break
				}
			}
		}
	}
	if _, err = a.ensureFlowNodeQuests(quest, flow, run); err != nil {
		return run, err
	}
	if err = a.scheduleFlowAgentExecutionsFromRun(run); err != nil {
		slog.Error("flow run schedule failed", "flow_run_id", run.ID, "flow_id", flowID, "error", err)
		return run, err
	}
	slog.Info("flow run started", "flow_run_id", run.ID, "flow_id", flowID, "quest_id", questID, "workspace_id", ws.ID)
	return a.store.GetFlowRun(context.Background(), run.ID)
}

func (a *App) TickFlowRun(flowRunID string) (domain.FlowRun, error) {
	runtime := flowruntime.Runtime{Store: a.store}
	return runtime.Tick(context.Background(), flowRunID)
}

func (a *App) CompileWorkflowToFlow(workflowID string) (domain.FlowGraph, error) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return domain.FlowGraph{}, err
	}
	workflows, err := a.store.ListWorkflows(context.Background())
	if err != nil {
		return domain.FlowGraph{}, err
	}
	for _, workflow := range workflows {
		if workflow.ID == workflowID {
			flow := flowruntime.CompileLinearWorkflow(ws.ID, workflow)
			return a.SaveFlow(flow)
		}
	}
	return domain.FlowGraph{}, fmt.Errorf("workflow %s not found", workflowID)
}

func (a *App) StartSandboxedExecution(projectAgentID, task, questID string) (domain.ExecutionInstance, error) {
	return a.startSandboxedExecution(projectAgentID, task, questID, "")
}

func (a *App) startSandboxedExecution(projectAgentID, task, questID, parentExecutionID string) (domain.ExecutionInstance, error) {
	return a.startSandboxedExecutionWithSeed(projectAgentID, task, questID, parentExecutionID, "")
}

func (a *App) startSandboxedExecutionWithSeed(projectAgentID, task, questID, parentExecutionID, rootSeedPath string) (domain.ExecutionInstance, error) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return domain.ExecutionInstance{}, err
	}
	agent, err := a.store.GetProjectAgent(context.Background(), projectAgentID)
	if err != nil {
		return domain.ExecutionInstance{}, err
	}
	now := time.Now().UTC()
	execID := domain.NewID("execution")
	snapshot, err := a.runtimeSnapshotForProjectAgent(ws.ID, agent, now)
	if err != nil {
		return domain.ExecutionInstance{}, err
	}
	createRequest := sandbox.CreateRequest{
		WorkspaceID: ws.ID, WorkspacePath: ws.Path, ExecutionID: execID,
		PreferWorktree: true, LiveWorkspace: sandbox.LiveFileMutationEnabled() && !a.questRequiresIsolatedWorkspace(context.Background(), ws.ID, questID),
	}
	if strings.TrimSpace(rootSeedPath) != "" {
		createRequest.SeedPath = rootSeedPath
		createRequest.PreferWorktree = false
	}
	if strings.TrimSpace(parentExecutionID) != "" {
		parent, parentErr := a.findExecution(parentExecutionID)
		if parentErr != nil {
			return domain.ExecutionInstance{}, fmt.Errorf("load parent execution: %w", parentErr)
		}
		if parent.WorkspaceID != ws.ID {
			return domain.ExecutionInstance{}, fmt.Errorf("parent execution belongs to another workspace")
		}
		if parent.Status != domain.RunCompleted {
			return domain.ExecutionInstance{}, fmt.Errorf("parent execution %s is not completed", parent.ID)
		}
		parentSandbox, sandboxErr := a.store.GetSandbox(context.Background(), parent.SandboxID)
		if sandboxErr != nil {
			return domain.ExecutionInstance{}, fmt.Errorf("load parent sandbox: %w", sandboxErr)
		}
		createRequest.SeedPath = parentSandbox.Path
		createRequest.ParentSandboxID = parentSandbox.ID
		createRequest.ParentExecutionID = parent.ID
		createRequest.BaselineChangeSetIDs = a.changeSetDependencyIDs(ws.ID, parent.ID)
	}
	sandboxRecord, err := a.sandboxBackend.Create(context.Background(), createRequest)
	if err != nil {
		return domain.ExecutionInstance{}, err
	}
	exec := domain.ExecutionInstance{
		ID: execID, WorkspaceID: ws.ID, ProjectAgentID: agent.ID, QuestID: questID,
		SandboxID: sandboxRecord.ID, Task: task, Status: domain.RunPending,
		Snapshot:  snapshot,
		StartedAt: now,
	}
	if err := a.store.SaveSandbox(context.Background(), sandboxRecord); err != nil {
		_ = a.sandboxBackend.Close(context.Background(), sandboxRecord, ws.Path)
		return domain.ExecutionInstance{}, err
	}
	if err := a.store.SaveExecution(context.Background(), exec); err != nil {
		_ = a.sandboxBackend.Close(context.Background(), sandboxRecord, ws.Path)
		return domain.ExecutionInstance{}, err
	}
	slog.Info("hub execution created",
		"execution_id", exec.ID,
		"project_agent_id", agent.ID,
		"quest_id", questID,
		"sandbox_id", sandboxRecord.ID,
		"parent_execution_id", sandboxRecord.ParentExecutionID,
		"task_preview", observability.Snippet(security.Redact(task), 160),
	)
	return exec, nil
}

func (a *App) BuildChangeSet(executionID string) (domain.ChangeSet, error) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return domain.ChangeSet{}, err
	}
	executions, err := a.store.ListExecutions(context.Background(), ws.ID, 200)
	if err != nil {
		return domain.ChangeSet{}, err
	}
	var exec domain.ExecutionInstance
	found := false
	for _, item := range executions {
		if item.ID == executionID {
			exec = item
			found = true
			break
		}
	}
	if !found {
		return domain.ChangeSet{}, fmt.Errorf("execution %s not found", executionID)
	}
	sandboxRecord, err := a.store.GetSandbox(context.Background(), exec.SandboxID)
	if err != nil {
		return domain.ChangeSet{}, err
	}
	baselinePath, dependencies, err := a.changeSetLineage(ws.ID, sandboxRecord)
	if err != nil {
		return domain.ChangeSet{}, err
	}
	applier := changesets.Applier{Store: a.store}
	return applier.BuildFromSandbox(context.Background(), changesets.BuildRequest{
		WorkspaceID: ws.ID, ExecutionID: exec.ID, QuestID: exec.QuestID, Title: "Changes from " + exec.ID,
		WorkspacePath: ws.Path, BaselinePath: baselinePath, SandboxPath: sandboxRecord.Path, DependsOn: dependencies,
	})
}

func (a *App) changeSetLineage(workspaceID string, record domain.SandboxRecord) (string, []string, error) {
	baselinePath := strings.TrimSpace(record.BaselinePath)
	if baselinePath != "" {
		if info, err := os.Stat(baselinePath); err == nil && info.IsDir() {
			if len(record.BaselineChangeSetIDs) > 0 {
				return baselinePath, append([]string(nil), record.BaselineChangeSetIDs...), nil
			}
			if strings.TrimSpace(record.ParentExecutionID) == "" {
				return baselinePath, nil, nil
			}
			return baselinePath, a.changeSetDependencyIDs(workspaceID, record.ParentExecutionID), nil
		}
	}
	if strings.TrimSpace(record.ParentExecutionID) == "" {
		return "", nil, fmt.Errorf("sandbox baseline for execution %s is unavailable", record.ExecutionID)
	}
	// Baselines live in the same managed temp root as the execution sandbox.
	// If only that snapshot was cleaned up, the completed parent sandbox is an
	// equivalent immutable source. Never fall back to the live workspace: that
	// would silently turn an incremental stage into a cumulative Change Set.
	if strings.TrimSpace(record.ParentSandboxID) != "" {
		parent, err := a.store.GetSandbox(context.Background(), record.ParentSandboxID)
		if err == nil {
			if info, statErr := os.Stat(parent.Path); statErr == nil && info.IsDir() {
				return parent.Path, a.changeSetDependencyIDs(workspaceID, record.ParentExecutionID), nil
			}
		}
	}
	return "", nil, fmt.Errorf("sandbox lineage baseline for execution %s is unavailable", record.ExecutionID)
}

func (a *App) changeSetDependencyIDs(workspaceID, parentExecutionID string) []string {
	sets, err := a.store.ListChangeSets(context.Background(), workspaceID)
	if err != nil {
		return nil
	}
	latestByExecution := map[string]domain.ChangeSet{}
	for _, set := range sets {
		current, exists := latestByExecution[set.ExecutionID]
		if !exists || set.CreatedAt.After(current.CreatedAt) {
			latestByExecution[set.ExecutionID] = set
		}
	}
	visited := map[string]bool{}
	queue := []string{strings.TrimSpace(parentExecutionID)}
	for len(queue) > 0 {
		currentExecutionID := queue[0]
		queue = queue[1:]
		if currentExecutionID == "" || visited[currentExecutionID] {
			continue
		}
		visited[currentExecutionID] = true
		if set, ok := latestByExecution[currentExecutionID]; ok {
			return []string{set.ID}
		}
		record, getErr := a.store.GetSandboxByExecution(context.Background(), currentExecutionID)
		if getErr != nil {
			continue
		}
		if len(record.BaselineChangeSetIDs) > 0 {
			return append([]string(nil), record.BaselineChangeSetIDs...)
		}
		queue = append(queue, sandboxParentExecutionIDs(record)...)
	}
	return nil
}

func (a *App) ApplyChangeSet(changeSetID string) (changesets.ApplyResult, error) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return changesets.ApplyResult{}, err
	}
	set, err := a.store.GetChangeSet(context.Background(), changeSetID)
	if err != nil {
		return changesets.ApplyResult{}, err
	}
	if err = a.guardWorld(set.WorkspaceID); err != nil {
		return changesets.ApplyResult{}, err
	}
	for _, dependencyID := range set.DependsOn {
		dependency, getErr := a.store.GetChangeSet(context.Background(), dependencyID)
		if getErr != nil {
			return changesets.ApplyResult{}, fmt.Errorf("load change set dependency %s: %w", dependencyID, getErr)
		}
		if dependency.WorkspaceID != set.WorkspaceID {
			return changesets.ApplyResult{}, fmt.Errorf("change set dependency %s belongs to another workspace", dependencyID)
		}
		if dependency.Status != domain.ChangeSetApplied {
			return changesets.ApplyResult{}, fmt.Errorf("apply prerequisite change set %s first (status=%s)", dependencyID, dependency.Status)
		}
	}
	applier := changesets.Applier{Store: a.store}
	result, err := applier.Apply(context.Background(), ws.Path, changeSetID)
	if err != nil {
		return changesets.ApplyResult{}, err
	}
	if len(result.Applied) > 0 {
		a.InvalidateProjectIndex()
		_ = a.cache.DeletePrefix(context.Background(), a.cachePrefix())
		_, _ = a.SaveMemory(domain.MemoryRecord{
			WorkspaceID: ws.ID, Kind: domain.MemoryProject,
			Content: fmt.Sprintf("Applied change set %s (%d files): %s", result.ChangeSet.Title, len(result.Applied), strings.Join(result.Applied, ", ")),
			Source:  "changeset-apply", Confidence: 0.75, Pinned: false,
		})
	}
	return result, nil
}

func (a *App) RejectChangeSet(changeSetID string) (domain.ChangeSet, error) {
	set, err := a.store.GetChangeSet(context.Background(), changeSetID)
	if err != nil {
		return domain.ChangeSet{}, err
	}
	if err = a.guardWorld(set.WorkspaceID); err != nil {
		return domain.ChangeSet{}, err
	}
	if err = a.ensureNoChangeSetDependents(set, "reject"); err != nil {
		return domain.ChangeSet{}, err
	}
	applier := changesets.Applier{Store: a.store}
	return applier.Reject(context.Background(), changeSetID)
}

func (a *App) RevertChangeSet(changeSetID string) (changesets.ApplyResult, error) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return changesets.ApplyResult{}, err
	}
	set, err := a.store.GetChangeSet(context.Background(), changeSetID)
	if err != nil {
		return changesets.ApplyResult{}, err
	}
	if err = a.guardWorld(set.WorkspaceID); err != nil {
		return changesets.ApplyResult{}, err
	}
	if err = a.ensureNoChangeSetDependents(set, "revert"); err != nil {
		return changesets.ApplyResult{}, err
	}
	applier := changesets.Applier{Store: a.store}
	result, err := applier.Revert(context.Background(), ws.Path, changeSetID)
	if err != nil {
		return changesets.ApplyResult{}, err
	}
	if len(result.Applied) > 0 {
		a.InvalidateProjectIndex()
		_ = a.cache.DeletePrefix(context.Background(), a.cachePrefix())
	}
	return result, nil
}

func (a *App) ensureNoChangeSetDependents(set domain.ChangeSet, action string) error {
	sets, err := a.store.ListChangeSets(context.Background(), set.WorkspaceID)
	if err != nil {
		return err
	}
	for _, candidate := range sets {
		if candidate.ID == set.ID || candidate.Status == domain.ChangeSetRejected || candidate.Status == domain.ChangeSetReverted {
			continue
		}
		if slices.Contains(candidate.DependsOn, set.ID) {
			return fmt.Errorf("cannot %s change set %s while dependent change set %s is %s", action, set.ID, candidate.ID, candidate.Status)
		}
	}
	executions, err := a.store.ListExecutions(context.Background(), set.WorkspaceID, 500)
	if err != nil {
		return err
	}
	for _, execution := range executions {
		if execution.Status != domain.RunPending && execution.Status != domain.RunRunning && execution.Status != domain.RunInterrupted && execution.Status != domain.RunPaused && execution.Status != domain.RunWaiting {
			continue
		}
		record, getErr := a.store.GetSandboxByExecution(context.Background(), execution.ID)
		if getErr != nil {
			continue
		}
		dependsOnExecution := record.ParentExecutionID == set.ExecutionID || slices.Contains(record.ParentExecutionIDs, set.ExecutionID)
		dependsOnChangeSet := slices.Contains(record.BaselineChangeSetIDs, set.ID)
		if dependsOnExecution || dependsOnChangeSet {
			return fmt.Errorf("cannot %s change set %s while dependent execution %s is %s", action, set.ID, execution.ID, execution.Status)
		}
	}
	return nil
}

func (a *App) requireWorkspace() (domain.Workspace, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.currentWorkspace == nil {
		return domain.Workspace{}, errors.New("workspace is not open")
	}
	return *a.currentWorkspace, nil
}

// Пустая карта возвращается как nil: вызывающие отличают «нет данных» от
// «есть пустая карта», и maps.Clone сам по себе этого различия не делает.
func cloneMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	return maps.Clone(values)
}
