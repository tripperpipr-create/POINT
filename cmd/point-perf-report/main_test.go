package main

import (
	"testing"
	"time"
)

// Отчёт обязан видеть зависший поток: запрос без ответа — это время ожидания
// модели до следующего события прогона, а не ноль. Иначе шестичасовое
// зависание 30.09 выглядело бы в отчёте «девятью секундами модели».
func TestAnalyzeCountsHungModelRequestsAndCommandFamilies(t *testing.T) {
	at := func(seconds int) time.Time { return time.Date(2026, 9, 30, 0, 0, seconds, 0, time.UTC) }
	events := []event{
		{RunID: "r1", Type: "run.started", Data: map[string]any{"stageRole": "implement"}, At: at(0)},
		{RunID: "r1", Type: "model.requested", At: at(1)},
		{RunID: "r1", Type: "model.usage", Data: map[string]any{"usage": map[string]any{"inputTokens": 1000.0, "cachedInputTokens": 600.0}}, At: at(4)},
		{RunID: "r1", Type: "model.responded", At: at(5)},
		{RunID: "r1", Type: "tool.requested", Data: map[string]any{"callId": "c1", "arguments": map[string]any{"command": "cd app && npm run verify"}}, At: at(5)},
		{RunID: "r1", Type: "tool.finished", Data: map[string]any{"callId": "c1", "tool": "run_command", "durationMs": 20000.0, "timing": map[string]any{"auditTotalMs": 1500.0}}, At: at(27)},
		{RunID: "r1", Type: "model.requested", At: at(28)},
		{RunID: "r1", Type: "run.failed", At: at(100)},
	}
	commands := map[string]*command{}
	flow := analyze("flow-1", events, commands)
	if len(flow.Stages) != 1 {
		t.Fatalf("stages = %+v", flow.Stages)
	}
	s := flow.Stages[0]
	if s.Role != "implement" || s.ModelCalls != 2 || s.ModelMs != 4000+72000 {
		t.Fatalf("model time = %+v", s)
	}
	if s.CommandMs != 20000 || s.AuditMs != 1500 || s.CachedTokens != 600 || s.InputTokens != 1000 {
		t.Fatalf("tool time or cache = %+v", s)
	}
	if c := commands["npm run verify"]; c == nil || c.Calls != 1 {
		t.Fatalf("command family lost the cd prefix: %+v", commands)
	}
}
