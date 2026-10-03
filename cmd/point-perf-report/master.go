package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"
)

// Ходы Мастера — самое частое, что ждёт человек. Разбор 03.10 по живой базе:
// ход с исследованием шёл 5–24 круга и 2–20 минут, по ~40 с на круг, и каждый
// круг звал один инструмент. Отчёт меряет то же самое одной командой, чтобы
// пакет чтений и быстрые круги проверялись числами «до» и «после».
//
// Длительность хода берётся из usage_records (outcome master_model): вызовы
// модели одного хода идут подряд, а пауза больше masterTurnGap — уже другой
// ход. Так меряются и старые ходы, у которых нет событий round. Сами события
// round (с 03.10) добавляют, сколько кругов шли без размышления.

const masterTurnGap = 4 * time.Minute

type masterCall struct {
	At           time.Time
	LatencyMs    int64
	InputTokens  int64
	OutputTokens int64
}

type masterTurn struct {
	StartedAt    time.Time `json:"startedAt"`
	WallMs       int64     `json:"wallMs"`
	ModelMs      int64     `json:"modelMs"`
	ModelCalls   int       `json:"modelCalls"`
	InputTokens  int64     `json:"inputTokens"`
	OutputTokens int64     `json:"outputTokens"`
}

type masterRound struct {
	DurationMs   int64  `json:"durationMs"`
	OutputTokens int64  `json:"outputTokens"`
	Thinking     string `json:"thinking"`
	Calls        int    `json:"calls"`
	ReadCalls    int    `json:"readCalls"`
	Rerun        bool   `json:"rerun"`
}

type masterReport struct {
	Turns          []masterTurn `json:"turns"`
	MedianWallMs   int64        `json:"medianWallMs"`
	P90WallMs      int64        `json:"p90WallMs"`
	CallsPerTurn   float64      `json:"modelCallsPerTurn"`
	MedianCallMs   int64        `json:"medianModelCallMs"`
	Rounds         int          `json:"rounds"`
	FastRounds     int          `json:"fastRounds"`
	Reruns         int          `json:"reruns"`
	ToolsPerRound  float64      `json:"toolsPerRound"`
	MedianFastMs   int64        `json:"medianFastRoundMs"`
	MedianThinkMs  int64        `json:"medianThinkingRoundMs"`
	OutputPerRound int64        `json:"outputTokensPerRound"`
}

// clusterMasterTurns склеивает вызовы модели Мастера в ходы по паузе.
func clusterMasterTurns(calls []masterCall) []masterTurn {
	sort.Slice(calls, func(i, j int) bool { return calls[i].At.Before(calls[j].At) })
	var turns []masterTurn
	var last time.Time
	for _, call := range calls {
		start := call.At.Add(-time.Duration(call.LatencyMs) * time.Millisecond)
		if len(turns) == 0 || start.Sub(last) > masterTurnGap {
			turns = append(turns, masterTurn{StartedAt: start})
		}
		turn := &turns[len(turns)-1]
		turn.ModelCalls++
		turn.ModelMs += call.LatencyMs
		turn.InputTokens += call.InputTokens
		turn.OutputTokens += call.OutputTokens
		turn.WallMs = call.At.Sub(turn.StartedAt).Milliseconds()
		last = call.At
	}
	return turns
}

// summarizeMaster считает медианы ходов и разбор кругов.
func summarizeMaster(turns []masterTurn, calls []masterCall, rounds []masterRound) masterReport {
	report := masterReport{Turns: turns, Rounds: len(rounds)}
	walls := make([]int64, 0, len(turns))
	totalCalls := 0
	for _, turn := range turns {
		walls = append(walls, turn.WallMs)
		totalCalls += turn.ModelCalls
	}
	report.MedianWallMs, report.P90WallMs = percentile(walls, 50), percentile(walls, 90)
	if len(turns) > 0 {
		report.CallsPerTurn = float64(totalCalls) / float64(len(turns))
	}
	latencies := make([]int64, 0, len(calls))
	for _, call := range calls {
		latencies = append(latencies, call.LatencyMs)
	}
	report.MedianCallMs = percentile(latencies, 50)
	var fast, thinking []int64
	tools, output := 0, int64(0)
	for _, round := range rounds {
		tools += round.Calls
		output += round.OutputTokens
		if round.Rerun {
			report.Reruns++
		}
		if round.Thinking == "off" {
			report.FastRounds++
			fast = append(fast, round.DurationMs)
		} else {
			thinking = append(thinking, round.DurationMs)
		}
	}
	if len(rounds) > 0 {
		report.ToolsPerRound = float64(tools) / float64(len(rounds))
		report.OutputPerRound = output / int64(len(rounds))
	}
	report.MedianFastMs, report.MedianThinkMs = percentile(fast, 50), percentile(thinking, 50)
	return report
}

func percentile(values []int64, p int) int64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	index := (len(sorted) - 1) * p / 100
	return sorted[index]
}

// buildMaster читает последние ходы Мастера: limit — число ходов.
func buildMaster(db *sql.DB, limit int) (*masterReport, error) {
	rows, err := db.Query(`SELECT created_at, latency_ms, input_tokens, output_tokens FROM usage_records WHERE outcome='master_model' ORDER BY created_at DESC LIMIT ?`, limit*30)
	if err != nil {
		return nil, err
	}
	var calls []masterCall
	for rows.Next() {
		var created string
		var call masterCall
		if err = rows.Scan(&created, &call.LatencyMs, &call.InputTokens, &call.OutputTokens); err != nil {
			rows.Close()
			return nil, err
		}
		if call.At, err = time.Parse(time.RFC3339Nano, created); err != nil {
			continue
		}
		calls = append(calls, call)
	}
	rows.Close()
	turns := clusterMasterTurns(calls)
	if len(turns) > limit {
		// Самый ранний ход мог попасть в выборку не целиком.
		cut := turns[len(turns)-limit].StartedAt
		turns = turns[len(turns)-limit:]
		kept := calls[:0]
		for _, call := range calls {
			if !call.At.Before(cut) {
				kept = append(kept, call)
			}
		}
		calls = kept
	}
	roundRows, err := db.Query(`SELECT detail FROM master_turn_events WHERE type='round' ORDER BY sequence DESC LIMIT ?`, limit*30)
	if err != nil {
		return nil, err
	}
	var rounds []masterRound
	for roundRows.Next() {
		var detail sql.NullString
		if err = roundRows.Scan(&detail); err != nil {
			roundRows.Close()
			return nil, err
		}
		var round masterRound
		if detail.Valid && json.Unmarshal([]byte(detail.String), &round) == nil {
			rounds = append(rounds, round)
		}
	}
	roundRows.Close()
	report := summarizeMaster(turns, calls, rounds)
	return &report, nil
}

func printMaster(out io.Writer, m *masterReport) {
	if m == nil || len(m.Turns) == 0 {
		return
	}
	fmt.Fprintf(out, "\nХоды Мастера (%d): медиана %s, p90 %s; вызовов модели на ход %.1f, медиана вызова %s.\n",
		len(m.Turns), seconds(m.MedianWallMs), seconds(m.P90WallMs), m.CallsPerTurn, seconds(m.MedianCallMs))
	if m.Rounds > 0 {
		fmt.Fprintf(out, "Круги с замером (%d): без размышления %d (медиана %s), с размышлением медиана %s, переигровок %d; инструментов на круг %.1f, вывода на круг %d ток.\n",
			m.Rounds, m.FastRounds, seconds(m.MedianFastMs), seconds(m.MedianThinkMs), m.Reruns, m.ToolsPerRound, m.OutputPerRound)
	}
}
