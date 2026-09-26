package domain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// WorkOrder is the single review surface owned by Master in Agent Hub v2.
// Model output may draft it, but only NormalizeWorkOrder and ApproveWorkOrder
// may create the immutable contract used by execution.
type WorkOrder struct {
	ID              string                `json:"id"`
	ProposalID      string                `json:"proposalId,omitempty"`
	Digest          string                `json:"digest,omitempty"`
	WorkspaceID     string                `json:"workspaceId,omitempty"`
	ConversationID  string                `json:"conversationId,omitempty"`
	Version         int                   `json:"version"`
	ApprovedVersion int                   `json:"approvedVersion,omitempty"`
	ApprovedDigest  string                `json:"approvedDigest,omitempty"`
	State           string                `json:"state"` // discussion | ready | approved
	Goal            string                `json:"goal"`
	Scope           []string              `json:"scope"`
	OutOfScope      []string              `json:"outOfScope,omitempty"`
	Assumptions     []string              `json:"assumptions,omitempty"`
	OpenQuestions   []string              `json:"openQuestions,omitempty"`
	Sources         []SourceSnapshotRef   `json:"sources,omitempty"`
	Criteria        []AcceptanceCriterion `json:"criteria"`
	Milestones      []MilestonePlan       `json:"milestones"`
	Workspace       WorkspacePlan         `json:"workspace"`
	Stack           StackPresetRef        `json:"stack"`
	Setup           SetupPlan             `json:"setupPlan,omitempty"`
	Roster          AgentRosterPlan       `json:"roster"`
	Routing         ModelRoutingPolicy    `json:"routing"`
	Network         []NetworkGrant        `json:"network,omitempty"`
	Secrets         []SecretRequirement   `json:"secrets,omitempty"`
	Budget          BudgetEnvelope        `json:"budget"`
	Completion      CompletionProfile     `json:"completion"`
	Delivery        DeliveryPolicy        `json:"delivery"`
	Runtime         *WorkOrderRuntime     `json:"runtime,omitempty"`
	CreatedAt       time.Time             `json:"createdAt"`
	UpdatedAt       time.Time             `json:"updatedAt"`
}

// WorkOrderRuntime is derived operational state. It is not part of the
// approved digest and can change while the immutable WorkOrder stays fixed.
type WorkOrderRuntime struct {
	QuestID        string      `json:"questId"`
	Status         QuestStatus `json:"status"`
	Assurance      string      `json:"assurance,omitempty"`
	OutcomeSummary string      `json:"outcomeSummary,omitempty"`
	Message        string      `json:"message,omitempty"`
	// LaunchPhase is the durable, user-visible step before a FlowRun exists.
	// Planning can take minutes; a single `preflight` status made that time look
	// like a frozen button rather than active work.
	LaunchPhase string `json:"launchPhase,omitempty"`
	// ResumeAfterRestart marks a launch the core's own shutdown interrupted
	// before any work ran. The extension resumes it once, with the key the
	// core does not hold.
	ResumeAfterRestart bool       `json:"resumeAfterRestart,omitempty"`
	LaunchStartedAt    *time.Time `json:"launchStartedAt,omitempty"`
	FlowID             string     `json:"flowId,omitempty"`
	FlowRunID          string     `json:"flowRunId,omitempty"`
	// AgentIDs — исполнители, созданные утверждением наряда. Карточка обещала
	// «будет создан» и после утверждения обязана показать, что он создан.
	AgentIDs        []string           `json:"agentIds,omitempty"`
	DeliveryReceipt *DeliveryReceipt   `json:"deliveryReceipt,omitempty"`
	Evidence        *EvidenceBundle    `json:"evidence,omitempty"`
	Milestones      []MilestoneRuntime `json:"milestones,omitempty"`
	// Stall — причина, по которой выполнение стоит. Раньше текст отказа жил
	// только в flowRun.NodeStates и не доходил ни до карточки, ни до человека.
	Stall *WorkOrderStall `json:"stall,omitempty"`
	// Stages — этапы Flow для экрана выполнения. Наблюдатель и так опрашивает
	// наряд раз в 2.5 с, поэтому экрану не нужен полный /api/state/runtime.
	Stages []WorkOrderStage `json:"stages,omitempty"`
	// PlannerNote — предупреждение, что план собран движком Point, а не моделью.
	PlannerNote string    `json:"plannerNote,omitempty"`
	UpdatedAt   time.Time `json:"updatedAt,omitempty"`
}

