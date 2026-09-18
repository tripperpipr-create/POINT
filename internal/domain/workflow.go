package domain

import "time"

type WorkflowStep struct {
	ID                     string             `json:"id"`
	Name                   string             `json:"name"`
	ProfileID              string             `json:"profileId"`
	Instruction            string             `json:"instruction"`
	IncludeOriginalContext bool               `json:"includeOriginalContext"`
	IncludePreviousResult  bool               `json:"includePreviousResult"`
	Kind                   string             `json:"kind,omitempty"` // agent, cursor, manual; empty means agent
	Condition              *WorkflowCondition `json:"condition,omitempty"`
	OnFailure              string             `json:"onFailure,omitempty"` // stop (default) or skip
}

type WorkflowCondition struct {
	Type  string `json:"type"`
	Value string `json:"value,omitempty"`
}

type AgentWorkflow struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Steps       []WorkflowStep `json:"steps"`
	CreatedAt   time.Time      `json:"createdAt"`
	UpdatedAt   time.Time      `json:"updatedAt"`
}

type WorkflowSnapshot struct {
	SchemaVersion      int           `json:"schemaVersion"`
	ApplicationVersion string        `json:"applicationVersion"`
	CapturedAt         time.Time     `json:"capturedAt"`
	Workflow           AgentWorkflow `json:"workflow"`
}

type WorkflowStepRun struct {
	StepID          string     `json:"stepId"`
	StepName        string     `json:"stepName"`
	ProfileID       string     `json:"profileId"`
	ProfileName     string     `json:"profileName"`
	RunID           string     `json:"runId,omitempty"`
	Status          RunStatus  `json:"status"`
	Result          string     `json:"result,omitempty"`
	ResultTruncated bool       `json:"resultTruncated,omitempty"`
	Error           string     `json:"error,omitempty"`
	StartedAt       *time.Time `json:"startedAt,omitempty"`
	FinishedAt      *time.Time `json:"finishedAt,omitempty"`
	Kind            string     `json:"kind,omitempty"`
	ClaimToken      string     `json:"-"` // never return extension credentials in API responses
	ClaimedAt       *time.Time `json:"claimedAt,omitempty"`
	LastHeartbeatAt *time.Time `json:"lastHeartbeatAt,omitempty"`
}

type WorkflowRun struct {
	ID           string            `json:"id"`
	WorkflowID   string            `json:"workflowId"`
	WorkspaceID  string            `json:"workspaceId"`
	Task         string            `json:"task"`
	ContextItems []RunContextItem  `json:"contextItems"`
	Snapshot     WorkflowSnapshot  `json:"snapshot"`
	Status       RunStatus         `json:"status"`
	CurrentStep  int               `json:"currentStep"`
	StepRuns     []WorkflowStepRun `json:"stepRuns"`
	Error        string            `json:"error,omitempty"`
	Result       string            `json:"result,omitempty"`
	StartedAt    time.Time         `json:"startedAt"`
	FinishedAt   *time.Time        `json:"finishedAt,omitempty"`
	DurationMs   int64             `json:"durationMs"`
}

func NewWorkflowSnapshot(applicationVersion string, workflow AgentWorkflow, capturedAt time.Time) WorkflowSnapshot {
	workflow.Steps = append([]WorkflowStep(nil), workflow.Steps...)
	version := 1
	for _, step := range workflow.Steps {
		if step.Kind != "" || step.Condition != nil || step.OnFailure != "" {
			version = 2
			break
		}
	}
	return WorkflowSnapshot{SchemaVersion: version, ApplicationVersion: applicationVersion, CapturedAt: capturedAt.UTC(), Workflow: workflow}
}
