package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"local-agent-workbench/internal/domain"
)

func migrationRunCheckpointsV1(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
ALTER TABLE runs ADD COLUMN controller_json TEXT NOT NULL DEFAULT '';
CREATE TABLE run_checkpoints (
  run_id TEXT NOT NULL,
  seq INTEGER NOT NULL CHECK(seq > 0),
  created_at TEXT NOT NULL,
  payload_json TEXT NOT NULL,
  in_flight_call_id TEXT NOT NULL DEFAULT '',
  pause_reason TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(run_id, seq),
  FOREIGN KEY(run_id) REFERENCES runs(id)
);
CREATE INDEX run_checkpoints_latest ON run_checkpoints(run_id, seq DESC);
`)
	return err
}

func (s *SQLite) SaveRunCheckpoint(ctx context.Context, checkpoint domain.RunCheckpoint) error {
	if checkpoint.RunID == "" || checkpoint.Seq <= 0 {
		return errors.New("run checkpoint requires runId and positive seq")
	}
	if checkpoint.CreatedAt.IsZero() {
		checkpoint.CreatedAt = time.Now().UTC()
	}
	payload, err := json.Marshal(checkpoint)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `INSERT INTO run_checkpoints(run_id,seq,created_at,payload_json,in_flight_call_id,pause_reason) VALUES(?,?,?,?,?,?)
ON CONFLICT(run_id,seq) DO UPDATE SET created_at=excluded.created_at,payload_json=excluded.payload_json,in_flight_call_id=excluded.in_flight_call_id,pause_reason=excluded.pause_reason`,
		checkpoint.RunID, checkpoint.Seq, formatTime(checkpoint.CreatedAt), string(payload), checkpoint.InFlightCallID, checkpoint.PauseReason)
	if err != nil {
		return err
	}
	// Точка несёт всю историю разговора, а пишется на каждый вызов
	// инструмента: прежде строки копились до сноса квеста, и база росла как
	// квадрат числа шагов. Читается только последняя (MAX(seq)), несколько
	// предыдущих остаются про запас.
	if _, err = tx.ExecContext(ctx, `DELETE FROM run_checkpoints WHERE run_id=? AND seq<=?`, checkpoint.RunID, checkpoint.Seq-keptRunCheckpoints); err != nil {
		return err
	}
	return tx.Commit()
}

// Сколько последних контрольных точек прогона хранится.
const keptRunCheckpoints = 3

func (s *SQLite) LatestRunCheckpoint(ctx context.Context, runID string) (domain.RunCheckpoint, error) {
	var payload string
	err := s.db.QueryRowContext(ctx, `SELECT payload_json FROM run_checkpoints WHERE run_id=? ORDER BY seq DESC LIMIT 1`, runID).Scan(&payload)
	if err != nil {
		return domain.RunCheckpoint{}, err
	}
	var checkpoint domain.RunCheckpoint
	if err = json.Unmarshal([]byte(payload), &checkpoint); err != nil {
		return domain.RunCheckpoint{}, fmt.Errorf("decode run checkpoint: %w", err)
	}
	return checkpoint, nil
}

func (s *SQLite) HasResumableRunCheckpoint(ctx context.Context, runID string) (bool, error) {
	checkpoint, err := s.LatestRunCheckpoint(ctx, runID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return checkpoint.Resumable(), nil
}

func encodeControllerJSON(state domain.RunControllerState) string {
	if state == (domain.RunControllerState{}) {
		return ""
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return ""
	}
	return string(raw)
}

func decodeControllerJSON(raw string) domain.RunControllerState {
	if raw == "" || raw == "null" {
		return domain.RunControllerState{}
	}
	var state domain.RunControllerState
	_ = json.Unmarshal([]byte(raw), &state)
	return state
}
