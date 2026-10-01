package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
)

type finishingModel struct {
	mu       sync.Mutex
	requests []providers.ModelRequest
}

func (m *finishingModel) Stream(_ context.Context, request providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.mu.Lock()
	m.requests = append(m.requests, request)
	m.mu.Unlock()
	return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "The work is finished."})
}

type scriptedVerifier struct {
	mu       sync.Mutex
	outcomes []StageVerifyOutcome
	requests []StageVerifyRequest
}

func (v *scriptedVerifier) VerifyBeforeCompletion(_ context.Context, request StageVerifyRequest) StageVerifyOutcome {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.requests = append(v.requests, request)
	if len(v.outcomes) == 0 {
		return StageVerifyOutcome{}
	}
	outcome := v.outcomes[0]
	v.outcomes = v.outcomes[1:]
	return outcome
}

func runWithVerifier(t *testing.T, verifier *scriptedVerifier) (*finishingModel, *memoryRepo, domain.Run) {
	t.Helper()
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	model := &finishingModel{}
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return model, nil })
	engine.SetStageVerifier(verifier)
	profile := domain.DefaultProfile()
	profile.Model = "verifier-model"
	profile.MaxDurationSeconds = 10
	run, err := engine.Start(StartInput{
		Configuration: domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC()),
		Workspace:     domain.Workspace{ID: "ws", Path: t.TempDir()},
		Task:          "Explain the result.",
		ExecutionID:   "execution-1", QuestID: "quest-1", FlowRunID: "flowrun-1", FlowNodeID: "node-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	return model, repo, waitForTerminalRun(t, repo, run.ID)
}

// Проваленная проверка Point возвращает этап в работу с причиной — пока агент
// может исправить, а не после приёмки.
func TestFailedPreAcceptCheckSendsTheStageBackWithTheCause(t *testing.T) {
	verifier := &scriptedVerifier{outcomes: []StageVerifyOutcome{
		{Ran: true, Passed: false, Feedback: "Check verify failed: npm 12 blocked install scripts"},
		{Ran: true, Passed: true},
	}}
	model, repo, finished := runWithVerifier(t, verifier)
	if finished.Status != domain.RunCompleted {
		t.Fatalf("run=%#v", finished)
	}
	if len(model.requests) != 2 || len(verifier.requests) != 2 {
		t.Fatalf("model turns=%d checks=%d, want 2 and 2", len(model.requests), len(verifier.requests))
	}
	last := model.requests[1].Messages[len(model.requests[1].Messages)-1]
	if last.Role != "user" || !strings.Contains(last.Content, "npm 12 blocked install scripts") {
		t.Fatalf("the agent did not get the cause: %+v", last)
	}
	if request := verifier.requests[0]; request.FlowNodeID != "node-1" || request.ExecutionID != "execution-1" || request.RunID != finished.ID {
		t.Fatalf("verifier request lost the correlation: %+v", request)
	}
	events, _ := repo.ListByRun(context.Background(), finished.ID)
	checks := 0
	checkpointsJournaled := false
	for _, event := range events {
		// Цена контрольных точек идёт нарастающим итогом в запросе к модели.
		if event.Type == domain.EventModelRequested && strings.Contains(string(event.Data), `"checkpoints":{"count":`) && !strings.Contains(string(event.Data), `"checkpoints":{"count":0`) {
			checkpointsJournaled = true
		}
		if event.Type == domain.EventCompletionChecked && strings.Contains(string(event.Data), `"checkKind":"pre_accept"`) {
			checks++
		}
	}
	if !checkpointsJournaled {
		t.Fatal("checkpoint cost is not journaled with model requests")
	}
	if checks != 2 {
		t.Fatalf("pre-accept checks journaled %d times, want 2", checks)
	}
}

// Проверка — помощь, а не второй судья: после двух возвратов этап завершается,
// и вердикт выносит приёмка.
func TestPreAcceptCheckDoesNotHoldTheStageForever(t *testing.T) {
	failing := StageVerifyOutcome{Ran: true, Passed: false, Feedback: "still failing"}
	verifier := &scriptedVerifier{outcomes: []StageVerifyOutcome{failing, failing, failing, failing}}
	model, _, finished := runWithVerifier(t, verifier)
	if finished.Status != domain.RunCompleted {
		t.Fatalf("run=%#v", finished)
	}
	if len(verifier.requests) != maxPreAcceptChecks || len(model.requests) != maxPreAcceptChecks+1 {
		t.Fatalf("checks=%d model turns=%d", len(verifier.requests), len(model.requests))
	}
}
