package domain

import "time"

// IntakeStatus is the durable lifecycle of a task that starts from a URL.
type IntakeStatus string

const (
	IntakeIngesting        IntakeStatus = "ingesting"
	IntakeDrafting         IntakeStatus = "drafting"
	IntakeAwaitingApproval IntakeStatus = "awaiting_approval"
	IntakePreparing        IntakeStatus = "preparing"
	IntakeExecuting        IntakeStatus = "executing"
	IntakeIntegrating      IntakeStatus = "integrating"
	IntakeVerifying        IntakeStatus = "verifying"
	IntakeCompleted        IntakeStatus = "completed"
	IntakeNeedsReview      IntakeStatus = "needs_review"
	IntakeBlocked          IntakeStatus = "blocked"
)

type SourceProvenance struct {
	URL         string    `json:"url"`
	FetchedAt   time.Time `json:"fetchedAt"`
	ContentType string    `json:"contentType,omitempty"`
	SizeBytes   int64     `json:"sizeBytes"`
	SHA256      string    `json:"sha256"`
}

type SourceArtifact struct {
	ID            string           `json:"id"`
	Kind          string           `json:"kind"` // git | html | pdf | docx | markdown | text
	Title         string           `json:"title,omitempty"`
	CanonicalURL  string           `json:"canonicalUrl"`
	ExtractedText string           `json:"extractedText,omitempty"`
	Provenance    SourceProvenance `json:"provenance"`
}

// SourceBundle is an immutable snapshot of every source used to draft a task.
type SourceBundle struct {
	ID            string           `json:"id"`
	PrimaryURL    string           `json:"primaryUrl"`
	Kind          string           `json:"kind"`
	Digest        string           `json:"digest"`
	Artifacts     []SourceArtifact `json:"artifacts"`
	WorkspacePath string           `json:"workspacePath,omitempty"`
	DefaultBranch string           `json:"defaultBranch,omitempty"`
	CreatedAt     time.Time        `json:"createdAt"`
}

type RuntimeSpec struct {
	ID             string            `json:"id"`
	Kind           string            `json:"kind"` // project | managed | generated
	Image          string            `json:"image,omitempty"`
	ImageDigest    string            `json:"imageDigest,omitempty"`
	Toolchains     map[string]string `json:"toolchains,omitempty"`
	ManifestPaths  []string          `json:"manifestPaths,omitempty"`
	DependencyLock string            `json:"dependencyLock,omitempty"`
}

type EnvironmentCommand struct {
	ID                   string   `json:"id"`
	Purpose              string   `json:"purpose"`
	Program              string   `json:"program"`
	Arguments            []string `json:"arguments,omitempty"`
	WorkingDirectory     string   `json:"workingDirectory,omitempty"`
	ProvidesVerification bool     `json:"providesVerification,omitempty"`
}

type EnvironmentService struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	HealthURL   string `json:"healthUrl,omitempty"`
	ComposeFile string `json:"composeFile,omitempty"`
}

type EnvironmentPlan struct {
	ID           string               `json:"id"`
	WorkspaceID  string               `json:"workspaceId,omitempty"`
	Strategy     string               `json:"strategy"` // project | managed | generated | blocked
	Runtime      RuntimeSpec          `json:"runtime"`
	Commands     []EnvironmentCommand `json:"commands,omitempty"`
	Services     []EnvironmentService `json:"services,omitempty"`
	NetworkHosts []string             `json:"networkHosts,omitempty"`
	HealthChecks []EnvironmentCommand `json:"healthChecks,omitempty"`
	Blockers     []string             `json:"blockers,omitempty"`
	Digest       string               `json:"digest"`
	CreatedAt    time.Time            `json:"createdAt"`
}

type CapabilityRequirement struct {
	ID             string   `json:"id"`
	Role           string   `json:"role"`
	Responsibility string   `json:"responsibility"`
	Capabilities   []string `json:"capabilities"`
	RequiredTools  []string `json:"requiredTools"`
	CriterionIDs   []string `json:"criterionIds,omitempty"`
}

type CapabilityCoverageItem struct {
	RequirementID string   `json:"requirementId"`
	AgentID       string   `json:"agentId,omitempty"`
	RuntimeID     string   `json:"runtimeId,omitempty"`
	ToolNames     []string `json:"toolNames,omitempty"`
	Ready         bool     `json:"ready"`
	Missing       []string `json:"missing,omitempty"`
}

type CapabilityCoverage struct {
	Ready bool                     `json:"ready"`
	Items []CapabilityCoverageItem `json:"items"`
}

type QuestToolLease struct {
	ID                string    `json:"id"`
	QuestID           string    `json:"questId"`
	WorkspaceID       string    `json:"workspaceId"`
	EnvironmentDigest string    `json:"environmentDigest"`
	ToolNames         []string  `json:"toolNames"`
	ExpiresAt         time.Time `json:"expiresAt"`
}

