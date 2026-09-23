package domain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

type TaskMode string

const (
	TaskModePrecise   TaskMode = "precise"
	TaskModeProject   TaskMode = "project"
	TaskModeUndecided TaskMode = "undecided"
)

// TaskBrief records the user's intended result independently of the execution plan.
// Approval fields are server-owned. A digest detects content changes, not user identity.
// Only a handler that has verified a user's approval may call ApproveTaskBrief.
type TaskBrief struct {
	SourceRequest   string                `json:"sourceRequest,omitempty"`
	Version         int                   `json:"version"`
	ApprovedVersion int                   `json:"approvedVersion,omitempty"`
	State           string                `json:"state"`
	Mode            TaskMode              `json:"mode"`
	Goal            string                `json:"goal"`
	ResultKind      string                `json:"resultKind"`
	Audience        string                `json:"audience,omitempty"`
	Scope           []string              `json:"scope,omitempty"`
	OutOfScope      []string              `json:"outOfScope,omitempty"`
	OpenQuestions   []string              `json:"openQuestions,omitempty"`
	Decisions       []BriefDecision       `json:"decisions,omitempty"`
	Criteria        []AcceptanceCriterion `json:"criteria,omitempty"`
	Permissions     TaskPermissions       `json:"permissions"`
	Budget          TaskBudget            `json:"budget"`
	// WorkOrder preserves the v2 execution contract when the established Flow
	// engine is used as a low-level runtime. It prevents routing, source,
	// isolation and delivery authority from being collapsed into legacy brief
	// fields while v2 milestones are compiled and executed.
	WorkOrder *WorkOrderExecutionContract `json:"workOrder,omitempty"`
	// FastAgent marks Cursor-style daily runs: precise write tasks may
	// auto-approve propose_patch without a Docker OS boundary. Commands stay ASK.
	FastAgent      bool   `json:"fastAgent,omitempty"`
	ApprovedDigest string `json:"approvedDigest,omitempty"`
}

type BriefDecision struct {
	Topic    string `json:"topic"`
	Decision string `json:"decision"`
	Source   string `json:"source"`
}

type AcceptanceCriterion struct {
	ID               string          `json:"id"`
	Text             string          `json:"text"`
	Kind             string          `json:"kind"` // verification | manual | reproduction
	Tool             string          `json:"tool,omitempty"`
	Arguments        json.RawMessage `json:"arguments,omitempty"`
	ExpectedExitCode *int            `json:"expectedExitCode,omitempty"`
}

type TaskPermissions struct {
	WriteFiles      bool `json:"writeFiles"`
	ExecuteCommands bool `json:"executeCommands"`
	// ProvisionProjectAgents is retained as a wire-compatible name. It grants
	// temporary subagents under an existing agent, never unattended creation
	// of a permanent roster peer.
	ProvisionProjectAgents bool     `json:"provisionProjectAgents,omitempty"`
	NetworkHosts           []string `json:"networkHosts,omitempty"`
	// ConfirmedGitRemotes are exact repository URLs the user approved (intake source or explicit allow).
	// Agents may not clone/fetch other remotes without Master→user escalation.
	ConfirmedGitRemotes []string `json:"confirmedGitRemotes,omitempty"`
}

type TaskBudget struct {
	Tokens           int64 `json:"tokens"`
	CostCents        int64 `json:"costCents,omitempty"`
	ActiveSeconds    int   `json:"activeSeconds"`
	MaxParallel      int   `json:"maxParallel"`
	MaxReplans       int   `json:"maxReplans"`
	MaxAttempts      int   `json:"maxAttempts"`
	MaxProjectAgents int   `json:"maxProjectAgents,omitempty"`
}

