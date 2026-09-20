package workflows

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"local-agent-workbench/internal/agent"
	"local-agent-workbench/internal/domain"
)

type memoryRepository struct {
	mu   sync.Mutex
	runs map[string]domain.WorkflowRun
}

func (r *memoryRepository) SaveWorkflowRun(_ context.Context, run domain.WorkflowRun) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.runs[run.ID] = run
	return nil
}

func (r *memoryRepository) get(id string) domain.WorkflowRun {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.runs[id]
}

type scriptedEngine struct {
	mu     sync.Mutex
	inputs []agent.StartInput
}

func (e *scriptedEngine) Start(input agent.StartInput) (domain.Run, error) {
	e.mu.Lock()
	index := len(e.inputs)
	e.inputs = append(e.inputs, input)
	e.mu.Unlock()
	runID := domain.NewID("run")
	profileID := input.Configuration.Profile.ID
	started := time.Now().UTC()
	run := domain.Run{ID: runID, ProfileID: profileID, Status: domain.RunRunning, StartedAt: started}
	go func() {
		result := "анализ архитектуры"
		if index == 1 {
			result = "финальный план"
		}
		finished := time.Now().UTC()
		input.OnFinished(domain.Run{ID: runID, ProfileID: profileID, Status: domain.RunCompleted, Result: result, StartedAt: started, FinishedAt: &finished})
	}()
	return run, nil
}

func (e *scriptedEngine) Cancel(string) error { return nil }

func (e *scriptedEngine) captured() []agent.StartInput {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]agent.StartInput(nil), e.inputs...)
}

