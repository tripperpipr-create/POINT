package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
)

type WorkOrderRevisionReplayV2 struct {
	WorkOrderID     string
	ExpectedVersion int
	ExpectedDigest  string
	Result          domain.WorkOrder
}

func (s *SQLite) WorkOrderRevisionReplayV2(ctx context.Context, key string) (WorkOrderRevisionReplayV2, bool, error) {
	var replay WorkOrderRevisionReplayV2
	var raw, resultDigest string
	err := s.db.QueryRowContext(ctx, `SELECT work_order_id,expected_version,expected_digest,result_digest,result_payload_json FROM work_order_revision_idempotency_v2 WHERE idempotency_key=?`, strings.TrimSpace(key)).Scan(
		&replay.WorkOrderID, &replay.ExpectedVersion, &replay.ExpectedDigest, &resultDigest, &raw,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return WorkOrderRevisionReplayV2{}, false, nil
	}
	if err != nil {
		return WorkOrderRevisionReplayV2{}, false, err
	}
	if err = json.Unmarshal([]byte(raw), &replay.Result); err != nil {
		return WorkOrderRevisionReplayV2{}, false, err
	}
	replay.Result.Digest = resultDigest
	s.attachWorkOrderRuntimeV2(ctx, &replay.Result)
	return replay, true, nil
}

func (s *SQLite) SaveWorkOrderRevisionReplayV2(ctx context.Context, key string, expectedVersion int, expectedDigest string, result domain.WorkOrder) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO work_order_revision_idempotency_v2(idempotency_key,work_order_id,expected_version,expected_digest,result_version,result_digest,result_payload_json,created_at) VALUES(?,?,?,?,?,?,?,?)`,
		strings.TrimSpace(key), result.ID, expectedVersion, strings.TrimSpace(expectedDigest), result.Version, domain.WorkOrderDigest(result), string(raw), formatTime(time.Now().UTC()))
	if err == nil {
		return nil
	}
	replay, ok, lookupErr := s.WorkOrderRevisionReplayV2(ctx, key)
	if lookupErr != nil {
		return lookupErr
	}
	if ok && replay.WorkOrderID == result.ID && replay.ExpectedVersion == expectedVersion && replay.ExpectedDigest == strings.TrimSpace(expectedDigest) && replay.Result.Version == result.Version && domain.WorkOrderDigest(replay.Result) == domain.WorkOrderDigest(result) {
		return nil
	}
	return err
}
