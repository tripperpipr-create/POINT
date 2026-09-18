package storage

import (
	"context"
	"database/sql"

	"local-agent-workbench/internal/domain"
)

func (s *SQLite) SaveFlowRun(ctx context.Context, run domain.FlowRun) error {
	var finished any
	if run.FinishedAt != nil {
		finished = formatTime(*run.FinishedAt)
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO flow_runs(id,flow_id,workspace_id,quest_id,status,node_states,snapshot,error,result,started_at,finished_at,duration_ms)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET status=excluded.status, node_states=excluded.node_states, snapshot=excluded.snapshot,
  error=excluded.error, result=excluded.result, finished_at=excluded.finished_at, duration_ms=excluded.duration_ms`,
		run.ID, run.FlowID, run.WorkspaceID, run.QuestID, run.Status, marshalJSON(run.NodeStates), marshalJSON(run.Snapshot),
		run.Error, run.Result, formatTime(run.StartedAt), finished, run.DurationMs)
	return err
}

func (s *SQLite) GetFlowRun(ctx context.Context, id string) (domain.FlowRun, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id,flow_id,workspace_id,quest_id,status,node_states,snapshot,error,result,started_at,finished_at,duration_ms
FROM flow_runs WHERE id=?`, id)
	var run domain.FlowRun
	var states, snapshot, started string
	var finished sql.NullString
	if err := row.Scan(&run.ID, &run.FlowID, &run.WorkspaceID, &run.QuestID, &run.Status, &states, &snapshot,
		&run.Error, &run.Result, &started, &finished, &run.DurationMs); err != nil {
		return domain.FlowRun{}, err
	}
	unmarshalJSON(states, &run.NodeStates)
	unmarshalJSON(snapshot, &run.Snapshot)
	run.StartedAt = parseTime(started)
	if finished.Valid && finished.String != "" {
		t := parseTime(finished.String)
		run.FinishedAt = &t
	}
	return run, nil
}

func (s *SQLite) ListFlowRuns(ctx context.Context, workspaceID string, limit int) ([]domain.FlowRun, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id,flow_id,workspace_id,quest_id,status,node_states,snapshot,error,result,started_at,finished_at,duration_ms
FROM flow_runs WHERE workspace_id=? ORDER BY started_at DESC LIMIT ?`, workspaceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.FlowRun
	for rows.Next() {
		var run domain.FlowRun
		var states, snapshot, started string
		var finished sql.NullString
		if err = rows.Scan(&run.ID, &run.FlowID, &run.WorkspaceID, &run.QuestID, &run.Status, &states, &snapshot,
			&run.Error, &run.Result, &started, &finished, &run.DurationMs); err != nil {
			return nil, err
		}
		unmarshalJSON(states, &run.NodeStates)
		unmarshalJSON(snapshot, &run.Snapshot)
		run.StartedAt = parseTime(started)
		if finished.Valid && finished.String != "" {
			t := parseTime(finished.String)
			run.FinishedAt = &t
		}
		result = append(result, run)
	}
	return result, rows.Err()
}

func (s *SQLite) ListFlowRunsByFlowID(ctx context.Context, flowID string) ([]domain.FlowRun, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id,flow_id,workspace_id,quest_id,status,node_states,snapshot,error,result,started_at,finished_at,duration_ms
FROM flow_runs WHERE flow_id=? ORDER BY started_at DESC`, flowID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.FlowRun
	for rows.Next() {
		var run domain.FlowRun
		var states, snapshot, started string
		var finished sql.NullString
		if err = rows.Scan(&run.ID, &run.FlowID, &run.WorkspaceID, &run.QuestID, &run.Status, &states, &snapshot,
			&run.Error, &run.Result, &started, &finished, &run.DurationMs); err != nil {
			return nil, err
		}
		unmarshalJSON(states, &run.NodeStates)
		unmarshalJSON(snapshot, &run.Snapshot)
		run.StartedAt = parseTime(started)
		if finished.Valid && finished.String != "" {
			t := parseTime(finished.String)
			run.FinishedAt = &t
		}
		result = append(result, run)
	}
	return result, rows.Err()
}

func (s *SQLite) MigrationVersions(ctx context.Context) ([]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var versions []int
	for rows.Next() {
		var version int
		if err = rows.Scan(&version); err != nil {
			return nil, err
		}
		versions = append(versions, version)
	}
	return versions, rows.Err()
}
