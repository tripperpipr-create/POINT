package diagnostics

import (
	"encoding/json"
	"slices"
	"sort"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/verification"
)

const SchemaVersion = 1

type Health string

const (
	HealthActive    Health = "active"
	HealthHealthy   Health = "healthy"
	HealthAttention Health = "attention"
	HealthFailed    Health = "failed"
)

type Signal struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Value    int64  `json:"value,omitempty"`
}

type ModelMetrics struct {
	Requests         int   `json:"requests"`
	Responses        int   `json:"responses"`
	Retries          int   `json:"retries"`
	Pending          int   `json:"pending"`
	InputTokens      int   `json:"inputTokens"`
	OutputTokens     int   `json:"outputTokens"`
	TotalTokens      int   `json:"totalTokens"`
	UsageReported    bool  `json:"usageReported"`
	LatencyMs        int64 `json:"latencyMs"`
	AverageLatencyMs int64 `json:"averageLatencyMs"`
	FirstResponseMs  int64 `json:"firstResponseMs"`
}

type ContextMetrics struct {
	Compactions         int `json:"compactions"`
	RemovedRounds       int `json:"removedRounds"`
	ReducedToolMessages int `json:"reducedToolMessages"`
	ReleasedTokens      int `json:"releasedTokens"`
	PeakInputTokens     int `json:"peakInputTokens"`
	LatestInputTokens   int `json:"latestInputTokens"`
	InputBudgetTokens   int `json:"inputBudgetTokens"`
}

type RetrievalMetrics struct {
	Searches          int `json:"searches"`
	TruncatedSearches int `json:"truncatedSearches"`
	CandidateChunks   int `json:"candidateChunks"`
	ReturnedChunks    int `json:"returnedChunks"`
	RelatedFiles      int `json:"relatedFiles"`
	UsedChars         int `json:"usedChars"`
}

type CompletionMetrics struct {
	Checks                int  `json:"checks"`
	RevisionRequests      int  `json:"revisionRequests"`
	AcceptedAfterRevision bool `json:"acceptedAfterRevision"`
	Rejected              bool `json:"rejected"`
}

type ToolMetric struct {
	Name       string `json:"name"`
	Calls      int    `json:"calls"`
	Succeeded  int    `json:"succeeded"`
	Failed     int    `json:"failed"`
	Pending    int    `json:"pending"`
	DurationMs int64  `json:"durationMs"`
}

type ToolMetrics struct {
	Calls      int          `json:"calls"`
	Succeeded  int          `json:"succeeded"`
	Failed     int          `json:"failed"`
	Pending    int          `json:"pending"`
	DurationMs int64        `json:"durationMs"`
	Items      []ToolMetric `json:"items"`
}

type ApprovalMetrics struct {
	Requested     int   `json:"requested"`
	Allowed       int   `json:"allowed"`
	Denied        int   `json:"denied"`
	Pending       int   `json:"pending"`
	WaitMs        int64 `json:"waitMs"`
	AverageWaitMs int64 `json:"averageWaitMs"`
	MaxWaitMs     int64 `json:"maxWaitMs"`
}

type PatchMetrics struct {
	Proposed int `json:"proposed"`
	Applied  int `json:"applied"`
	Rejected int `json:"rejected"`
}

type VerificationMetrics struct {
	Required           bool `json:"required"`
	Recorded           bool `json:"recorded"`
	SuccessfulCommands int  `json:"successfulCommands"`
}

type WorkspaceMetrics struct {
	Audits               int `json:"audits"`
	ChangedFiles         int `json:"changedFiles"`
	RevertibleChanges    int `json:"revertibleChanges"`
	RecordedChanges      int `json:"recordedChanges"`
	NonRevertibleChanges int `json:"nonRevertibleChanges"`
	OmittedChanges       int `json:"omittedChanges"`
	IncompleteAudits     int `json:"incompleteAudits"`
}

type GuardrailMetrics struct {
	DuplicatePlans     int `json:"duplicatePlans"`
	InspectionRequired int `json:"inspectionRequired"`
	InspectionScope    int `json:"inspectionScope"`
	InspectionStale    int `json:"inspectionStale"`
}

