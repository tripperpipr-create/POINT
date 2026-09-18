package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
	"local-agent-workbench/internal/sandbox"
)

type retryEventModel struct{}

func (retryEventModel) Stream(_ context.Context, _ providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	if err := emit(providers.ModelEvent{Kind: providers.EventRetry, Attempt: 2, DelayMs: 250, Message: "temporary provider error"}); err != nil {
		return err
	}
	return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "Recovered."})
}

func TestProviderRetryIsPersistedInRunChronicle(t *testing.T) {
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return retryEventModel{}, nil })
	profile := domain.DefaultProfile()
	profile.MaxDurationSeconds = 5
	run, err := engine.Start(StartInput{
		Configuration: domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC()),
		Workspace:     domain.Workspace{ID: "ws", Path: t.TempDir()},
		Task:          "recover provider",
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		repo.mu.Lock()
		status := repo.runs[run.ID].Status
		repo.mu.Unlock()
		if status == domain.RunCompleted {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	eventsList, _ := repo.ListByRun(context.Background(), run.ID)
	for _, event := range eventsList {
		if event.Type == domain.EventModelRetrying && strings.Contains(string(event.Data), `"attempt":2`) {
			return
		}
	}
	t.Fatalf("provider retry event was not persisted: %#v", eventsList)
}

type fallbackModel struct {
	mu     sync.Mutex
	models []string
}

func (m *fallbackModel) Stream(_ context.Context, request providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.mu.Lock()
	m.models = append(m.models, request.Model)
	m.mu.Unlock()
	if request.Model == "primary-model" {
		return errors.New("provider rate limit: 429")
	}
	return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "Fallback completed the task."})
}

func TestProjectTaskFreezesModelFallback(t *testing.T) {
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	model := &fallbackModel{}
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return model, nil })
	engine.SetProcessExecutor(strongBoundaryExecutor{})
	profile := domain.DefaultProfile()
	profile.Model = "primary-model"
	profile.FallbackModels = []string{"fallback-model"}
	profile.MaxDurationSeconds = 5
	profile.MaxSteps = 2
	brief := domain.NormalizeTaskBrief(domain.TaskBrief{
		Mode: domain.TaskModeProject, State: "ready", Goal: "Build", ResultKind: "report",
		Criteria: []domain.AcceptanceCriterion{{ID: "c1", Text: "Report", Kind: "manual"}},
	})
	approved, err := domain.ApproveTaskBrief(brief)
	if err != nil {
		t.Fatal(err)
	}
	run, err := engine.Start(StartInput{
		Configuration: domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC()),
		Workspace:     domain.Workspace{ID: "ws", Path: t.TempDir()},
		SandboxPath:   t.TempDir(),
		Task:          "Build the report",
		TaskBrief:     &approved,
	})
	if err != nil {
		t.Fatal(err)
	}
	finished := waitForTerminalRun(t, repo, run.ID)
	if finished.Status != domain.RunFailed {
		t.Fatalf("project fallback must fail closed, got %#v", finished)
	}
	if !strings.Contains(finished.Error, "freezes model") {
		t.Fatalf("error=%q", finished.Error)
	}
}

type reasoningBudgetModel struct {
	mu     sync.Mutex
	models []string
}

func (m *reasoningBudgetModel) Stream(_ context.Context, request providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.mu.Lock()
	m.models = append(m.models, request.Model)
	m.mu.Unlock()
	if request.Model == "primary-model" {
		return errors.New("model returned no answer: the entire output budget of 65536 tokens went to reasoning (finish_reason=length)")
	}
	return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "Fallback completed the task."})
}

type reasoningDisableThinkingModel struct {
	mu             sync.Mutex
	disableRetries int
	requests       int
}

func (m *reasoningDisableThinkingModel) Stream(_ context.Context, request providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.mu.Lock()
	m.requests++
	disable := request.DisableThinking
	if disable {
		m.disableRetries++
	}
	m.mu.Unlock()
	if !disable {
		return errors.New("model returned no answer: the entire output budget of 16384 tokens went to reasoning (finish_reason=length)")
	}
	return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "Completed after thinking was disabled."})
}