// WorkOrderStall — узел Flow, который не пошёл дальше, и текст его отказа.
type WorkOrderStall struct {
	NodeID     string `json:"nodeId,omitempty"`
	NodeName   string `json:"nodeName,omitempty"`
	WaitReason string `json:"waitReason"`
	Error      string `json:"error,omitempty"`
}

// WorkOrderStage — этап Flow в виде, пригодном для экрана выполнения.
type WorkOrderStage struct {
	ID          string     `json:"id"`
	Name        string     `json:"name,omitempty"`
	Kind        string     `json:"kind,omitempty"`
	Status      string     `json:"status,omitempty"`
	AgentID     string     `json:"agentId,omitempty"`
	ExecutionID string     `json:"executionId,omitempty"`
	RunID       string     `json:"runId,omitempty"`
	WaitReason  string     `json:"waitReason,omitempty"`
	StartedAt   *time.Time `json:"startedAt,omitempty"`
	FinishedAt  *time.Time `json:"finishedAt,omitempty"`
}

type SourceSnapshotRef struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"` // text | image | workspace_file | local_file | url | git
	Label     string `json:"label,omitempty"`
	Locator   string `json:"locator,omitempty"`
	Digest    string `json:"digest"`
	MediaType string `json:"mediaType,omitempty"`
}

type WorkspacePlan struct {
	Mode          string `json:"mode"` // existing | managed
	Path          string `json:"path"`
	Isolation     string `json:"isolation"` // git_worktree | snapshot | live_write
	InitializeGit bool   `json:"initializeGit,omitempty"`
	ExpertOptIn   bool   `json:"expertOptIn,omitempty"`
}

type StackPresetRef struct {
	ID       string `json:"id"`
	Version  string `json:"version"`
	Category string `json:"category"`
	Source   string `json:"source"` // explicit | preference | benchmark
}

// SetupPlan is approved together with the WorkOrder. Bootstrap may execute
// only these commands and write only these files; ExpectedPaths are its gate.
type SetupPlan struct {
	ID            string         `json:"id,omitempty"`
	Version       string         `json:"version,omitempty"`
	Commands      []SetupCommand `json:"commands,omitempty"`
	Files         []SetupFile    `json:"files,omitempty"`
	ExpectedPaths []string       `json:"expectedPaths,omitempty"`
	OwnedPaths    []string       `json:"ownedPaths,omitempty"`
}

type SetupCommand struct {
	Command        string `json:"command"`
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty"`
}

type SetupFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type AgentDraft struct {
	ID              string   `json:"id"`
	BlueprintID     string   `json:"blueprintId,omitempty"`
	Existing        bool     `json:"existing,omitempty"`
	Name            string   `json:"name"`
	Role            string   `json:"role"`
	Mission         string   `json:"mission"`
	RequiredTools   []string `json:"requiredTools,omitempty"`
	ProjectOnly     bool     `json:"projectOnly,omitempty"`
	RequiresConsent bool     `json:"requiresConsent"`
}

type SubagentPlan struct {
	ParentAgentID string   `json:"parentAgentId"`
	Role          string   `json:"role"`
	Mission       string   `json:"mission"`
	RequiredTools []string `json:"requiredTools,omitempty"`
}

type AgentRosterPlan struct {
	// AgentIDs is the canonical v2 roster. Permanent/Temporary remain readable
	// for historical WorkOrders written before the dedicated selector existed.
	AgentIDs  []string       `json:"agentIds,omitempty"`
	Permanent []AgentDraft   `json:"permanent,omitempty"`
	Temporary []SubagentPlan `json:"temporary,omitempty"`
}