type RunDiagnostics struct {
	SchemaVersion int                 `json:"schemaVersion"`
	RunID         string              `json:"runId"`
	Health        Health              `json:"health"`
	StopReason    string              `json:"stopReason"`
	DurationMs    int64               `json:"durationMs"`
	Model         ModelMetrics        `json:"model"`
	Context       ContextMetrics      `json:"context"`
	Retrieval     RetrievalMetrics    `json:"retrieval"`
	Completion    CompletionMetrics   `json:"completion"`
	Tools         ToolMetrics         `json:"tools"`
	Approvals     ApprovalMetrics     `json:"approvals"`
	Patches       PatchMetrics        `json:"patches"`
	Verification  VerificationMetrics `json:"verification"`
	Workspace     WorkspaceMetrics    `json:"workspace"`
	Guardrails    GuardrailMetrics    `json:"guardrails"`
	Signals       []Signal            `json:"signals"`
}

type eventPayload struct {
	Tool                 string             `json:"tool"`
	DurationMs           int64              `json:"durationMs"`
	Usage                *usagePayload      `json:"usage"`
	Result               *domain.ToolResult `json:"result"`
	EstimatedInputTokens int                `json:"estimatedInputTokens"`
	InputBudgetTokens    int                `json:"inputBudgetTokens"`
	ReleasedTokens       int                `json:"releasedTokens"`
	RemovedRounds        int                `json:"removedRounds"`
	ReducedToolMessages  int                `json:"reducedToolMessages"`
	Status               string             `json:"status"`
	Code                 string             `json:"code"`
	Arguments            json.RawMessage    `json:"arguments"`
	TotalChanges         int                `json:"totalChanges"`
	RevertibleChanges    int                `json:"revertibleChanges"`
	RecordedChanges      int                `json:"recordedChanges"`
	NonRevertibleChanges int                `json:"nonRevertibleChanges"`
	OmittedChanges       int                `json:"omittedRevertibleChanges"`
	SnapshotComplete     *bool              `json:"snapshotComplete"`
}

type usagePayload struct {
	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
}

type toolOperation struct {
	name          string
	requestedAt   time.Time
	startedAt     time.Time
	finishedAt    time.Time
	reportedMs    int64
	result        *domain.ToolResult
	arguments     json.RawMessage
	finishedEvent bool
}

