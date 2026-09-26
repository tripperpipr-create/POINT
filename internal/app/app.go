package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"local-agent-workbench/internal/agent"
	"local-agent-workbench/internal/backup"
	"local-agent-workbench/internal/cache"
	"local-agent-workbench/internal/connections"
	"local-agent-workbench/internal/dbconn"
	"local-agent-workbench/internal/diagnostics"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/mcp"
	"local-agent-workbench/internal/sandbox"
	"local-agent-workbench/internal/servers"
	"local-agent-workbench/internal/storage"
	workbenchtools "local-agent-workbench/internal/tools"
	"local-agent-workbench/internal/workflows"
	"local-agent-workbench/internal/workspace"
)

const Version = "1.2.3"

type App struct {
	masterTurnsMu       sync.Mutex
	masterTurnCancels   map[string]context.CancelFunc
	masterTurnsWG       sync.WaitGroup
	masterTurnsStopping bool
	masterTurnSignals   map[string]chan struct{}
	masterFacts         masterFactsCache
	masterSummaryMu     sync.Mutex
	masterSummaryTurns  map[string]int
	// Запуск утверждённого наряда переживает свой HTTP-запрос: планировщик
	// milestone идёт к модели минутами. Реестр отмен держит запуск по квесту —
	// один на квест, и его можно остановить отменой.
	workOrderLaunchMu       sync.Mutex
	workOrderLaunchCancels  map[string]context.CancelFunc
	workOrderLaunchWG       sync.WaitGroup
	workOrderLaunchStopping bool
	ctx                     context.Context
	dataDir                 string
	databasePath            string
	directoryPicker         func(context.Context) (string, error)
	eventSink               func(domain.Event)
	store                   *storage.SQLite
	engine                  *agent.Engine
	workflowManager         *workflows.Manager
	mu                      sync.RWMutex
	flowMergeMu             sync.Mutex
	externalMu              sync.Mutex
	externalCancels         map[string]context.CancelFunc
	externalWG              sync.WaitGroup
	diagnosticsMu           sync.Mutex
	diagnosticsMemo         map[string]diagnostics.RunDiagnostics
	learningMu              sync.Mutex
	learningReviewMu        sync.Mutex
	masterLearningMu        sync.Mutex
	masterLearningRunning   bool
	learningWG              sync.WaitGroup
	learningCtx             context.Context
	learningCancel          context.CancelFunc
	learningStopping        bool
	// mcpSessions — доступ сущностей для исполнителей, принимающих инструменты
	// только по MCP. Реестр живёт столько же, сколько ядро: сессии в нём
	// открываются и закрываются вокруг конкретной работы.
	mcpSessions *mcp.Registry
	// selfURL — адрес, по которому ядро доступно исполнителям на этой машине.
	// Знает его только тот, кто поднял HTTP-сервер.
	selfURL               string
	backupManager         *backup.Manager
	backupMu              sync.Mutex
	backupTimer           *time.Timer
	backupWG              sync.WaitGroup
	backupCtx             context.Context
	backupCancel          context.CancelFunc
	backupStopping        bool
	lastBackupAt          time.Time
	backupDebounce        time.Duration
	backupInterval        time.Duration
	toolExecutionMu       sync.Mutex
	currentWorkspace      *domain.Workspace
	currentFS             *workspace.FS
	cache                 cache.Cache
	workspaceBoundary     string
	dbSecrets             *dbconn.MemorySecrets
	mcpRuntime            mcpRuntime // MCP-серверы владельца: секреты и надзор (mcp_runtime.go)
	sandboxBackend        sandbox.Backend
	sourceFetcher         SourceFetcher
	gitRunner             GitRunner
	deliveredAppRunner    DeliveredAppRunner
	completionCheckRunner CompletionCheckRunner
	networkGrants         *workbenchtools.NetworkGrantBook
	masterWatchMu         sync.Mutex
	masterWatchCancel     context.CancelFunc
	masterWatchWG         sync.WaitGroup
	masterWatchIntervened map[string]time.Time
	// flowOrchestratorKeys keeps the credential used to start a FlowRun so later
	// stages can auto-start after scheduleFlowAgentExecutionsFromRun (which has no
	// request body). Never persisted; cleared when the FlowRun reaches a terminal status.
	flowOrchestratorKeysMu sync.Mutex
	flowOrchestratorKeys   map[string]string
}

