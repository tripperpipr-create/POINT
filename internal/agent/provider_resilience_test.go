package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
)

func fastProviderRetries(t *testing.T) {
	t.Helper()
	previousDelay, previousResume := transientDelay, providerUnavailableAutoResume
	transientDelay = func(int) time.Duration { return time.Millisecond }
	providerUnavailableAutoResume = 20 * time.Millisecond
	t.Cleanup(func() {
		transientDelay, providerUnavailableAutoResume = previousDelay, previousResume
	})
}

func startResilienceRun(t *testing.T, repo *memoryRepo, model providers.Model) domain.Run {
	t.Helper()
	engine := NewEngine(repo, nil)
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return model, nil })
	profile := domain.DefaultProfile()
	profile.MaxDurationSeconds = 10
	run, err := engine.Start(StartInput{
		Configuration: domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC()),
		Workspace:     domain.Workspace{ID: "ws", Path: t.TempDir()},
		Task:          "answer after provider trouble",
	})
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func respondedContents(t *testing.T, repo *memoryRepo, runID string) []string {
	t.Helper()
	eventsList, err := repo.ListByRun(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	var contents []string
	for _, event := range eventsList {
		if event.Type != domain.EventModelResponded {
			continue
		}
		var payload struct {
			Content string `json:"content"`
		}
		_ = json.Unmarshal(event.Data, &payload)
		contents = append(contents, payload.Content)
	}
	return contents
}

// Провайдер начал ответ, оборвал поток и повторил запрос: текст первой
// попытки не должен остаться в ответе. Прежде повтор его задваивал.
type restartMidStreamModel struct{}

func (restartMidStreamModel) Stream(_ context.Context, _ providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	for _, event := range []providers.ModelEvent{
		{Kind: providers.EventTextDelta, Delta: "Half an ans"},
		{Kind: providers.EventReasoning, Reasoning: &providers.ReasoningBlock{Type: "text", Text: "stale thought"}},
		{Kind: providers.EventRetry, Attempt: 2, Message: "temporary provider stream interrupt"},
		{Kind: providers.EventTextDelta, Delta: "Full answer."},
	} {
		if err := emit(event); err != nil {
			return err
		}
	}
	return nil
}

func TestProviderRestartDoesNotDuplicateStreamedText(t *testing.T) {
	repo := newMemoryRepo()
	run := startResilienceRun(t, repo, restartMidStreamModel{})
	finished := waitForTerminalRun(t, repo, run.ID)
	if finished.Status != domain.RunCompleted || finished.Result != "Full answer." {
		t.Fatalf("run=%#v", finished)
	}
}

// Обрыв посреди потока, которого HTTP-слой уже не спас, — не конец прогона.
type flakyStreamModel struct {
	mu       sync.Mutex
	failures int
	calls    int
	err      error
}

func (m *flakyStreamModel) Stream(_ context.Context, _ providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.mu.Lock()
	m.calls++
	fail := m.calls <= m.failures
	m.mu.Unlock()
	if fail {
		_ = emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "partial "})
		return m.err
	}
	return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "Recovered answer."})
}

func TestWindowsConnectionAbortMidStreamIsRetried(t *testing.T) {
	fastProviderRetries(t)
	repo := newMemoryRepo()
	model := &flakyStreamModel{failures: 3, err: errors.New("read tcp 10.0.0.2:51234->10.0.0.1:443: wsarecv: An established connection was aborted by the software in your host machine.")}
	run := startResilienceRun(t, repo, model)
	finished := waitForTerminalRun(t, repo, run.ID)
	if finished.Status != domain.RunCompleted || finished.Result != "Recovered answer." {
		t.Fatalf("run=%#v", finished)
	}
	if contents := respondedContents(t, repo, run.ID); len(contents) != 1 || strings.Contains(contents[0], "partial") {
		t.Fatalf("responded=%q", contents)
	}
}

// Провайдер молчит дольше всех повторов: прогон встаёт на паузу
// provider_unavailable, сам продолжает и доживает до ответа.
func TestProviderOutagePausesAndResumesInsteadOfFailing(t *testing.T) {
	fastProviderRetries(t)
	repo := newMemoryRepo()
	model := &flakyStreamModel{failures: maxTransientModelRetries + 2, err: errors.New("upstream closed the stream without sending any content")}
	run := startResilienceRun(t, repo, model)
	finished := waitForTerminalRun(t, repo, run.ID)
	if finished.Status != domain.RunCompleted {
		t.Fatalf("outage must not fail the run: %#v", finished)
	}
	codes := guardrailCodes(t, repo, run.ID)
	found := false
	for _, code := range codes {
		if code == "provider_unavailable" {
			found = true
		}
	}
	if !found {
		t.Fatalf("guardrails=%v", codes)
	}
}

// Окно профиля больше настоящего: провайдер отказал «не влезло», движок
// сжимает разговор и повторяет ход, а не роняет прогон.
type contextOverflowModel struct {
	mu    sync.Mutex
	calls int
}

