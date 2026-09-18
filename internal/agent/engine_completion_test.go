package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
)

type completionRepairModel struct {
	calls       int
	sawFeedback bool
}

func (m *completionRepairModel) Stream(_ context.Context, request providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.calls++
	for _, message := range request.Messages {
		if message.Role == "user" && strings.Contains(message.Content, "<point_completion_gate>") {
			m.sawFeedback = true
		}
	}
	switch m.calls {
	case 1:
		arguments, _ := json.Marshal(map[string]any{"path": "main.go"})
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: "repair-read", Name: "read_file", Arguments: arguments}})
	case 2:
		arguments, _ := json.Marshal(map[string]any{
			"path": "main.go", "content": "package main\n\nfunc Ready() bool { return true }\n", "reason": "implement readiness",
		})
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: "repair-patch", Name: "propose_patch", Arguments: arguments}})
	case 3:
		return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "Implemented successfully."})
	case 4:
		arguments, _ := json.Marshal(map[string]any{"command": "go test ./...", "reason": "verify the workspace after the accepted change"})
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: "repair-verify", Name: "run_command", Arguments: arguments}})
	default:
		return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "Implemented and verified with go test ./...."})
	}
}

func TestEngineRepairsPrematureCompletionWithVerificationEvidence(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module completion-fixture\n\ngo 1.25\n"), 0600); err != nil {
		t.Fatal(err)
	}
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	model := &completionRepairModel{}
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return model, nil })
	profile := domain.DefaultProfile()
	profile.AllowedTools = []string{"read_file", "propose_patch", "run_command"}
	profile.MaxDurationSeconds = 10
	run, err := engine.Start(StartInput{
		Configuration: domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC()),
		Workspace:     domain.Workspace{ID: "ws", Path: root},
		Task:          "implement readiness",
	})
	if err != nil {
		t.Fatal(err)
	}
	firstApproval := resolveNextApproval(t, engine, run.ID, "", true)
	resolveNextApproval(t, engine, run.ID, firstApproval, true)
	finished := waitForTerminalRun(t, repo, run.ID)
	if finished.Status != domain.RunCompleted || !strings.Contains(finished.Result, "verified with go test") {
		t.Fatalf("run=%#v", finished)
	}
	if !model.sawFeedback {
		t.Fatal("model did not receive deterministic completion feedback")
	}
	statuses := completionStatuses(t, repo, run.ID)
	if fmt.Sprint(statuses) != "[revision_required accepted_after_revision]" {
		t.Fatalf("completion statuses=%v", statuses)
	}
}

type completionRejectModel struct {
	sawFeedback bool
}

func (m *completionRejectModel) Stream(_ context.Context, request providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	for _, message := range request.Messages {
		if message.Role == "user" && strings.Contains(message.Content, "<point_completion_gate>") {
			m.sawFeedback = true
		}
	}
	return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "Everything is done and tests pass."})
}

func TestEngineRejectsRepeatedUnevidencedCompletion(t *testing.T) {
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	model := &completionRejectModel{}
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return model, nil })
	profile := domain.DefaultProfile()
	profile.AllowedTools = []string{"run_command"}
	profile.MaxDurationSeconds = 5
	run, err := engine.Start(StartInput{
		Configuration: domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC()),
		Workspace:     domain.Workspace{ID: "ws", Path: t.TempDir()},
		Task:          "Run tests before completion",
	})
	if err != nil {
		t.Fatal(err)
	}
	finished := waitForTerminalRun(t, repo, run.ID)
	if finished.Status != domain.RunFailed || !strings.Contains(finished.Error, "completion gate") {
		t.Fatalf("unevidenced run was not rejected: %#v", finished)
	}
	if !model.sawFeedback {
		t.Fatal("model did not receive a correction opportunity")
	}
	statuses := completionStatuses(t, repo, run.ID)
	if fmt.Sprint(statuses) != "[revision_required revision_required rejected]" {
		t.Fatalf("completion statuses=%v", statuses)
	}
}

type repeatedFailedVerificationModel struct{ calls int }

func (m *repeatedFailedVerificationModel) Stream(_ context.Context, _ providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.calls++
	if m.calls == 1 || m.calls == 3 {
		arguments, _ := json.Marshal(map[string]any{"command": "go test ./point_agent_missing_package", "reason": "exercise failed verification handling"})
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: fmt.Sprintf("failed-command-%d", m.calls), Name: "run_command", Arguments: arguments}})
	}
	return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "Tests pass."})
}

func TestEngineRetriesFailedCommandInsteadOfTreatingItAsCompleted(t *testing.T) {
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	model := &repeatedFailedVerificationModel{}
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return model, nil })
	profile := domain.DefaultProfile()
	profile.AllowedTools = []string{"run_command"}
	profile.MaxDurationSeconds = 10
	run, err := engine.Start(StartInput{
		Configuration: domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC()),
		Workspace:     domain.Workspace{ID: "ws", Path: t.TempDir()},
		Task:          "Run tests before completion",
	})
	if err != nil {
		t.Fatal(err)
	}
	firstApproval := resolveNextApproval(t, engine, run.ID, "", true)
	resolveNextApproval(t, engine, run.ID, firstApproval, true)
	finished := waitForTerminalRun(t, repo, run.ID)
	if finished.Status != domain.RunFailed || !strings.Contains(finished.Error, "verification_failed") {
		t.Fatalf("failed verification was accepted: %#v", finished)
	}
	eventsList, err := repo.ListByRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	started, duplicates := 0, 0
	for _, event := range eventsList {
		if event.Type == domain.EventToolStarted && strings.Contains(string(event.Data), `"tool":"run_command"`) {
			started++
		}
		if event.Type == domain.EventToolFinished && strings.Contains(string(event.Data), "duplicate_tool_call") {
			duplicates++
		}
	}
	if started != 2 || duplicates != 0 {
		t.Fatalf("failed command starts=%d duplicate rejections=%d", started, duplicates)
	}
}

type emptyModel struct{}

func (emptyModel) Stream(context.Context, providers.ModelRequest, func(providers.ModelEvent) error) error {
	return nil
}

func TestEmptyModelResponseFailsInsteadOfReportingFalseSuccess(t *testing.T) {
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return emptyModel{}, nil })
	profile := domain.DefaultProfile()
	profile.MaxDurationSeconds = 5
	run, err := engine.Start(StartInput{
		Configuration: domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC()),
		Workspace:     domain.Workspace{ID: "ws", Path: t.TempDir()},
		Task:          "do not accept empty",
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	var final domain.Run
	for time.Now().Before(deadline) {
		repo.mu.Lock()
		final = repo.runs[run.ID]
		repo.mu.Unlock()
		if final.Status == domain.RunFailed {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if final.Status != domain.RunFailed || !strings.Contains(final.Error, "empty response") {
		t.Fatalf("empty response was not rejected: %#v", final)
	}
}