type Bootstrap struct {
	Version             string                        `json:"version"`
	Profiles            []domain.AgentProfile         `json:"profiles"`
	ProfileTemplates    []domain.AgentProfileTemplate `json:"profileTemplates"`
	ToolCatalog         []domain.ToolCatalogItem      `json:"toolCatalog"`
	CustomTools         []domain.CustomTool           `json:"customTools"`
	CustomToolTemplates []domain.CustomToolTemplate   `json:"customToolTemplates"`
	Workspaces          []domain.Workspace            `json:"workspaces"`
	Runs                []domain.Run                  `json:"runs"`
	RunDiagnostics      []diagnostics.RunDiagnostics  `json:"runDiagnostics"`
	Workflows           []domain.AgentWorkflow        `json:"workflows"`
	WorkflowRuns        []domain.WorkflowRun          `json:"workflowRuns"`
	CurrentWorkspace    *domain.Workspace             `json:"currentWorkspace,omitempty"`
	ProviderCatalog     []domain.ProviderPreset       `json:"providerCatalog"`
	IndexStatus         workspace.IndexStatus         `json:"indexStatus"`
	Changes             []domain.PatchProposal        `json:"changes"`
	DefaultProfileID    string                        `json:"defaultProfileId"`
	Preferences         map[string]any                `json:"preferences,omitempty"`
	// Optional Agent Hub MVP fields (backward compatible).
	// Эти два — без omitempty намеренно. Интерфейс по наличию ключей решает,
	// знает ли ядро про Hub, и при их отсутствии уходит на legacy-путь
	// сохранения, теряющий личность, миссию, ограничения, навыки и проектные
	// правила — пять шагов конструктора из десяти. С omitempty пустой ростер
	// исчезал из ответа, и «агентов пока нет» становилось неотличимо от «ядро
	// о них не знает». Пустой список — это ответ, а не молчание.
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
	IntakeSessions           []domain.IntakeSession           `json:"intakeSessions,omitempty"`
	Sandbox                  sandbox.Capabilities             `json:"sandbox"`
}

type WorkspaceView struct {
	Workspace domain.Workspace  `json:"workspace"`
	Tree      []domain.FileNode `json:"tree"`
}
type StartRunRequest struct {
	ProfileID            string                   `json:"profileId"`
	Task                 string                   `json:"task"`
	Goal                 string                   `json:"goal,omitempty"`
	AcceptanceCriteria   []string                 `json:"acceptanceCriteria,omitempty"`
	Constraints          []string                 `json:"constraints,omitempty"`
	APIKey               string                   `json:"apiKey"`
	ContextItems         []domain.RunContextInput `json:"contextItems,omitempty"`
	PreflightFingerprint string                   `json:"preflightFingerprint,omitempty"`
	QuestID              string                   `json:"questId,omitempty"`
	FlowRunID            string                   `json:"flowRunId,omitempty"`
	FlowNodeID           string                   `json:"flowNodeId,omitempty"`
	StageRole            string                   `json:"stageRole,omitempty"`
	ExecutionID          string                   `json:"executionId,omitempty"`
	ModelBinding         *domain.ModelBinding     `json:"modelBinding,omitempty"`
	WorkContract         *domain.WorkContract     `json:"workContract,omitempty"`
	// CompletionCheckKind stamps EventCompletionChecked (e.g. "merged-result").
	CompletionCheckKind string `json:"completionCheckKind,omitempty"`
	// The fields below are populated only by the atomic FastAgent v2 launcher.
	// They are deliberately unexported so HTTP callers cannot select durable
	// identities or bypass normal run preparation.
	preparedRunID              string
	preparedAgentID            string
	preparedStartedAt          time.Time
	preparedConfiguration      *domain.RunConfigurationSnapshot
	initialBudgetReservationID string
	preparedAgent              *preparedAgentRun
}
type AgentRunPreviewRequest struct {
	ProfileID          string                   `json:"profileId"`
	Task               string                   `json:"task"`
	Goal               string                   `json:"goal,omitempty"`
	AcceptanceCriteria []string                 `json:"acceptanceCriteria,omitempty"`
	Constraints        []string                 `json:"constraints,omitempty"`
	ContextItems       []domain.RunContextInput `json:"contextItems,omitempty"`
}
type AgentToolPreview struct {
	Definition           domain.ToolDefinition `json:"definition"`
	DisplayName          string                `json:"displayName"`
	Category             string                `json:"category"`
	Risk                 string                `json:"risk"`
	RequiresApproval     bool                  `json:"requiresApproval"`
	ApprovalReason       string                `json:"approvalReason,omitempty"`
	ProvidesVerification bool                  `json:"providesVerification,omitempty"`
}
type AgentTokenEstimate struct {
	SystemPrompt   int `json:"systemPrompt"`
	Task           int `json:"task"`
	ToolSchemas    int `json:"toolSchemas"`
	Context        int `json:"context"`
	Total          int `json:"total"`
	ContextWindow  int `json:"contextWindow"`
	AvailableInput int `json:"availableInput"`
	ReservedOutput int `json:"reservedOutput"`
}
type AgentRunPreview struct {
	Fingerprint   string                 `json:"fingerprint"`
	Version       string                 `json:"version"`
	Workspace     domain.Workspace       `json:"workspace"`
	Profile       domain.AgentProfile    `json:"profile"`
	SystemMessage string                 `json:"systemMessage"`
	Task          string                 `json:"task"`
	Tools         []AgentToolPreview     `json:"tools"`
	Context       domain.ContextPreview  `json:"context"`
	Tokens        AgentTokenEstimate     `json:"tokens"`
	Completion    agent.CompletionPolicy `json:"completion"`
	Warnings      []string               `json:"warnings"`
}
type TerminalCommandRequest struct {
	Command        string `json:"command"`
	CWD            string `json:"cwd"`
	TimeoutSeconds int    `json:"timeoutSeconds"`
}
type TerminalCommandResult struct {
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	ExitCode   int    `json:"exitCode"`
	DurationMs int64  `json:"durationMs"`
	TimedOut   bool   `json:"timedOut"`
	Truncated  bool   `json:"truncated"`
}
type RunDetails struct {
	Run         domain.Run                 `json:"run"`
	Events      []domain.Event             `json:"events"`
	Approvals   []domain.Approval          `json:"approvals"`
	Patches     []domain.PatchProposal     `json:"patches"`
	Diagnostics diagnostics.RunDiagnostics `json:"diagnostics"`
}