type ModelRoutingPolicy struct {
	Mode               string                    `json:"mode"` // fixed | auto
	RouterConnectionID string                    `json:"routerConnectionId,omitempty"`
	RouterModel        string                    `json:"routerModel,omitempty"`
	FixedConnectionID  string                    `json:"fixedConnectionId,omitempty"`
	FixedModel         string                    `json:"fixedModel,omitempty"`
	FallbackMode       string                    `json:"fallbackMode"` // auto | fixed | wait
	FallbackModels     []string                  `json:"fallbackModels,omitempty"`
	MaxWaitSeconds     int                       `json:"maxWaitSeconds,omitempty"`
	CostKnown          bool                      `json:"costKnown"`
	Experimental       bool                      `json:"experimental,omitempty"`
	Certification      string                    `json:"certification"` // certified | experimental
	Adapter            AdapterCapabilityManifest `json:"adapter"`
}

type AdapterCapabilityManifest struct {
	Tools            bool     `json:"tools"`
	StructuredOutput bool     `json:"structuredOutput"`
	PauseResume      bool     `json:"pauseResume"`
	ContextTokens    int      `json:"contextTokens"`
	CostVisibility   string   `json:"costVisibility"` // known | unknown
	AllowedRoles     []string `json:"allowedRoles,omitempty"`
}

type NetworkGrant struct {
	Host    string `json:"host"` // exact FQDN:port
	Purpose string `json:"purpose"`
}

// SecretRequirement deliberately has no value field. Secret bytes belong to
// the IDE secret host and are referenced only by SecretRef.
type SecretRequirement struct {
	Name      string `json:"name"`
	Purpose   string `json:"purpose"`
	SecretRef string `json:"secretRef,omitempty"`
	Satisfied bool   `json:"satisfied"`
	Required  bool   `json:"required"`
}

type BudgetEnvelope struct {
	Preset           string `json:"preset"` // small | medium | large
	Tokens           int64  `json:"tokens"`
	CostCents        int64  `json:"costCents,omitempty"`
	ActiveSeconds    int    `json:"activeSeconds"`
	MaxParallel      int    `json:"maxParallel"`
	MaxReplans       int    `json:"maxReplans"`
	MaxAttempts      int    `json:"maxAttempts"`
	MaxSteps         int    `json:"maxSteps"`
	MaxProjectAgents int    `json:"maxProjectAgents"`
}

type DeliveryPolicy struct {
	ApplyMode           string `json:"applyMode"`  // automatic | manual
	CommitMode          string `json:"commitMode"` // squash | staged | none
	KeepPartialDays     int    `json:"keepPartialDays"`
	KeepServicesRunning bool   `json:"keepServicesRunning"`
	ApplicationURL      string `json:"applicationUrl,omitempty"`
}

// CompletionCheckAcceptance is the one profile entry proved by the acceptance
// criteria themselves instead of by a command, so it carries no command.
const CompletionCheckAcceptance = "acceptance"

// CompletionCheck is one machine-verifiable requirement of the completion
// profile. The command is approved with the rest of the WorkOrder and enters
// the approval digest, so nothing at runtime can substitute a weaker check.
type CompletionCheck struct {
	Kind             string `json:"kind"`
	Command          string `json:"command,omitempty"`
	URL              string `json:"url,omitempty"`
	ExpectedExitCode int    `json:"expectedExitCode,omitempty"`
}

type CompletionProfile struct {
	ID      string            `json:"id"`
	Version string            `json:"version"`
	Checks  []CompletionCheck `json:"checks"`
}

// CompletionCheckEvidenceID namespaces profile evidence so that a criterion
// check can never accidentally stand in for a required profile check.
func CompletionCheckEvidenceID(kind string) string {
	return "completion:" + strings.ToLower(strings.TrimSpace(kind))
}

type WorkOrderApproval struct {
	WorkOrder WorkOrder `json:"workOrder"`
	QuestID   string    `json:"questId"`
	AgentIDs  []string  `json:"agentIds,omitempty"`
	FlowID    string    `json:"flowId,omitempty"`
	FlowRunID string    `json:"flowRunId,omitempty"`
	Status    string    `json:"status"`
	Message   string    `json:"message,omitempty"`
	Replayed  bool      `json:"replayed,omitempty"`
}