// WorkOrderExecutionContract is the immutable, secret-free subset of an
// approved WorkOrder required by the execution and delivery layers. Values of
// secrets are deliberately impossible to represent here.
type WorkOrderExecutionContract struct {
	ID           string              `json:"id"`
	Version      int                 `json:"version"`
	Digest       string              `json:"digest"`
	SourceDigest string              `json:"sourceDigest"`
	Sources      []SourceSnapshotRef `json:"sources,omitempty"`
	Milestones   []MilestonePlan     `json:"milestones"`
	Workspace    WorkspacePlan       `json:"workspace"`
	Stack        StackPresetRef      `json:"stack"`
	Setup        SetupPlan           `json:"setupPlan,omitempty"`
	Routing      ModelRoutingPolicy  `json:"routing"`
	Network      []NetworkGrant      `json:"network,omitempty"`
	Secrets      []SecretRequirement `json:"secrets,omitempty"`
	Completion   CompletionProfile   `json:"completion"`
	Delivery     DeliveryPolicy      `json:"delivery"`
}

// TaskBriefRevision is an immutable content snapshot. Brief omits approval state;
// the current proposal carries the approval bound to this version and digest.
type TaskBriefRevision struct {
	ProposalID  string    `json:"proposalId"`
	WorkspaceID string    `json:"workspaceId"`
	Version     int       `json:"version"`
	Digest      string    `json:"digest"`
	Brief       TaskBrief `json:"brief"`
	CreatedAt   time.Time `json:"createdAt"`
}

// NormalizeTaskBrief normalizes an untrusted draft and deliberately discards its
// claimed approval. Do not use it to load already approved persisted state.
// It never infers permissions from the task mode, result kind, or model text.
func NormalizeTaskBrief(b TaskBrief) TaskBrief {
	b.ApprovedVersion, b.ApprovedDigest = 0, ""
	if b.Version == 0 {
		b.Version = 1
	}
	if b.State != "discussion" && b.State != "ready" {
		b.State = "discussion"
	}
	if b.Mode == "" {
		b.Mode = TaskModeUndecided
	}
	b.Goal, b.ResultKind, b.Audience = strings.TrimSpace(b.Goal), strings.TrimSpace(b.ResultKind), strings.TrimSpace(b.Audience)
	b.Scope, b.OutOfScope, b.OpenQuestions = briefStrings(b.Scope), briefStrings(b.OutOfScope), briefStrings(b.OpenQuestions)
	b.Decisions = append([]BriefDecision(nil), b.Decisions...)
	for i := range b.Decisions {
		d := &b.Decisions[i]
		d.Topic, d.Decision, d.Source = strings.TrimSpace(d.Topic), strings.TrimSpace(d.Decision), strings.TrimSpace(d.Source)
	}
	b.Criteria = append([]AcceptanceCriterion(nil), b.Criteria...)
	for i := range b.Criteria {
		c := &b.Criteria[i]
		c.ID, c.Text, c.Kind, c.Tool = strings.TrimSpace(c.ID), strings.TrimSpace(c.Text), strings.TrimSpace(c.Kind), strings.TrimSpace(c.Tool)
		// A draft names the tool in the model's own words ("shell", "bash").
		// Resolve the synonym before approval signs the content, or the
		// criterion reaches launch naming a tool Point does not have.
		c.Tool = CanonicalToolName(c.Tool)
		c.Arguments = append(json.RawMessage(nil), c.Arguments...)
		if c.ExpectedExitCode != nil {
			value := *c.ExpectedExitCode
			c.ExpectedExitCode = &value
		}
	}
	b.Permissions.NetworkHosts = briefStrings(b.Permissions.NetworkHosts)
	for i := range b.Permissions.NetworkHosts {
		b.Permissions.NetworkHosts[i] = strings.ToLower(b.Permissions.NetworkHosts[i])
	}
	b.Permissions.ConfirmedGitRemotes = briefStrings(b.Permissions.ConfirmedGitRemotes)
	for i := range b.Permissions.ConfirmedGitRemotes {
		b.Permissions.ConfirmedGitRemotes[i] = strings.TrimSpace(b.Permissions.ConfirmedGitRemotes[i])
	}
	if b.WorkOrder != nil {
		contract := *b.WorkOrder
		contract.Sources = append([]SourceSnapshotRef(nil), contract.Sources...)
		contract.Milestones = append([]MilestonePlan(nil), contract.Milestones...)
		for index := range contract.Milestones {
			contract.Milestones[index].Scope = append([]string(nil), contract.Milestones[index].Scope...)
			contract.Milestones[index].CriterionIDs = append([]string(nil), contract.Milestones[index].CriterionIDs...)
			contract.Milestones[index].DependsOn = append([]string(nil), contract.Milestones[index].DependsOn...)
			contract.Milestones[index].OwnedPaths = append([]string(nil), contract.Milestones[index].OwnedPaths...)
		}
		contract.Network = append([]NetworkGrant(nil), contract.Network...)
		contract.Secrets = append([]SecretRequirement(nil), contract.Secrets...)
		contract.Routing.FallbackModels = append([]string(nil), contract.Routing.FallbackModels...)
		contract.Routing.Adapter.AllowedRoles = append([]string(nil), contract.Routing.Adapter.AllowedRoles...)
		b.WorkOrder = &contract
	}
	if b.Budget.Tokens == 0 {
		b.Budget.Tokens = 200000
	}
	if b.Budget.ActiveSeconds == 0 {
		b.Budget.ActiveSeconds = 3600
	}
	if b.Budget.MaxParallel == 0 {
		b.Budget.MaxParallel = 2
	}
	if b.Mode == TaskModePrecise {
		b.Budget.MaxParallel = 1
	}
	if b.Budget.MaxReplans == 0 {
		b.Budget.MaxReplans = 6
	}
	if b.Budget.MaxAttempts == 0 {
		b.Budget.MaxAttempts = 3
	}
	if b.Permissions.ProvisionProjectAgents && b.Budget.MaxProjectAgents == 0 {
		b.Budget.MaxProjectAgents = 1
		if b.Mode == TaskModeProject {
			b.Budget.MaxProjectAgents = 2
		}
	}
	return b
}