func New(dataDir string, options ...Option) (*App, error) {
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, err
	}
	dataDir, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, err
	}
	databasePath := filepath.Join(dataDir, databaseFileForRuntime())
	backupCtx, backupCancel := context.WithCancel(context.Background())
	preMigrationCtx, stopPreMigration := context.WithTimeout(context.Background(), 2*time.Minute)
	_, err = backup.CreatePreMigration(preMigrationCtx, databasePath, filepath.Join(dataDir, "backups"), time.Now().UTC())
	stopPreMigration()
	if err != nil {
		backupCancel()
		return nil, fmt.Errorf("create pre-migration recovery point: %w", err)
	}
	store, err := storage.Open(databasePath)
	if err != nil {
		backupCancel()
		return nil, err
	}
	cacheStore, err := cache.FromEnvironment(context.Background())
	if err != nil {
		backupCancel()
		_ = store.Close()
		return nil, err
	}
	learningCtx, learningCancel := context.WithCancel(context.Background())
	application := &App{
		dataDir: dataDir, databasePath: databasePath, store: store, cache: cacheStore,
		dbSecrets: dbconn.NewMemorySecrets(), learningCtx: learningCtx, learningCancel: learningCancel,
		backupManager: backup.NewManager(store, filepath.Join(dataDir, "backups"), backup.DefaultPolicy()),
		mcpSessions:   mcp.NewRegistry(),
		backupCtx:     backupCtx, backupCancel: backupCancel, backupDebounce: 5 * time.Second, backupInterval: 30 * time.Minute,
	}
	for _, option := range options {
		option(application)
	}
	if application.sandboxBackend == nil {
		application.sandboxBackend, err = sandbox.BackendFromEnvironment(filepath.Join(os.TempDir(), "point-sandboxes"))
		if err != nil {
			learningCancel()
			backupCancel()
			_ = cacheStore.Close()
			_ = store.Close()
			return nil, fmt.Errorf("configure execution sandbox: %w", err)
		}
		if reason := application.sandboxBackend.Capabilities().Unavailable; reason != "" {
			slog.Warn("execution sandbox unavailable at startup; quests wait for Docker", "reason", reason)
		}
	}
	application.engine = agent.NewEngine(store, application.emitEvent)
	application.engine.SetTrustedCustomToolLookup(application.trustedCustomTool)
	application.engine.SetBudgetController(application)
	application.engine.SetToolSessionOpener(application)
	application.networkGrants = workbenchtools.NewNetworkGrantBook()
	application.engine.SetNetworkGrants(application.networkGrants)
	if executor, ok := application.sandboxBackend.(sandbox.ProcessExecutor); ok {
		application.engine.SetProcessExecutor(executor)
	}
	application.workflowManager = workflows.NewManager(store, application.engine)
	// Засев идёт чертежами: они единственная хранимая сущность, профиль из них
	// выводится (см. storedProfiles). Раньше здесь засевались профили, а следом
	// шёл цикл зеркалирования их в чертежи — двойная запись при каждом старте.
	blueprints, err := store.ListBlueprints(context.Background())
	if err != nil {
		backupCancel()
		_ = cacheStore.Close()
		_ = store.Close()
		return nil, err
	}
	seed := func(profile domain.AgentProfile) error {
		return store.SaveBlueprint(context.Background(), domain.BlueprintFromProfile(profile))
	}
	// Migration 29 performs the final idempotent profile reconciliation before
	// startup. Keeping another copy loop here would be an unmeasured compatibility
	// path and could silently resurrect data written by an obsolete binary.
	if len(blueprints) == 0 {
		profile := domain.DefaultProfile()
		if baseURL := strings.TrimSpace(os.Getenv("DEFAULT_OLLAMA_URL")); baseURL != "" {
			profile.BaseURL = baseURL
		}
		if model := defaultOllamaModelFromEnv(); model != "" {
			profile.Model = model
		}
		if err = seed(profile); err != nil {
			backupCancel()
			_ = cacheStore.Close()
			_ = store.Close()
			return nil, err
		}
		blueprints, err = store.ListBlueprints(context.Background())
		if err != nil {
			backupCancel()
			_ = cacheStore.Close()
			_ = store.Close()
			return nil, err
		}
	}
	baseURL := strings.TrimSpace(os.Getenv("DEFAULT_OLLAMA_URL"))
	defaultModel := defaultOllamaModelFromEnv()
	for _, blueprint := range blueprints {
		if blueprint.ID != "default" {
			continue
		}
		profile := domain.ProfileFromBlueprint(blueprint)
		// Компилятор промпта уже собрал текст из полей чертежа; в засеве нас
		// интересуют только сами поля, поэтому берём исходную инструкцию.
		profile.SystemPrompt = blueprint.SystemPrompt
		changed := localizeLegacyDefaultProfile(&profile)
		if baseURL != "" && profile.BaseURL == "http://127.0.0.1:11434" {
			profile.BaseURL = baseURL
			changed = true
		}
		if defaultModel != "" && profile.Model != defaultModel {
			profile.Model = defaultModel
			changed = true
		}
		if changed {
			profile.UpdatedAt = time.Now().UTC()
			if err = seed(profile); err != nil {
				backupCancel()
				_ = cacheStore.Close()
				_ = store.Close()
				return nil, err
			}
		}
	}
	application.StartMasterWatch()
	return application, nil
}