// Analyze derives an auditable operational summary from immutable run artifacts.
// It deliberately does not attempt to grade the semantic quality of model text.
func Analyze(run domain.Run, events []domain.Event, approvals []domain.Approval, patches []domain.PatchProposal, now time.Time) RunDiagnostics {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	result := RunDiagnostics{
		SchemaVersion: SchemaVersion,
		RunID:         run.ID,
		StopReason:    stopReason(run),
		DurationMs:    runDuration(run, events, now),
		Tools:         ToolMetrics{Items: []ToolMetric{}},
		Signals:       []Signal{},
	}

	modelRequests := make([]time.Time, 0)
	toolOperations := make([]*toolOperation, 0)
	for _, event := range events {
		var payload eventPayload
		_ = json.Unmarshal(event.Data, &payload)
		switch event.Type {
		case domain.EventModelRequested:
			result.Model.Requests++
			modelRequests = append(modelRequests, event.CreatedAt)
			result.Context.LatestInputTokens = payload.EstimatedInputTokens
			if payload.EstimatedInputTokens > result.Context.PeakInputTokens {
				result.Context.PeakInputTokens = payload.EstimatedInputTokens
			}
			if payload.InputBudgetTokens > 0 {
				result.Context.InputBudgetTokens = payload.InputBudgetTokens
			}
		case domain.EventModelResponded:
			result.Model.Responses++
			if len(modelRequests) > 0 {
				latency := nonNegativeMilliseconds(event.CreatedAt.Sub(modelRequests[0]))
				result.Model.LatencyMs += latency
				if result.Model.Responses == 1 {
					result.Model.FirstResponseMs = latency
				}
				modelRequests = modelRequests[1:]
			}
		case domain.EventModelRetrying:
			result.Model.Retries++
		case domain.EventContextCompacted:
			result.Context.Compactions++
			result.Context.ReleasedTokens += payload.ReleasedTokens
			result.Context.RemovedRounds += payload.RemovedRounds
			result.Context.ReducedToolMessages += payload.ReducedToolMessages
		case domain.EventCompletionChecked:
			result.Completion.Checks++
			switch payload.Status {
			case "revision_required":
				result.Completion.RevisionRequests++
			case "accepted_after_revision":
				result.Completion.AcceptedAfterRevision = true
			case "rejected":
				result.Completion.Rejected = true
			}
		case domain.EventModelUsage:
			collectUsage(&result.Model, payload.Usage)
		case domain.EventModelStreamed:
			// Releases before schema version 1 stored provider usage as a streamed event.
			collectUsage(&result.Model, payload.Usage)
		case domain.EventToolRequested:
			toolOperations = append(toolOperations, &toolOperation{name: payload.Tool, requestedAt: event.CreatedAt, arguments: append(json.RawMessage(nil), payload.Arguments...)})
		case domain.EventToolStarted:
			operation := firstToolOperation(toolOperations, payload.Tool, func(item *toolOperation) bool { return item.startedAt.IsZero() && !item.finishedEvent })
			if operation == nil {
				operation = &toolOperation{name: payload.Tool, requestedAt: event.CreatedAt}
				toolOperations = append(toolOperations, operation)
			}
			operation.startedAt = event.CreatedAt
		case domain.EventToolFinished:
			operation := firstToolOperation(toolOperations, payload.Tool, func(item *toolOperation) bool { return !item.finishedEvent })
			if operation == nil {
				operation = &toolOperation{name: payload.Tool, requestedAt: event.CreatedAt}
				toolOperations = append(toolOperations, operation)
			}
			operation.finishedAt = event.CreatedAt
			operation.reportedMs = payload.DurationMs
			operation.result = payload.Result
			operation.finishedEvent = true
			collectRetrieval(&result.Retrieval, payload.Tool, payload.Result)
		case domain.EventWorkspaceChanged:
			result.Workspace.Audits++
			result.Workspace.ChangedFiles += payload.TotalChanges
			result.Workspace.RevertibleChanges += payload.RevertibleChanges
			result.Workspace.RecordedChanges += payload.RecordedChanges
			result.Workspace.NonRevertibleChanges += payload.NonRevertibleChanges
			result.Workspace.OmittedChanges += payload.OmittedChanges
			if payload.SnapshotComplete == nil || !*payload.SnapshotComplete {
				result.Workspace.IncompleteAudits++
			}
		case domain.EventAgentGuardrail:
			switch payload.Code {
			case "duplicate_tool_plan", "tool_plan_recovery":
				result.Guardrails.DuplicatePlans++
			case "inspection_required":
				result.Guardrails.InspectionRequired++
			case "inspection_scope_required":
				result.Guardrails.InspectionScope++
			case "inspection_stale":
				result.Guardrails.InspectionStale++
			}
		}
	}
	result.Model.Pending = len(modelRequests)
	if result.Model.Responses > 0 {
		result.Model.AverageLatencyMs = result.Model.LatencyMs / int64(result.Model.Responses)
	}
	result.Tools = summarizeTools(toolOperations, isTerminal(run.Status))
	result.Approvals = summarizeApprovals(approvals, run, events, now)
	result.Patches = summarizePatches(patches)
	result.Verification = summarizeVerification(run, events, toolOperations)
	result.Signals = signals(run, result)
	result.Health = health(run, result.Signals)
	return result
}

func collectUsage(metrics *ModelMetrics, usage *usagePayload) {
	if usage == nil {
		return
	}
	metrics.UsageReported = true
	metrics.InputTokens += usage.InputTokens
	metrics.OutputTokens += usage.OutputTokens
	metrics.TotalTokens = metrics.InputTokens + metrics.OutputTokens
}

func collectRetrieval(metrics *RetrievalMetrics, tool string, result *domain.ToolResult) {
	if tool != "search_code" || result == nil || !result.OK {
		return
	}
	var output struct {
		CandidateChunks int   `json:"candidateChunks"`
		ReturnedChunks  int   `json:"returnedChunks"`
		RelatedFiles    []any `json:"relatedFiles"`
		UsedChars       int   `json:"usedChars"`
		Truncated       bool  `json:"truncated"`
	}
	if json.Unmarshal(result.Output, &output) != nil {
		return
	}
	metrics.Searches++
	metrics.CandidateChunks += output.CandidateChunks
	metrics.ReturnedChunks += output.ReturnedChunks
	metrics.RelatedFiles += len(output.RelatedFiles)
	metrics.UsedChars += output.UsedChars
	if output.Truncated {
		metrics.TruncatedSearches++
	}
}

func toolResultSuccessful(result *domain.ToolResult) bool {
	if result == nil || !result.OK {
		return false
	}
	var output struct {
		ExitCode *int `json:"exitCode"`
		TimedOut bool `json:"timedOut"`
	}
	if json.Unmarshal(result.Output, &output) != nil || output.ExitCode == nil {
		return true
	}
	return *output.ExitCode == 0 && !output.TimedOut
}

