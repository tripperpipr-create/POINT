package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// Exact measurements come from correlated master_turn_events. Historical
// usage_records lack turn IDs and are reported separately as approximate data.

const masterTurnGap = 4 * time.Minute

type masterCall struct {
	WorkspaceID  string
	At           time.Time
	LatencyMs    int64
	InputTokens  int64
	OutputTokens int64
}

type masterTurn struct {
	WorkspaceID   string    `json:"workspaceId,omitempty"`
	TurnID        string    `json:"turnId,omitempty"`
	Complete      bool      `json:"complete"`
	Status        string    `json:"status,omitempty"`
	PreparationMs int64     `json:"preparationMs"`
	ReadToolMs    int64     `json:"readToolMs"`
	StartedAt     time.Time `json:"startedAt"`
	WallMs        int64     `json:"wallMs"`
	ModelMs       int64     `json:"modelMs"`
	ModelCalls    int       `json:"modelCalls"`
	InputTokens   int64     `json:"inputTokens"`
	OutputTokens  int64     `json:"outputTokens"`
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
	DataQuality         string        `json:"dataQuality"`
	IncompleteTurns     int           `json:"incompleteTurns"`
	MedianPreparationMs int64         `json:"medianPreparationMs"`
	MedianReadToolMs    int64         `json:"medianReadToolMs"`
	Approximate         *masterReport `json:"approximate,omitempty"`
	Turns               []masterTurn  `json:"turns"`
	MedianWallMs        int64         `json:"medianWallMs"`
	P90WallMs           int64         `json:"p90WallMs"`
	CallsPerTurn        float64       `json:"modelCallsPerTurn"`
	MedianCallMs        int64         `json:"medianModelCallMs"`
	Rounds              int           `json:"rounds"`
	FastRounds          int           `json:"fastRounds"`
	Reruns              int           `json:"reruns"`
	ToolsPerRound       float64       `json:"toolsPerRound"`
	MedianFastMs        int64         `json:"medianFastRoundMs"`
	MedianThinkMs       int64         `json:"medianThinkingRoundMs"`
	OutputPerRound      int64         `json:"outputTokensPerRound"`
}