func TestProjectTaskFallsBackWhenReasoningConsumesOutputBudget(t *testing.T) {
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	model := &reasoningBudgetModel{}
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return model, nil })
	engine.SetProcessExecutor(strongBoundaryExecutor{})
	profile := domain.DefaultProfile()
	profile.Model = "primary-model"
	profile.FallbackModels = []string{"fallback-model"}
	profile.MaxDurationSeconds = 5
	profile.MaxSteps = 2
	brief := domain.NormalizeTaskBrief(domain.TaskBrief{
		Mode: domain.TaskModeProject, State: "ready", Goal: "Build", ResultKind: "report",
		Criteria: []domain.AcceptanceCriterion{{ID: "c1", Text: "Report", Kind: "manual"}},
	})
	approved, err := domain.ApproveTaskBrief(brief)
	if err != nil {
		t.Fatal(err)
	}
	run, err := engine.Start(StartInput{
		Configuration: domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC()),
		Workspace:     domain.Workspace{ID: "ws", Path: t.TempDir()},
		SandboxPath:   t.TempDir(),
		Task:          "Build the report",
		TaskBrief:     &approved,
	})
	if err != nil {
		t.Fatal(err)
	}
	finished := waitForTerminalRun(t, repo, run.ID)
	if finished.Status != domain.RunCompleted || finished.Model != "fallback-model" {
		t.Fatalf("run=%#v", finished)
	}
	model.mu.Lock()
	defer model.mu.Unlock()
	if len(model.models) < 2 || model.models[len(model.models)-1] != "fallback-model" {
		t.Fatalf("models=%v", model.models)
	}
}

func TestPaidRuntimeRetriesWithDisableThinkingAfterReasoningBudget(t *testing.T) {
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	model := &reasoningDisableThinkingModel{}
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return model, nil })
	engine.SetProcessExecutor(strongBoundaryExecutor{})
	profile := domain.DefaultProfile()
	// Глушение осталось только там, где токены размышления оплачены: свой
	// endpoint принимает переключатель и может стоять перед платным API.
	profile.Provider = domain.ProviderOpenAI
	profile.ProviderPreset = "custom"
	profile.BaseURL = "https://gateway.invalid/v1"
	profile.Model = "primary-model"
	profile.FallbackModels = nil
	profile.MaxDurationSeconds = 5
	profile.MaxSteps = 2
	profile.ReasoningEffort = "low"
	brief := domain.NormalizeTaskBrief(domain.TaskBrief{
		Mode: domain.TaskModeProject, State: "ready", Goal: "Build", ResultKind: "report",
		Criteria: []domain.AcceptanceCriterion{{ID: "c1", Text: "Report", Kind: "manual"}},
	})
	approved, err := domain.ApproveTaskBrief(brief)
	if err != nil {
		t.Fatal(err)
	}
	run, err := engine.Start(StartInput{
		Configuration: domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC()),
		Workspace:     domain.Workspace{ID: "ws", Path: t.TempDir()},
		SandboxPath:   t.TempDir(),
		Task:          "Build the report",
		TaskBrief:     &approved,
	})
	if err != nil {
		t.Fatal(err)
	}
	finished := waitForTerminalRun(t, repo, run.ID)
	if finished.Status != domain.RunCompleted {
		t.Fatalf("run=%#v", finished)
	}
	model.mu.Lock()
	defer model.mu.Unlock()
	if model.disableRetries < 1 || model.requests < 2 {
		t.Fatalf("disableRetries=%d requests=%d", model.disableRetries, model.requests)
	}
}

// reasoningKeepsThinkingModel отвечает со второго захода, не требуя тишины.
type reasoningKeepsThinkingModel struct {
	mu        sync.Mutex
	requests  int
	suppressd int
}

func (m *reasoningKeepsThinkingModel) Stream(_ context.Context, request providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.mu.Lock()
	m.requests++
	first := m.requests == 1
	if request.DisableThinking {
		m.suppressd++
	}
	m.mu.Unlock()
	if first {
		return errors.New("model returned no answer: the entire output budget of 16384 tokens went to reasoning (finish_reason=length)")
	}
	return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "Completed while still thinking."})
}

// Бесплатный рантайм не расплачивается за размышление, поэтому обрезанный ход
// не отнимает размышление у всего остатка прогона: движок подсказывает про
// бюджет и идёт дальше думающим.
func TestFreeRuntimeKeepsThinkingAfterReasoningBudget(t *testing.T) {
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	model := &reasoningKeepsThinkingModel{}
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return model, nil })
	engine.SetProcessExecutor(strongBoundaryExecutor{})
	profile := domain.DefaultProfile()
	profile.Model = "primary-model"
	profile.FallbackModels = nil
	profile.MaxDurationSeconds = 5
	profile.MaxSteps = 3
	profile.ReasoningEffort = "low"
	brief := domain.NormalizeTaskBrief(domain.TaskBrief{
		Mode: domain.TaskModeProject, State: "ready", Goal: "Build", ResultKind: "report",
		Criteria: []domain.AcceptanceCriterion{{ID: "c1", Text: "Report", Kind: "manual"}},
	})
	approved, err := domain.ApproveTaskBrief(brief)
	if err != nil {
		t.Fatal(err)
	}
	run, err := engine.Start(StartInput{
		Configuration: domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC()),
		Workspace:     domain.Workspace{ID: "ws", Path: t.TempDir()},
		SandboxPath:   t.TempDir(),
		Task:          "Build the report",
		TaskBrief:     &approved,
	})
	if err != nil {
		t.Fatal(err)
	}
	finished := waitForTerminalRun(t, repo, run.ID)
	if finished.Status != domain.RunCompleted {
		t.Fatalf("status=%s error=%s", finished.Status, finished.Error)
	}
	model.mu.Lock()
	defer model.mu.Unlock()
	if model.suppressd != 0 {
		t.Fatalf("thinking was suppressed %d times on a free runtime", model.suppressd)
	}
	if model.requests < 2 {
		t.Fatalf("requests=%d", model.requests)
	}
}