func firstToolOperation(items []*toolOperation, name string, predicate func(*toolOperation) bool) *toolOperation {
	for _, item := range items {
		if item.name == name && predicate(item) {
			return item
		}
	}
	return nil
}

func summarizeTools(operations []*toolOperation, terminal bool) ToolMetrics {
	items := map[string]*ToolMetric{}
	for _, operation := range operations {
		name := operation.name
		if name == "" {
			name = "unknown"
		}
		metric := items[name]
		if metric == nil {
			metric = &ToolMetric{Name: name}
			items[name] = metric
		}
		metric.Calls++
		duration := operation.reportedMs
		if duration <= 0 && !operation.startedAt.IsZero() && !operation.finishedAt.IsZero() {
			duration = nonNegativeMilliseconds(operation.finishedAt.Sub(operation.startedAt))
		}
		metric.DurationMs += duration
		if operation.finishedEvent && toolResultSuccessful(operation.result) {
			metric.Succeeded++
		} else if operation.finishedEvent || terminal {
			metric.Failed++
		} else {
			metric.Pending++
		}
	}
	names := make([]string, 0, len(items))
	for name := range items {
		names = append(names, name)
	}
	sort.Strings(names)
	result := ToolMetrics{Items: make([]ToolMetric, 0, len(names))}
	for _, name := range names {
		item := *items[name]
		result.Items = append(result.Items, item)
		result.Calls += item.Calls
		result.Succeeded += item.Succeeded
		result.Failed += item.Failed
		result.Pending += item.Pending
		result.DurationMs += item.DurationMs
	}
	return result
}

func summarizeApprovals(approvals []domain.Approval, run domain.Run, events []domain.Event, now time.Time) ApprovalMetrics {
	result := ApprovalMetrics{Requested: len(approvals)}
	end := now
	if run.FinishedAt != nil {
		end = *run.FinishedAt
	} else if isTerminal(run.Status) && len(events) > 0 && events[len(events)-1].CreatedAt.After(run.StartedAt) {
		end = events[len(events)-1].CreatedAt
	}
	for _, approval := range approvals {
		approvalEnd := end
		if approval.ResolvedAt != nil {
			approvalEnd = *approval.ResolvedAt
		}
		wait := nonNegativeMilliseconds(approvalEnd.Sub(approval.CreatedAt))
		result.WaitMs += wait
		if wait > result.MaxWaitMs {
			result.MaxWaitMs = wait
		}
		switch approval.Status {
		case domain.ApprovalAllowed:
			result.Allowed++
		case domain.ApprovalDenied:
			result.Denied++
		default:
			result.Pending++
		}
	}
	if len(approvals) > 0 {
		result.AverageWaitMs = result.WaitMs / int64(len(approvals))
	}
	return result
}

func summarizePatches(patches []domain.PatchProposal) PatchMetrics {
	result := PatchMetrics{Proposed: len(patches)}
	for _, patch := range patches {
		switch strings.ToLower(patch.Status) {
		case "applied":
			result.Applied++
		case "rejected":
			result.Rejected++
		}
	}
	return result
}

func summarizeVerification(run domain.Run, events []domain.Event, operations []*toolOperation) VerificationMetrics {
	result := VerificationMetrics{Required: len(run.ChangedFiles) > 0 || verification.TaskRequires(run.Task)}
	var latestAppliedPatch time.Time
	for _, event := range events {
		if (event.Type == domain.EventPatchApplied || event.Type == domain.EventWorkspaceChanged) && event.CreatedAt.After(latestAppliedPatch) {
			latestAppliedPatch = event.CreatedAt
		}
	}
	type attempt struct {
		last       *toolOperation
		everPassed bool
	}
	checks := map[string]attempt{}
	for _, operation := range operations {
		if !operation.finishedEvent || !isVerificationOperation(run, operation) {
			continue
		}
		identity := verification.CheckIdentity(operation.name, operation.arguments)
		previous := checks[identity]
		passed := toolResultSuccessful(operation.result)
		checks[identity] = attempt{last: operation, everPassed: previous.everPassed || passed}
		if passed {
			result.SuccessfulCommands++
		}
	}
	if required, recorded := structuredTaskVerification(run, events); required {
		result.Required, result.Recorded = true, recorded
		return result
	}
	for _, check := range checks {
		passed := toolResultSuccessful(check.last.result)
		if check.everPassed && (!passed || check.last.finishedAt.Before(latestAppliedPatch)) {
			result.Recorded = false
			return result
		}
		if passed && (latestAppliedPatch.IsZero() || !check.last.finishedAt.Before(latestAppliedPatch)) {
			result.Recorded = true
		}
	}
	// Modern runs persist the complete criterion evidence. A failed gate or a
	// pending user criterion must not become a learning success through heuristics.
	for _, event := range events {
		if event.Type != domain.EventCompletionChecked {
			continue
		}
		var payload struct {
			Status   string `json:"status"`
			Evidence *struct {
				Status string `json:"status"`
			} `json:"evidence"`
		}
		if json.Unmarshal(event.Data, &payload) == nil && payload.Evidence != nil {
			result.Recorded = payload.Status == "accepted_after_revision" && payload.Evidence.Status == "verified"
			result.Required = true
		}
	}

	return result
}

