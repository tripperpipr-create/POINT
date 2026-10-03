package orchestrator

import (
	"context"
	"sync"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
)

// Скорость хода Мастера. Замер 25.09–02.10 по живой базе: ход с исследованием
// шёл 5–24 круга и 2–20 минут, по ~40 с на круг, и почти всё это время —
// размышление (~2,2 тыс. токенов вывода на круг). Каждый из 112 кругов звал
// ровно один инструмент, хотя ядро принимает до восьми.
//
// Отсюда два правила. Независимые чтения одного круга исполняются разом, а не
// по очереди. Круг, который лишь продолжает чтение после успешных чтений,
// идёт без размышления (6–7 с вместо 40): что читать, модель уже решила в
// прошлом круге. Ответ человеку и задание пишет только размышляющий круг —
// быстрый, решивший ответить, переигрывается с размышлением (решение
// владельца от 03.10.2026).

// masterFastRoundsAllowed — можно ли промежуточным кругам чтения идти без
// размышления. Только там, где рантайм принимает переключатель и сам его не
// гасит: на платном размышление и так выключено целиком.
func masterFastRoundsAllowed(cfg domain.OrchestratorConfig) bool {
	return domain.RuntimeAcceptsThinkingSwitch(cfg.Provider, cfg.ProviderPreset) &&
		!domain.ShouldSuppressThinking(cfg.Provider, cfg.ProviderPreset)
}

// masterFastRoundAnswers — быстрый круг решил закончить исследование: ответить
// текстом или оформить разговор. Такой круг переигрывается с размышлением.
func masterFastRoundAnswers(calls []providers.ToolCall) bool {
	if len(calls) == 0 {
		return true
	}
	for _, call := range calls {
		if IsMasterActionTool(call.Name) {
			return true
		}
	}
	return false
}

// masterReadJob — читающий вызов круга, допущенный к исполнению.
type masterReadJob struct {
	index int
	call  providers.ToolCall
}

// executeMasterReadCalls исполняет чтения одного круга разом. У каждого свой
// срок (masterReadToolContext), результаты стоят в порядке вызовов.
func executeMasterReadCalls(ctx context.Context, tools TaskReadTools, jobs []masterReadJob, longestRound time.Duration) []domain.ToolResult {
	results := make([]domain.ToolResult, len(jobs))
	if len(jobs) == 1 {
		results[0] = executeMasterReadTool(ctx, tools, jobs[0].call, longestRound)
		return results
	}
	var wg sync.WaitGroup
	for position, job := range jobs {
		wg.Add(1)
		go func(position int, call providers.ToolCall) {
			defer wg.Done()
			results[position] = executeMasterReadTool(ctx, tools, call, longestRound)
		}(position, job.call)
	}
	wg.Wait()
	return results
}

// roundReport — что случилось в круге, для замера «до/после» (point-perf-report).
func (t *masterTrace) roundReport(started time.Time, outputTokens int64, thinking bool, calls, readCalls int, rerun bool) {
	if t == nil {
		return
	}
	mode := "on"
	if !thinking {
		mode = "off"
	}
	t.flush()
	t.send("round", "", map[string]any{
		"round": t.round, "durationMs": time.Since(started).Milliseconds(), "outputTokens": outputTokens,
		"thinking": mode, "calls": calls, "readCalls": readCalls, "rerun": rerun,
	})
}
