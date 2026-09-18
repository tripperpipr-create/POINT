package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
)

type repeatingToolModel struct {
	mu           sync.Mutex
	calls        int
	sawGuardrail bool
}

func (m *repeatingToolModel) Stream(_ context.Context, request providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	for _, message := range request.Messages {
		if message.Role == "tool" && strings.Contains(message.Content, `"code":"duplicate_tool_call"`) {
			m.sawGuardrail = true
		}
	}
	return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: fmt.Sprintf("call-%d", m.calls), Name: "list_files", Arguments: json.RawMessage(`{"maxDepth":1}`)}})
}

func TestIdenticalSuccessfulToolPlanIsNotExecutedRepeatedly(t *testing.T) {
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	model := &repeatingToolModel{}
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return model, nil })
	profile := domain.DefaultProfile()
	profile.AllowedTools = []string{"list_files"}
	profile.MaxSteps = 10
	profile.MaxDurationSeconds = 5
	configuration := domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC())
	run, err := engine.Start(StartInput{Configuration: configuration, Workspace: domain.Workspace{ID: "ws", Path: t.TempDir()}, Task: "inspect once"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
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
	if final.Status != domain.RunFailed || !strings.Contains(final.Error, "agent stalled") || final.RequestCount != 6 {
		t.Fatalf("final run=%#v", final)
	}
	model.mu.Lock()
	modelCalls, sawGuardrail := model.calls, model.sawGuardrail
	model.mu.Unlock()
	if modelCalls != 6 || !sawGuardrail {
		t.Fatalf("model calls=%d saw duplicate feedback=%v", modelCalls, sawGuardrail)
	}
	eventsList, _ := repo.ListByRun(context.Background(), run.ID)
	started, guarded, recovered := 0, 0, 0
	for _, event := range eventsList {
		if event.Type == domain.EventToolStarted {
			started++
		}
		if event.Type == domain.EventAgentGuardrail {
			guarded++
			if strings.Contains(string(event.Data), `"code":"tool_plan_recovery"`) {
				recovered++
			}
		}
	}
	if started != 1 || recovered != 1 || guarded < 3 {
		t.Fatalf("tool execution was not bounded: started=%d recovered=%d guardrails=%d events=%#v", started, recovered, guarded, eventsList)
	}
}

type mixedRepeatingModel struct{ calls int }

func (m *mixedRepeatingModel) Stream(_ context.Context, _ providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.calls++
	if err := emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: fmt.Sprintf("list-%d", m.calls), Name: "list_files", Arguments: json.RawMessage(`{"maxDepth":1}`)}}); err != nil {
		return err
	}
	return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: fmt.Sprintf("read-%d", m.calls), Name: "read_file", Arguments: json.RawMessage(`{"path":"missing.go"}`)}})
}

func TestSuccessfulCallInMixedFailingPlanIsNotRepeated(t *testing.T) {
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	model := &mixedRepeatingModel{}
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return model, nil })
	profile := domain.DefaultProfile()
	profile.AllowedTools = []string{"list_files", "read_file"}
	profile.MaxSteps = 10
	profile.MaxDurationSeconds = 5
	run, err := engine.Start(StartInput{
		Configuration: domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC()),
		Workspace:     domain.Workspace{ID: "ws", Path: t.TempDir()},
		Task:          "inspect mixed plan",
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		repo.mu.Lock()
		status := repo.runs[run.ID].Status
		repo.mu.Unlock()
		if status == domain.RunFailed {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	eventsList, _ := repo.ListByRun(context.Background(), run.ID)
	started := map[string]int{}
	for _, event := range eventsList {
		if event.Type != domain.EventToolStarted {
			continue
		}
		var payload struct {
			Tool string `json:"tool"`
		}
		_ = json.Unmarshal(event.Data, &payload)
		started[payload.Tool]++
	}
	if started["list_files"] != 1 || started["read_file"] != 2 {
		t.Fatalf("successful calls were repeated or failures were not retryable: %#v", started)
	}
}

func TestToolPlanRecoveryLetsModelChangeApproach(t *testing.T) {
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	model := &recoveringToolPlanModel{}
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return model, nil })
	profile := domain.DefaultProfile()
	profile.AllowedTools = []string{"list_files"}
	profile.MaxSteps = 10
	profile.MaxDurationSeconds = 5
	run, err := engine.Start(StartInput{
		Configuration: domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC()),
		Workspace:     domain.Workspace{ID: "ws", Path: t.TempDir()},
		Task:          "inspect once then finish",
	})
	if err != nil {
		t.Fatal(err)
	}
	finished := waitForTerminalRun(t, repo, run.ID)
	if finished.Status != domain.RunCompleted || !strings.Contains(finished.Result, "Recovered after plan gate") {
		t.Fatalf("recovery did not complete: %#v", finished)
	}
	if !model.sawRecovery {
		t.Fatal("model never received tool plan recovery feedback")
	}
	codes := guardrailCodes(t, repo, run.ID)
	if len(codes) < 2 || codes[len(codes)-1] == "agent_stalled" {
		t.Fatalf("expected soft recovery without stall, codes=%v", codes)
	}
	foundRecovery := false
	for _, code := range codes {
		if code == "tool_plan_recovery" {
			foundRecovery = true
		}
	}
	if !foundRecovery {
		t.Fatalf("missing tool_plan_recovery guardrail: %v", codes)
	}
}

type recoveringToolPlanModel struct {
	calls       int
	sawRecovery bool
}

func (m *recoveringToolPlanModel) Stream(_ context.Context, request providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.calls++
	for _, message := range request.Messages {
		if message.Role == "user" && strings.Contains(message.Content, "<point_tool_plan_gate>") && strings.Contains(message.Content, "tool_plan_recovery") {
			m.sawRecovery = true
			return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "Recovered after plan gate without repeating the same call."})
		}
	}
	return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: fmt.Sprintf("recover-%d", m.calls), Name: "list_files", Arguments: json.RawMessage(`{"maxDepth":1}`)}})
}

func TestInspectionHintMatchesRequirement(t *testing.T) {
	hint := inspectionHint(patchInspectionRequirement{Code: "inspection_required", RequiredTool: "list_files"})
	if !strings.Contains(hint, "list_files") {
		t.Fatalf("new-file hint=%q", hint)
	}
	hint = inspectionHint(patchInspectionRequirement{Code: "inspection_stale", RequiredTool: "read_file"})
	if !strings.Contains(hint, "read_file") {
		t.Fatalf("stale hint=%q", hint)
	}
}

func TestPlanHasUncompletedCalls(t *testing.T) {
	call := providers.ToolCall{Name: "list_files", Arguments: json.RawMessage(`{"maxDepth":1}`)}
	key := toolExecutionKey(call, 0)
	if !planHasUncompletedCalls([]providers.ToolCall{call}, map[string]struct{}{}, 0) {
		t.Fatal("empty completion map should be retryable")
	}
	if planHasUncompletedCalls([]providers.ToolCall{call}, map[string]struct{}{key: {}}, 0) {
		t.Fatal("completed plan should not be retryable")
	}
}
