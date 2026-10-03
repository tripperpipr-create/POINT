package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExactMasterTurnsSeparateAdjacentParallelAndIncomplete(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "perf.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, schema := range []string{
		`CREATE TABLE master_turn_events(sequence INTEGER PRIMARY KEY AUTOINCREMENT,workspace_id TEXT,turn_id TEXT,type TEXT,detail TEXT)`,
		`CREATE TABLE usage_records(workspace_id TEXT,created_at TEXT,latency_ms INTEGER,input_tokens INTEGER,output_tokens INTEGER,outcome TEXT)`,
	} {
		if _, err = db.Exec(schema); err != nil {
			t.Fatal(err)
		}
	}
	at := func(ms int) time.Time { return time.Date(2026, 10, 4, 0, 0, 0, ms*int(time.Millisecond), time.UTC) }
	event := func(workspace, id, kind string, fields any) {
		t.Helper()
		raw, _ := json.Marshal(fields)
		if _, err = db.Exec(`INSERT INTO master_turn_events(workspace_id,turn_id,type,detail) VALUES(?,?,?,?)`, workspace, id, kind, string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	start := func(w, id string, ms int) {
		event(w, id, "turn_started", masterMeasurement{StartedAt: at(ms), PreparationMs: 10})
	}
	finish := func(w, id string, ms, end int) {
		event(w, id, "turn_timing", masterMeasurement{StartedAt: at(ms), EndedAt: at(end), DurationMs: int64(end - ms), PreparationMs: 10, Status: "completed"})
	}
	call := func(id string, ms, end int) {
		event("w", id, "model_call", masterMeasurement{StartedAt: at(ms), EndedAt: at(end), DurationMs: int64(end - ms), InputTokens: 100, OutputTokens: 10})
	}
	start("w", "adjacent-a", 0)
	call("adjacent-a", 10, 100)
	finish("w", "adjacent-a", 0, 110)
	event("w", "adjacent-a", "round", masterRound{DurationMs: 90, Thinking: "on"})
	start("w", "adjacent-b", 111)
	call("adjacent-b", 121, 200)
	finish("w", "adjacent-b", 111, 210)
	start("w", "parallel-a", 211)
	start("w", "parallel-b", 211)
	call("parallel-a", 221, 250)
	call("parallel-a", 251, 280)
	call("parallel-b", 221, 290)
	for _, bounds := range [][2]int{{280, 310}, {290, 330}, {325, 340}, {350, 360}} {
		event("w", "parallel-a", "read_tool", masterMeasurement{StartedAt: at(bounds[0]), EndedAt: at(bounds[1]), DurationMs: int64(bounds[1] - bounds[0])})
	}
	event("w", "parallel-a", "round", masterRound{DurationMs: 60, Thinking: "off", Calls: 4, Rerun: true})
	finish("w", "parallel-b", 211, 300)
	finish("w", "parallel-a", 211, 370)
	start("w", "unfinished", 371)
	start("other", "parallel-a", 372)
	finish("other", "parallel-a", 372, 400)
	if _, err = db.Exec(`INSERT INTO usage_records VALUES('w','2026-10-03T00:00:00Z',8000,50,5,'master_model'),('w','2026-10-04T00:00:00.100Z',90,100,10,'master_model')`); err != nil {
		t.Fatal(err)
	}
	report, err := buildMaster(db, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Turns) != 6 || report.IncompleteTurns != 1 || report.DataQuality != "exact" || report.Rounds != 2 || report.MedianWallMs != 99 {
		t.Fatalf("%+v", report)
	}
	var parallel *masterTurn
	for i := range report.Turns {
		turn := &report.Turns[i]
		if turn.TurnID == "parallel-a" && turn.WorkspaceID == "w" {
			parallel = turn
		}
	}
	if parallel == nil || parallel.ModelCalls != 2 || parallel.ModelMs != 58 || parallel.ReadToolMs != 70 || parallel.InputTokens != 200 {
		t.Fatalf("%+v", parallel)
	}
	if report.Approximate == nil || len(report.Approximate.Turns) != 1 || report.Approximate.MedianWallMs != 8000 || report.Approximate.DataQuality != "approximate_4min_gap" {
		t.Fatalf("legacy %+v", report.Approximate)
	}
	selected, err := buildMaster(db, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected.Turns) != 2 || selected.Rounds != 0 || selected.IncompleteTurns != 1 || selected.MedianWallMs != 28 {
		t.Fatalf("selected %+v", selected)
	}
	var out bytes.Buffer
	printMaster(&out, report)
	if !strings.Contains(out.String(), "приблизительно") || !strings.Contains(out.String(), "неполных 1") {
		t.Fatal(out.String())
	}
}

func TestMasterIntervalUnionDoesNotSumParallelWork(t *testing.T) {
	base := time.Now()
	at := func(ms int) time.Time { return base.Add(time.Duration(ms) * time.Millisecond) }
	intervals := []masterInterval{{at(20), at(40)}, {at(0), at(30)}, {at(10), at(15)}, {at(50), at(60)}, {at(50), at(60)}, {at(90), at(80)}, {time.Time{}, at(100)}}
	if got := intervalUnionMs(intervals); got != 50 {
		t.Fatal(got)
	}
}
