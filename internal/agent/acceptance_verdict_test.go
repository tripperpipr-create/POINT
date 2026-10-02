package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
)

func TestCompletionVerdictRequiresObservedIndependentAcceptance(t *testing.T) {
	for _, tc := range []struct {
		observed, passed, review bool
		want                     string
	}{
		{false, true, false, "implementation_ready"}, {true, false, false, "implementation_ready"},
		{true, true, true, "needs_review"}, {true, true, false, "accepted_after_revision"},
	} {
		if got := completionStatusAfterPreAccept(tc.observed, tc.passed, tc.review); got != tc.want {
			t.Fatalf("%+v: %s", tc, got)
		}
	}
}

func TestManualPreAcceptDoesNotSpendCorrectionTurnsOrClaimAcceptance(t *testing.T) {
	v := &scriptedVerifier{outcomes: []StageVerifyOutcome{{Ran: true, Passed: true, NeedsReview: true, PendingCriterionIDs: []string{"http"}}}}
	model, repo, run := runWithVerifier(t, v)
	if run.Status != domain.RunCompleted || len(model.requests) != 1 {
		t.Fatalf("run=%s turns=%d", run.Status, len(model.requests))
	}
	events, _ := repo.ListByRun(context.Background(), run.ID)
	seen := false
	for _, event := range events {
		if event.Type != domain.EventCompletionChecked {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(event.Data, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["acceptancePassed"] == true {
			t.Fatal("manual criterion reported accepted")
		}
		if payload["checkKind"] == "pre_accept" {
			seen = payload["status"] == "needs_review" && payload["machineChecksPassed"] == true
		}
	}
	if !seen {
		t.Fatal("missing manual review verdict")
	}
}

func TestManagedVerificationKeepsScopeAndRegressionGuards(t *testing.T) {
	profile := domain.DefaultProfile()
	profile.ManagedVerification = true
	brief := &domain.TaskBrief{Criteria: []domain.AcceptanceCriterion{{ID: "test", Kind: "verification", Tool: "run_command", Arguments: json.RawMessage(`{"command":"go test ./..."}`)}}}
	tracker := newCompletionTracker(profile, "Fix code and test", nil, brief)
	if missing := tracker.Missing(0, nil); len(missing) != 0 {
		t.Fatalf("managed batch duplicated: %+v", missing)
	}
	if missing := tracker.Missing(1, []string{"forbidden.go"}); len(missing) != 1 || missing[0].Code != "task_scope_violation" {
		t.Fatalf("scope guard lost: %+v", missing)
	}
	managed := SystemMessage(profile)
	if !strings.Contains(managed, "Point manages independent acceptance") || strings.Contains(managed, "run it once before editing") {
		t.Fatal("managed prompt contradicts independent verification")
	}
	profile.ManagedVerification = false
	if !strings.Contains(SystemMessage(profile), "run it once before editing") {
		t.Fatal("standalone prompt lost verification")
	}
	if strings.Contains(tracker.ContractInstructions(), "For machine criteria execute") {
		t.Fatal("brief prompt still requires duplicate checks")
	}
}