func briefStrings(input []string) []string {
	var output []string
	for _, value := range input {
		if value = strings.TrimSpace(value); value != "" {
			output = append(output, value)
		}
	}
	return output
}

type TaskBriefValidationIssue struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

type TaskBriefValidationError struct {
	Issues []TaskBriefValidationIssue
}

func (e *TaskBriefValidationError) Error() string {
	messages := make([]string, 0, len(e.Issues))
	for _, issue := range e.Issues {
		messages = append(messages, issue.Message)
	}
	return strings.Join(messages, "; ")
}

func ValidateTaskBrief(b TaskBrief) error {
	issues := ValidateTaskBriefIssues(b)
	if len(issues) == 0 {
		return nil
	}
	return &TaskBriefValidationError{Issues: issues}
}

// ValidateTaskBriefIssues reports every independently detectable defect so a
// model draft can be repaired in one bounded follow-up instead of one error per
// turn. Paths are JSON pointers into TaskBrief and also define repair sections.
func ValidateTaskBriefIssues(b TaskBrief) []TaskBriefValidationIssue {
	var issues []TaskBriefValidationIssue
	add := func(code, path, message string) {
		issues = append(issues, TaskBriefValidationIssue{Code: code, Path: path, Message: message})
	}
	if b.Version < 1 {
		add("invalid_version", "/version", "task brief version must be positive")
	}
	switch b.State {
	case "discussion", "ready", "approved", "executing":
	default:
		add("invalid_state", "/state", fmt.Sprintf("invalid task brief state %q", b.State))
	}
	switch b.Mode {
	case TaskModePrecise, TaskModeProject, TaskModeUndecided:
	default:
		add("invalid_mode", "/mode", fmt.Sprintf("invalid task mode %q", b.Mode))
	}
	switch b.ResultKind {
	case "", "code", "report", "workspace_change", "hub_tool":
	default:
		add("invalid_result_kind", "/resultKind", fmt.Sprintf("invalid task result kind %q", b.ResultKind))
	}
	if b.ResultKind != "workspace_change" && b.Permissions.WriteFiles {
		add("write_files_result_mismatch", "/permissions", "only a workspace_change task may authorize file changes")
	}
	if b.Budget.Tokens <= 0 || b.Budget.CostCents < 0 || b.Budget.ActiveSeconds <= 0 || b.Budget.MaxParallel <= 0 || b.Budget.MaxParallel > 8 || b.Budget.MaxReplans < 0 || b.Budget.MaxAttempts <= 0 || b.Budget.MaxProjectAgents < 0 || b.Budget.MaxProjectAgents > 8 {
		add("invalid_budget", "/budget", "task budget requires positive tokens/time/attempts, 1-8 parallel executions and nonnegative replans")
	}
	if b.Mode == TaskModePrecise && b.Budget.MaxParallel != 1 {
		add("precise_parallelism", "/budget", "precise tasks require one executor")
	}
	if b.FastAgent {
		if b.Mode != TaskModePrecise && (b.Mode != TaskModeProject || b.WorkOrder == nil) {
			add("fast_agent_mode", "/mode", "fast agent requires precise mode or a WorkOrder execution contract")
		}
		if b.ResultKind != "workspace_change" || !b.Permissions.WriteFiles {
			add("fast_agent_permissions", "/permissions", "fast agent requires workspace_change with writeFiles")
		}
	}
	if b.Permissions.ProvisionProjectAgents && b.Budget.MaxProjectAgents == 0 {
		add("provisioning_budget", "/budget", "project agent provisioning requires a positive maxProjectAgents budget")
	}
	for i, host := range b.Permissions.NetworkHosts {
		parsed, err := url.Parse("https://" + host)
		if err != nil || parsed.Host != host || parsed.Hostname() == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || strings.ContainsAny(host, "* \\\t\r\n") {
			add("invalid_network_host", fmt.Sprintf("/permissions/networkHosts/%d", i), fmt.Sprintf("network destination must be an explicit host, got %q", host))
		}
	}
	for i, remote := range b.Permissions.ConfirmedGitRemotes {
		if err := validateConfirmedGitRemote(remote); err != nil {
			add("invalid_git_remote", fmt.Sprintf("/permissions/confirmedGitRemotes/%d", i), err.Error())
		}
	}
	ids := make(map[string]bool)
	for i, c := range b.Criteria {
		path := fmt.Sprintf("/criteria/%d", i)
		if strings.TrimSpace(c.ID) == "" || strings.TrimSpace(c.Text) == "" || ids[c.ID] {
			add("invalid_criterion_identity", path, "criteria require unique IDs and text")
		}
		ids[c.ID] = true
		switch c.Kind {
		case "manual":
			if c.Tool != "" || len(c.Arguments) != 0 || c.ExpectedExitCode != nil {
				add("manual_criterion_evidence", path, fmt.Sprintf("manual criterion %q cannot claim tool evidence", c.ID))
			}
		case "verification", "reproduction":
			if strings.TrimSpace(c.Tool) == "" || len(c.Arguments) == 0 || !json.Valid(c.Arguments) || bytes.TrimSpace(c.Arguments)[0] != '{' {
				add("criterion_tool_arguments", path, fmt.Sprintf("criterion %q requires a tool and JSON object arguments", c.ID))
			}
			if c.ExpectedExitCode != nil && (*c.ExpectedExitCode < 0 || *c.ExpectedExitCode > 255 || (c.Kind == "verification" && *c.ExpectedExitCode != 0)) {
				add("criterion_exit_code", path, fmt.Sprintf("criterion %q has an invalid expected exit code", c.ID))
			}
			if c.Kind == "reproduction" && c.ExpectedExitCode == nil {
				add("reproduction_exit_code", path, fmt.Sprintf("reproduction criterion %q requires an expected exit code", c.ID))
			}
		default:
			add("invalid_criterion_kind", path, fmt.Sprintf("invalid criterion kind %q", c.Kind))
		}
	}
	for i, d := range b.Decisions {
		if strings.TrimSpace(d.Topic) == "" || strings.TrimSpace(d.Decision) == "" || strings.TrimSpace(d.Source) == "" {
			add("invalid_decision", fmt.Sprintf("/decisions/%d", i), "decisions require a topic, decision and source")
		}
	}
	if b.State != "discussion" {
		if b.Mode == TaskModeUndecided || b.Mode == "" {
			add("incomplete_mode", "/mode", "choose a task mode before approval")
		}
		if strings.TrimSpace(b.Goal) == "" || b.ResultKind == "" {
			add("incomplete_goal", "/goal", "task approval requires a goal and result kind")
		}
		if len(b.OpenQuestions) != 0 {
			add("open_questions", "/openQuestions", "resolve open questions before approval")
		}
		if len(b.Criteria) == 0 {
			add("missing_criteria", "/criteria", "task approval requires acceptance criteria")
		}
	}
	if b.State == "approved" || b.State == "executing" {
		if b.ApprovedVersion != b.Version || b.ApprovedDigest == "" || b.ApprovedDigest != TaskBriefDigest(b) {
			add("invalid_approval", "/approvedDigest", "task brief approval does not match its version and content")
		}
	} else if b.ApprovedVersion != 0 || b.ApprovedDigest != "" {
		add("draft_approval", "/approvedDigest", "draft task brief cannot contain approval metadata")
	}
	return issues
}