func NormalizeWorkOrder(in WorkOrder) WorkOrder {
	in.Digest = ""
	in.Runtime = nil
	in.ApprovedVersion, in.ApprovedDigest = 0, ""
	if in.ID == "" {
		in.ID = NewID("workorder")
	}
	if in.Version <= 0 {
		in.Version = 1
	}
	if in.State != "discussion" && in.State != "staffing" && in.State != "ready" {
		in.State = "discussion"
	}
	in.Goal = strings.TrimSpace(in.Goal)
	in.Scope = briefStrings(in.Scope)
	in.OutOfScope = briefStrings(in.OutOfScope)
	in.Assumptions = briefStrings(in.Assumptions)
	in.OpenQuestions = briefStrings(in.OpenQuestions)
	if len(in.OpenQuestions) > 2 {
		in.OpenQuestions = in.OpenQuestions[:2]
	}
	for i := range in.Roster.Permanent {
		if in.Roster.Permanent[i].ID == "" {
			in.Roster.Permanent[i].ID = NewID("agentdraft")
		}
		in.Roster.Permanent[i].Name = strings.TrimSpace(in.Roster.Permanent[i].Name)
		in.Roster.Permanent[i].Role = strings.TrimSpace(in.Roster.Permanent[i].Role)
		in.Roster.Permanent[i].Mission = strings.TrimSpace(in.Roster.Permanent[i].Mission)
	}
	for i := range in.Network {
		in.Network[i].Host = strings.ToLower(strings.TrimSpace(in.Network[i].Host))
		in.Network[i].Purpose = strings.TrimSpace(in.Network[i].Purpose)
	}
	sort.Slice(in.Network, func(i, j int) bool { return in.Network[i].Host < in.Network[j].Host })
	if in.Budget.Preset == "" {
		in.Budget = BudgetEnvelope{Preset: "medium", Tokens: 200000, ActiveSeconds: 3600, MaxParallel: 2, MaxReplans: 6, MaxAttempts: 3, MaxProjectAgents: 2}
	}
	if in.Delivery.ApplyMode == "" {
		in.Delivery.ApplyMode = "automatic"
	}
	if strings.TrimSpace(in.Completion.ID) == "" {
		in.Completion = CompletionProfile{ID: "criteria", Version: "1", Checks: []CompletionCheck{{Kind: CompletionCheckAcceptance}}}
	}
	in.Completion.ID = strings.TrimSpace(in.Completion.ID)
	in.Completion.Version = strings.TrimSpace(in.Completion.Version)
	in.Completion.Checks = normalizeCompletionChecks(in.Completion.Checks)
	in.Setup.ID = strings.TrimSpace(in.Setup.ID)
	in.Setup.Version = strings.TrimSpace(in.Setup.Version)
	in.Setup.ExpectedPaths = briefStrings(in.Setup.ExpectedPaths)
	in.Setup.OwnedPaths = briefStrings(in.Setup.OwnedPaths)
	for index := range in.Setup.Commands {
		in.Setup.Commands[index].Command = strings.TrimSpace(in.Setup.Commands[index].Command)
		if in.Setup.Commands[index].TimeoutSeconds <= 0 {
			in.Setup.Commands[index].TimeoutSeconds = 600
		}
	}
	for index := range in.Setup.Files {
		in.Setup.Files[index].Path = filepath.ToSlash(strings.TrimSpace(in.Setup.Files[index].Path))
	}
	if in.Delivery.CommitMode == "" {
		in.Delivery.CommitMode = "squash"
	}
	if in.Delivery.KeepPartialDays == 0 {
		in.Delivery.KeepPartialDays = 30
	}
	in.Delivery.ApplicationURL = strings.TrimSpace(in.Delivery.ApplicationURL)
	if in.Routing.Mode == "" {
		in.Routing.Mode = "fixed"
	}
	if in.Routing.Certification == "" {
		in.Routing.Certification = "experimental"
		if in.Routing.Mode == "fixed" {
			in.Routing.Experimental = true
		}
	}
	if in.Routing.FallbackMode == "" {
		in.Routing.FallbackMode = "auto"
	}
	if in.Workspace.Isolation == "" {
		in.Workspace.Isolation = "snapshot"
	}
	if in.Budget.MaxSteps <= 0 {
		in.Budget.MaxSteps = 64
	}
	in.Criteria = append([]AcceptanceCriterion(nil), in.Criteria...)
	for index := range in.Criteria {
		// Same synonym repair as a task brief draft: "shell" is not a tool.
		in.Criteria[index].Tool = CanonicalToolName(in.Criteria[index].Tool)
	}
	if len(in.Milestones) == 0 && strings.TrimSpace(in.Goal) != "" {
		criterionIDs := make([]string, 0, len(in.Criteria))
		for _, criterion := range in.Criteria {
			criterionIDs = append(criterionIDs, criterion.ID)
		}
		in.Milestones = []MilestonePlan{{ID: "milestone-1", Goal: in.Goal, Scope: append([]string(nil), in.Scope...), CriterionIDs: criterionIDs}}
	}
	for index := range in.Milestones {
		milestone := &in.Milestones[index]
		milestone.ID = strings.TrimSpace(milestone.ID)
		milestone.Goal = strings.TrimSpace(milestone.Goal)
		milestone.Scope = briefStrings(milestone.Scope)
		milestone.CriterionIDs = briefStrings(milestone.CriterionIDs)
		milestone.DependsOn = briefStrings(milestone.DependsOn)
		milestone.OwnedPaths = briefStrings(milestone.OwnedPaths)
	}
	now := time.Now().UTC()
	if in.CreatedAt.IsZero() {
		in.CreatedAt = now
	}
	in.UpdatedAt = now
	return in
}

