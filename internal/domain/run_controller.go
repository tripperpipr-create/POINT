package domain

import "time"

// Pause reasons recorded on a cooperative checkpoint. User cancel is a
// separate terminal status, not a pause reason.
const (
	PauseReasonUserRequested       = "user_requested"
	PauseReasonActiveTimeExhausted = "active_time_exhausted"
	// PauseReasonStepBudgetExhausted — ходы кончились раньше, чем выполнено
	// задание: лимит достиг утверждённого потолка или агент перестал
	// продвигаться. Продолжение с продлением даёт одну порцию ходов.
	PauseReasonStepBudgetExhausted = "step_budget_exhausted"
	PauseReasonMasterSupervision   = "master_supervision"
)

// RunControllerState is the live-facing slice of autonomous-run bookkeeping.
// Full conversation recovery lives in RunCheckpoint; this travels with Run for UI.
type RunControllerState struct {
	PauseReason            string `json:"pauseReason,omitempty"`
	ActiveSecondsBudget    int    `json:"activeSecondsBudget,omitempty"`
	ActiveElapsedMs        int64  `json:"activeElapsedMs,omitempty"`
	ActiveSecondsRemaining int    `json:"activeSecondsRemaining,omitempty"`
	ActiveTimeExtensions   int    `json:"activeTimeExtensions,omitempty"`
	// StepLimit — текущий лимит ходов, StepCeiling — утверждённый потолок
	// автопродления, StepExtensions — ручные продления.
	StepLimit      int  `json:"stepLimit,omitempty"`
	StepCeiling    int  `json:"stepCeiling,omitempty"`
	StepExtensions int  `json:"stepExtensions,omitempty"`
	Resumable      bool `json:"resumable,omitempty"`
	CheckpointSeq  int  `json:"checkpointSeq,omitempty"`
}

// RemainingActiveSeconds reports how many whole seconds of budget remain.
// A zero budget means the controller is not governing active time.
func (s RunControllerState) RemainingActiveSeconds() int {
	if s.ActiveSecondsBudget <= 0 {
		return 0
	}
	totalMs := int64(s.ActiveSecondsBudget*(s.ActiveTimeExtensions+1)) * 1000
	remaining := totalMs - s.ActiveElapsedMs
	if remaining <= 0 {
		return 0
	}
	return int((remaining + 999) / 1000)
}

// RunCheckpoint is a durable snapshot of one agent loop at a safe boundary.
// InFlightCallID must be empty for the checkpoint to be resumable after restart.
type RunCheckpoint struct {
	RunID                string    `json:"runId"`
	Seq                  int       `json:"seq"`
	CreatedAt            time.Time `json:"createdAt"`
	BriefVersion         int       `json:"briefVersion,omitempty"`
	BriefDigest          string    `json:"briefDigest,omitempty"`
	SandboxPath          string    `json:"sandboxPath,omitempty"`
	ExecutionID          string    `json:"executionId,omitempty"`
	QuestID              string    `json:"questId,omitempty"`
	FlowRunID            string    `json:"flowRunId,omitempty"`
	FlowNodeID           string    `json:"flowNodeId,omitempty"`
	WorkspaceRevision    int       `json:"workspaceRevision"`
	ActiveElapsedMs      int64     `json:"activeElapsedMs"`
	ActiveSecondsBudget  int       `json:"activeSecondsBudget,omitempty"`
	ActiveTimeExtensions int       `json:"activeTimeExtensions,omitempty"`
	// StepLimit и StepExtensions — бюджет ходов на момент чекпойнта;
	// StepGrant — сколько ходов добавит ручное продление; StepWrapUp —
	// следующий ход последний и без инструментов.
	StepLimit           int       `json:"stepLimit,omitempty"`
	StepExtensions      int       `json:"stepExtensions,omitempty"`
	StepGrant           int       `json:"stepGrant,omitempty"`
	StepWrapUp          bool      `json:"stepWrapUp,omitempty"`
	PauseReason         string    `json:"pauseReason,omitempty"`
	InFlightCallID      string    `json:"inFlightCallId,omitempty"`
	CurrentModel        string    `json:"currentModel,omitempty"`
	RemainingFallbacks  []string  `json:"remainingFallbacks,omitempty"`
	NextStep            int       `json:"nextStep"`
	CompletionRevisions int       `json:"completionRevisions"`
	IdenticalToolPlans  int       `json:"identicalToolPlans"`
	LastToolPlan        string    `json:"lastToolPlan,omitempty"`
	ToolPlanRecoveries  int       `json:"toolPlanRecoveries"`
	ToolOutputBytes     int       `json:"toolOutputBytes"`
	CompletedToolCalls  []string  `json:"completedToolCalls,omitempty"`
	HistoryJSON         []byte    `json:"historyJson,omitempty"`
	CompletionJSON      []byte    `json:"completionJson,omitempty"`
	ObservationsJSON    []byte    `json:"observationsJson,omitempty"`
	TaskBriefJSON       []byte    `json:"taskBriefJson,omitempty"`
	ContextItemsJSON    []byte    `json:"contextItemsJson,omitempty"`
	ChangedFiles        []string  `json:"changedFiles,omitempty"`
	ToolsUsed           []string  `json:"toolsUsed,omitempty"`
	Step                int       `json:"step"`
	RequestCount        int       `json:"requestCount"`
	ActiveToolName      string    `json:"activeToolName,omitempty"`
	HeartbeatAt         time.Time `json:"heartbeatAt,omitempty"`
}

// Resumable reports whether this checkpoint is a safe round boundary.
func (c RunCheckpoint) Resumable() bool {
	return c.RunID != "" && c.Seq > 0 && c.InFlightCallID == ""
}
