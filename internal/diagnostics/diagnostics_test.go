package diagnostics

import (
	"encoding/json"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

func TestAnalyzeBuildsAuditableCompletedRunSummary(t *testing.T) {
	started := time.Date(2026, time.August, 8, 10, 0, 0, 0, time.UTC)
	finished := started.Add(12 * time.Second)
	run := domain.Run{
		ID: "run-1", Status: domain.RunCompleted, StartedAt: started, FinishedAt: &finished,
		ChangedFiles: []string{"main.go"},
	}
	events := []domain.Event{
		event(domain.EventModelRequested, started.Add(time.Second), map[string]any{"estimatedInputTokens": 220, "inputBudgetTokens": 500}),
		event(domain.EventContextCompacted, started.Add(1200*time.Millisecond), map[string]any{"releasedTokens": 90, "removedRounds": 2, "reducedToolMessages": 1}),
		event(domain.EventModelRetrying, started.Add(1500*time.Millisecond), map[string]any{"attempt": 2, "delayMs": 250}),
		event(domain.EventModelUsage, started.Add(2500*time.Millisecond), map[string]any{"usage": map[string]int{"inputTokens": 120, "outputTokens": 30}}),
		event(domain.EventModelResponded, started.Add(3*time.Second), nil),
		event(domain.EventToolRequested, started.Add(4*time.Second), map[string]any{"tool": "propose_patch"}),
		event(domain.EventToolStarted, started.Add(4100*time.Millisecond), map[string]any{"tool": "propose_patch"}),
		event(domain.EventToolFinished, started.Add(4300*time.Millisecond), map[string]any{"tool": "propose_patch", "durationMs": 200, "result": map[string]any{"ok": true}}),
		event(domain.EventPatchApplied, started.Add(5*time.Second), nil),
		event(domain.EventToolRequested, started.Add(6*time.Second), map[string]any{"tool": "run_command", "arguments": map[string]any{"command": "go test ./..."}}),
		event(domain.EventToolStarted, started.Add(6100*time.Millisecond), map[string]any{"tool": "run_command"}),
		event(domain.EventToolFinished, started.Add(8100*time.Millisecond), map[string]any{"tool": "run_command", "durationMs": 2000, "result": map[string]any{"ok": true}}),
	}
	resolved := started.Add(6 * time.Second)
	diagnostics := Analyze(run, events, []domain.Approval{{Status: domain.ApprovalAllowed, CreatedAt: started.Add(5 * time.Second), ResolvedAt: &resolved}}, []domain.PatchProposal{{Status: "applied"}}, finished)

	if diagnostics.Health != HealthHealthy || diagnostics.StopReason != "completed" || diagnostics.DurationMs != 12_000 {
		t.Fatalf("unexpected outcome: %#v", diagnostics)
	}
	if diagnostics.Model.Requests != 1 || diagnostics.Model.Responses != 1 || diagnostics.Model.Retries != 1 || diagnostics.Model.InputTokens != 120 || diagnostics.Model.OutputTokens != 30 || diagnostics.Model.LatencyMs != 2_000 {
		t.Fatalf("unexpected model metrics: %#v", diagnostics.Model)
	}
	if diagnostics.Context.Compactions != 1 || diagnostics.Context.ReleasedTokens != 90 || diagnostics.Context.RemovedRounds != 2 || diagnostics.Context.ReducedToolMessages != 1 || diagnostics.Context.PeakInputTokens != 220 || diagnostics.Context.InputBudgetTokens != 500 || !hasSignal(diagnostics.Signals, "context_compacted") {
		t.Fatalf("unexpected context metrics: %#v signals=%#v", diagnostics.Context, diagnostics.Signals)
	}
	if diagnostics.Tools.Calls != 2 || diagnostics.Tools.Succeeded != 2 || diagnostics.Tools.Failed != 0 || diagnostics.Tools.DurationMs != 2_200 {
		t.Fatalf("unexpected tool metrics: %#v", diagnostics.Tools)
	}
	if diagnostics.Approvals.Allowed != 1 || diagnostics.Approvals.WaitMs != 1_000 || diagnostics.Patches.Applied != 1 {
		t.Fatalf("unexpected approval/patch metrics: approvals=%#v patches=%#v", diagnostics.Approvals, diagnostics.Patches)
	}
	if !diagnostics.Verification.Required || !diagnostics.Verification.Recorded || !hasSignal(diagnostics.Signals, "verification_recorded") {
		t.Fatalf("verification signal missing: %#v", diagnostics.Signals)
	}
	if !hasSignal(diagnostics.Signals, "provider_retried") {
		t.Fatalf("provider retry signal missing: %#v", diagnostics.Signals)
	}
}

func TestVerificationMustFollowAppliedPatch(t *testing.T) {
	started := time.Date(2026, time.August, 8, 13, 0, 0, 0, time.UTC)
	finished := started.Add(10 * time.Second)
	run := domain.Run{ID: "order", Status: domain.RunCompleted, StartedAt: started, FinishedAt: &finished, ChangedFiles: []string{"main.go"}}
	events := []domain.Event{
		event(domain.EventToolRequested, started.Add(time.Second), map[string]any{"tool": "run_command", "arguments": map[string]any{"command": "go test ./..."}}),
		event(domain.EventToolStarted, started.Add(2*time.Second), map[string]any{"tool": "run_command"}),
		event(domain.EventToolFinished, started.Add(3*time.Second), map[string]any{"tool": "run_command", "result": map[string]any{"ok": true}}),
		event(domain.EventPatchApplied, started.Add(5*time.Second), nil),
	}
	diagnostics := Analyze(run, events, nil, []domain.PatchProposal{{Status: "applied"}}, finished)
	if diagnostics.Verification.Recorded || !hasSignal(diagnostics.Signals, "verification_missing") {
		t.Fatalf("pre-change command was treated as verification: %#v", diagnostics)
	}
}

func TestAnalyzeReadsLegacyUsageAndSurfacesObservableRisks(t *testing.T) {
	started := time.Date(2026, time.August, 8, 11, 0, 0, 0, time.UTC)
	finished := started.Add(40 * time.Second)
	run := domain.Run{ID: "legacy", Status: domain.RunCompleted, StartedAt: started, FinishedAt: &finished, ChangedFiles: []string{"api.go"}}
	events := []domain.Event{
		event(domain.EventModelRequested, started, nil),
		event(domain.EventModelStreamed, started.Add(time.Second), map[string]any{"usage": map[string]int{"inputTokens": 20, "outputTokens": 5}}),
		event(domain.EventModelResponded, started.Add(2*time.Second), nil),
		event(domain.EventToolRequested, started.Add(3*time.Second), map[string]any{"tool": "read_file"}),
		event(domain.EventToolFinished, started.Add(4*time.Second), map[string]any{"tool": "read_file", "result": map[string]any{"ok": false, "error": map[string]string{"code": "not_found", "message": "missing"}}}),
	}
	diagnostics := Analyze(run, events, []domain.Approval{{Status: domain.ApprovalDenied, CreatedAt: started.Add(5 * time.Second), ResolvedAt: timePointer(started.Add(36 * time.Second))}}, []domain.PatchProposal{{Status: "rejected"}}, finished)

	if !diagnostics.Model.UsageReported || diagnostics.Model.TotalTokens != 25 {
		t.Fatalf("legacy usage was not collected: %#v", diagnostics.Model)
	}
	if diagnostics.Health != HealthAttention || diagnostics.Tools.Failed != 1 || diagnostics.Approvals.Denied != 1 || diagnostics.Patches.Rejected != 1 {
		t.Fatalf("observable risks lost: %#v", diagnostics)
	}
	for _, code := range []string{"tool_failures", "approval_denied", "approval_wait_long", "verification_missing"} {
		if !hasSignal(diagnostics.Signals, code) {
			t.Fatalf("signal %s missing: %#v", code, diagnostics.Signals)
		}
	}
}

func TestAnalyzeDoesNotCallActiveToolAFailure(t *testing.T) {
	started := time.Date(2026, time.August, 8, 12, 0, 0, 0, time.UTC)
	run := domain.Run{ID: "active", Status: domain.RunWaiting, StartedAt: started}
	events := []domain.Event{
		event(domain.EventModelRequested, started, nil),
		event(domain.EventModelResponded, started.Add(time.Second), nil),
		event(domain.EventToolRequested, started.Add(2*time.Second), map[string]any{"tool": "run_command", "arguments": map[string]any{"command": "go test ./..."}}),
	}
	diagnostics := Analyze(run, events, []domain.Approval{{Status: domain.ApprovalPending, CreatedAt: started.Add(2 * time.Second)}}, nil, started.Add(7*time.Second))
	if diagnostics.Health != HealthActive || diagnostics.DurationMs != 7_000 || diagnostics.Tools.Pending != 1 || diagnostics.Tools.Failed != 0 || diagnostics.Approvals.Pending != 1 || diagnostics.Approvals.WaitMs != 5_000 {
		t.Fatalf("active work was misclassified: %#v", diagnostics)
	}
}

func TestStopReasonClassifiesLimitsAndProviderErrors(t *testing.T) {
	for _, test := range []struct {
		errorText string
		want      string
	}{
		{"Run timed out", "timeout"},
		{"maximum step count (20) reached", "step_limit"},
		{"agent stalled after repeating an identical tool plan 3 times", "agent_stalled"},
		{"provider HTTP 502", "provider_error"},
		{"unexpected failure", "failed"},
	} {
		if got := stopReason(domain.Run{Status: domain.RunFailed, Error: test.errorText}); got != test.want {
			t.Errorf("stopReason(%q)=%q, want %q", test.errorText, got, test.want)
		}
	}
}

func TestAnalyzeSurfacesAgentStallGuardrail(t *testing.T) {
	started := time.Date(2026, time.August, 8, 14, 0, 0, 0, time.UTC)
	finished := started.Add(time.Second)
	run := domain.Run{ID: "stalled", Status: domain.RunFailed, Error: "agent stalled after repeating an identical tool plan 3 times", StartedAt: started, FinishedAt: &finished}
	diagnostics := Analyze(run, []domain.Event{event(domain.EventAgentGuardrail, finished, map[string]any{"code": "agent_stalled"})}, nil, nil, finished)
	if diagnostics.StopReason != "agent_stalled" || diagnostics.Health != HealthFailed || !hasSignal(diagnostics.Signals, "agent_stalled") {
		t.Fatalf("stalled run diagnostics=%#v", diagnostics)
	}
}

func TestAnalyzeCountsInspectionGuardrails(t *testing.T) {
	started := time.Date(2026, time.August, 10, 10, 0, 0, 0, time.UTC)
	finished := started.Add(time.Second)
	run := domain.Run{ID: "guarded", Status: domain.RunCompleted, StartedAt: started, FinishedAt: &finished}
	events := []domain.Event{
		event(domain.EventAgentGuardrail, started.Add(100*time.Millisecond), map[string]any{"code": "inspection_required", "path": "main.go"}),
		event(domain.EventAgentGuardrail, started.Add(200*time.Millisecond), map[string]any{"code": "inspection_scope_required", "path": "main.go"}),
		event(domain.EventAgentGuardrail, started.Add(300*time.Millisecond), map[string]any{"code": "inspection_stale", "path": "main.go"}),
		event(domain.EventAgentGuardrail, started.Add(400*time.Millisecond), map[string]any{"code": "duplicate_tool_plan"}),
	}
	diagnostics := Analyze(run, events, nil, nil, finished)
	if diagnostics.Guardrails.InspectionRequired != 1 || diagnostics.Guardrails.InspectionScope != 1 || diagnostics.Guardrails.InspectionStale != 1 || diagnostics.Guardrails.DuplicatePlans != 1 {
		t.Fatalf("guardrail metrics=%#v", diagnostics.Guardrails)
	}
	if !hasSignal(diagnostics.Signals, "blind_edit_prevented") || !hasSignal(diagnostics.Signals, "edit_scope_prevented") || !hasSignal(diagnostics.Signals, "stale_edit_prevented") {
		t.Fatalf("inspection signals=%#v", diagnostics.Signals)
	}
}

func TestAnalyzeCollectsSearchRetrievalEvidence(t *testing.T) {
	started := time.Date(2026, time.August, 10, 11, 0, 0, 0, time.UTC)
	finished := started.Add(time.Second)
	run := domain.Run{ID: "retrieval", Status: domain.RunCompleted, StartedAt: started, FinishedAt: &finished}
	searchOutput := func(candidates, returned, related, used int, truncated bool) map[string]any {
		relatedFiles := make([]map[string]string, related)
		for index := range relatedFiles {
			relatedFiles[index] = map[string]string{"path": "related.go", "relation": "imported_by"}
		}
		return map[string]any{
			"ok": true,
			"output": map[string]any{
				"candidateChunks": candidates, "returnedChunks": returned,
				"relatedFiles": relatedFiles, "usedChars": used, "truncated": truncated,
			},
		}
	}
	events := []domain.Event{
		event(domain.EventToolRequested, started.Add(100*time.Millisecond), map[string]any{"tool": "search_code"}),
		event(domain.EventToolFinished, started.Add(200*time.Millisecond), map[string]any{"tool": "search_code", "result": searchOutput(8, 2, 3, 3200, true)}),
		event(domain.EventToolRequested, started.Add(300*time.Millisecond), map[string]any{"tool": "search_code"}),
		event(domain.EventToolFinished, started.Add(400*time.Millisecond), map[string]any{"tool": "search_code", "result": searchOutput(1, 1, 1, 700, false)}),
	}
	diagnostics := Analyze(run, events, nil, nil, finished)
	if diagnostics.Retrieval.Searches != 2 || diagnostics.Retrieval.TruncatedSearches != 1 || diagnostics.Retrieval.CandidateChunks != 9 || diagnostics.Retrieval.ReturnedChunks != 3 || diagnostics.Retrieval.RelatedFiles != 4 || diagnostics.Retrieval.UsedChars != 3900 {
		t.Fatalf("retrieval metrics=%#v", diagnostics.Retrieval)
	}
	if !hasSignal(diagnostics.Signals, "retrieval_truncated") {
		t.Fatalf("retrieval signal=%#v", diagnostics.Signals)
	}
}

func TestNonzeroAndTimedOutCommandsAreFailedEvidence(t *testing.T) {
	started := time.Date(2026, time.August, 8, 15, 0, 0, 0, time.UTC)
	finished := started.Add(4 * time.Second)
	run := domain.Run{ID: "failed-verification", Status: domain.RunCompleted, StartedAt: started, FinishedAt: &finished, ChangedFiles: []string{"main.go"}}
	events := []domain.Event{
		event(domain.EventPatchApplied, started.Add(time.Second), nil),
		event(domain.EventToolRequested, started.Add(2*time.Second), map[string]any{"tool": "run_command", "arguments": map[string]any{"command": "go test ./..."}}),
		event(domain.EventToolStarted, started.Add(2100*time.Millisecond), map[string]any{"tool": "run_command"}),
		event(domain.EventToolFinished, started.Add(3*time.Second), map[string]any{
			"tool": "run_command", "result": map[string]any{"ok": true, "output": map[string]any{"exitCode": 7, "timedOut": false}},
		}),
	}
	diagnostics := Analyze(run, events, nil, []domain.PatchProposal{{Status: "applied"}}, finished)
	if diagnostics.Tools.Succeeded != 0 || diagnostics.Tools.Failed != 1 {
		t.Fatalf("nonzero command was not classified as failed: %#v", diagnostics.Tools)
	}
	if diagnostics.Verification.Recorded || diagnostics.Verification.SuccessfulCommands != 0 {
		t.Fatalf("nonzero command became verification evidence: %#v", diagnostics.Verification)
	}
	if !hasSignal(diagnostics.Signals, "tool_failures") || !hasSignal(diagnostics.Signals, "verification_missing") {
		t.Fatalf("failure signals missing: %#v", diagnostics.Signals)
	}

	events[3] = event(domain.EventToolFinished, started.Add(3*time.Second), map[string]any{
		"tool": "run_command", "result": map[string]any{"ok": true, "output": map[string]any{"exitCode": 0, "timedOut": true}},
	})
	diagnostics = Analyze(run, events, nil, []domain.PatchProposal{{Status: "applied"}}, finished)
	if diagnostics.Tools.Failed != 1 || diagnostics.Verification.Recorded {
		t.Fatalf("timed out command became successful evidence: %#v", diagnostics)
	}

	events[1] = event(domain.EventToolRequested, started.Add(2*time.Second), map[string]any{"tool": "run_command", "arguments": map[string]any{"command": "go version"}})
	events[3] = event(domain.EventToolFinished, started.Add(3*time.Second), map[string]any{
		"tool": "run_command", "result": map[string]any{"ok": true, "output": map[string]any{"exitCode": 0, "timedOut": false}},
	})
	diagnostics = Analyze(run, events, nil, []domain.PatchProposal{{Status: "applied"}}, finished)
	if diagnostics.Tools.Succeeded != 1 || diagnostics.Verification.Recorded || diagnostics.Verification.SuccessfulCommands != 0 {
		t.Fatalf("irrelevant exit-zero command became verification evidence: %#v", diagnostics)
	}
}

func TestAnalyzeSurfacesCompletionGateOutcome(t *testing.T) {
	started := time.Date(2026, time.August, 8, 16, 0, 0, 0, time.UTC)
	finished := started.Add(2 * time.Second)
	run := domain.Run{
		ID: "completion-rejected", Status: domain.RunFailed,
		Error: "agent final answer rejected by completion gate: verification_required", StartedAt: started, FinishedAt: &finished,
	}
	events := []domain.Event{
		event(domain.EventCompletionChecked, started.Add(time.Second), map[string]any{"status": "revision_required"}),
		event(domain.EventCompletionChecked, started.Add(1500*time.Millisecond), map[string]any{"status": "rejected"}),
	}
	diagnostics := Analyze(run, events, nil, nil, finished)
	if diagnostics.Completion.Checks != 2 || diagnostics.Completion.RevisionRequests != 1 || !diagnostics.Completion.Rejected || diagnostics.Completion.AcceptedAfterRevision {
		t.Fatalf("completion metrics=%#v", diagnostics.Completion)
	}
	if diagnostics.StopReason != "completion_evidence_missing" || !hasSignal(diagnostics.Signals, "completion_evidence_missing") {
		t.Fatalf("completion rejection was not surfaced: %#v", diagnostics)
	}

	run.ID = "completion-repaired"
	run.Status = domain.RunCompleted
	run.Error = ""
	events[1] = event(domain.EventCompletionChecked, started.Add(1500*time.Millisecond), map[string]any{"status": "accepted_after_revision"})
	diagnostics = Analyze(run, events, nil, nil, finished)
	if !diagnostics.Completion.AcceptedAfterRevision || diagnostics.Completion.Rejected || !hasSignal(diagnostics.Signals, "completion_revised") {
		t.Fatalf("repaired completion was not surfaced: %#v", diagnostics)
	}
}

func TestCustomVerificationToolCountsAsEvidenceFromImmutableSnapshot(t *testing.T) {
	started := time.Date(2026, time.August, 8, 17, 0, 0, 0, time.UTC)
	finished := started.Add(3 * time.Second)
	custom := domain.CustomTool{ID: "custom_verify", ProvidesVerification: true}
	profile := domain.DefaultProfile()
	profile.AllowedTools = []string{custom.ID}
	run := domain.Run{
		ID: "custom-verification", Status: domain.RunCompleted, StartedAt: started, FinishedAt: &finished, ChangedFiles: []string{"main.go"},
		ConfigurationSnapshot: domain.NewRunConfigurationSnapshot("test", profile, []domain.CustomTool{custom}, started),
	}
	events := []domain.Event{
		event(domain.EventPatchApplied, started.Add(time.Second), nil),
		event(domain.EventToolRequested, started.Add(2*time.Second), map[string]any{"tool": custom.ID, "arguments": map[string]any{"reason": "verify"}}),
		event(domain.EventToolStarted, started.Add(2100*time.Millisecond), map[string]any{"tool": custom.ID}),
		event(domain.EventToolFinished, started.Add(2500*time.Millisecond), map[string]any{
			"tool": custom.ID, "result": map[string]any{"ok": true, "output": map[string]any{"exitCode": 0, "timedOut": false}},
		}),
	}
	diagnostics := Analyze(run, events, nil, []domain.PatchProposal{{Status: "applied"}}, finished)
	if !diagnostics.Verification.Recorded || diagnostics.Verification.SuccessfulCommands != 1 || !hasSignal(diagnostics.Signals, "verification_recorded") {
		t.Fatalf("custom verification evidence missing: %#v", diagnostics)
	}

	run.Task = "Run tests before completion"
	run.ChangedFiles = nil
	events = events[1:]
	diagnostics = Analyze(run, events, nil, nil, finished)
	if !diagnostics.Verification.Required || !diagnostics.Verification.Recorded || diagnostics.Verification.SuccessfulCommands != 1 {
		t.Fatalf("explicit custom verification was not diagnosed: %#v", diagnostics.Verification)
	}

	custom.ProvidesVerification = false
	run.ConfigurationSnapshot = domain.NewRunConfigurationSnapshot("test", profile, []domain.CustomTool{custom}, started)
	diagnostics = Analyze(run, events, nil, []domain.PatchProposal{{Status: "applied"}}, finished)
	if diagnostics.Verification.Recorded || diagnostics.Verification.SuccessfulCommands != 0 {
		t.Fatalf("undeclared custom tool became verification evidence: %#v", diagnostics.Verification)
	}
}

func TestWorkspaceAuditMetricsSurfaceRollbackCoverage(t *testing.T) {
	started := time.Now().UTC()
	finished := started.Add(time.Second)
	run := domain.Run{ID: "run-workspace-audit", Status: domain.RunCompleted, StartedAt: started, FinishedAt: &finished, ChangedFiles: []string{"main.go", ".env"}}
	complete := true
	events := []domain.Event{
		event(domain.EventWorkspaceChanged, started.Add(500*time.Millisecond), map[string]any{
			"totalChanges": 2, "revertibleChanges": 1, "recordedChanges": 1,
			"nonRevertibleChanges": 1, "omittedRevertibleChanges": 0, "snapshotComplete": complete,
		}),
	}
	result := Analyze(run, events, nil, nil, finished)
	if result.Workspace.Audits != 1 || result.Workspace.ChangedFiles != 2 || result.Workspace.RecordedChanges != 1 || result.Workspace.NonRevertibleChanges != 1 || result.Workspace.IncompleteAudits != 0 {
		t.Fatalf("workspace metrics=%#v", result.Workspace)
	}
	if !hasSignal(result.Signals, "workspace_changes_captured") || !hasSignal(result.Signals, "workspace_changes_non_revertible") || result.Health != HealthAttention {
		t.Fatalf("workspace signals=%#v health=%s", result.Signals, result.Health)
	}
}

func event(kind domain.EventType, createdAt time.Time, payload any) domain.Event {
	data := json.RawMessage(`{}`)
	if payload != nil {
		data, _ = json.Marshal(payload)
	}
	return domain.Event{Type: kind, CreatedAt: createdAt, Data: data}
}

func timePointer(value time.Time) *time.Time { return &value }

func hasSignal(items []Signal, code string) bool {
	for _, item := range items {
		if item.Code == code {
			return true
		}
	}
	return false
}