func TestRunSwitchesToFallbackModelOnClassifiedProviderError(t *testing.T) {
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	model := &fallbackModel{}
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return model, nil })
	profile := domain.DefaultProfile()
	profile.Model = "primary-model"
	profile.FallbackModels = []string{"fallback-model"}
	profile.MaxDurationSeconds = 5
	profile.MaxSteps = 2
	run, err := engine.Start(StartInput{
		Configuration: domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC()),
		Workspace:     domain.Workspace{ID: "ws", Path: t.TempDir()},
		Task:          "Explain the result.",
	})
	if err != nil {
		t.Fatal(err)
	}
	finished := waitForTerminalRun(t, repo, run.ID)
	if finished.Status != domain.RunCompleted || finished.Model != "fallback-model" {
		t.Fatalf("run=%#v", finished)
	}
	if finished.ConfigurationSnapshot.Profile.Model != "fallback-model" {
		t.Fatalf("snapshot model=%q", finished.ConfigurationSnapshot.Profile.Model)
	}
	startedDigest := domain.NewRunConfigurationSnapshot("test", profile, nil, finished.ConfigurationSnapshot.CapturedAt).ConfigurationDigest
	if finished.ConfigurationSnapshot.ConfigurationDigest == "" || finished.ConfigurationSnapshot.ConfigurationDigest == startedDigest {
		t.Fatalf("fallback must change configuration digest; got %q started %q", finished.ConfigurationSnapshot.ConfigurationDigest, startedDigest)
	}
	model.mu.Lock()
	models := append([]string(nil), model.models...)
	model.mu.Unlock()
	if strings.Join(models, ",") != "primary-model,fallback-model" {
		t.Fatalf("models=%v", models)
	}
	eventsList, err := repo.ListByRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	sawFallback := false
	for _, event := range eventsList {
		if event.Type == domain.EventModelRetrying && strings.Contains(string(event.Data), `"fallback":true`) {
			if !strings.Contains(string(event.Data), `"configurationDigest"`) {
				t.Fatalf("fallback event missing configuration digest: %s", event.Data)
			}
			sawFallback = true
			break
		}
	}
	if !sawFallback {
		t.Fatal("fallback transition was not recorded")
	}
}

type strongBoundaryExecutor struct{}

func (strongBoundaryExecutor) PrepareProcess(context.Context, sandbox.ProcessRequest) (sandbox.PreparedProcess, error) {
	return sandbox.PreparedProcess{}, errors.New("strong boundary stub does not execute commands")
}

func (strongBoundaryExecutor) Capabilities() sandbox.Capabilities {
	return sandbox.Capabilities{StrongOSBoundary: true, ProcessIsolation: true, NetworkIsolation: true}
}

// На официальном endpoint'е переключателя «не размышляй» нет: поле уходит сверх
// спецификации и возвращает 400. Признак живёт до конца прогона, поэтому один
// такой повтор отравил бы ошибкой формата все оставшиеся шаги.
func TestStrictEndpointNeverRetriesWithDisableThinking(t *testing.T) {
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	model := &reasoningDisableThinkingModel{}
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return model, nil })
	engine.SetProcessExecutor(strongBoundaryExecutor{})
	profile := domain.DefaultProfile()
	profile.Provider = domain.ProviderOpenAI
	profile.ProviderPreset = "openai"
	profile.BaseURL = "https://api.openai.com/v1"
	profile.Model = "primary-model"
	profile.FallbackModels = nil
	profile.MaxDurationSeconds = 5
	profile.MaxSteps = 2
	run, err := engine.Start(StartInput{
		Configuration: domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC()),
		Workspace:     domain.Workspace{ID: "ws", Path: t.TempDir()},
		SandboxPath:   t.TempDir(),
		Task:          "Build the report",
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForTerminalRun(t, repo, run.ID)
	model.mu.Lock()
	defer model.mu.Unlock()
	if model.disableRetries != 0 {
		t.Fatalf("строгий endpoint получил %d запросов с полем сверх спецификации", model.disableRetries)
	}
}
