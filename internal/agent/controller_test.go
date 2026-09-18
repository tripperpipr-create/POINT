package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
)

func TestActiveClockExcludesPausedIntervals(t *testing.T) {
	clock := newActiveClock(2)
	clock.start()
	time.Sleep(30 * time.Millisecond)
	clock.stop()
	elapsed, _ := clock.snapshot()
	if elapsed < 20 {
		t.Fatalf("expected active work to count, elapsed=%d", elapsed)
	}
	time.Sleep(40 * time.Millisecond)
	elapsed2, _ := clock.snapshot()
	if elapsed2 != elapsed {
		t.Fatalf("paused interval consumed budget: before=%d after=%d", elapsed, elapsed2)
	}
}

func TestActiveTimeExhaustionRequestsPause(t *testing.T) {
	clock := newActiveClock(1)
	clock.restore(1000, 0, 1)
	if !clock.exhausted() {
		t.Fatal("expected exhausted clock")
	}
	if err := clock.extend(); err != nil {
		t.Fatal(err)
	}
	if clock.exhausted() {
		t.Fatal("extend should grant another second of budget")
	}
	if err := clock.extend(); err == nil {
		t.Fatal("second extend must fail")
	}
}

func TestCheckpointRoundTripConversation(t *testing.T) {
	history := newConversationHistory([]providers.Message{{Role: "system", Content: "sys"}, {Role: "user", Content: "task"}})
	history.AppendRound(conversationRound{
		Step:      1,
		Assistant: providers.Message{Role: "assistant", Content: "looking"},
		Tools: []conversationToolTurn{{
			Call:         providers.ToolCall{ID: "c1", Name: "read_file", Arguments: json.RawMessage(`{"path":"a.go"}`)},
			Result:       domain.ToolResult{OK: true, Output: json.RawMessage(`{"path":"a.go"}`)},
			Message:      providers.Message{Role: "tool", ToolCallID: "c1", Content: `{"path":"a.go"}`},
			ExecutionKey: "read:a.go:0",
			Replayable:   true,
		}},
	})
	raw, err := history.marshal()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := unmarshalConversationHistory(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.rounds) != 1 || restored.rounds[0].Assistant.Content != "looking" {
		t.Fatalf("restored=%#v", restored)
	}
}

type singleToolModel struct {
	name string
	args map[string]any
}

func (m *singleToolModel) Stream(_ context.Context, _ providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	args, _ := json.Marshal(m.args)
	return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: "t1", Name: m.name, Arguments: args}})
}

func TestActiveTimeBudgetIgnoresApprovalWait(t *testing.T) {
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) {
		return &singleToolModel{name: "run_command", args: map[string]any{"command": "go test ./...", "reason": "verify"}}, nil
	})
	brief, err := domain.ApproveTaskBrief(domain.NormalizeTaskBrief(domain.TaskBrief{
		Mode: domain.TaskModePrecise, Goal: "test", ResultKind: "code",
		Criteria:    []domain.AcceptanceCriterion{{ID: "c1", Text: "manual check", Kind: "manual"}},
		Permissions: domain.TaskPermissions{ExecuteCommands: true},
		Budget:      domain.TaskBudget{Tokens: 1000, ActiveSeconds: 1, MaxParallel: 1, MaxAttempts: 1},
	}))
	if err != nil {
		t.Fatal(err)
	}
	profile := domain.DefaultProfile()
	profile.ID = "p1"
	profile.AllowedTools = []string{"run_command"}
	profile.MaxDurationSeconds = 30
	profile.ApprovalMode = domain.ApprovalAlways
	run, err := engine.Start(StartInput{
		Workspace: domain.Workspace{ID: "ws", Path: t.TempDir()}, Task: "test", TaskBrief: &brief,
		Configuration: domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC()),
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		stored := repo.runs[run.ID]
		if stored.Status == domain.RunWaiting {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if repo.runs[run.ID].Status != domain.RunWaiting {
		engine.StopAll()
		t.Fatalf("expected waiting approval, got %#v", repo.runs[run.ID])
	}
	time.Sleep(1200 * time.Millisecond)
	stored := repo.runs[run.ID]
	if stored.Controller.ActiveElapsedMs >= 1000 {
		engine.StopAll()
		t.Fatalf("approval wait consumed active budget: %#v", stored.Controller)
	}
	engine.StopAll()
}

func TestContinueFromCheckpointRestoresMessages(t *testing.T) {
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) {
		return finalAnswerModel{}, nil
	})
	profile := domain.DefaultProfile()
	profile.ID = "p1"
	profile.AllowedTools = []string{}
	profile.MaxSteps = 3
	snapshot := domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC())
	run := domain.Run{
		ID: "run-resume", AgentID: "agent", ProfileID: profile.ID, WorkspaceID: "ws",
		Task: "Return Answer", ConfigurationSnapshot: snapshot, Status: domain.RunPaused,
		StartedAt: time.Now().UTC(), Controller: domain.RunControllerState{Resumable: true, CheckpointSeq: 1},
	}
	if err := repo.SaveRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	history := newConversationHistory(BuildStableMessages(profile, nil, run.Task, nil))
	historyJSON, _ := history.marshal()
	completion := newCompletionTracker(profile, run.Task, nil)
	completionJSON, _ := completion.marshal()
	observations := newObservationTracker(nil)
	observationsJSON, _ := observations.marshal()
	checkpoint := domain.RunCheckpoint{
		RunID: run.ID, Seq: 1, CreatedAt: time.Now().UTC(), NextStep: 1,
		HistoryJSON: historyJSON, CompletionJSON: completionJSON, ObservationsJSON: observationsJSON,
	}
	if err := repo.SaveRunCheckpoint(context.Background(), checkpoint); err != nil {
		t.Fatal(err)
	}
	continued, err := engine.ContinueFromCheckpoint(StartInput{
		Configuration: snapshot, Workspace: domain.Workspace{ID: "ws", Path: t.TempDir()},
		Task: run.Task,
	}, run, checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	finished := waitForTerminalRun(t, repo, continued.ID)
	if finished.Status != domain.RunCompleted {
		t.Fatalf("resume failed: %#v", finished)
	}
}

func TestInFlightCheckpointRejectsContinue(t *testing.T) {
	engine := NewEngine(newMemoryRepo(), nil)
	run := domain.Run{ID: "run-inflight", Status: domain.RunPaused, ConfigurationSnapshot: domain.NewRunConfigurationSnapshot("test", domain.DefaultProfile(), nil, time.Now().UTC())}
	run.ConfigurationSnapshot.Profile.ID = "p1"
	checkpoint := domain.RunCheckpoint{RunID: run.ID, Seq: 1, InFlightCallID: "call-1"}
	_, err := engine.ContinueFromCheckpoint(StartInput{
		Configuration: run.ConfigurationSnapshot, Workspace: domain.Workspace{ID: "ws", Path: t.TempDir()}, Task: "x",
	}, run, checkpoint)
	if err == nil || !strings.Contains(err.Error(), "unknown_outcome") {
		t.Fatalf("expected unknown_outcome, got %v", err)
	}
}