// TaskBriefDigest is stable across JSON argument whitespace and object key order.
// Invalid JSON returns an empty digest and can never establish valid approval.
func TaskBriefDigest(b TaskBrief) string {
	b.Version, b.ApprovedVersion, b.State, b.ApprovedDigest = 0, 0, "", ""
	b.Criteria = append([]AcceptanceCriterion(nil), b.Criteria...)
	for i := range b.Criteria {
		if len(b.Criteria[i].Arguments) == 0 {
			continue
		}
		if !json.Valid(b.Criteria[i].Arguments) {
			return ""
		}
		var value any
		decoder := json.NewDecoder(bytes.NewReader(b.Criteria[i].Arguments))
		decoder.UseNumber()
		if decoder.Decode(&value) != nil {
			return ""
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return ""
		}
		b.Criteria[i].Arguments = raw
	}
	raw, err := json.Marshal(b)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// ApproveTaskBrief must be invoked only for an authenticated user action. Draft
// normalization means a model cannot bypass validation by supplying these fields.
func ApproveTaskBrief(b TaskBrief) (TaskBrief, error) {
	b = NormalizeTaskBrief(b)
	b.State = "ready"
	if err := ValidateTaskBrief(b); err != nil {
		return TaskBrief{}, err
	}
	b.State, b.ApprovedVersion, b.ApprovedDigest = "approved", b.Version, TaskBriefDigest(b)
	return b, nil
}

// IsTaskBriefApproved validates a trusted stored approval; it does not authenticate
// the caller. Incoming model/client drafts must always be normalized first.
func IsTaskBriefApproved(b TaskBrief) bool {
	return (b.State == "approved" || b.State == "executing") && ValidateTaskBrief(b) == nil
}

func validateConfirmedGitRemote(remote string) error {
	remote = strings.TrimSpace(remote)
	if remote == "" || strings.ContainsAny(remote, " \t\r\n") {
		return fmt.Errorf("confirmed git remote must be an explicit URL, got %q", remote)
	}
	lower := strings.ToLower(remote)
	if strings.HasPrefix(lower, "git@") {
		if !strings.Contains(remote, ":") {
			return fmt.Errorf("confirmed git remote must be an explicit URL, got %q", remote)
		}
		return nil
	}
	parsed, err := url.Parse(remote)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || (parsed.Scheme != "https" && parsed.Scheme != "ssh") {
		return fmt.Errorf("confirmed git remote must be an https or ssh URL, got %q", remote)
	}
	return nil
}
