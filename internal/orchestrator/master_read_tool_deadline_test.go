package orchestrator

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
)

type slowReadTools struct{}

func (slowReadTools) Definitions() []domain.ToolDefinition { return nil }

func (slowReadTools) Execute(ctx context.Context, _ string, _ json.RawMessage) domain.ToolResult {
	<-ctx.Done()
	return domain.ToolResult{OK: false, Error: &domain.ToolError{Code: "search_failed", Message: ctx.Err().Error()}}
}

// 02.10.2026 поиск по тексту съел весь срок хода, и на ответ «по собранному»
// не осталось времени. Инструмент получает свой срок и запас под ответ не
// трогает; остановленный — отказ с подсказкой, а ход жив.
func TestMasterReadToolCannotEatTheAnswerReserve(t *testing.T) {
	turn, cancel := context.WithTimeout(context.Background(), masterAnswerReserve+2*time.Second)
	defer cancel()
	toolCtx, toolCancel := masterReadToolContext(turn, time.Second)
	defer toolCancel()
	deadline, _ := toolCtx.Deadline()
	if left := time.Until(deadline); left > 6*time.Second {
		t.Fatalf("tool got %s although only the answer reserve remains", left)
	}
	started := time.Now()
	result := executeMasterReadTool(turn, slowReadTools{}, providers.ToolCall{Name: "search_text"}, time.Second)
	if result.OK || result.Error == nil || result.Error.Code != "tool_timeout" || turn.Err() != nil {
		t.Fatalf("slow tool result = %+v, turn err = %v", result, turn.Err())
	}
	if time.Since(started) > 8*time.Second {
		t.Fatal("slow tool held the turn")
	}
	long, longCancel := context.WithTimeout(context.Background(), time.Hour)
	defer longCancel()
	capped, cappedCancel := masterReadToolContext(long, time.Minute)
	defer cappedCancel()
	if deadline, _ = capped.Deadline(); time.Until(deadline) > masterReadToolTimeout {
		t.Fatal("a read tool must never get more than masterReadToolTimeout")
	}
}
