package diagnostics

import (
	"encoding/json"
	"local-agent-workbench/internal/domain"
	"testing"
	"time"
)

func TestHistoricalAcceptanceYieldsToPendingEvidence(t *testing.T) {
	raw := json.RawMessage(`{"status":"accepted_after_revision","acceptancePassed":true,"evidence":{"status":"needs_review","criteria":[{"criterionId":"http","status":"needs_review"}]}}`)
	original := string(raw)
	at := time.Now()
	run := domain.Run{ID: "r", Status: domain.RunCompleted, StartedAt: at, FinishedAt: &at}
	result := Analyze(run, []domain.Event{{Type: domain.EventCompletionChecked, CreatedAt: at, Data: raw}}, nil, nil, at)
	if result.Completion.AcceptedAfterRevision || !result.Completion.NeedsReview || len(result.Completion.PendingCriterionIDs) != 1 || result.Completion.PendingCriterionIDs[0] != "http" {
		t.Fatalf("optimistic historical verdict survived: %+v", result.Completion)
	}
	if string(raw) != original {
		t.Fatal("historical event mutated")
	}
}
