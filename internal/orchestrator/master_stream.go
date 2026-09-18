package orchestrator

import (
	"context"

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
	err := model.Stream(ctx, request, onEvent)
	if err == nil || !providers.IsTruncatedReasoningError(err) {
		return err
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
		if err = model.Stream(ctx, request, onEvent); err == nil || !providers.IsTruncatedReasoningError(err) {
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
	return model.Stream(ctx, retry, onEvent)
}