// clusterMasterTurns склеивает вызовы модели Мастера в ходы по паузе.
func clusterMasterTurns(calls []masterCall) []masterTurn {
	sort.Slice(calls, func(i, j int) bool { return calls[i].At.Before(calls[j].At) })
	var turns []masterTurn
	positions := map[string]int{}
	last := map[string]time.Time{}
	for _, call := range calls {
		start := call.At.Add(-time.Duration(call.LatencyMs) * time.Millisecond)
		position, exists := positions[call.WorkspaceID]
		if !exists || start.Sub(last[call.WorkspaceID]) > masterTurnGap {
			position = len(turns)
			turns = append(turns, masterTurn{WorkspaceID: call.WorkspaceID, StartedAt: start})
			positions[call.WorkspaceID] = position
		}
		turn := &turns[position]
		turn.ModelCalls++
		turn.ModelMs += call.LatencyMs
		turn.InputTokens += call.InputTokens
		turn.OutputTokens += call.OutputTokens
		turn.WallMs = call.At.Sub(turn.StartedAt).Milliseconds()
		last[call.WorkspaceID] = call.At
	}
	sort.Slice(turns, func(i, j int) bool { return turns[i].StartedAt.Before(turns[j].StartedAt) })
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

type masterInterval struct{ start, end time.Time }

// intervalUnionMs measures wall time occupied by work, not the sum of parallel jobs.
func intervalUnionMs(intervals []masterInterval) int64 {
	valid := make([]masterInterval, 0, len(intervals))
	for _, v := range intervals {
		if !v.start.IsZero() && !v.end.Before(v.start) {
			valid = append(valid, v)
		}
	}
	if len(valid) == 0 {
		return 0
	}
	sort.Slice(valid, func(i, j int) bool { return valid[i].start.Before(valid[j].start) })
	start, end := valid[0].start, valid[0].end
	var duration time.Duration
	for _, v := range valid[1:] {
		if v.start.After(end) {
			duration += end.Sub(start)
			start, end = v.start, v.end
		} else if v.end.After(end) {
			end = v.end
		}
	}
	return (duration + end.Sub(start)).Milliseconds()
}

type masterMeasurement struct {
	StartedAt     time.Time `json:"startedAt"`
	EndedAt       time.Time `json:"endedAt"`
	DurationMs    int64     `json:"durationMs"`
	PreparationMs int64     `json:"preparationMs"`
	InputTokens   int64     `json:"inputTokens"`
	OutputTokens  int64     `json:"outputTokens"`
	Status        string    `json:"status"`
}

func buildMaster(db *sql.DB, limit int) (*masterReport, error) {
	rows, err := db.Query(`SELECT workspace_id,turn_id FROM master_turn_events WHERE type IN ('turn_started','turn_timing') GROUP BY workspace_id,turn_id ORDER BY MAX(sequence) DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	var turns []masterTurn
	for rows.Next() {
		var turn masterTurn
		if err = rows.Scan(&turn.WorkspaceID, &turn.TurnID); err != nil {
			rows.Close()
			return nil, err
		}
		turns = append(turns, turn)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	var calls []masterCall
	var rounds []masterRound
	var complete []masterTurn
	var preparation, reading []int64
	for i := range turns {
		turn := &turns[i]
		events, e := db.Query(`SELECT type,detail FROM master_turn_events WHERE workspace_id=? AND turn_id=? ORDER BY sequence`, turn.WorkspaceID, turn.TurnID)
		if e != nil {
			return nil, e
		}
		var tools, models []masterInterval
		for events.Next() {
			var kind string
			var detail sql.NullString
			if e = events.Scan(&kind, &detail); e != nil {
				events.Close()
				return nil, e
			}
			if !detail.Valid {
				continue
			}
			var m masterMeasurement
			if json.Unmarshal([]byte(detail.String), &m) != nil {
				continue
			}
			switch kind {
			case "turn_started":
				turn.StartedAt = m.StartedAt
				turn.PreparationMs = m.PreparationMs
			case "turn_timing":
				if !m.StartedAt.IsZero() && !m.EndedAt.Before(m.StartedAt) && !m.EndedAt.IsZero() {
					turn.StartedAt = m.StartedAt
					turn.WallMs = m.DurationMs
					turn.PreparationMs = m.PreparationMs
					turn.Status = m.Status
					turn.Complete = true
				}
			case "model_call":
				turn.ModelCalls++
				turn.InputTokens += m.InputTokens
				turn.OutputTokens += m.OutputTokens
				models = append(models, masterInterval{m.StartedAt, m.EndedAt})
				calls = append(calls, masterCall{WorkspaceID: turn.WorkspaceID, At: m.EndedAt, LatencyMs: m.DurationMs, InputTokens: m.InputTokens, OutputTokens: m.OutputTokens})
			case "read_tool":
				tools = append(tools, masterInterval{m.StartedAt, m.EndedAt})
			case "round":
				var round masterRound
				if json.Unmarshal([]byte(detail.String), &round) == nil {
					rounds = append(rounds, round)
				}
			}
		}
		e = events.Err()
		events.Close()
		if e != nil {
			return nil, e
		}
		turn.ModelMs = intervalUnionMs(models)
		turn.ReadToolMs = intervalUnionMs(tools)
		if turn.Complete {
			complete = append(complete, *turn)
			preparation = append(preparation, turn.PreparationMs)
			reading = append(reading, turn.ReadToolMs)
		}
	}
	report := summarizeMaster(complete, calls, rounds)
	report.Turns = turns
	report.DataQuality = "exact"
	report.IncompleteTurns = len(turns) - len(complete)
	report.MedianPreparationMs = percentile(preparation, 50)
	report.MedianReadToolMs = percentile(reading, 50)
	// Counts include observed calls of incomplete turns, while wall percentiles only
	// include finished measurements. Missing termination is never a zero-duration turn.
	totalCalls := 0
	for _, turn := range turns {
		totalCalls += turn.ModelCalls
	}
	if len(turns) > 0 {
		report.CallsPerTurn = float64(totalCalls) / float64(len(turns))
	}
	report.Approximate, err = buildApproximateMaster(db, limit)
	if err != nil {
		return nil, err
	}
	return &report, nil
}

// Historical usage has no turn ID. Keep the four-minute heuristic in a separate
// report and exclude usage after exact instrumentation began in each workspace.
// Historical round events have no timestamps, so they cannot be safely selected.
func buildApproximateMaster(db *sql.DB, limit int) (*masterReport, error) {
	cutoffs := map[string]time.Time{}
	rows, err := db.Query(`SELECT workspace_id,detail FROM master_turn_events WHERE type='turn_started' ORDER BY sequence`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var workspace string
		var detail sql.NullString
		if err = rows.Scan(&workspace, &detail); err != nil {
			rows.Close()
			return nil, err
		}
		var m masterMeasurement
		if detail.Valid && json.Unmarshal([]byte(detail.String), &m) == nil && !m.StartedAt.IsZero() {
			if old, ok := cutoffs[workspace]; !ok || m.StartedAt.Before(old) {
				cutoffs[workspace] = m.StartedAt
			}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	query := `SELECT workspace_id,created_at,latency_ms,input_tokens,output_tokens FROM usage_records WHERE outcome='master_model'`
	var predicates []string
	var args []any
	for workspace, cutoff := range cutoffs {
		predicates = append(predicates, `(workspace_id<>? OR julianday(created_at)<julianday(?))`)
		args = append(args, workspace, cutoff.Format(time.RFC3339Nano))
	}
	if len(predicates) > 0 {
		query += " AND " + strings.Join(predicates, " AND ")
	}
	query += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit*30)
	rows, err = db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	var calls []masterCall
	for rows.Next() {
		var created string
		var call masterCall
		if err = rows.Scan(&call.WorkspaceID, &created, &call.LatencyMs, &call.InputTokens, &call.OutputTokens); err != nil {
			rows.Close()
			return nil, err
		}
		if call.At, err = time.Parse(time.RFC3339Nano, created); err != nil {
			continue
		}
		if cutoff, ok := cutoffs[call.WorkspaceID]; ok && !call.At.Before(cutoff) {
			continue
		}
		calls = append(calls, call)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	turns := clusterMasterTurns(calls)
	if len(turns) == 0 {
		return nil, nil
	}
	if len(turns) > limit {
		turns = turns[len(turns)-limit:]
		var kept []masterCall
		for _, call := range calls {
			for _, turn := range turns {
				if call.WorkspaceID == turn.WorkspaceID && !call.At.Before(turn.StartedAt) && !call.At.After(turn.StartedAt.Add(time.Duration(turn.WallMs)*time.Millisecond)) {
					kept = append(kept, call)
					break
				}
			}
		}
		calls = kept
	}
	report := summarizeMaster(turns, calls, nil)
	report.DataQuality = "approximate_4min_gap"
	return &report, nil
}

func printMaster(out io.Writer, m *masterReport) {
	if m == nil {
		return
	}
	if len(m.Turns) > 0 {
		fmt.Fprintf(out, "\nХоды Мастера по turnId (%d, неполных %d): медиана завершённых %s, p90 %s; подготовка %s, читающие инструменты %s; вызовов модели на ход %.1f, медиана вызова %s.\n", len(m.Turns), m.IncompleteTurns, seconds(m.MedianWallMs), seconds(m.P90WallMs), seconds(m.MedianPreparationMs), seconds(m.MedianReadToolMs), m.CallsPerTurn, seconds(m.MedianCallMs))
		if m.Rounds > 0 {
			fmt.Fprintf(out, "Круги выбранных ходов (%d): без размышления %d (медиана %s), с размышлением медиана %s, перепроверок %d; инструментов на круг %.1f, вывода на круг %d ток.\n", m.Rounds, m.FastRounds, seconds(m.MedianFastMs), seconds(m.MedianThinkMs), m.Reruns, m.ToolsPerRound, m.OutputPerRound)
		}
	}
	if a := m.Approximate; a != nil {
		fmt.Fprintf(out, "\nИсторические данные — приблизительно, группировка по паузе 4 минуты (%d): медиана %s, p90 %s; вызовов на группу %.1f. Эти значения не входят в точные медианы.\n", len(a.Turns), seconds(a.MedianWallMs), seconds(a.P90WallMs), a.CallsPerTurn)
	}
}