func TestSequentialWorkflowHandoffAndImmutableSnapshot(t *testing.T) {
	repo := &memoryRepository{runs: make(map[string]domain.WorkflowRun)}
	engine := &scriptedEngine{}
	manager := NewManager(repo, engine)
	first := domain.DefaultProfile()
	first.ID, first.Name = "analyst", "Аналитик"
	second := domain.DefaultProfile()
	second.ID, second.Name = "developer", "Разработчик"
	workflow := domain.AgentWorkflow{ID: "workflow_0123456789abcdef01234567", Name: "Анализ и план", Steps: []domain.WorkflowStep{
		{ID: "step_0123456789abcdef01234567", Name: "Анализ", ProfileID: first.ID, Instruction: "Изучи риски", IncludeOriginalContext: true},
		{ID: "step_1123456789abcdef01234567", Name: "План", ProfileID: second.ID, Instruction: "Составь план", IncludePreviousResult: true},
	}}
	originalContext := []domain.RunContextItem{{ID: "context-1", Kind: domain.ContextText, Label: "Требования", Content: "Нужен безопасный API", Size: 34}}
	run, err := manager.Start(StartInput{ApplicationVersion: "0.5.0", Workflow: workflow, Profiles: []domain.AgentProfile{first, second}, Workspace: domain.Workspace{ID: "ws", Path: t.TempDir()}, Task: "Спроектируй изменение", APIKeys: map[string]string{first.ID: "key-a", second.ID: "key-b"}, ContextItems: originalContext})
	if err != nil {
		t.Fatal(err)
	}
	workflow.Name = "изменено после старта"
	workflow.Steps[0].Instruction = "подмена"
	deadline := time.Now().Add(3 * time.Second)
	var stored domain.WorkflowRun
	for time.Now().Before(deadline) {
		stored = repo.get(run.ID)
		if stored.Status == domain.RunCompleted {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if stored.Status != domain.RunCompleted || stored.Result != "финальный план" || len(stored.StepRuns) != 2 {
		t.Fatalf("workflow run=%#v", stored)
	}
	if stored.Snapshot.Workflow.Name != "Анализ и план" || stored.Snapshot.Workflow.Steps[0].Instruction != "Изучи риски" {
		t.Fatalf("workflow snapshot changed: %#v", stored.Snapshot)
	}
	inputs := engine.captured()
	if len(inputs) != 2 || inputs[0].APIKey != "key-a" || inputs[1].APIKey != "key-b" {
		t.Fatalf("child inputs=%#v", inputs)
	}
	if len(inputs[0].ContextItems) != 1 || inputs[0].ContextItems[0].Content != "Нужен безопасный API" {
		t.Fatalf("original context=%#v", inputs[0].ContextItems)
	}
	if len(inputs[1].ContextItems) != 1 || inputs[1].ContextItems[0].Label != "Результат предыдущего этапа" || inputs[1].ContextItems[0].Content != "анализ архитектуры" {
		t.Fatalf("handoff context=%#v", inputs[1].ContextItems)
	}
	if inputs[0].Configuration.Profile.ID != first.ID || inputs[1].Configuration.Profile.ID != second.ID {
		t.Fatalf("wrong profile snapshots")
	}
}

func TestWorkflowFinalizerFailureCannotPublishCompletedStatus(t *testing.T) {
	repo := &memoryRepository{runs: make(map[string]domain.WorkflowRun)}
	manager := NewManager(repo, &scriptedEngine{})
	profile := domain.DefaultProfile()
	workflow := domain.AgentWorkflow{ID: "workflow_0123456789abcdef01234567", Name: "Finalize", Steps: []domain.WorkflowStep{{ID: "step_0123456789abcdef01234567", Name: "Run", ProfileID: profile.ID}}}
	finalized := make(chan domain.WorkflowRun, 1)
	run, err := manager.Start(StartInput{
		ApplicationVersion: "test", Workflow: workflow, Profiles: []domain.AgentProfile{profile},
		Workspace: domain.Workspace{ID: "ws", Path: t.TempDir()}, Task: "finish safely",
		OnFinished: func(candidate domain.WorkflowRun) error {
			finalized <- candidate
			return errors.New("change set persistence failed")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case candidate := <-finalized:
		if candidate.Status != domain.RunCompleted {
			t.Fatalf("finalizer candidate status=%s", candidate.Status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("workflow finalizer was not called")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		stored := repo.get(run.ID)
		if stored.Status == domain.RunFailed {
			if !strings.Contains(stored.Error, "change set persistence failed") {
				t.Fatalf("finalizer failure=%q", stored.Error)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("workflow published completed status after finalizer failure")
}

type blockingEngine struct {
	mu       sync.Mutex
	run      domain.Run
	finished func(domain.Run)
	started  chan struct{}
	once     sync.Once
}

func (e *blockingEngine) Start(input agent.StartInput) (domain.Run, error) {
	e.mu.Lock()
	e.run = domain.Run{ID: domain.NewID("run"), ProfileID: input.Configuration.Profile.ID, Status: domain.RunRunning, StartedAt: time.Now().UTC()}
	e.finished = input.OnFinished
	run := e.run
	e.mu.Unlock()
	close(e.started)
	return run, nil
}

func (e *blockingEngine) Cancel(id string) error {
	e.once.Do(func() {
		e.mu.Lock()
		run, callback := e.run, e.finished
		e.mu.Unlock()
		if run.ID == id && callback != nil {
			now := time.Now().UTC()
			run.Status, run.Error, run.FinishedAt = domain.RunCancelled, "Run cancelled", &now
			callback(run)
		}
	})
	return nil
}

func TestWorkflowCancellationCancelsCurrentChild(t *testing.T) {
	repo := &memoryRepository{runs: make(map[string]domain.WorkflowRun)}
	engine := &blockingEngine{started: make(chan struct{})}
	manager := NewManager(repo, engine)
	profile := domain.DefaultProfile()
	workflow := domain.AgentWorkflow{ID: "workflow_0123456789abcdef01234567", Name: "Long workflow", Steps: []domain.WorkflowStep{{ID: "step_0123456789abcdef01234567", Name: "Wait", ProfileID: profile.ID}}}
	run, err := manager.Start(StartInput{ApplicationVersion: "test", Workflow: workflow, Profiles: []domain.AgentProfile{profile}, Workspace: domain.Workspace{ID: "ws", Path: t.TempDir()}, Task: "wait"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-engine.started:
	case <-time.After(time.Second):
		t.Fatal("child run did not start")
	}
	if err = manager.Cancel(run.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		stored := repo.get(run.ID)
		if stored.Status == domain.RunCancelled {
			if stored.StepRuns[0].Status != domain.RunCancelled {
				t.Fatalf("child stage status=%s", stored.StepRuns[0].Status)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("workflow did not cancel")
}

func TestExternalCursorStepClaimHeartbeatComplete(t *testing.T) {
	repo := &memoryRepository{runs: make(map[string]domain.WorkflowRun)}
	manager := NewManager(repo, &scriptedEngine{})
	profile := domain.DefaultCursorProfile()
	workflow := domain.AgentWorkflow{
		ID: "workflow_0123456789abcdef01234567", Name: "Cursor campaign",
		Steps: []domain.WorkflowStep{
			{ID: "step_0123456789abcdef01234567", Name: "Cursor", ProfileID: profile.ID, Kind: "cursor", Instruction: "Implement"},
			{ID: "step_1123456789abcdef01234567", Name: "Verify", ProfileID: profile.ID, Kind: "manual", OnFailure: "skip"},
		},
	}
	run, err := manager.Start(StartInput{
		ApplicationVersion: "test",
		Workflow:           workflow,
		Profiles:           []domain.AgentProfile{profile},
		Workspace:          domain.Workspace{ID: "ws", Path: t.TempDir()},
		Task:               "ship feature",
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		stored := repo.get(run.ID)
		if len(stored.StepRuns) > 0 && stored.StepRuns[0].Status == domain.RunWaiting {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	stored := repo.get(run.ID)
	if stored.StepRuns[0].Status != domain.RunWaiting || stored.StepRuns[0].Kind != "cursor" {
		t.Fatalf("waiting cursor step=%#v", stored.StepRuns[0])
	}
	token, err := manager.Claim(run.ID, workflow.Steps[0].ID)
	if err != nil || token == "" {
		t.Fatalf("claim token=%q err=%v", token, err)
	}
	if _, err = manager.Claim(run.ID, workflow.Steps[0].ID); err == nil {
		t.Fatal("duplicate claim must fail")
	}
	if err = manager.Heartbeat(run.ID, workflow.Steps[0].ID, token); err != nil {
		t.Fatal(err)
	}
	if err = manager.Complete(run.ID, workflow.Steps[0].ID, token, domain.RunCompleted, "done by cursor", ""); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		stored = repo.get(run.ID)
		if len(stored.StepRuns) > 1 && stored.StepRuns[1].Status == domain.RunWaiting {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	stored = repo.get(run.ID)
	if stored.StepRuns[0].Status != domain.RunCompleted || stored.StepRuns[0].Result != "done by cursor" {
		t.Fatalf("completed cursor step=%#v", stored.StepRuns[0])
	}
	if stored.StepRuns[1].Status != domain.RunWaiting || stored.StepRuns[1].Kind != "manual" {
		t.Fatalf("manual step=%#v", stored.StepRuns[1])
	}
	manualToken, err := manager.Claim(run.ID, workflow.Steps[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = manager.Complete(run.ID, workflow.Steps[1].ID, manualToken, domain.RunFailed, "", "blocked"); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		stored = repo.get(run.ID)
		if stored.Status == domain.RunCompleted || stored.Status == domain.RunFailed {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	stored = repo.get(run.ID)
	if stored.Status != domain.RunCompleted {
		t.Fatalf("onFailure=skip should continue to completion, got %#v", stored)
	}
}

func TestWorkflowSuccessWaitsForFinalization(t *testing.T) {
	repo := &memoryRepository{runs: map[string]domain.WorkflowRun{}}
	manager := NewManager(repo, &scriptedEngine{})
	entered, release := make(chan struct{}), make(chan struct{})
	profile := domain.DefaultProfile()
	profile.ID = "one"
	workflow := domain.AgentWorkflow{ID: "workflow_0123456789abcdef01234567", Name: "Finalize", Steps: []domain.WorkflowStep{{ID: "step_0123456789abcdef01234567", Name: "Work", ProfileID: profile.ID, Instruction: "Reply"}}}
	run, err := manager.Start(StartInput{ApplicationVersion: "test", Workflow: workflow, Profiles: []domain.AgentProfile{profile}, Workspace: domain.Workspace{ID: "ws", Path: t.TempDir()}, Task: "Reply", OnFinished: func(domain.WorkflowRun) error { close(entered); <-release; return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.StopAll()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("finalizer not called")
	}
	before := repo.get(run.ID)
	activeBefore := manager.IsWorkflowActive(workflow.ID)
	close(release)
	if before.Status == domain.RunCompleted || !activeBefore {
		t.Fatal("completion exposed before finalization")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if repo.get(run.ID).Status == domain.RunCompleted {
			if manager.IsWorkflowActive(workflow.ID) {
				t.Fatal("completed workflow still blocks definition")
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("completion never persisted")
}
