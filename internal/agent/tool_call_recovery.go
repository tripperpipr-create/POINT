package agent

import (
	"context"
	"fmt"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/modeljson"
	"local-agent-workbench/internal/providers"
)

// recoverToolCalls подбирает вызовы инструментов, которые модель отдала не
// протоколом, а текстом ответа (`<tool_call>{…}</tool_call>`: так пишет Qwen,
// когда парсер шлюза его не узнал), и отмечает в журнале вызовы с
// починенными аргументами. Без этого такой ход выглядел финалом без
// доказательств и уходил на доработку, а модель не понимала почему.
//
// Извлекаются только предложенные в этом ходе инструменты; текст не может
// выдать модели инструмент, которого у неё нет.
func (e *Engine) recoverToolCalls(ctx context.Context, active *activeRun, step int, offered []domain.ToolDefinition, calls []providers.ToolCall, text string) ([]providers.ToolCall, string) {
	repaired := 0
	for _, call := range calls {
		if call.Repaired {
			repaired++
		}
	}
	if repaired > 0 {
		e.publishOrLog(ctx, e.snapshot(active), domain.EventAgentGuardrail, "agent", map[string]any{
			"code": "tool_call_recovered", "source": "repaired", "count": repaired,
		})
	}
	if len(calls) > 0 || len(offered) == 0 {
		return calls, text
	}
	names := make(map[string]bool, len(offered))
	for _, definition := range offered {
		names[definition.Name] = true
	}
	extracted, rest := modeljson.ExtractTextToolCalls(text, func(name string) bool { return names[name] })
	if len(extracted) == 0 {
		return calls, text
	}
	recovered := make([]providers.ToolCall, 0, len(extracted))
	for index, call := range extracted {
		recovered = append(recovered, providers.ToolCall{
			ID: fmt.Sprintf("text_call_%d_%d", step, index), Name: call.Name, Arguments: call.Arguments, Repaired: true,
		})
	}
	e.publishOrLog(ctx, e.snapshot(active), domain.EventAgentGuardrail, "agent", map[string]any{
		"code": "tool_call_recovered", "source": "text", "count": len(recovered), "tools": toolCallNames(recovered),
	})
	return recovered, rest
}
