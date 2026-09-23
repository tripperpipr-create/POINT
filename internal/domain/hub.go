package domain

import (
	"strings"
	"time"
)

// ToolRisk and ToolPolicy are separate axes: severity vs user policy.
type ToolRisk string

const (
	ToolRiskLow      ToolRisk = "LOW"
	ToolRiskMedium   ToolRisk = "MEDIUM"
	ToolRiskHigh     ToolRisk = "HIGH"
	ToolRiskCritical ToolRisk = "CRITICAL"
)

type ToolPolicy string

const (
	ToolPolicyAllow ToolPolicy = "ALLOW"
	ToolPolicyAsk   ToolPolicy = "ASK"
	ToolPolicyDeny  ToolPolicy = "DENY"
)

type QuestImportance string

const (
	QuestNormal    QuestImportance = "normal"
	QuestImportant QuestImportance = "important"
	QuestCritical  QuestImportance = "critical"
)

type QuestStatus string

const (
	QuestDraft            QuestStatus = "draft"
	QuestProposed         QuestStatus = "proposed"
	QuestActive           QuestStatus = "active"
	QuestPaused           QuestStatus = "paused"
	QuestAwaitingApproval QuestStatus = "awaiting_approval"
	QuestPreflight        QuestStatus = "preflight"
	QuestRunning          QuestStatus = "running"
	QuestAwaitingUser     QuestStatus = "awaiting_user"
	QuestVerifying        QuestStatus = "verifying"
	QuestApplying         QuestStatus = "applying"
	QuestCompleted        QuestStatus = "completed"
	QuestNeedsReview      QuestStatus = "needs_review"
	QuestBlocked          QuestStatus = "blocked"
	QuestFailed           QuestStatus = "failed"
	QuestCancelled        QuestStatus = "cancelled"
)

func IsTerminalQuestStatus(status QuestStatus) bool {
	switch status {
	case QuestCompleted, QuestNeedsReview, QuestBlocked, QuestFailed, QuestCancelled:
		return true
	default:
		return false
	}
}

type MemoryKind string

const (
	MemoryProject   MemoryKind = "project"
	MemoryProfile   MemoryKind = "profile"
	MemoryAgent     MemoryKind = "agent"
	MemoryCompanion MemoryKind = "companion"
	MemoryQuest     MemoryKind = "quest"
)

type ChangeSetStatus string

const (
	ChangeSetPending    ChangeSetStatus = "pending"
	ChangeSetApproved   ChangeSetStatus = "approved"
	ChangeSetApplied    ChangeSetStatus = "applied"
	ChangeSetReverted   ChangeSetStatus = "reverted"
	ChangeSetRejected   ChangeSetStatus = "rejected"
	ChangeSetConflict   ChangeSetStatus = "conflict"
	ChangeSetSuperseded ChangeSetStatus = "superseded"
)

type ConnectionStatus string

const (
	ConnectionConnected    ConnectionStatus = "connected"
	ConnectionDisconnected ConnectionStatus = "disconnected"
	ConnectionError        ConnectionStatus = "error"
	ConnectionUnknown      ConnectionStatus = "unknown"
)

type FlowNodeKind string

const (
	FlowNodeInput     FlowNodeKind = "input"
	FlowNodeAgent     FlowNodeKind = "agent"
	FlowNodeTool      FlowNodeKind = "tool"
	FlowNodeCondition FlowNodeKind = "condition"
	FlowNodeParallel  FlowNodeKind = "parallel"
	FlowNodeJoin      FlowNodeKind = "join"
	FlowNodeLoop      FlowNodeKind = "loop"
	FlowNodeVerifier  FlowNodeKind = "verifier"
	FlowNodeApproval  FlowNodeKind = "approval"
	FlowNodeOutput    FlowNodeKind = "output"
)

// AgentBlueprint is the global reusable agent definition.
type AgentBlueprint struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	RoleDescription string            `json:"roleDescription"`
	Personality     string            `json:"personality,omitempty"`
	Mission         string            `json:"mission,omitempty"`
	SystemPrompt    string            `json:"systemPrompt"`
	Goals           []string          `json:"goals"`
	Rules           []string          `json:"rules"`
	Constraints     []string          `json:"constraints,omitempty"`
	SkillIDs        []string          `json:"skillIds,omitempty"`
	AllowedTools    []string          `json:"allowedTools"`
	ToolPolicies    map[string]string `json:"toolPolicies,omitempty"`
	// ConnectionID — ссылка на подключение, откуда берутся адрес и ключ.
	// Provider/ProviderPreset/BaseURL ниже остаются compatibility-путём для
	// строк, которым связь не досталась: до этой ссылки ключ подбирался
	// поиском первого подключения с тем же пресетом, и при двух ключах одного
	// провайдера запрос молча уходил с чужим.
	ConnectionID        string       `json:"connectionId,omitempty"`
	Provider            ProviderKind `json:"provider"`
	ProviderPreset      string       `json:"providerPreset"`
	BaseURL             string       `json:"baseUrl"`
	PrimaryModel        string       `json:"primaryModel"`
	FallbackModels      []string     `json:"fallbackModels,omitempty"`
	Temperature         float64      `json:"temperature"`
	MaxOutputTokens     int          `json:"maxOutputTokens"`
	ContextWindowTokens int          `json:"contextWindowTokens"`
	ReasoningEffort     string       `json:"reasoningEffort"`
	MaxSteps            int          `json:"maxSteps"`
	MaxDurationSeconds  int          `json:"maxDurationSeconds"`
	ApprovalMode        ApprovalMode `json:"approvalMode"`
	CreatedAt           time.Time    `json:"createdAt"`
	UpdatedAt           time.Time    `json:"updatedAt"`
}