func (m *contextOverflowModel) Stream(_ context.Context, _ providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.mu.Lock()
	m.calls++
	first := m.calls == 1
	m.mu.Unlock()
	if first {
		return errors.New(`provider returned 400 Bad Request: {"error":{"code":"context_length_exceeded","message":"This model's maximum context length is 32768 tokens"}}`)
	}
	return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "Fits now."})
}

func TestProviderContextOverflowCompactsAndRetries(t *testing.T) {
	repo := newMemoryRepo()
	model := &contextOverflowModel{}
	run := startResilienceRun(t, repo, model)
	finished := waitForTerminalRun(t, repo, run.ID)
	if finished.Status != domain.RunCompleted || finished.Result != "Fits now." {
		t.Fatalf("run=%#v", finished)
	}
}

func TestShrinkToolResultKeepsErrorAndMarksTruncation(t *testing.T) {
	big, _ := json.Marshal(strings.Repeat("я", 20000))
	result := shrinkToolResult(domain.ToolResult{OK: true, Output: big}, overBudgetToolOutputBytes)
	var text string
	if err := json.Unmarshal(result.Output, &text); err != nil {
		t.Fatal(err)
	}
	if !result.Truncated || len(text) > overBudgetToolOutputBytes+200 || !strings.Contains(text, "total tool-output budget") {
		t.Fatalf("truncated=%v len=%d", result.Truncated, len(text))
	}
	small := domain.ToolResult{OK: false, Error: &domain.ToolError{Code: "x", Message: "y"}, Output: json.RawMessage(`"ok"`)}
	if got := shrinkToolResult(small, overBudgetToolOutputBytes); got.Error == nil || string(got.Output) != `"ok"` {
		t.Fatalf("small result changed: %#v", got)
	}
}

func TestTransientRetryDelayGrowsToCeilingWithJitter(t *testing.T) {
	if got := transientRetryDelay(1); got < 1600*time.Millisecond || got > 2400*time.Millisecond {
		t.Fatalf("first delay=%s", got)
	}
	if got := transientRetryDelay(20); got < 48*time.Second || got > 72*time.Second {
		t.Fatalf("ceiling delay=%s", got)
	}
}

// Qwen через шлюз без парсера вызовов пишет вызов текстом. Движок его
// исполняет, а не принимает за финал без доказательств.
type textToolCallModel struct {
	mu    sync.Mutex
	calls int
}

func (m *textToolCallModel) Stream(_ context.Context, request providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.mu.Lock()
	m.calls++
	first := m.calls == 1
	m.mu.Unlock()
	if first {
		return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "Посмотрю файлы.\n<tool_call>\n{\"name\": \"list_files\", \"arguments\": {\"maxDepth\": 1}}\n</tool_call>"})
	}
	for _, message := range request.Messages {
		if message.Role == "tool" {
			return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "Listed."})
		}
	}
	return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "no tool result seen"})
}

func TestTextToolCallIsExecuted(t *testing.T) {
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return &textToolCallModel{}, nil })
	profile := domain.DefaultProfile()
	profile.MaxDurationSeconds = 10
	profile.AllowedTools = []string{"list_files"}
	run, err := engine.Start(StartInput{
		Configuration: domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC()),
		Workspace:     domain.Workspace{ID: "ws", Path: t.TempDir()},
		Task:          "list files",
	})
	if err != nil {
		t.Fatal(err)
	}
	finished := waitForTerminalRun(t, repo, run.ID)
	if finished.Status != domain.RunCompleted || finished.Result != "Listed." {
		t.Fatalf("run=%#v", finished)
	}
	found := false
	for _, code := range guardrailCodes(t, repo, run.ID) {
		if code == "tool_call_recovered" {
			found = true
		}
	}
	if !found {
		t.Fatal("recovered text call must be journaled")
	}
}

// Поток из сотен мелких дельт пишется в журнал немногими записями, а текст
// в них тот же, что в ответе.
type chattyModel struct{}

func (chattyModel) Stream(_ context.Context, _ providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	for i := 0; i < 500; i++ {
		if err := emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "ab"}); err != nil {
			return err
		}
	}
	return nil
}

func TestStreamedDeltasAreCoalesced(t *testing.T) {
	repo := newMemoryRepo()
	run := startResilienceRun(t, repo, chattyModel{})
	finished := waitForTerminalRun(t, repo, run.ID)
	if finished.Status != domain.RunCompleted {
		t.Fatalf("run=%#v", finished)
	}
	eventsList, _ := repo.ListByRun(context.Background(), run.ID)
	var streamed strings.Builder
	count := 0
	for _, event := range eventsList {
		if event.Type != domain.EventModelStreamed {
			continue
		}
		count++
		var payload struct {
			Delta string `json:"delta"`
		}
		_ = json.Unmarshal(event.Data, &payload)
		streamed.WriteString(payload.Delta)
	}
	if streamed.String() != strings.Repeat("ab", 500) || count > 10 {
		t.Fatalf("streamed events=%d text=%d bytes", count, streamed.Len())
	}
}
