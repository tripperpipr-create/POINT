package orchestrator

import (
	"context"
	"fmt"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
)

// streamMasterModel даёт модели думать как обычно и вытаскивает ход, в котором
// размышление съело весь бюджет вывода.
//
// Такой ход заканчивается ничем: ни текста, ни вызова инструмента, только
// finish_reason=length. Лечится это двумя способами, и порядок между ними —
// решение владельца, а не техники: сначала месту для ответа, потом тишине.
//
// Сначала повтор с бо́льшим пределом вывода. Это обычное поле запроса, его
// принимают все endpoint'ы, и на бесплатном рантайме оно ничего не стоит.
//
// И только если места уже не добавить — повтор с погашенным «размышлением».
// Он возможен не везде: гасится оно полем сверх спецификации OpenAI, и
// официальный endpoint отвечает на него 400 — там повтор подменил бы честную
// причину отказа чужой ошибкой формата. Знание о том, свой ли за адресом
// рантайм, приходит от вызывающего вместе с запросом.
//
// Каждая попытка называется в ленте: повтор идёт минуты, и молчаливое «думаю»
// на третьем круге неотличимо от зависшей модели.
func streamMasterModel(ctx context.Context, model providers.Model, request providers.ModelRequest, selfHostedRuntime bool, trace *masterTrace, onEvent func(providers.ModelEvent) error) error {
	started := time.Now()
	err := measureMasterModel(ctx, model, request, trace, onEvent)
	if err == nil || !providers.IsTruncatedReasoningError(err) {
		return err
	}
	// Повтор начинает размышление с нуля и идёт не быстрее упёршейся
	// попытки. Если до конца хода столько не осталось, он лишь дождётся
	// обрыва: ход 29.09 так потратил последние пять минут и кончился
	// «Модель Мастера не ответила» вместо честной причины.
	spent := time.Since(started)
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < spent {
		trace.retry("повтор не успеет до конца хода", map[string]any{"reason": "deadline", "spentSeconds": int(spent.Seconds())})
		return fmt.Errorf("модель размышляла %d с и исчерпала предел вывода; повтор не успеет до конца хода: %w", int(spent.Seconds()), err)
	}
	grown := domain.GrowThinkingOutputBudget(request.MaxOutputTokens, request.Model)
	// Вывод не может занимать больше половины окна: остальное нужно самому
	// разговору, и запрос с таким пределом провайдер просто отвергнет.
	if half := request.ContextWindowTokens / 2; half > 0 && grown > half {
		grown = 0
	}
	if grown > request.MaxOutputTokens {
		trace.retry("больше места на ответ", map[string]any{
			"reason": "reasoning_budget", "from": request.MaxOutputTokens, "to": grown,
		})
		request.MaxOutputTokens = grown
		retryStarted := time.Now()
		if err = measureMasterModel(ctx, model, request, trace, onEvent); err == nil || !providers.IsTruncatedReasoningError(err) {
			return err
		}
		if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < time.Since(retryStarted) {
			return err
		}
	}
	if request.DisableThinking || !selfHostedRuntime {
		return err
	}
	trace.retry("ответ без размышления", map[string]any{"reason": "disable_thinking", "budget": request.MaxOutputTokens})
	retry := request
	retry.DisableThinking = true
	retry.ReasoningEffort = ""
	return measureMasterModel(ctx, model, retry, trace, onEvent)
}

// masterOutputBudget — предел вывода хода Мастера. Платному рантайму он
// зажат в 8192…16384: вывод там стоит денег. Бесплатному (llmux, локальные)
// сразу даётся полный предел размышляющей модели, не больше половины окна:
// прежде первая попытка упиралась в 8192, и повтор начинал размышление с нуля
// — живой ход 29.09 потерял на этом три с половиной минуты из десяти.
func masterOutputBudget(cfg domain.OrchestratorConfig, window int) int {
	output := min(max(cfg.MaxOutputTokens, 8192), 16384)
	if domain.RuntimeChargesForTokens(cfg.Provider, cfg.ProviderPreset) {
		return output
	}
	grown := domain.GrowThinkingOutputBudget(output, cfg.Model)
	if half := window / 2; half > 0 && grown > half {
		grown = half
	}
	return max(output, grown)
}

type masterTimingKey struct{}
type masterTimingSink func(string, map[string]any)

// WithMasterTiming records model attempts in the owning turn, including calls
// outside a traced round (for example brief repair). It carries no model input.
func WithMasterTiming(ctx context.Context, sink func(string, map[string]any)) context.Context {
	return context.WithValue(ctx, masterTimingKey{}, masterTimingSink(sink))
}

func measureMasterModel(ctx context.Context, model providers.Model, request providers.ModelRequest, trace *masterTrace, onEvent func(providers.ModelEvent) error) error {
	started := time.Now()
	var input, output int64
	err := model.Stream(ctx, request, func(event providers.ModelEvent) error {
		if event.Kind == providers.EventUsage {
			input = max(input, int64(event.InputTokens))
			output = max(output, int64(event.OutputTokens))
		}
		return onEvent(event)
	})
	ended := time.Now()
	detail := map[string]any{
		"startedAt": started.UTC(), "endedAt": ended.UTC(), "durationMs": ended.Sub(started).Milliseconds(),
		"inputTokens": input, "outputTokens": output, "failed": err != nil, "model": request.Model,
		"thinking": !request.DisableThinking,
	}
	if trace != nil {
		detail["round"] = trace.round
	}
	if sink, ok := ctx.Value(masterTimingKey{}).(masterTimingSink); ok {
		sink("model_call", detail)
	} else {
		trace.send("model_call", "", detail)
	}
	return err
}