func WorkOrderDigest(order WorkOrder) string {
	order.Digest = ""
	order.Runtime = nil
	order.ApprovedVersion, order.ApprovedDigest = 0, ""
	order.State = ""
	order.UpdatedAt = time.Time{}
	data, _ := json.Marshal(order)
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func ApproveWorkOrder(order WorkOrder) (WorkOrder, error) {
	if err := ValidateWorkOrder(order); err != nil {
		return WorkOrder{}, err
	}
	if order.State != "ready" {
		return WorkOrder{}, errors.New("work order is not ready")
	}
	order.ApprovedVersion = order.Version
	order.ApprovedDigest = WorkOrderDigest(order)
	order.State = "approved"
	order.UpdatedAt = time.Now().UTC()
	return order, nil
}

func IsApprovedWorkOrder(order WorkOrder) bool {
	return order.State == "approved" && order.ApprovedVersion == order.Version && order.ApprovedDigest != "" && order.ApprovedDigest == WorkOrderDigest(order)
}

func workOrderDefinitionComplete(state string) bool {
	return state == "staffing" || state == "ready" || state == "approved"
}

func ValidateWorkOrder(order WorkOrder) error {
	var problems []string
	if strings.TrimSpace(order.ID) == "" || order.Version <= 0 {
		problems = append(problems, "id and positive version are required")
	}
	if strings.TrimSpace(order.Goal) == "" {
		problems = append(problems, "goal is required")
	}
	if len(order.Criteria) == 0 {
		problems = append(problems, "at least one acceptance criterion is required")
	}
	criterionIDs := map[string]bool{}
	for _, criterion := range order.Criteria {
		if strings.TrimSpace(criterion.ID) == "" || strings.TrimSpace(criterion.Text) == "" || criterionIDs[criterion.ID] {
			problems = append(problems, "criteria require unique ids and text")
			break
		}
		criterionIDs[criterion.ID] = true
		switch criterion.Kind {
		case "manual":
			if criterion.Tool != "" || len(criterion.Arguments) > 0 || criterion.ExpectedExitCode != nil {
				problems = append(problems, fmt.Sprintf("manual criterion %q cannot claim tool evidence", criterion.ID))
			}
		case "verification", "reproduction":
			if workOrderDefinitionComplete(order.State) {
				trimmed := bytes.TrimSpace(criterion.Arguments)
				if strings.TrimSpace(criterion.Tool) == "" || len(trimmed) == 0 || !json.Valid(trimmed) || trimmed[0] != '{' {
					problems = append(problems, fmt.Sprintf("criterion %q requires a tool and JSON object arguments", criterion.ID))
				}
			}
		default:
			problems = append(problems, fmt.Sprintf("unsupported criterion kind %q", criterion.Kind))
		}
	}
	milestoneIDs := map[string]bool{}
	criterionOwners := map[string]string{}
	for _, milestone := range order.Milestones {
		if milestone.ID == "" || milestone.Goal == "" || milestoneIDs[milestone.ID] {
			problems = append(problems, "milestones require unique ids and goals")
			continue
		}
		milestoneIDs[milestone.ID] = true
	}
	for _, milestone := range order.Milestones {
		for _, criterionID := range milestone.CriterionIDs {
			if !criterionIDs[criterionID] {
				problems = append(problems, fmt.Sprintf("milestone %q references unknown criterion %q", milestone.ID, criterionID))
				break
			}
			if owner := criterionOwners[criterionID]; owner != "" && owner != milestone.ID {
				problems = append(problems, fmt.Sprintf("criterion %q belongs to more than one milestone", criterionID))
				break
			}
			criterionOwners[criterionID] = milestone.ID
		}
	}
	for _, milestone := range order.Milestones {
		for _, dependency := range milestone.DependsOn {
			if dependency == milestone.ID || !milestoneIDs[dependency] {
				problems = append(problems, fmt.Sprintf("milestone %q has invalid dependency %q", milestone.ID, dependency))
				break
			}
		}
	}
	if milestonePlanHasCycle(order.Milestones) {
		problems = append(problems, "milestone dependencies contain a cycle")
	}
	for criterionID := range criterionIDs {
		if criterionOwners[criterionID] == "" {
			problems = append(problems, fmt.Sprintf("criterion %q is not assigned to a milestone", criterionID))
			break
		}
	}
	for _, source := range order.Sources {
		if strings.TrimSpace(source.ID) == "" || strings.TrimSpace(source.Digest) == "" {
			problems = append(problems, "every source reference requires an id and digest")
			break
		}
	}
	if len(order.OpenQuestions) > 2 || workOrderDefinitionComplete(order.State) && len(order.OpenQuestions) > 0 {
		problems = append(problems, "staffing or ready work order cannot have open questions")
	}
	if order.Workspace.Mode != "existing" && order.Workspace.Mode != "managed" {
		problems = append(problems, "workspace mode must be existing or managed")
	}
	if strings.TrimSpace(order.Workspace.Path) == "" {
		problems = append(problems, "workspace path is required")
	}
	if workOrderDefinitionComplete(order.State) && (strings.TrimSpace(order.Stack.ID) == "" || strings.TrimSpace(order.Stack.Version) == "" || strings.TrimSpace(order.Stack.Category) == "") {
		problems = append(problems, "ready work order requires a versioned stack preset")
	}
	if workOrderDefinitionComplete(order.State) {
		if order.Setup.ID != "" {
			if order.Setup.Version == "" || len(order.Setup.ExpectedPaths) == 0 {
				problems = append(problems, "setup plan requires a version and expected paths")
			}
			for _, command := range order.Setup.Commands {
				if command.Command == "" || len(command.Command) > 4096 || command.TimeoutSeconds <= 0 || command.TimeoutSeconds > 1800 {
					problems = append(problems, "setup plan contains an invalid command")
					break
				}
			}
			paths := append(append([]string(nil), order.Setup.ExpectedPaths...), order.Setup.OwnedPaths...)
			for _, file := range order.Setup.Files {
				paths = append(paths, file.Path)
			}
			for _, path := range paths {
				clean := filepath.Clean(strings.TrimSpace(path))
				if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
					problems = append(problems, "setup plan paths must stay inside the workspace")
					break
				}
			}
		}
		if order.Completion.ID == "" || order.Completion.Version == "" || len(order.Completion.Checks) == 0 {
			problems = append(problems, "ready work order requires a versioned completion profile")
		} else {
			seenChecks := map[string]bool{}
			for _, check := range order.Completion.Checks {
				if !supportedCompletionCheckV2(check.Kind) || seenChecks[check.Kind] {
					problems = append(problems, fmt.Sprintf("unsupported or duplicate completion check %q", check.Kind))
					break
				}
				seenChecks[check.Kind] = true
				// A named check nobody can execute is how a profile turns into a
				// promise: every kind but acceptance carries its own command.
				if check.Kind != CompletionCheckAcceptance && (check.Command == "" || len(check.Command) > 4096) {
					problems = append(problems, fmt.Sprintf("completion check %q requires an approved command", check.Kind))
					break
				}
			}
			if !seenChecks[CompletionCheckAcceptance] {
				problems = append(problems, "completion profile must require the acceptance criteria")
			}
		}
	}
	if order.Workspace.Isolation != "snapshot" && order.Workspace.Isolation != "git_worktree" && order.Workspace.Isolation != "live_write" {
		problems = append(problems, "unsupported workspace isolation")
	}
	if order.Workspace.Isolation == "live_write" && !order.Workspace.ExpertOptIn {
		problems = append(problems, "live_write requires explicit expert opt-in")
	}
	if order.Routing.Mode != "fixed" && order.Routing.Mode != "auto" {
		problems = append(problems, "routing mode must be fixed or auto")
	}
	if order.Routing.Mode == "auto" && !order.Routing.CostKnown {
		problems = append(problems, "auto routing requires known model cost")
	}
	if order.Routing.Mode == "auto" && (order.Routing.Certification != "certified" || !order.Routing.Adapter.StructuredOutput || order.Routing.Adapter.ContextTokens <= 0) {
		problems = append(problems, "auto routing requires a certified router with structured output and known context")
	}
	if order.Routing.Certification != "certified" && order.Routing.Certification != "experimental" {
		problems = append(problems, "routing certification must be certified or experimental")
	}
	if order.Routing.Mode == "fixed" && (strings.TrimSpace(order.Routing.FixedConnectionID) == "" || strings.TrimSpace(order.Routing.FixedModel) == "") {
		problems = append(problems, "fixed routing requires a connection and model")
	}
	permanentIDs := make(map[string]bool, len(order.Roster.Permanent))
	for _, draft := range order.Roster.Permanent {
		permanentIDs[draft.ID] = true
		if draft.Existing {
			if strings.TrimSpace(draft.ID) == "" {
				problems = append(problems, "existing agents require an id")
				break
			}
			continue
		}
		if draft.Name == "" || draft.Role == "" || draft.Mission == "" {
			problems = append(problems, "permanent agent drafts require name, role and mission")
			break
		}
		if draft.BlueprintID == "" && !draft.RequiresConsent {
			problems = append(problems, "new permanent agents require explicit consent in the approval card")
			break
		}
		if draft.ProjectOnly && !draft.RequiresConsent {
			problems = append(problems, "project-only agents require explicit user choice")
			break
		}
	}
	if order.Budget.MaxProjectAgents > 0 && len(order.Roster.Permanent) > order.Budget.MaxProjectAgents {
		problems = append(problems, "agent roster exceeds the project-agent budget")
	}
	for _, temporary := range order.Roster.Temporary {
		if !permanentIDs[temporary.ParentAgentID] || strings.TrimSpace(temporary.Role) == "" || strings.TrimSpace(temporary.Mission) == "" {
			problems = append(problems, "temporary subagents require an approved parent, role and mission")
			break
		}
	}
	for _, grant := range order.Network {
		if err := validateExactTLSHost(grant.Host); err != nil || strings.TrimSpace(grant.Purpose) == "" {
			problems = append(problems, "network grants require exact public FQDN:port and purpose")
			break
		}
	}
	for _, secret := range order.Secrets {
		if workOrderDefinitionComplete(order.State) && secret.Required && !secret.Satisfied {
			problems = append(problems, fmt.Sprintf("required secret %q is not satisfied", secret.Name))
		}
	}
	if order.Budget.Tokens <= 0 || order.Budget.ActiveSeconds <= 0 || order.Budget.MaxParallel < 1 || order.Budget.MaxParallel > 8 || order.Budget.MaxAttempts < 1 || order.Budget.MaxSteps < 1 || order.Budget.MaxProjectAgents < 0 || order.Budget.MaxProjectAgents > 8 {
		problems = append(problems, "invalid budget envelope")
	}
	if order.Routing.Mode == "fixed" && !order.Routing.CostKnown && (order.Budget.Tokens <= 0 || order.Budget.ActiveSeconds <= 0 || order.Budget.MaxSteps <= 0) {
		problems = append(problems, "fixed routing with unknown cost requires token, time and step caps")
	}
	if order.Delivery.ApplyMode != "automatic" && order.Delivery.ApplyMode != "manual" {
		problems = append(problems, "delivery applyMode must be automatic or manual")
	}
	if order.Delivery.ApplicationURL != "" {
		parsed, err := url.Parse(order.Delivery.ApplicationURL)
		host := strings.ToLower(parsed.Hostname())
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.Fragment != "" ||
			(host != "localhost" && host != "127.0.0.1" && host != "::1") {
			problems = append(problems, "delivery applicationUrl must be an absolute local HTTP(S) URL")
		}
	}
	if order.Delivery.KeepServicesRunning && order.Delivery.ApplicationURL == "" {
		problems = append(problems, "running delivered services require applicationUrl")
	}
	if order.Delivery.KeepPartialDays != 30 {
		problems = append(problems, "partial results must be retained for 30 days")
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func normalizeCompletionChecks(items []CompletionCheck) []CompletionCheck {
	result := make([]CompletionCheck, 0, len(items))
	for _, item := range items {
		item.Kind = strings.ToLower(strings.TrimSpace(item.Kind))
		item.Command = strings.TrimSpace(item.Command)
		item.URL = strings.TrimSpace(item.URL)
		if item.Kind == "" {
			continue
		}
		if item.Kind == CompletionCheckAcceptance {
			item.Command, item.ExpectedExitCode = "", 0
		}
		result = append(result, item)
	}
	return result
}

func supportedCompletionCheckV2(value string) bool {
	switch value {
	case "acceptance", "build", "migrations", "automated_tests", "service_start", "health", "http_smoke",
		"browser_journey", "browser_errors", "accessibility", "screenshots", "fixtures", "live_smoke",
		"dependency_audit", "secret_scan", "container_config", "performance", "readme", "cli_smoke":
		return true
	default:
		return false
	}
}

func milestonePlanHasCycle(milestones []MilestonePlan) bool {
	dependencies := make(map[string][]string, len(milestones))
	for _, milestone := range milestones {
		dependencies[milestone.ID] = milestone.DependsOn
	}
	state := map[string]uint8{}
	var visit func(string) bool
	visit = func(id string) bool {
		switch state[id] {
		case 1:
			return true
		case 2:
			return false
		}
		state[id] = 1
		for _, dependencyID := range dependencies[id] {
			if _, exists := dependencies[dependencyID]; exists && visit(dependencyID) {
				return true
			}
		}
		state[id] = 2
		return false
	}
	for id := range dependencies {
		if visit(id) {
			return true
		}
	}
	return false
}

func validateExactTLSHost(value string) error {
	host, port, err := net.SplitHostPort(strings.TrimSpace(value))
	if err != nil || port != "443" || net.ParseIP(host) != nil || strings.ContainsAny(host, "*/") || !strings.Contains(host, ".") {
		return errors.New("host must be an exact TLS FQDN on port 443")
	}
	if number, err := strconv.Atoi(port); err != nil || number != 443 {
		return errors.New("port must be 443")
	}
	return nil
}
