package main

import (
	"testing"
	"time"
)

// Вызовы модели одного хода идут подряд, пауза больше четырёх минут — новый
// ход. Круги с замером делятся на быстрые и с размышлением.
func TestMasterTurnsClusterByPauseAndRoundsSplitByThinking(t *testing.T) {
	at := func(minute, second int) time.Time { return time.Date(2026, 10, 3, 10, minute, second, 0, time.UTC) }
	calls := []masterCall{
		{At: at(0, 40), LatencyMs: 40000, OutputTokens: 2000},
		{At: at(0, 50), LatencyMs: 7000, OutputTokens: 100},
		{At: at(1, 30), LatencyMs: 40000, OutputTokens: 2000},
		{At: at(20, 10), LatencyMs: 10000, OutputTokens: 300},
	}
	turns := clusterMasterTurns(calls)
	if len(turns) != 2 || turns[0].ModelCalls != 3 || turns[0].WallMs != 90000 || turns[1].ModelCalls != 1 {
		t.Fatalf("turns = %+v", turns)
	}
	report := summarizeMaster(turns, calls, []masterRound{
		{DurationMs: 40000, Thinking: "on", Calls: 3, OutputTokens: 2000},
		{DurationMs: 7000, Thinking: "off", Calls: 2, OutputTokens: 100},
		{DurationMs: 40000, Thinking: "on", Rerun: true, OutputTokens: 2000},
	})
	if report.FastRounds != 1 || report.Reruns != 1 || report.MedianFastMs != 7000 || report.MedianThinkMs != 40000 || report.ToolsPerRound != 5.0/3 {
		t.Fatalf("report = %+v", report)
	}
	if report.MedianWallMs != 10000 || report.P90WallMs != 10000 {
		t.Fatalf("percentiles = %d %d", report.MedianWallMs, report.P90WallMs)
	}
}
