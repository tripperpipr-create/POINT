package domain

import "time"

// MilestonePlan keeps future work coarse. A detailed Flow is compiled only
// when this milestone becomes current, so replanning does not silently change
// the approved product scope.
type MilestonePlan struct {
	ID           string         `json:"id"`
	Goal         string         `json:"goal"`
	Scope        []string       `json:"scope,omitempty"`
	CriterionIDs []string       `json:"criterionIds"`
	DependsOn    []string       `json:"dependsOn,omitempty"`
	OwnedPaths   []string       `json:"ownedPaths,omitempty"`
	Budget       BudgetEnvelope `json:"budget,omitempty"`
}

type MilestoneRuntime struct {
	MilestoneID string      `json:"milestoneId"`
	Status      QuestStatus `json:"status"`
	FlowID      string      `json:"flowId,omitempty"`
	FlowRunID   string      `json:"flowRunId,omitempty"`
	Checkpoint  string      `json:"checkpoint,omitempty"`
	Attempt     int         `json:"attempt"`
	StartedAt   *time.Time  `json:"startedAt,omitempty"`
	UpdatedAt   time.Time   `json:"updatedAt"`
	FinishedAt  *time.Time  `json:"finishedAt,omitempty"`
}

// WriterLease is the durable, transactional single-writer authority for a
// workspace. Pauses and process restarts retain it; completion/cancellation
// release it explicitly.
type WriterLease struct {
	WorkspaceID string     `json:"workspaceId"`
	QuestID     string     `json:"questId"`
	Token       string     `json:"token"`
	State       string     `json:"state"` // active | released
	AcquiredAt  time.Time  `json:"acquiredAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
	ReleasedAt  *time.Time `json:"releasedAt,omitempty"`
}

// DeliveryReceipt is the machine-verifiable proof that the checked revision
// reached the approved target. A URL is required only for runnable apps.
type DeliveryReceipt struct {
	ID                string    `json:"id"`
	QuestID           string    `json:"questId"`
	WorkOrderDigest   string    `json:"workOrderDigest"`
	Target            string    `json:"target"`
	WorkspaceRevision string    `json:"workspaceRevision"`
	CommitID          string    `json:"commitId,omitempty"`
	URL               string    `json:"url,omitempty"`
	ComposeFile       string    `json:"composeFile,omitempty"`
	ServicesRunning   bool      `json:"servicesRunning"`
	DeliveredAt       time.Time `json:"deliveredAt"`
}

type DeliveredApplicationControl struct {
	QuestID           string    `json:"questId"`
	DeliveryReceiptID string    `json:"deliveryReceiptId"`
	WorkOrderDigest   string    `json:"workOrderDigest"`
	Action            string    `json:"action"` // start | stop
	Status            string    `json:"status"` // executing | running | stopped | failed | unknown_outcome
	URL               string    `json:"url,omitempty"`
	Summary           string    `json:"summary,omitempty"`
	Replayed          bool      `json:"replayed,omitempty"`
	UpdatedAt         time.Time `json:"updatedAt"`
}

// ContextDisclosureEntry is a local-only audit record. Content is identified
// by digest/category and never copied into the evidence payload itself.
type ContextDisclosureEntry struct {
	ModelConnectionID string    `json:"modelConnectionId"`
	Model             string    `json:"model"`
	Category          string    `json:"category"`
	Path              string    `json:"path,omitempty"`
	Digest            string    `json:"digest"`
	Bytes             int64     `json:"bytes"`
	SentAt            time.Time `json:"sentAt"`
}

// ModelCertification is separate from a health probe. Only the complete live
// acceptance matrix may create status=certified; ordinary probes remain
// experimental evidence.
type ModelCertification struct {
	ID              string                    `json:"id"`
	ConnectionID    string                    `json:"connectionId"`
	Model           string                    `json:"model"`
	Provider        ProviderKind              `json:"provider"`
	Runtime         string                    `json:"runtime"`
	Status          string                    `json:"status"` // certified | experimental
	MatrixVersion   string                    `json:"matrixVersion"`
	RequiredRuns    int                       `json:"requiredRuns"`
	PassedRuns      int                       `json:"passedRuns"`
	CostKnown       bool                      `json:"costKnown"`
	Capabilities    AdapterCapabilityManifest `json:"capabilities"`
	EvidenceIDs     []string                  `json:"evidenceIds,omitempty"`
	CertifiedAt     *time.Time                `json:"certifiedAt,omitempty"`
	LastEvaluatedAt time.Time                 `json:"lastEvaluatedAt"`
}

type ModelCallLedgerEntry struct {
	ID             string    `json:"id"`
	MilestoneID    string    `json:"milestoneId,omitempty"`
	StageID        string    `json:"stageId,omitempty"`
	ConnectionID   string    `json:"connectionId"`
	Provider       string    `json:"provider,omitempty"`
	Model          string    `json:"model"`
	Role           string    `json:"role"`
	InputTokens    int64     `json:"inputTokens"`
	OutputTokens   int64     `json:"outputTokens"`
	CostCents      int64     `json:"costCents"`
	CostKnown      bool      `json:"costKnown"`
	UsageReported  bool      `json:"usageReported"`
	ActiveMillis   int64     `json:"activeMillis"`
	Outcome        string    `json:"outcome,omitempty"`
	FallbackReason string    `json:"fallbackReason,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
}