func isVerificationOperation(run domain.Run, operation *toolOperation) bool {
	if operation.name == "run_command" {
		return verification.IsCommand(commandFromArguments(operation.arguments))
	}
	if !slices.Contains(run.ConfigurationSnapshot.Profile.AllowedTools, operation.name) {
		return false
	}
	for _, tool := range run.ConfigurationSnapshot.CustomTools {
		if tool.ID == operation.name {
			return tool.ProvidesVerification
		}
	}
	return false
}

func commandFromArguments(arguments json.RawMessage) string {
	var input struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(arguments, &input) != nil {
		return ""
	}
	return input.Command
}

func signals(run domain.Run, diagnostics RunDiagnostics) []Signal {
	result := make([]Signal, 0, 8)
	switch run.Status {
	case domain.RunCompleted:
		result = append(result, Signal{Code: "run_completed", Severity: "success"})
	case domain.RunFailed:
		result = append(result, Signal{Code: "run_failed", Severity: "error"})
	case domain.RunCancelled:
		result = append(result, Signal{Code: "run_cancelled", Severity: "warning"})
	case domain.RunInterrupted:
		result = append(result, Signal{Code: "run_interrupted", Severity: "warning"})
	}
	if diagnostics.Tools.Failed > 0 {
		result = append(result, Signal{Code: "tool_failures", Severity: "warning", Value: int64(diagnostics.Tools.Failed)})
	}
	if diagnostics.Approvals.Denied > 0 {
		result = append(result, Signal{Code: "approval_denied", Severity: "warning", Value: int64(diagnostics.Approvals.Denied)})
	}
	if diagnostics.Approvals.Pending > 0 {
		result = append(result, Signal{Code: "approval_pending", Severity: "info", Value: int64(diagnostics.Approvals.Pending)})
	}
	if diagnostics.Approvals.MaxWaitMs >= 30_000 {
		result = append(result, Signal{Code: "approval_wait_long", Severity: "info", Value: diagnostics.Approvals.MaxWaitMs})
	}
	if diagnostics.Model.Responses > 0 && !diagnostics.Model.UsageReported {
		result = append(result, Signal{Code: "tokens_unreported", Severity: "info"})
	}
	if diagnostics.Model.Retries > 0 {
		result = append(result, Signal{Code: "provider_retried", Severity: "info", Value: int64(diagnostics.Model.Retries)})
	}
	if diagnostics.Context.Compactions > 0 {
		result = append(result, Signal{Code: "context_compacted", Severity: "info", Value: int64(diagnostics.Context.ReleasedTokens)})
	}
	if diagnostics.Retrieval.TruncatedSearches > 0 {
		result = append(result, Signal{Code: "retrieval_truncated", Severity: "info", Value: int64(diagnostics.Retrieval.TruncatedSearches)})
	}
	if diagnostics.Completion.AcceptedAfterRevision {
		result = append(result, Signal{Code: "completion_revised", Severity: "success", Value: int64(diagnostics.Completion.RevisionRequests)})
	} else if diagnostics.Completion.Rejected {
		result = append(result, Signal{Code: "completion_evidence_missing", Severity: "error"})
	} else if diagnostics.Completion.RevisionRequests > 0 {
		result = append(result, Signal{Code: "completion_revision_requested", Severity: "info", Value: int64(diagnostics.Completion.RevisionRequests)})
	}
	if diagnostics.Model.Pending > 0 && isTerminal(run.Status) {
		result = append(result, Signal{Code: "model_requests_unanswered", Severity: "warning", Value: int64(diagnostics.Model.Pending)})
	}
	if diagnostics.Verification.Required {
		if diagnostics.Verification.Recorded {
			result = append(result, Signal{Code: "verification_recorded", Severity: "success"})
		} else if isTerminal(run.Status) {
			result = append(result, Signal{Code: "verification_missing", Severity: "warning"})
		}
	}
	if diagnostics.Workspace.RecordedChanges > 0 {
		result = append(result, Signal{Code: "workspace_changes_captured", Severity: "success", Value: int64(diagnostics.Workspace.RecordedChanges)})
	}
	if diagnostics.Workspace.NonRevertibleChanges > 0 {
		result = append(result, Signal{Code: "workspace_changes_non_revertible", Severity: "warning", Value: int64(diagnostics.Workspace.NonRevertibleChanges)})
	}
	if diagnostics.Workspace.OmittedChanges > 0 {
		result = append(result, Signal{Code: "workspace_history_limit_reached", Severity: "warning", Value: int64(diagnostics.Workspace.OmittedChanges)})
	}
	if diagnostics.Workspace.IncompleteAudits > 0 {
		result = append(result, Signal{Code: "workspace_audit_incomplete", Severity: "warning", Value: int64(diagnostics.Workspace.IncompleteAudits)})
	}
	if diagnostics.Guardrails.InspectionRequired > 0 {
		result = append(result, Signal{Code: "blind_edit_prevented", Severity: "info", Value: int64(diagnostics.Guardrails.InspectionRequired)})
	}
	if diagnostics.Guardrails.InspectionScope > 0 {
		result = append(result, Signal{Code: "edit_scope_prevented", Severity: "info", Value: int64(diagnostics.Guardrails.InspectionScope)})
	}
	if diagnostics.Guardrails.InspectionStale > 0 {
		result = append(result, Signal{Code: "stale_edit_prevented", Severity: "info", Value: int64(diagnostics.Guardrails.InspectionStale)})
	}
	if diagnostics.StopReason == "agent_stalled" {
		result = append(result, Signal{Code: "agent_stalled", Severity: "error"})
	}
	return result
}