// ProjectAgent is a per-workspace instance derived from a blueprint.
type ProjectAgent struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspaceId"`
	BlueprintID string `json:"blueprintId"`
	// Status is server-owned lifecycle state. Draft agents are visible to the
	// constructor but are never runnable or eligible for party selection.
	Status     string `json:"status"`
	RoleFamily string `json:"roleFamily,omitempty"`
	// ParentAgentID marks an execution-only specialist owned by another agent.
	// Temporary specialists never appear as permanent peers in the roster.
	ParentAgentID       string            `json:"parentAgentId,omitempty"`
	OwnerQuestID        string            `json:"ownerQuestId,omitempty"`
	Temporary           bool              `json:"temporary,omitempty"`
	Name                string            `json:"name"`
	RoleDescription     string            `json:"roleDescription"`
	Personality         string            `json:"personality,omitempty"`
	Mission             string            `json:"mission,omitempty"`
	SystemPrompt        string            `json:"systemPrompt"`
	Goals               []string          `json:"goals"`
	Rules               []string          `json:"rules"`
	Constraints         []string          `json:"constraints,omitempty"`
	ProjectRules        []string          `json:"projectRules,omitempty"`
	SkillIDs            []string          `json:"skillIds,omitempty"`
	AllowedTools        []string          `json:"allowedTools"`
	ToolPolicies        map[string]string `json:"toolPolicies,omitempty"`
	ConnectionID        string            `json:"connectionId,omitempty"`
	Provider            ProviderKind      `json:"provider"`
	ProviderPreset      string            `json:"providerPreset"`
	BaseURL             string            `json:"baseUrl"`
	PrimaryModel        string            `json:"primaryModel"`
	FallbackModels      []string          `json:"fallbackModels,omitempty"`
	Temperature         float64           `json:"temperature"`
	MaxOutputTokens     int               `json:"maxOutputTokens"`
	ContextWindowTokens int               `json:"contextWindowTokens"`
	ReasoningEffort     string            `json:"reasoningEffort"`
	MaxSteps            int               `json:"maxSteps"`
	MaxDurationSeconds  int               `json:"maxDurationSeconds"`
	ApprovalMode        ApprovalMode      `json:"approvalMode"`
	Experience          int               `json:"experience"`
	Level               int               `json:"level"`
	TasksCompleted      int               `json:"tasksCompleted"`
	SuccessCount        int               `json:"successCount"`
	CreatedAt           time.Time         `json:"createdAt"`
	UpdatedAt           time.Time         `json:"updatedAt"`
}

// SkillDefinition is a reusable skill module.
type SkillDefinition struct {
	ID              string                `json:"id"`
	Name            string                `json:"name"`
	Description     string                `json:"description"`
	Instructions    string                `json:"instructions"`
	References      []string              `json:"references,omitempty"`
	Scripts         []string              `json:"scripts,omitempty"`
	RequiredTools   []string              `json:"requiredTools,omitempty"`
	PermissionDelta map[string]ToolPolicy `json:"permissionDelta,omitempty"`
	Configuration   map[string]any        `json:"configuration,omitempty"`
	CreatedAt       time.Time             `json:"createdAt"`
	UpdatedAt       time.Time             `json:"updatedAt"`
}

// SkillRuntime is the resolved skill payload sent to a single agent run.
// It is derived at start time and is not a separately persisted Hub entity.
type SkillRuntime struct {
	ID                     string                `json:"id"`
	Name                   string                `json:"name"`
	Description            string                `json:"description,omitempty"`
	Instructions           string                `json:"instructions"`
	References             []string              `json:"references,omitempty"`
	Scripts                []string              `json:"scripts,omitempty"`
	RequiredTools          []string              `json:"requiredTools,omitempty"`
	PermissionRequirements map[string]ToolPolicy `json:"permissionRequirements,omitempty"`
	Configuration          map[string]any        `json:"configuration,omitempty"`
}

// ProjectSkillInstance binds a skill definition into a workspace with local config.
type ProjectSkillInstance struct {
	ID            string         `json:"id"`
	WorkspaceID   string         `json:"workspaceId"`
	SkillID       string         `json:"skillId"`
	Configuration map[string]any `json:"configuration,omitempty"`
	Enabled       bool           `json:"enabled"`
	CreatedAt     time.Time      `json:"createdAt"`
	UpdatedAt     time.Time      `json:"updatedAt"`
}

// SkillCurationSuggestion is a deterministic, read-only curator finding. The
// Hub never applies these automatically: merge, deprecate and conflict review
// all require an explicit user edit after inspecting the cited definitions.
type SkillCurationSuggestion struct {
	ID              string    `json:"id"`
	Kind            string    `json:"kind"`   // duplicate | conflict | regression | stale
	Action          string    `json:"action"` // merge | review | deprecate
	PrimarySkillID  string    `json:"primarySkillId"`
	RelatedSkillIDs []string  `json:"relatedSkillIds"`
	Title           string    `json:"title"`
	Summary         string    `json:"summary"`
	Evidence        []string  `json:"evidence"`
	Confidence      float64   `json:"confidence"`
	GeneratedAt     time.Time `json:"generatedAt"`
}

// AgentImprovement is an append-only audit entry for one autonomous change to
// a permanent project agent. Exact before/after snapshots make the latest
// applied revision recoverable without deleting the learned history.
type AgentImprovement struct {
	ID                          string                    `json:"id"`
	WorkspaceID                 string                    `json:"workspaceId"`
	ProjectAgentID              string                    `json:"projectAgentId"`
	BlueprintID                 string                    `json:"blueprintId,omitempty"`
	SourceRunID                 string                    `json:"sourceRunId"`
	SkillID                     string                    `json:"skillId,omitempty"`
	Kind                        string                    `json:"kind"`                      // skill_created | skill_updated | skill_recovery | curation_merge_proposed
	Status                      string                    `json:"status"`                    // applying | applied | applied_unproven | applied_proven | skipped | failed | rolled_back
	PromotionStatus             string                    `json:"promotionStatus,omitempty"` // candidate | promoted | rejected | project_only
	Effect                      string                    `json:"effect,omitempty"`          // improved | neutral | regressed | insufficient_sample
	Trigger                     string                    `json:"trigger"`
	Evidence                    []string                  `json:"evidence"`
	ShadowEvaluation            *LearningShadowEvaluation `json:"shadowEvaluation,omitempty"`
	BeforeSkill                 *SkillDefinition          `json:"beforeSkill,omitempty"`
	AfterSkill                  *SkillDefinition          `json:"afterSkill,omitempty"`
	BeforeSkillIDs              []string                  `json:"beforeSkillIds,omitempty"`
	AfterSkillIDs               []string                  `json:"afterSkillIds,omitempty"`
	BeforeBlueprintSkillIDs     []string                  `json:"beforeBlueprintSkillIds,omitempty"`
	AfterBlueprintSkillIDs      []string                  `json:"afterBlueprintSkillIds,omitempty"`
	BeforeAgentSkillIDs         map[string][]string       `json:"beforeAgentSkillIds,omitempty"`
	AfterAgentSkillIDs          map[string][]string       `json:"afterAgentSkillIds,omitempty"`
	MemoryID                    string                    `json:"memoryId,omitempty"`
	MemoryStatus                string                    `json:"memoryStatus,omitempty"` // candidate | promoted | confirmed
	MemoryKey                   string                    `json:"memoryKey,omitempty"`
	MemorySignature             string                    `json:"memorySignature,omitempty"`
	MemorySourceWorkspaces      []string                  `json:"memorySourceWorkspaces,omitempty"`
	BeforeMemory                *MemoryRecord             `json:"beforeMemory,omitempty"`
	AfterMemory                 *MemoryRecord             `json:"afterMemory,omitempty"`
	InstructionStatus           string                    `json:"instructionStatus,omitempty"` // candidate | promoted | confirmed
	InstructionKey              string                    `json:"instructionKey,omitempty"`
	Instruction                 string                    `json:"instruction,omitempty"`
	InstructionSignature        string                    `json:"instructionSignature,omitempty"`
	InstructionSourceWorkspaces []string                  `json:"instructionSourceWorkspaces,omitempty"`
	BeforeBlueprintRules        []string                  `json:"beforeBlueprintRules,omitempty"`
	AfterBlueprintRules         []string                  `json:"afterBlueprintRules,omitempty"`
	BeforeAgentRules            map[string][]string       `json:"beforeAgentRules,omitempty"`
	AfterAgentRules             map[string][]string       `json:"afterAgentRules,omitempty"`
	ReviewMode                  string                    `json:"reviewMode"` // model | deterministic
	Model                       string                    `json:"model,omitempty"`
	Failure                     string                    `json:"failure,omitempty"`
	CanaryEvaluation            *SkillCanaryEvaluation    `json:"canaryEvaluation,omitempty"`
	RollbackAvailable           bool                      `json:"rollbackAvailable,omitempty"`
	CreatedAt                   time.Time                 `json:"createdAt"`
	UpdatedAt                   time.Time                 `json:"updatedAt"`
}

