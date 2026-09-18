package diagnostics

import (
	"local-agent-workbench/internal/domain"
	"testing"
	"time"
)

func TestStructuredVerificationRequiresCurrentCompleteEvidence(t *testing.T) {
	at := time.Now().UTC()
	brief := domain.TaskBrief{Version: 2, Criteria: []domain.AcceptanceCriterion{{ID: "test", Kind: "verification", Text: "Tests", Tool: "run_command"}}}
	start := event(domain.EventRunStarted, at, map[string]any{"taskBrief": brief})
	successfulCommand := []domain.Event{
		event(domain.EventToolRequested, at.Add(time.Second), map[string]any{"tool": "run_command", "arguments": map[string]any{"command": "go test ./..."}}),
		event(domain.EventToolFinished, at.Add(2*time.Second), map[string]any{"tool": "run_command", "result": domain.ToolResult{OK: true, Output: []byte(`{"exitCode":0,"timedOut":false}`)}}),
	}
	evidence := func(version int, status, kind string) domain.Event {
		return event(domain.EventCompletionChecked, at.Add(3*time.Second), map[string]any{"status": "accepted_after_revision", "evidence": map[string]any{"briefVersion": version, "status": status, "criteria": []any{map[string]any{"criterionId": "test", "kind": kind, "status": "satisfied"}}}})
	}
	for _, tc := range []struct {
		name string
		tail []domain.Event
		want bool
	}{
		{"missing", nil, false},
		{"verified", []domain.Event{evidence(2, "verified", "verification")}, true},
		{"old version", []domain.Event{evidence(1, "verified", "verification")}, false},
		{"user review", []domain.Event{evidence(2, "needs_review", "verification")}, false},
		{"wrong criterion", []domain.Event{evidence(2, "verified", "manual")}, false},
		{"later command", []domain.Event{evidence(2, "verified", "verification"), event(domain.EventToolRequested, at.Add(4*time.Second), map[string]any{"tool": "run_command"})}, false},
		{"later change", []domain.Event{evidence(2, "verified", "verification"), event(domain.EventWorkspaceChanged, at.Add(4*time.Second), map[string]any{})}, false},
		{"later malformed gate", []domain.Event{evidence(2, "verified", "verification"), event(domain.EventCompletionChecked, at.Add(4*time.Second), map[string]any{})}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := append([]domain.Event{start}, successfulCommand...)
			events = append(events, tc.tail...)
			run := domain.Run{ID: "structured", Status: domain.RunCompleted, StartedAt: at}
			got := Analyze(run, events, nil, nil, at).Verification
			if !got.Required || got.Recorded != tc.want {
				t.Fatalf("got %+v, recorded want %v", got, tc.want)
			}
			run.Status = domain.RunInterrupted
			if Analyze(run, events, nil, nil, at).Verification.Recorded {
				t.Fatal("interrupted run counted for learning")
			}
		})
	}
}