func defaultOllamaModelFromEnv() string {
	for _, key := range []string{"POINT_PHP_INTAKE_MODEL", "POINT_ACCEPTANCE_MODEL", "POINT_DEFAULT_MODEL", "DEFAULT_OLLAMA_MODEL"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func localizeLegacyDefaultProfile(profile *domain.AgentProfile) bool {
	if profile.Name != "Local coding agent" ||
		profile.RoleDescription != "Careful repository assistant" ||
		profile.SystemPrompt != "You are a careful coding agent. Inspect the workspace, make the smallest correct change, and verify it. Explain every tool call. Never claim a change before it is accepted." {
		return false
	}
	localized := domain.DefaultProfile()
	profile.Name = localized.Name
	profile.RoleDescription = localized.RoleDescription
	profile.SystemPrompt = localized.SystemPrompt
	return true
}

func (a *App) Shutdown(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	a.stopMasterTurns()
	a.stopWorkOrderLaunches()
	a.stopMCPServers()
	a.stopMasterWatch()
	a.externalMu.Lock()
	for _, cancel := range a.externalCancels {
		cancel()
	}
	a.externalMu.Unlock()
	a.workflowManager.StopAll()
	a.engine.StopAll()
	a.learningMu.Lock()
	a.learningStopping = true
	if a.learningCancel != nil {
		a.learningCancel()
	}
	a.learningMu.Unlock()
	a.backupMu.Lock()
	a.backupStopping = true
	if a.backupTimer != nil {
		a.backupTimer.Stop()
		a.backupTimer = nil
	}
	if a.backupCancel != nil {
		a.backupCancel()
	}
	a.backupMu.Unlock()
	done := make(chan struct{})
	go func() {
		a.learningWG.Wait()
		a.backupWG.Wait()
		a.externalWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
	_ = a.cache.Close()
	_ = a.store.Close()
}

func (a *App) SetWorkspaceBoundary(root string) error {
	if strings.TrimSpace(root) == "" {
		a.workspaceBoundary = ""
		return nil
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	canonical, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return err
	}
	a.workspaceBoundary = filepath.Clean(canonical)
	return nil
}

var errForeignWorld = errors.New("resource belongs to another project world")

func (a *App) currentWorldID() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.currentWorkspace == nil {
		return ""
	}
	return a.currentWorkspace.ID
}

func (a *App) guardWorld(workspaceID string) error {
	current := a.currentWorldID()
	if current == "" {
		return errors.New("workspace is not open")
	}
	if workspaceID != current {
		return errForeignWorld
	}
	return nil
}

func (a *App) Bootstrap() (Bootstrap, error) {
	ctx := context.Background()
	profiles, err := a.storedProfiles(ctx)
	if err != nil {
		return Bootstrap{}, err
	}
	worldID := a.currentWorldID()
	runs, err := a.store.ListRunsForWorkspace(ctx, worldID, 100)
	if err != nil {
		return Bootstrap{}, err
	}
	if profiles == nil {
		profiles = []domain.AgentProfile{}
	}
	defaultProfileID := ""
	if value, settingErr := a.store.Setting(ctx, "hub.defaultProfileId"); settingErr == nil {
		defaultProfileID = value
	} else if !storage.IsNotFound(settingErr) {
		return Bootstrap{}, settingErr
	}
	preferences := map[string]any{}
	if value, settingErr := a.store.Setting(ctx, "hub.preferences"); settingErr == nil && strings.TrimSpace(value) != "" {
		if err := json.Unmarshal([]byte(value), &preferences); err != nil {
			return Bootstrap{}, fmt.Errorf("decode hub preferences: %w", err)
		}
	} else if settingErr != nil && !storage.IsNotFound(settingErr) {
		return Bootstrap{}, settingErr
	}
	profileExists := false
	for _, profile := range profiles {
		if profile.ID == defaultProfileID {
			profileExists = true
			break
		}
	}
	if !profileExists {
		for _, profile := range profiles {
			if profile.ID == "default" {
				defaultProfileID = profile.ID
				break
			}
		}
		if defaultProfileID == "" && len(profiles) > 0 {
			defaultProfileID = profiles[0].ID
		}
		if defaultProfileID != "" {
			if err := a.store.SaveSetting(ctx, "hub.defaultProfileId", defaultProfileID); err != nil {
				return Bootstrap{}, err
			}
		}
	}
	workspaces := []domain.Workspace{}
	a.mu.RLock()
	if a.currentWorkspace != nil {
		workspaces = []domain.Workspace{*a.currentWorkspace}
	}
	a.mu.RUnlock()
	if runs == nil {
		runs = []domain.Run{}
	}
	workflowDefinitions, err := a.store.ListWorkflows(ctx)
	if err != nil {
		return Bootstrap{}, err
	}
	workflowRuns, err := a.store.ListWorkflowRunsForWorkspace(ctx, worldID, 100)
	if err != nil {
		return Bootstrap{}, err
	}
	if workflowDefinitions == nil {
		workflowDefinitions = []domain.AgentWorkflow{}
	}
	if workflowRuns == nil {
		workflowRuns = []domain.WorkflowRun{}
	}
	for index := range runs {
		runs[index] = publicRun(runs[index])
	}
	runDiagnostics, err := a.recentRunDiagnostics(ctx, runs, 20)
	if err != nil {
		return Bootstrap{}, err
	}
	for index := range workflowRuns {
		workflowRuns[index] = publicWorkflowRun(workflowRuns[index])
	}
	customTools, err := a.store.ListCustomTools(ctx)
	if err != nil {
		return Bootstrap{}, err
	}
	if customTools == nil {
		customTools = []domain.CustomTool{}
	}
	changes, err := a.store.ListPatchesForWorkspace(ctx, worldID, 200)
	if err != nil {
		return Bootstrap{}, err
	}
	if changes == nil {
		changes = []domain.PatchProposal{}
	}
	changes = publicPatches(changes)
	toolCatalog := domain.BuiltInToolCatalog()
	for _, tool := range customTools {
		toolCatalog = append(toolCatalog, domain.ToolCatalogItem{
			Name: tool.ID, DisplayName: tool.DisplayName, Description: tool.Description,
			Category: "execute", Risk: "CRITICAL", RequiresApproval: true,
			ProvidesVerification: tool.ProvidesVerification,
		})
	}
	a.mu.RLock()
	current := a.currentWorkspace
	currentFS := a.currentFS
	a.mu.RUnlock()
	indexStatus := workspace.IndexStatus{State: "no_workspace"}
	if currentFS != nil {
		indexStatus = currentFS.IndexStatus()
	}
	workspaceID := ""
	if current != nil {
		workspaceID = current.ID
	}
	hub, err := a.loadHubBootstrap(ctx, workspaceID)
	if err != nil {
		return Bootstrap{}, err
	}
	intakes, err := a.store.ListIntakeSessions(ctx, workspaceID)
	if err != nil {
		return Bootstrap{}, err
	}
	return Bootstrap{
		Version: Version, Profiles: profiles,
		ProfileTemplates: domain.BuiltInAgentTemplates(), ToolCatalog: toolCatalog, CustomTools: customTools, CustomToolTemplates: domain.BuiltInCustomToolTemplates(),
		Workspaces: workspaces, Runs: runs, RunDiagnostics: runDiagnostics, Workflows: workflowDefinitions, WorkflowRuns: workflowRuns, CurrentWorkspace: current,
		ProviderCatalog: domain.BuiltInProviderCatalog(), IndexStatus: indexStatus, Changes: changes,
		DefaultProfileID: defaultProfileID, Preferences: preferences,
		Blueprints: hub.Blueprints, ProjectAgents: hub.ProjectAgents, Skills: hub.Skills, ProjectSkills: hub.ProjectSkills,
		Teams: hub.Teams, Quests: hub.Quests, Flows: hub.Flows, FlowRuns: hub.FlowRuns, Executions: hub.Executions,
		ChangeSets: hub.ChangeSets, Memories: hub.Memories, Connections: hub.Connections, ModelRouting: hub.ModelRouting, ServerProfiles: hub.ServerProfiles, DBConnections: hub.DBConnections, UsageRecords: hub.UsageRecords,
		BudgetReservations: hub.BudgetReservations, ModelPricingProfiles: hub.ModelPricingProfiles,
		LearningSignals: hub.LearningSignals, SkillOutcomes: hub.SkillOutcomes, SkillCuration: hub.SkillCuration,
		Companion: hub.Companion, Orchestrator: hub.Orchestrator, CompanionMessages: hub.CompanionMessages,
		CompanionInterventions: hub.CompanionInterventions, CompanionDismissedCount: hub.CompanionDismissedCount,
		CompanionActionProposals: hub.CompanionActionProposals,
		IDEObservations:          hub.IDEObservations,
		QuestProposals:           hub.QuestProposals, ModelCatalog: hub.ModelCatalog,
		ModelCandidates: hub.ModelCandidates, ModelEvidence: hub.ModelEvidence,
		AgentPrepChains: hub.AgentPrepChains, TeamEvents: hub.TeamEvents,
		IntakeSessions: intakes,
		Sandbox:        hub.Sandbox,
	}, nil
}

func (a *App) fs() (*workspace.FS, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.currentFS == nil {
		return nil, errors.New("no workspace is open")
	}
	return a.currentFS, nil
}
func (a *App) emitEvent(event domain.Event) {
	if event.WorkspaceID == "" && event.RunID != "" {
		if run, err := a.store.GetRun(context.Background(), event.RunID); err == nil {
			event.WorkspaceID = run.WorkspaceID
		}
	}
	if event.Type == domain.EventPatchApplied {
		_ = a.cache.DeletePrefix(context.Background(), a.cachePrefix())
	}
	if event.Type == domain.EventModelUsage {
		a.recordUsageFromEvent(event)
	}
	if event.Type == domain.EventToolFinished {
		a.noteCustomToolApprovalFromEvent(event)
	}
	a.handleMasterWatchEvent(event)
	a.scheduleEventBackup(event.Type)
	if a.eventSink != nil {
		a.eventSink(event)
	}
}
