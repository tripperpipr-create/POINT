package main

import (
	"database/sql"
	"path/filepath"
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

func TestAnalyzeCountsSystemChecksWithoutRunAndNoNestedDoubleCount(t *testing.T) {
	at := time.Now()
	flow := analyze("f", []event{{RunID: "r", Type: "tool.finished", At: at, Data: map[string]any{"durationMs": 100.0, "tool": "run_command", "timing": map[string]any{"auditTotalMs": 10.0}}}, {ExecutionID: "accept", Type: "dependencies.finished", At: at.Add(time.Second), Data: map[string]any{"durationMs": 200.0, "storageMode": "volume", "cacheState": "warm"}}, {ExecutionID: "accept", Type: "verification.finished", At: at.Add(2 * time.Second), Data: map[string]any{"durationMs": 300.0}}}, map[string]*command{})
	if len(flow.Stages) != 2 || flow.Stages[0].ToolMs != 100 || flow.Stages[1].PreparationMs != 200 || flow.Stages[1].VerificationMs != 300 || flow.Stages[1].ToolMs != 0 {
		t.Fatal(flow)
	}
}

func TestAnalyzeSeparatesPhasesAndManualReviewFromMachineFailure(t *testing.T) {
	at := time.Now()
	flow := analyze("f", []event{
		{RunID: "r", Type: "dependencies.finished", At: at, Data: map[string]any{"durationMs": 200.0, "phase": "writer", "engine": "embedded-moby"}},
		{RunID: "r", Type: "verification.finished", At: at, Data: map[string]any{"durationMs": 300.0, "phase": "pre_accept"}},
		{RunID: "r", Type: "verification.finished", At: at, Data: map[string]any{"durationMs": 400.0, "phase": "accept_shadow"}},
		{RunID: "r", Type: "completion.checked", At: at, Data: map[string]any{"checkKind": "pre_accept", "status": "needs_review", "machineChecksPassed": true}},
		{RunID: "r", Type: "completion.checked", At: at, Data: map[string]any{"checkKind": "accept", "verificationService": map[string]any{"wouldReuse": "verification-1", "agrees": true}}},
	}, map[string]*command{})
	s := flow.Stages[0]
	if s.PreAcceptFailed != 0 || s.WriterPreparationMs != 200 || s.PreAcceptMs != 300 || s.ShadowAcceptMs != 400 || s.ToolMs != 0 || s.WouldReuse != "verification-1" || s.AcceptReused || s.Engine != "embedded-moby" || len(s.Unmeasured) == 0 {
		t.Fatalf("incorrect phase accounting: %+v", s)
	}
}

func TestBuildUsesFlowBoundsAndUncorrelatedAcceptEvents(t *testing.T) {
	p := filepath.Join(t.TempDir(), "journal.db")
	db, e := sql.Open("sqlite", p)
	if e != nil {
		t.Fatal(e)
	}
	for _, stmt := range []string{
		`CREATE TABLE flow_runs(id TEXT,started_at TEXT,finished_at TEXT)`,
		`CREATE TABLE executions(id TEXT,flow_run_id TEXT,run_id TEXT)`,
		`CREATE TABLE events(sequence INTEGER,run_id TEXT,flow_run_id TEXT,execution_id TEXT,type TEXT,data TEXT,created_at TEXT)`,
		`INSERT INTO flow_runs VALUES('f','2026-10-01T00:00:00Z','2026-10-01T00:00:30Z')`,
		`INSERT INTO executions VALUES('accept','f','')`,
		`INSERT INTO events VALUES(1,'writer','f','','tool.finished','{"tool":"run_command","durationMs":100,"timing":{"auditTotalMs":10}}','2026-10-01T00:00:05Z')`,
		`INSERT INTO events VALUES(2,'','','accept','dependencies.finished','{"durationMs":200,"storageMode":"volume","cacheState":"warm"}','2026-10-01T00:00:20Z')`,
		`INSERT INTO events VALUES(3,'','','accept','verification.finished','{"durationMs":300}','2026-10-01T00:00:25Z')`,
	} {
		if _, e = db.Exec(stmt); e != nil {
			db.Close()
			t.Fatal(e)
		}
	}
	db.Close()
	r, e := build(p, 1, 0)
	if e != nil {
		t.Fatal(e)
	}
	if r.Summary.WallMs != 30000 || r.Summary.ToolMs != 100 || r.Summary.PreparationMs != 200 || r.Summary.VerificationMs != 300 || len(r.Flows[0].Stages) != 2 {
		t.Fatal(r)
	}
}