type DeliveryTarget struct {
	Kind          string `json:"kind"` // local_branch | workspace
	WorkspacePath string `json:"workspacePath,omitempty"`
	Branch        string `json:"branch,omitempty"`
	RemotePublish bool   `json:"remotePublish"`
}

type CriterionEvidence struct {
	CriterionID string `json:"criterionId"`
	Satisfied   bool   `json:"satisfied"`
	Tool        string `json:"tool,omitempty"`
	Command     string `json:"command,omitempty"`
	ExitCode    *int   `json:"exitCode,omitempty"`
	DurationMs  int64  `json:"durationMs,omitempty"`
	Summary     string `json:"summary,omitempty"`
	ArtifactID  string `json:"artifactId,omitempty"`
}

type VerificationCheck struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Command    string `json:"command,omitempty"`
	ExitCode   *int   `json:"exitCode,omitempty"`
	Satisfied  bool   `json:"satisfied"`
	Summary    string `json:"summary,omitempty"`
	ArtifactID string `json:"artifactId,omitempty"`
	DurationMs int64  `json:"durationMs,omitempty"`
}

type EvidenceDecisionRecord struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Target    string    `json:"target,omitempty"`
	Action    string    `json:"action,omitempty"`
	Reason    string    `json:"reason,omitempty"`
	Status    string    `json:"status,omitempty"`
	CreatedAt time.Time `json:"createdAt,omitempty"`
}

type EvidenceBundle struct {
	Version                  int                      `json:"version"`
	ID                       string                   `json:"id"`
	QuestID                  string                   `json:"questId"`
	PointVersion             string                   `json:"pointVersion"`
	SourceDigest             string                   `json:"sourceDigest"`
	SourceVersions           []SourceSnapshotRef      `json:"sourceVersions"`
	BriefDigest              string                   `json:"briefDigest"`
	EnvironmentDigest        string                   `json:"environmentDigest"`
	StackPreset              StackPresetRef           `json:"stackPreset"`
	CompletionProfile        CompletionProfile        `json:"completionProfile"`
	DockerImages             []string                 `json:"dockerImages"`
	NetworkPolicyDigest      string                   `json:"networkPolicyDigest,omitempty"`
	CommitIDs                []string                 `json:"commitIds,omitempty"`
	ChangedFiles             []string                 `json:"changedFiles,omitempty"`
	Criteria                 []CriterionEvidence      `json:"criteria"`
	VerificationChecks       []VerificationCheck      `json:"verificationChecks"`
	ModelCalls               []ModelCallLedgerEntry   `json:"modelCalls"`
	ContextDisclosures       []ContextDisclosureEntry `json:"contextDisclosures"`
	ReproductionCommands     []string                 `json:"reproductionCommands,omitempty"`
	KnownLimitations         []string                 `json:"knownLimitations,omitempty"`
	SupervisionInterventions []EvidenceDecisionRecord `json:"supervisionInterventions,omitempty"`
	NetworkDecisions         []EvidenceDecisionRecord `json:"networkDecisions,omitempty"`
	WorkspaceRevision        string                   `json:"workspaceRevision,omitempty"`
	DeliveryVerified         bool                     `json:"deliveryVerified,omitempty"`
	DeliveryConflict         bool                     `json:"deliveryConflict,omitempty"`
	DeliveryTarget           string                   `json:"deliveryTarget,omitempty"`
	DeliveryReceipt          *DeliveryReceipt         `json:"deliveryReceipt,omitempty"`
	CreatedAt                time.Time                `json:"createdAt"`
}

type IntakeSession struct {
	ID           string                  `json:"id"`
	WorkspaceID  string                  `json:"workspaceId,omitempty"`
	QuestID      string                  `json:"questId,omitempty"`
	ProposalID   string                  `json:"proposalId,omitempty"`
	URL          string                  `json:"url"`
	Status       IntakeStatus            `json:"status"`
	Source       SourceBundle            `json:"source"`
	Environment  EnvironmentPlan         `json:"environment"`
	Requirements []CapabilityRequirement `json:"requirements,omitempty"`
	Coverage     CapabilityCoverage      `json:"coverage"`
	Brief        *TaskBrief              `json:"brief,omitempty"`
	Delivery     DeliveryTarget          `json:"delivery"`
	Evidence     *EvidenceBundle         `json:"evidence,omitempty"`
	Blockers     []string                `json:"blockers,omitempty"`
	Error        string                  `json:"error,omitempty"`
	CreatedAt    time.Time               `json:"createdAt"`
	UpdatedAt    time.Time               `json:"updatedAt"`
}