// SkillCanaryMetrics exposes the observed counts and rates behind a rollout
// decision. Rates are included for presentation only; the integer counters are
// the durable evidence and prevent an opaque composite score from emerging.
type SkillCanaryMetrics struct {
	Runs                 int     `json:"runs"`
	Completed            int     `json:"completed"`
	Healthy              int     `json:"healthy"`
	Failed               int     `json:"failed"`
	ToolCalls            int     `json:"toolCalls"`
	ToolFailures         int     `json:"toolFailures"`
	VerificationRequired int     `json:"verificationRequired"`
	VerificationRecorded int     `json:"verificationRecorded"`
	CompletionRate       float64 `json:"completionRate"`
	HealthyRate          float64 `json:"healthyRate"`
	ToolFailureRate      float64 `json:"toolFailureRate"`
	VerificationRate     float64 `json:"verificationRate"`
}

// SkillCanaryEvaluation is a deterministic comparison of an exact candidate
// Skill revision with its exact predecessor. It deliberately carries no
// synthetic rating: every decision must cite a user-readable metric delta.
type SkillCanaryEvaluation struct {
	SchemaVersion        int                 `json:"schemaVersion"`
	Status               string              `json:"status"`           // pending | healthy | regressed
	Effect               string              `json:"effect,omitempty"` // improved | neutral | regressed | insufficient_sample
	Candidate            SkillAttribution    `json:"candidate"`
	Baseline             *SkillAttribution   `json:"baseline,omitempty"`
	CandidateMetrics     SkillCanaryMetrics  `json:"candidateMetrics"`
	BaselineMetrics      *SkillCanaryMetrics `json:"baselineMetrics,omitempty"`
	MinimumCandidateRuns int                 `json:"minimumCandidateRuns"`
	Reasons              []string            `json:"reasons"`
	AutomaticRollback    bool                `json:"automaticRollback"`
	EvaluatedAt          time.Time           `json:"evaluatedAt"`
}

// LearningShadowEvaluation records whether personal benchmark cases could
// prove a candidate before/after apply. Absence of a set is not failure; it
// only means proof stays unproven.
type LearningShadowEvaluation struct {
	Status           string    `json:"status"` // skipped_no_set | compared | deferred
	BenchmarkSetID   string    `json:"benchmarkSetId,omitempty"`
	BenchmarkSetName string    `json:"benchmarkSetName,omitempty"`
	BaselineCases    int       `json:"baselineCases,omitempty"`
	CandidateCases   int       `json:"candidateCases,omitempty"`
	Passed           bool      `json:"passed,omitempty"`
	Reasons          []string  `json:"reasons,omitempty"`
	EvaluatedAt      time.Time `json:"evaluatedAt"`
}