func health(run domain.Run, signals []Signal) Health {
	switch run.Status {
	case domain.RunPending, domain.RunRunning, domain.RunWaiting:
		return HealthActive
	case domain.RunFailed:
		return HealthFailed
	case domain.RunCancelled, domain.RunInterrupted:
		return HealthAttention
	}
	for _, signal := range signals {
		if signal.Severity == "warning" || signal.Severity == "error" {
			return HealthAttention
		}
	}
	return HealthHealthy
}

func stopReason(run domain.Run) string {
	switch run.Status {
	case domain.RunPending, domain.RunRunning, domain.RunWaiting:
		return "active"
	case domain.RunCompleted:
		return "completed"
	case domain.RunCancelled:
		return "cancelled_by_user"
	case domain.RunInterrupted:
		return "interrupted"
	}
	errorText := strings.ToLower(run.Error)
	if strings.Contains(errorText, "timed out") || strings.Contains(errorText, "deadline") {
		return "timeout"
	}
	if strings.Contains(errorText, "maximum step") {
		return "step_limit"
	}
	if strings.Contains(errorText, "agent stalled") || strings.Contains(errorText, "identical tool plan") {
		return "agent_stalled"
	}
	if strings.Contains(errorText, "completion gate") || strings.Contains(errorText, "verification_required") || strings.Contains(errorText, "verification_failed") || strings.Contains(errorText, "verification_tool_unavailable") {
		return "completion_evidence_missing"
	}
	if strings.Contains(errorText, "model") || strings.Contains(errorText, "provider") || strings.Contains(errorText, "http") {
		return "provider_error"
	}
	return "failed"
}

func runDuration(run domain.Run, events []domain.Event, now time.Time) int64 {
	if run.FinishedAt != nil {
		return nonNegativeMilliseconds(run.FinishedAt.Sub(run.StartedAt))
	}
	end := now
	if isTerminal(run.Status) && len(events) > 0 && events[len(events)-1].CreatedAt.After(run.StartedAt) {
		end = events[len(events)-1].CreatedAt
	}
	computed := nonNegativeMilliseconds(end.Sub(run.StartedAt))
	if run.DurationMs > computed {
		return run.DurationMs
	}
	return computed
}

func nonNegativeMilliseconds(duration time.Duration) int64 {
	if duration <= 0 {
		return 0
	}
	return duration.Milliseconds()
}

func isTerminal(status domain.RunStatus) bool {
	return status == domain.RunCompleted || status == domain.RunFailed || status == domain.RunCancelled || status == domain.RunInterrupted
}
