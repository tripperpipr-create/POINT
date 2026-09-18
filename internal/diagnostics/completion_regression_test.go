package diagnostics

import (
	"local-agent-workbench/internal/domain"
	"testing"
	"time"
)

func TestDiagnosticsCannotHideLaterFailureWithAnotherSuccess(t *testing.T) {
	started := time.Now().UTC()
	run := domain.Run{ID: "regression", Status: domain.RunCompleted, ChangedFiles: []string{"main.go"}, StartedAt: started}
	events := []domain.Event{}
	check := func(command string, exit int) {
		at := started.Add(time.Duration(len(events)+1) * time.Second)
		events = append(events, event(domain.EventToolRequested, at, map[string]any{"tool": "run_command", "arguments": map[string]any{"command": command}}), event(domain.EventToolFinished, at.Add(time.Millisecond), map[string]any{"tool": "run_command", "result": domain.ToolResult{OK: true, Output: []byte(fmtOutcome(exit))}}))
	}
	check("go test ./...", 0)
	check("go test ./...", 1)
	check("go build ./...", 0)
	if Analyze(run, events, nil, nil, started).Verification.Recorded {
		t.Fatal("later failure hidden")
	}
	check("go test ./...", 0)
	if !Analyze(run, events, nil, nil, started).Verification.Recorded {
		t.Fatal("successful rerun rejected")
	}
}
func fmtOutcome(exit int) string {
	if exit == 0 {
		return `{"exitCode":0,"timedOut":false}`
	}
	return `{"exitCode":1,"timedOut":false}`
}