// LearningPrinciple indexes a portable success/failure lesson for an agent
// across quests without binding knowledge to Quest budget identity.
type LearningPrinciple struct {
	ID             string    `json:"id"`
	WorkspaceID    string    `json:"workspaceId"`
	ProjectAgentID string    `json:"projectAgentId"`
	BlueprintID    string    `json:"blueprintId,omitempty"`
	QuestID        string    `json:"questId,omitempty"`
	Kind           string    `json:"kind"` // success | failure
	Key            string    `json:"key"`
	Content        string    `json:"content"`
	Signature      string    `json:"signature"`
	SourceRunID    string    `json:"sourceRunId"`
	ImprovementID  string    `json:"improvementId,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
}

// LearningSignal is immutable, bounded evidence that a finished Run may
// contain a reusable lesson. Signals deliberately do not mutate an agent by
// themselves: the reviewer and curator consume them through separate guarded
// promotion paths.
type LearningSignalKind string

const (
	LearningSignalVerifiedSuccess   LearningSignalKind = "verified_success"
	LearningSignalRunFailure        LearningSignalKind = "run_failure"
	LearningSignalUserFeedback      LearningSignalKind = "user_feedback"
	LearningSignalToolFailure       LearningSignalKind = "tool_failure"
	LearningSignalApprovalDenied    LearningSignalKind = "approval_denied"
	LearningSignalVerificationGap   LearningSignalKind = "verification_gap"
	LearningSignalCompletionRevised LearningSignalKind = "completion_revised"
)

type SkillAttribution struct {
	SkillID         string `json:"skillId"`
	Name            string `json:"name"`
	Revision        int    `json:"revision,omitempty"`
	Digest          string `json:"digest"`
	PromotionStatus string `json:"promotionStatus,omitempty"`
}

type LearningSignal struct {
	ID                string             `json:"id"`
	WorkspaceID       string             `json:"workspaceId"`
	ProjectAgentID    string             `json:"projectAgentId"`
	BlueprintID       string             `json:"blueprintId,omitempty"`
	RunID             string             `json:"runId"`
	Kind              LearningSignalKind `json:"kind"`
	Status            string             `json:"status"` // observed | reviewed | consumed | dismissed
	Summary           string             `json:"summary"`
	Evidence          []string           `json:"evidence"`
	SkillAttributions []SkillAttribution `json:"skillAttributions"`
	CreatedAt         time.Time          `json:"createdAt"`
	UpdatedAt         time.Time          `json:"updatedAt"`
}

// SkillOutcome attributes the operational outcome of one Run to the exact
// immutable Skill version that was actually loaded into the model context.
// It is evidence for later curation, never proof that the Skill alone caused
// the outcome.
type SkillOutcome struct {
	ID                   string    `json:"id"`
	WorkspaceID          string    `json:"workspaceId"`
	ProjectAgentID       string    `json:"projectAgentId"`
	BlueprintID          string    `json:"blueprintId,omitempty"`
	RunID                string    `json:"runId"`
	SkillID              string    `json:"skillId"`
	SkillName            string    `json:"skillName"`
	SkillRevision        int       `json:"skillRevision,omitempty"`
	SkillDigest          string    `json:"skillDigest"`
	PromotionStatus      string    `json:"promotionStatus,omitempty"`
	RunStatus            RunStatus `json:"runStatus"`
	Health               string    `json:"health"`
	ToolCalls            int       `json:"toolCalls"`
	ToolFailures         int       `json:"toolFailures"`
	ApprovalDenied       int       `json:"approvalDenied"`
	FeedbackCount        int       `json:"feedbackCount"`
	CompletionRevisions  int       `json:"completionRevisions"`
	VerificationRequired bool      `json:"verificationRequired"`
	VerificationRecorded bool      `json:"verificationRecorded"`
	CreatedAt            time.Time `json:"createdAt"`
}

// Team groups project agents for quests and flows.
type Team struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspaceId"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	AgentIDs    []string  `json:"agentIds"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// Quest is a user work unit with optional subquests.
type Quest struct {
	Brief            *TaskBrief      `json:"brief,omitempty"`
	ID               string          `json:"id"`
	WorkspaceID      string          `json:"workspaceId"`
	ParentID         string          `json:"parentId,omitempty"`
	Kind             string          `json:"kind,omitempty"` // project | milestone | agent_provisioning | flow_node
	ControllerState  string          `json:"controllerState,omitempty"`
	Controller       map[string]any  `json:"controller,omitempty"`
	PrerequisiteIDs  []string        `json:"prerequisiteIds,omitempty"`
	Title            string          `json:"title"`
	Description      string          `json:"description"`
	Objectives       []string        `json:"objectives,omitempty"`
	Constraints      []string        `json:"constraints,omitempty"`
	DefinitionOfDone []string        `json:"definitionOfDone,omitempty"`
	Importance       QuestImportance `json:"importance"`
	Status           QuestStatus     `json:"status"`
	TeamID           string          `json:"teamId,omitempty"`
	FlowID           string          `json:"flowId,omitempty"`
	FlowRunID        string          `json:"flowRunId,omitempty"`
	FlowNodeID       string          `json:"flowNodeId,omitempty"`
	AssignedAgentID  string          `json:"assignedAgentId,omitempty"`
	BudgetTokens     int64           `json:"budgetTokens,omitempty"`
	BudgetCents      int64           `json:"budgetCents,omitempty"`
	CreatedAt        time.Time       `json:"createdAt"`
	UpdatedAt        time.Time       `json:"updatedAt"`
	FinishedAt       *time.Time      `json:"finishedAt,omitempty"`
}

// FlowGraph is a persisted node-based flow definition.
type FlowGraph struct {
	ID          string     `json:"id"`
	WorkspaceID string     `json:"workspaceId"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Nodes       []FlowNode `json:"nodes"`
	Edges       []FlowEdge `json:"edges"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

type FlowNode struct {
	ID            string            `json:"id"`
	Kind          FlowNodeKind      `json:"kind"`
	Name          string            `json:"name"`
	Config        map[string]any    `json:"config,omitempty"`
	AgentID       string            `json:"agentId,omitempty"`
	ToolName      string            `json:"toolName,omitempty"`
	FailurePolicy FlowFailurePolicy `json:"failurePolicy,omitempty"`
	PositionX     float64           `json:"positionX,omitempty"`
	PositionY     float64           `json:"positionY,omitempty"`
}

type FlowFailurePolicy struct {
	Mode            string `json:"mode,omitempty"` // stop | retry | fallback_agent
	MaxRetries      int    `json:"maxRetries,omitempty"`
	FallbackAgentID string `json:"fallbackAgentId,omitempty"`
}

type FlowEdge struct {
	ID        string `json:"id"`
	From      string `json:"from"`
	To        string `json:"to"`
	Label     string `json:"label,omitempty"`
	Condition string `json:"condition,omitempty"`
}

// FlowRun is a persisted graph execution state machine snapshot.
type FlowRun struct {
	ID          string                   `json:"id"`
	FlowID      string                   `json:"flowId"`
	WorkspaceID string                   `json:"workspaceId"`
	QuestID     string                   `json:"questId,omitempty"`
	Status      RunStatus                `json:"status"`
	NodeStates  map[string]FlowNodeState `json:"nodeStates"`
	Snapshot    map[string]any           `json:"snapshot,omitempty"`
	Error       string                   `json:"error,omitempty"`
	Result      string                   `json:"result,omitempty"`
	StartedAt   time.Time                `json:"startedAt"`
	FinishedAt  *time.Time               `json:"finishedAt,omitempty"`
	DurationMs  int64                    `json:"durationMs"`
}

type FlowNodeState struct {
	Status     string         `json:"status"`
	Attempts   int            `json:"attempts"`
	Output     map[string]any `json:"output,omitempty"`
	Error      string         `json:"error,omitempty"`
	StartedAt  *time.Time     `json:"startedAt,omitempty"`
	FinishedAt *time.Time     `json:"finishedAt,omitempty"`
}

// ExecutionInstance is one concrete task run of a project agent.
type ExecutionInstance struct {
	ID               string                   `json:"id"`
	WorkspaceID      string                   `json:"workspaceId"`
	ProjectAgentID   string                   `json:"projectAgentId"`
	QuestID          string                   `json:"questId,omitempty"`
	FlowRunID        string                   `json:"flowRunId,omitempty"`
	FlowNodeID       string                   `json:"flowNodeId,omitempty"`
	RunID            string                   `json:"runId,omitempty"`
	Runtime          string                   `json:"runtime,omitempty"`
	RuntimeSessionID string                   `json:"runtimeSessionId,omitempty"`
	SandboxID        string                   `json:"sandboxId,omitempty"`
	Task             string                   `json:"task"`
	Status           RunStatus                `json:"status"`
	Snapshot         RunConfigurationSnapshot `json:"snapshot"`
	Error            string                   `json:"error,omitempty"`
	Result           string                   `json:"result,omitempty"`
	StartedAt        time.Time                `json:"startedAt"`
	FinishedAt       *time.Time               `json:"finishedAt,omitempty"`
	DurationMs       int64                    `json:"durationMs"`
}

// SandboxRecord tracks an isolated execution workspace.
type SandboxRecord struct {
	ID                   string     `json:"id"`
	WorkspaceID          string     `json:"workspaceId"`
	ExecutionID          string     `json:"executionId"`
	Kind                 string     `json:"kind"` // worktree | copy | merge-copy | live
	Backend              string     `json:"backend"`
	BackendVersion       string     `json:"backendVersion,omitempty"`
	BackendImage         string     `json:"backendImage,omitempty"`
	BackendImageDigest   string     `json:"backendImageDigest,omitempty"`
	Path                 string     `json:"path"`
	BaseCommit           string     `json:"baseCommit,omitempty"`
	ParentSandboxID      string     `json:"parentSandboxId,omitempty"`
	ParentExecutionID    string     `json:"parentExecutionId,omitempty"`
	ParentSandboxIDs     []string   `json:"parentSandboxIds,omitempty"`
	ParentExecutionIDs   []string   `json:"parentExecutionIds,omitempty"`
	BaselinePath         string     `json:"baselinePath,omitempty"`
	BaselineChangeSetIDs []string   `json:"baselineChangeSetIds,omitempty"`
	CreatedAt            time.Time  `json:"createdAt"`
	ClosedAt             *time.Time `json:"closedAt,omitempty"`
}

// ChangeSet groups sandbox mutations for review/apply.
type ChangeSet struct {
	ID           string               `json:"id"`
	WorkspaceID  string               `json:"workspaceId"`
	ExecutionID  string               `json:"executionId"`
	QuestID      string               `json:"questId,omitempty"`
	Title        string               `json:"title"`
	Kind         string               `json:"kind,omitempty"` // execution | merge
	Status       ChangeSetStatus      `json:"status"`
	DependsOn    []string             `json:"dependsOn,omitempty"`
	Supersedes   []string             `json:"supersedes,omitempty"`
	SupersededBy string               `json:"supersededBy,omitempty"`
	Items        []ChangeItem         `json:"items"`
	Resolutions  []ConflictResolution `json:"resolutions,omitempty"`
	CreatedAt    time.Time            `json:"createdAt"`
	UpdatedAt    time.Time            `json:"updatedAt"`
	AppliedAt    *time.Time           `json:"appliedAt,omitempty"`
}

type ChangeItem struct {
	ID               string `json:"id"`
	Path             string `json:"path"`
	Kind             string `json:"kind"` // create | modify | delete
	OriginalHash     string `json:"originalHash,omitempty"`
	ProposedHash     string `json:"proposedHash,omitempty"`
	AppliedHash      string `json:"appliedHash,omitempty"`
	AppliedOperation string `json:"appliedOperation,omitempty"` // write | delete | kept
	Diff             string `json:"diff,omitempty"`
	PatchID          string `json:"patchId,omitempty"`
	// Exact snapshots are persisted for deterministic apply/revert but omitted
	// from API payloads; the review surface uses the bounded diff above.
	OriginalContent string `json:"-"`
	ProposedContent string `json:"-"`
}

// MemoryRecord is persistent knowledge. Profile memory is global and owned by
// an AgentBlueprint; every other kind stays scoped to one workspace.
type MemoryRecord struct {
	ID          string     `json:"id"`
	WorkspaceID string     `json:"workspaceId"`
	Kind        MemoryKind `json:"kind"`
	OwnerID     string     `json:"ownerId,omitempty"`
	Content     string     `json:"content"`
	Source      string     `json:"source"`
	Confidence  float64    `json:"confidence"`
	Pinned      bool       `json:"pinned"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

// Connection is a provider account/status without storing secrets.
type Connection struct {
	ID          string           `json:"id"`
	Provider    ProviderKind     `json:"provider"`
	PresetID    string           `json:"presetId"`
	DisplayName string           `json:"displayName"`
	BaseURL     string           `json:"baseUrl,omitempty"`
	Status      ConnectionStatus `json:"status"`
	SecretRef   string           `json:"secretRef,omitempty"`
	LastError   string           `json:"lastError,omitempty"`
	LastProbeAt *time.Time       `json:"lastProbeAt,omitempty"`
	// DefaultModel и IsDefault нужны, чтобы новый агент не начинался с пустых
	// полей: подключение уже знает, какой моделью им обычно пользуются.
	DefaultModel string `json:"defaultModel,omitempty"`
	IsDefault    bool   `json:"isDefault,omitempty"`
	// APIVersion живёт отдельным полем, а не в BaseURL: Azure требует
	// ?api-version=, а NormalizeCompatibleBaseURL намеренно срезает query.
	APIVersion string `json:"apiVersion,omitempty"`
	// Каталог сохраняется после проверки, чтобы список моделей был виден и без
	// сети. Пустой каталог означает «ещё не проверяли», а не «моделей нет».
	Models           []ConnectionModel `json:"models,omitempty"`
	CatalogUpdatedAt *time.Time        `json:"catalogUpdatedAt,omitempty"`
	CreatedAt        time.Time         `json:"createdAt"`
	UpdatedAt        time.Time         `json:"updatedAt"`
}

// WorkspaceModelRouting выбирает общие модели для кодинга и дешёвых
// оркестраторских ходов, не изменяя сохранённые профили агентов и Мастера.
type WorkspaceModelRouting struct {
	WorkspaceID        string    `json:"workspaceId"`
	CodingConnectionID string    `json:"codingConnectionId,omitempty"`
	CodingModel        string    `json:"codingModel,omitempty"`
	CheapConnectionID  string    `json:"cheapConnectionId,omitempty"`
	CheapModel         string    `json:"cheapModel,omitempty"`
	UpdatedAt          time.Time `json:"updatedAt"`
}

// ConnectionModel — одна модель в каталоге подключения. State говорит, откуда
// взяты пределы: confirmed — прислал провайдер, known — нашлась в справочнике,
// unknown — неизвестна, и тогда пределы спрашивают у человека, а не выдумывают.
type ConnectionModel struct {
	ID            string   `json:"id"`
	DisplayName   string   `json:"displayName,omitempty"`
	OwnedBy       string   `json:"ownedBy,omitempty"`
	ContextWindow int      `json:"contextWindow,omitempty"`
	MaxOutput     int      `json:"maxOutput,omitempty"`
	Capabilities  []string `json:"capabilities,omitempty"`
	State         string   `json:"state"`
}

// UsageRecord is an immutable billing/usage fact.
type UsageRecord struct {
	ID             string    `json:"id"`
	WorkspaceID    string    `json:"workspaceId"`
	ExecutionID    string    `json:"executionId,omitempty"`
	QuestID        string    `json:"questId,omitempty"`
	ProjectAgentID string    `json:"projectAgentId,omitempty"`
	Provider       string    `json:"provider"`
	Model          string    `json:"model"`
	InputTokens    int64     `json:"inputTokens"`
	OutputTokens   int64     `json:"outputTokens"`
	TotalTokens    int64     `json:"totalTokens"`
	CostCents      *int64    `json:"costCents,omitempty"`
	LatencyMs      int64     `json:"latencyMs"`
	Outcome        string    `json:"outcome"`
	CreatedAt      time.Time `json:"createdAt"`
}

// OrchestratorConfig is the project-scoped system agent that assigns parties
// and starts Flows. It is separate from Companion: Companion only recommends.
type OrchestratorConfig struct {
	Learning *MasterLearningConfig `json:"learning,omitempty"`
	ID                 string       `json:"id"`
	WorkspaceID        string       `json:"workspaceId,omitempty"`
	Preset             string       `json:"preset"`
	ConnectionID       string       `json:"connectionId,omitempty"`
	Provider           ProviderKind `json:"provider,omitempty"`
	ProviderPreset     string       `json:"providerPreset,omitempty"`
	BaseURL            string       `json:"baseUrl,omitempty"`
	APIVersion         string       `json:"apiVersion,omitempty"`
	Model              string       `json:"model,omitempty"`
	Temperature        float64      `json:"temperature,omitempty"`
	MaxOutputTokens    int          `json:"maxOutputTokens,omitempty"`
	PlanningDepth      int          `json:"planningDepth"`
	Parallelism        int          `json:"parallelism"`
	ApprovalStrictness int          `json:"approvalStrictness"`
	TeamPreference     int          `json:"teamPreference"`
	CreatedAt          time.Time    `json:"createdAt"`
	UpdatedAt          time.Time    `json:"updatedAt"`
}

// CompanionConfig stores personality and mode preferences.
type CompanionConfig struct {
	ID                     string       `json:"id"`
	WorkspaceID            string       `json:"workspaceId,omitempty"`
	Configured             bool         `json:"configured"`
	Preset                 string       `json:"preset"`
	ConnectionID           string       `json:"connectionId,omitempty"`
	Provider               ProviderKind `json:"provider,omitempty"`
	ProviderPreset         string       `json:"providerPreset,omitempty"`
	BaseURL                string       `json:"baseUrl,omitempty"`
	APIVersion             string       `json:"apiVersion,omitempty"`
	Model                  string       `json:"model,omitempty"`
	Temperature            float64      `json:"temperature,omitempty"`
	MaxOutputTokens        int          `json:"maxOutputTokens,omitempty"`
	Criticality            int          `json:"criticality"`
	Creativity             int          `json:"creativity"`
	Verbosity              int          `json:"verbosity"`
	Initiative             int          `json:"initiative"`
	QuestionStrictness     int          `json:"questionStrictness"`
	RiskTolerance          int          `json:"riskTolerance"`
	AutoAct                bool         `json:"autoAct"`
	AutoOpenChatOnCritical bool         `json:"autoOpenChatOnCritical"`
	AutoSendModelPrompt    bool         `json:"autoSendModelPrompt"`
	// SkillIDs — навыки, надетые на помощника. Свои, а не агентские: помощник
	// отвечает в боковой панели и практики у него другие. Навык, которому
	// нужен инструмент вне доступа помощника, на него не надевается.
	SkillIDs  []string  `json:"skillIds,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// CompanionMessage is one durable, project-scoped turn in the Companion chat.
// Credentials and raw model requests are intentionally never stored here.
type CompanionMessage struct {
	ConversationID string             `json:"conversationId,omitempty"`
	TurnID         string             `json:"turnId,omitempty"`
	Attachments    []MasterAttachment `json:"attachments,omitempty"`
	MemoryIDs      []string           `json:"memoryIds,omitempty"`
	ID             string             `json:"id"`
	WorkspaceID    string             `json:"workspaceId"`
	// Speaker разделяет разговоры компаньона и мастера в общей хронике.
	// Пустое значение читается как компаньон — так ведут себя прежние записи.
	Speaker          string           `json:"speaker,omitempty"`
	Role             string           `json:"role"` // user | assistant
	Content          string           `json:"content"`
	Level            string           `json:"level,omitempty"`
	Mode             string           `json:"mode,omitempty"`
	Provider         string           `json:"provider,omitempty"`
	Model            string           `json:"model,omitempty"`
	FactsUsed        []string         `json:"factsUsed,omitempty"`
	Questions        []string         `json:"questions,omitempty"`
	Clarifications   []MasterQuestion `json:"clarifications,omitempty"`
	UsageRecordID    string           `json:"usageRecordId,omitempty"`
	ProposalID       string           `json:"proposalId,omitempty"`
	ActionProposalID string           `json:"actionProposalId,omitempty"`
	FallbackReason   string           `json:"fallbackReason,omitempty"`
	InputTokens      int64            `json:"inputTokens,omitempty"`
	OutputTokens     int64            `json:"outputTokens,omitempty"`
	TotalTokens      int64            `json:"totalTokens,omitempty"`
	LatencyMs        int64            `json:"latencyMs,omitempty"`
	// Feedback — "up", "down" или пусто. Оценка принадлежит реплике, а не
	// машине: у компаньона она лежит в состоянии рабочей области и пропадает
	// вместе с ним, и повторить причину недовольства потом уже нечем.
	Feedback string `json:"feedback,omitempty"`
	// Reasoning и Steps — как Мастер пришёл к этому ответу: рассуждение модели и
	// раунды читающих инструментов. Ядро собирало и то, и другое с самого начала
	// и выбрасывало: на экране оставалось одно слово «Думает…» на полторы минуты.
	// Хранятся вместе с репликой, иначе пропадут при первом же переоткрытии
	// панели — ровно так уже терялась карточка предложенного квеста.
	Reasoning string         `json:"reasoning,omitempty"`
	Steps     []ChatTurnStep `json:"steps,omitempty"`
	CreatedAt time.Time      `json:"createdAt"`
}

// ChatTurnStep — один раунд читающего инструмента внутри хода: что Мастер
// посмотрел в проекте, прежде чем ответить. Обрезанный результат назван
// обрезанным: ответ, построенный на первых 16 КБ файла, читается иначе, чем
// ответ по файлу целиком.
type ChatTurnStep struct {
	Round     int    `json:"round"`
	Tool      string `json:"tool"`
	Argument  string `json:"argument,omitempty"`
	Result    string `json:"result,omitempty"`
	Failed    bool   `json:"failed,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

type CompanionActionKind string

const (
	CompanionActionCreateFlow  CompanionActionKind = "create_flow"
	CompanionActionCreateAgent CompanionActionKind = "create_agent"
	CompanionActionCreateTeam  CompanionActionKind = "create_team"
	CompanionActionCreateSkill CompanionActionKind = "create_skill"
	// CompanionActionCreateTool — заготовка самодельного инструмента. Модель
	// его не создаёт и не получает: она только готовит черновик по команде,
	// которую человек назвал сам, а создание и выдачу решает человек.
	CompanionActionCreateTool CompanionActionKind = "create_tool"
)

// CompanionActionProposal is a durable, reviewable Hub mutation draft. The
// Companion may prepare it, but only an explicit user decision can apply it.
type CompanionActionProposal struct {
	ID              string              `json:"id"`
	WorkspaceID     string              `json:"workspaceId"`
	Kind            CompanionActionKind `json:"kind"`
	Title           string              `json:"title"`
	Rationale       string              `json:"rationale"`
	Flow            *FlowGraph          `json:"flow,omitempty"`
	Agent           *ProjectAgent       `json:"agent,omitempty"`
	Team            *Team               `json:"team,omitempty"`
	Skill           *SkillDefinition    `json:"skill,omitempty"`
	Tool            *CustomTool         `json:"tool,omitempty"`
	Status          string              `json:"status"` // pending | modified | applied | ignored
	AppliedEntityID string              `json:"appliedEntityId,omitempty"`
	// ContinuationPrompt хранит исходную задачу, ради которой пришлось сначала
	// создать агента. После Apply интерфейс повторяет именно её, а не служебную
	// фразу «продолжай», из которой получался квест с неверным названием.
	ContinuationPrompt string    `json:"continuationPrompt,omitempty"`
	ContinuationLabel  string    `json:"continuationLabel,omitempty"`
	CreatedAt          time.Time `json:"createdAt"`
	UpdatedAt          time.Time `json:"updatedAt"`
}

// IDEObservation is bounded, workspace-scoped evidence reported by the IDE.
// It lets Companion reason about editor diagnostics and terminal/task outcomes
// without turning the LLM into the runtime or granting it terminal access.
type IDEObservation struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspaceId"`
	Kind        string    `json:"kind"` // diagnostic | terminal | task | debug | run | scm
	Source      string    `json:"source,omitempty"`
	Level       string    `json:"level"` // info | warning | error
	Summary     string    `json:"summary"`
	Detail      string    `json:"detail,omitempty"`
	Path        string    `json:"path,omitempty"`
	Line        int       `json:"line,omitempty"`
	Command     string    `json:"command,omitempty"`
	ExitCode    *int      `json:"exitCode,omitempty"`
	ObservedAt  time.Time `json:"observedAt"`
	FirstSeen   time.Time `json:"firstSeen,omitempty"`
	LastSeen    time.Time `json:"lastSeen,omitempty"`
	Count       int       `json:"count,omitempty"`
	NoveltyHash string    `json:"noveltyHash,omitempty"`
	FocusPath   string    `json:"focusPath,omitempty"`
}

// CompanionIntervention is a live, non-blocking recommendation derived from
// observable project state. Enforcement remains in policy/permission layers.
type CompanionInterventionAction string

const (
	CompanionInterventionOpenRun         CompanionInterventionAction = "open_run"
	CompanionInterventionMessageRun      CompanionInterventionAction = "message_run"
	CompanionInterventionPrompt          CompanionInterventionAction = "companion_prompt"
	CompanionInterventionProbeConnection CompanionInterventionAction = "probe_connection"
)

type CompanionIntervention struct {
	ID            string                      `json:"id"`
	Level         string                      `json:"level"` // suggestion | warning | critical
	Title         string                      `json:"title"`
	Detail        string                      `json:"detail"`
	ActionTab     string                      `json:"actionTab,omitempty"`
	RelatedID     string                      `json:"relatedId,omitempty"`
	RelatedPath   string                      `json:"relatedPath,omitempty"`
	RelatedLine   int                         `json:"relatedLine,omitempty"`
	ActionKind    CompanionInterventionAction `json:"actionKind,omitempty"`
	ActionLabel   string                      `json:"actionLabel,omitempty"`
	ActionMessage string                      `json:"actionMessage,omitempty"`
	OccurrenceKey string                      `json:"occurrenceKey"`
}

// QuestProposal is a typed Companion recommendation.
type AgentSelectionBreakdown struct {
	AgentID            string   `json:"agentId"`
	RoleFit            int      `json:"roleFit"`
	Evidence           int      `json:"evidence"`
	Verification       int      `json:"verification"`
	Diversity          int      `json:"diversity"`
	LoadPenalty        int      `json:"loadPenalty"`
	LatencyPenalty     int      `json:"latencyPenalty"`
	CostPenalty        int      `json:"costPenalty"`
	Total              int      `json:"total"`
	Matched            []string `json:"matched,omitempty"`
	ConfirmedSuccesses int      `json:"confirmedSuccesses"`
	Attempts           int      `json:"attempts"`
}

type QuestProposal struct {
	Brief       *TaskBrief `json:"brief,omitempty"`
	ID          string     `json:"id"`
	WorkspaceID string     `json:"workspaceId"`
	Title       string     `json:"title"`
	// Task — задача словами человека. Без неё в квест уходило объяснение выбора
	// отряда, и оно же попадало в контекст исполняющего агента: он читал, как его
	// выбирали, вместо того что нужно сделать.
	Task             string   `json:"task,omitempty"`
	Rationale        string   `json:"rationale"`
	Unknowns         []string `json:"unknowns,omitempty"`
	Objectives       []string `json:"objectives,omitempty"`
	Constraints      []string `json:"constraints,omitempty"`
	DefinitionOfDone []string `json:"definitionOfDone,omitempty"`
	TeamAgentIDs     []string `json:"teamAgentIds,omitempty"`
	// TeamAgentIDsLocked отличает явный выбор человека от предварительного
	// предложения Мастера. Список виден в обоих случаях, но только явный выбор
	// обязан пережить последующий Start без повторного редактирования.
	TeamAgentIDsLocked bool                      `json:"teamAgentIdsLocked,omitempty"`
	SelectionBreakdown []AgentSelectionBreakdown `json:"selectionBreakdown,omitempty"`
	PlanPreview        []PlanStagePreview        `json:"planPreview,omitempty"`
	FlowID             string                    `json:"flowId,omitempty"`
	Importance         QuestImportance           `json:"importance"`
	EstimateTokens     int64                     `json:"estimateTokens,omitempty"`
	EstimateCents      *int64                    `json:"estimateCents,omitempty"`
	Status             string                    `json:"status"`
	CreatedAt          time.Time                 `json:"createdAt"`
}

// BlueprintFromProfile adapts a legacy AgentProfile into a blueprint.
func BlueprintFromProfile(profile AgentProfile) AgentBlueprint {
	model := profile.Model
	return AgentBlueprint{
		ID: profile.ID, Name: profile.Name, RoleDescription: profile.RoleDescription,
		SystemPrompt: profile.SystemPrompt, Goals: append([]string(nil), profile.Goals...),
		Rules: append([]string(nil), profile.Rules...), AllowedTools: append([]string(nil), profile.AllowedTools...),
		Provider: profile.Provider, ProviderPreset: profile.ProviderPreset, ConnectionID: profile.ConnectionID, BaseURL: profile.BaseURL,
		PrimaryModel: model, FallbackModels: append([]string(nil), profile.FallbackModels...), Temperature: profile.Temperature, MaxOutputTokens: profile.MaxOutputTokens,
		ContextWindowTokens: profile.ContextWindowTokens, ReasoningEffort: profile.ReasoningEffort,
		ToolPolicies: cloneStringMap(profile.ToolPolicies),
		MaxSteps:     profile.MaxSteps, MaxDurationSeconds: profile.MaxDurationSeconds, ApprovalMode: profile.ApprovalMode,
		CreatedAt: profile.CreatedAt, UpdatedAt: profile.UpdatedAt,
	}
}

// ProfileFromProjectAgent adapts a project agent back to legacy AgentProfile for run engine compatibility.
func ProfileFromProjectAgent(agent ProjectAgent) AgentProfile {
	return AgentProfile{
		ID: agent.ID, Name: agent.Name, RoleDescription: agent.RoleDescription, SystemPrompt: CompileProjectAgentPrompt(agent),
		Goals: append([]string(nil), agent.Goals...), Rules: append([]string(nil), agent.Rules...),
		Provider: agent.Provider, ProviderPreset: agent.ProviderPreset, ConnectionID: agent.ConnectionID, BaseURL: agent.BaseURL,
		Model: agent.PrimaryModel, Temperature: agent.Temperature, MaxOutputTokens: agent.MaxOutputTokens,
		ContextWindowTokens: agent.ContextWindowTokens, ReasoningEffort: agent.ReasoningEffort,
		AllowedTools: append([]string(nil), agent.AllowedTools...), ToolPolicies: cloneStringMap(agent.ToolPolicies),
		FallbackModels: append([]string(nil), agent.FallbackModels...), MaxSteps: agent.MaxSteps,
		MaxDurationSeconds: agent.MaxDurationSeconds, ApprovalMode: agent.ApprovalMode,
		CreatedAt: agent.CreatedAt, UpdatedAt: agent.UpdatedAt,
	}
}

// ProfileFromBlueprint adapts a global blueprint for legacy clients. New Hub
// executions should resolve a workspace-scoped ProjectAgent instead.
func ProfileFromBlueprint(blueprint AgentBlueprint) AgentProfile {
	return AgentProfile{
		ID: blueprint.ID, Name: blueprint.Name, RoleDescription: blueprint.RoleDescription,
		SystemPrompt: CompileBlueprintPrompt(blueprint), Goals: append([]string(nil), blueprint.Goals...),
		Rules: append([]string(nil), blueprint.Rules...), Provider: blueprint.Provider,
		ProviderPreset: blueprint.ProviderPreset, ConnectionID: blueprint.ConnectionID, BaseURL: blueprint.BaseURL, Model: blueprint.PrimaryModel,
		Temperature: blueprint.Temperature, MaxOutputTokens: blueprint.MaxOutputTokens,
		ContextWindowTokens: blueprint.ContextWindowTokens, ReasoningEffort: blueprint.ReasoningEffort,
		AllowedTools: append([]string(nil), blueprint.AllowedTools...), ToolPolicies: cloneStringMap(blueprint.ToolPolicies),
		FallbackModels: append([]string(nil), blueprint.FallbackModels...), MaxSteps: blueprint.MaxSteps,
		MaxDurationSeconds: blueprint.MaxDurationSeconds, ApprovalMode: blueprint.ApprovalMode,
		CreatedAt: blueprint.CreatedAt, UpdatedAt: blueprint.UpdatedAt,
	}
}

// CompileProjectAgentPrompt builds the derived, read-only prompt layer used by
// the runtime. SystemPrompt remains the user's additional instruction source;
// derived identity fields are never persisted back into it.
func CompileProjectAgentPrompt(agent ProjectAgent) string {
	return compileAgentPrompt(
		agent.Name, agent.Personality, agent.RoleDescription, agent.Mission, agent.SystemPrompt,
		agent.Constraints, agent.ProjectRules,
	)
}

// CompileBlueprintPrompt applies the same deterministic compiler to a global
// blueprint. Project-only rules are added after the blueprint is instantiated.
func CompileBlueprintPrompt(blueprint AgentBlueprint) string {
	return compileAgentPrompt(
		blueprint.Name, blueprint.Personality, blueprint.RoleDescription, blueprint.Mission, blueprint.SystemPrompt,
		blueprint.Constraints, nil,
	)
}

func compileAgentPrompt(name, personality, role, mission, instructions string, constraints, projectRules []string) string {
	sections := make([]string, 0, 7)
	appendText := func(heading, value string) {
		if value = strings.TrimSpace(value); value != "" {
			sections = append(sections, heading+"\n"+value)
		}
	}
	appendList := func(heading string, values []string) {
		clean := make([]string, 0, len(values))
		for _, value := range values {
			if value = strings.TrimSpace(value); value != "" {
				clean = append(clean, value)
			}
		}
		if len(clean) > 0 {
			sections = append(sections, heading+"\n- "+strings.Join(clean, "\n- "))
		}
	}
	appendText("IDENTITY:", name)
	appendText("PERSONALITY:", personality)
	appendText("ROLE:", role)
	appendText("MISSION:", mission)
	appendList("CONSTRAINTS:", constraints)
	appendList("PROJECT RULES:", projectRules)
	appendText("ADDITIONAL INSTRUCTIONS:", instructions)
	return strings.Join(sections, "\n\n")
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

// EstimateQuestTokens — потолок расхода квеста, а не прогноз.
//
// Оценка попадает в BudgetTokens, а бюджет — единственное, что вообще
// останавливает трату: проверка перед запуском пропускает квест без бюджета
// целиком («BudgetTokens <= 0 && BudgetCents <= 0 → return nil»). Пока оценку
// ставил только компаньон, квесты Мастера — то есть основной путь — уходили в
// работу без всякого предела. Правило одно на обоих: два оценщика разошлись бы,
// и одинаковые квесты получали бы разные потолки в зависимости от того, кто их
// предложил.
func EstimateQuestTokens(proposal QuestProposal) int64 {
	estimate := int64(8000 + len(proposal.Objectives)*5000 + len(proposal.TeamAgentIDs)*2500)
	switch proposal.Importance {
	case QuestImportant:
		estimate = estimate * 3 / 2
	case QuestCritical:
		estimate *= 2
	}
	if estimate > 250000 {
		return 250000
	}
	return estimate
}
