package orchestrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
)

// Ход 29.09: первая попытка Мастера на llmux была зажата в 8192, модель
// исчерпала их размышлением, и повтор начал думать с нуля. Бесплатный рантайм
// получает полный предел сразу; платный остаётся в 8192…16384.
func TestMasterOutputBudgetIsFullOnFreeRuntime(t *testing.T) {
	window := intakeContextWindowTokens
	free := domain.OrchestratorConfig{Provider: domain.ProviderOpenAI, ProviderPreset: "llmux", Model: "Qwen3.8-27B", MaxOutputTokens: 8192}
	if got := masterOutputBudget(free, window); got != domain.MaxThinkingOutputTokens {
		t.Fatalf("free runtime budget=%d", got)
	}
	if got := masterOutputBudget(free, 20000); got != 10000 {
		t.Fatalf("budget passed half of a narrow window: %d", got)
	}
	paid := domain.OrchestratorConfig{Provider: domain.ProviderOpenAI, ProviderPreset: "openai", Model: "gpt-5", MaxOutputTokens: 64000}
	if got := masterOutputBudget(paid, window); got != 16384 {
		t.Fatalf("paid runtime budget=%d", got)
	}
}

func TestMasterTurnDeadlineIsLongerOnFreeRuntime(t *testing.T) {
	if got := masterTurnTimeoutSeconds(domain.OrchestratorConfig{Provider: domain.ProviderOpenAI, ProviderPreset: "llmux"}); got != masterIntakeOllamaTimeoutSeconds {
		t.Fatalf("llmux turn deadline=%d", got)
	}
	if got := masterTurnTimeoutSeconds(domain.OrchestratorConfig{Provider: domain.ProviderOpenAI, ProviderPreset: "openai"}); got != masterIntakeTimeoutSeconds {
		t.Fatalf("paid turn deadline=%d", got)
	}
}

// Повтор идёт не быстрее упёршейся попытки: если столько времени до конца
// хода нет, он не начинается, и ошибка называет причину.
func TestStreamMasterModelSkipsRetryThatCannotFinish(t *testing.T) {
	model := &slowTruncatedModel{delay: 60 * time.Millisecond}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := streamMasterModel(ctx, model, providers.ModelRequest{Model: "qwen3.5:9b", MaxOutputTokens: 8192}, true, nil, func(providers.ModelEvent) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "повтор не успеет") {
		t.Fatalf("err=%v", err)
	}
	if model.calls != 1 {
		t.Fatalf("retry started without time for it: %d calls", model.calls)
	}
}

type slowTruncatedModel struct {
	delay time.Duration
	calls int
}

func (m *slowTruncatedModel) Stream(context.Context, providers.ModelRequest, func(providers.ModelEvent) error) error {
	m.calls++
	time.Sleep(m.delay)
	return errTruncatedReasoning
}

var errTruncatedReasoning = errorString("model returned no answer: the entire output budget of 8192 tokens went to reasoning (finish_reason=length)")

type errorString string

func (e errorString) Error() string { return string(e) }

func TestMasterTimeRunsShortComparesWithLongestRound(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if masterTimeRunsShort(ctx, 0) || masterTimeRunsShort(ctx, time.Second) {
		t.Fatal("plenty of time reported as short")
	}
	if !masterTimeRunsShort(ctx, 2*time.Minute) {
		t.Fatal("round longer than the time left not detected")
	}
	if masterTimeRunsShort(context.Background(), time.Hour) {
		t.Fatal("no deadline reported as short")
	}
}

// Срок вышел посреди круга, но модель уже сказала человеку, что делает:
// ход кончается сказанным, а не «Модель Мастера не ответила».
func TestMasterTurnDeadlineKeepsWhatWasSaid(t *testing.T) {
	store := newChatStoreStub()
	model := &deadlineAfterFirstRoundModel{}
	service := ChatService{Store: store, ModelFactory: func(providers.Config) (providers.Model, error) { return model, nil }}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	response, err := service.Chat(ctx, intakeRequest("Сделай деплой"))
	if err != nil {
		t.Fatalf("turn failed instead of keeping the spoken part: %v", err)
	}
	if !strings.Contains(response.Reply, "Смотрю CI-конфиги") {
		t.Fatalf("spoken part lost: %q", response.Reply)
	}
}

type deadlineAfterFirstRoundModel struct{ calls int }

func (m *deadlineAfterFirstRoundModel) Stream(ctx context.Context, _ providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.calls++
	if m.calls == 1 {
		if err := emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "Смотрю CI-конфиги обоих проектов."}); err != nil {
			return err
		}
		call := toolCall("c1", "read_file", map[string]any{"path": "cf-pages/.gitlab-ci.yml"})
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &call})
	}
	<-ctx.Done()
	return ctx.Err()
}
