package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
)

func (s *SQLite) BeginDeliveredAppControlV2(ctx context.Context, idempotencyKey string, requested domain.DeliveredApplicationControl) (domain.DeliveredApplicationControl, bool, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" {
		return domain.DeliveredApplicationControl{}, false, errors.New("idempotencyKey is required")
	}
	tx, err := s.beginTx(ctx)
	if err != nil {
		return domain.DeliveredApplicationControl{}, false, err
	}
	defer tx.Rollback()
	var questID, receiptID, digest, action, raw string
	err = tx.QueryRowContext(ctx, `SELECT quest_id,delivery_receipt_id,work_order_digest,action,response_json FROM delivered_app_controls_v2 WHERE idempotency_key=?`, idempotencyKey).Scan(&questID, &receiptID, &digest, &action, &raw)
	if err == nil {
		if questID != requested.QuestID || receiptID != requested.DeliveryReceiptID || digest != requested.WorkOrderDigest || action != requested.Action {
			return domain.DeliveredApplicationControl{}, false, errors.New("idempotency key belongs to a different application action")
		}
		var replay domain.DeliveredApplicationControl
		if json.Unmarshal([]byte(raw), &replay) != nil {
			return domain.DeliveredApplicationControl{}, false, errors.New("stored application action is invalid")
		}
		if replay.Status == "executing" {
			replay.Status = "unknown_outcome"
			replay.Summary = "Previous process ended before the external action was journaled; automatic repetition is forbidden."
		}
		replay.Replayed = true
		return replay, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return domain.DeliveredApplicationControl{}, false, err
	}
	requested.Status = "executing"
	requested.UpdatedAt = time.Now().UTC()
	rawBytes, err := json.Marshal(requested)
	if err != nil {
		return domain.DeliveredApplicationControl{}, false, err
	}
	stamp := formatTime(requested.UpdatedAt)
	if _, err = tx.ExecContext(ctx, `INSERT INTO delivered_app_controls_v2(idempotency_key,quest_id,delivery_receipt_id,work_order_digest,action,response_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`,
		idempotencyKey, requested.QuestID, requested.DeliveryReceiptID, requested.WorkOrderDigest, requested.Action, string(rawBytes), stamp, stamp); err != nil {
		return domain.DeliveredApplicationControl{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return domain.DeliveredApplicationControl{}, false, err
	}
	return requested, false, nil
}

// LatestDeliveredAppControlV2 returns the most recent start/stop journal entry
// for a quest, so a reopened Hub knows what was last done with the app.
func (s *SQLite) LatestDeliveredAppControlV2(ctx context.Context, questID string) (domain.DeliveredApplicationControl, bool, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT response_json FROM delivered_app_controls_v2 WHERE quest_id=? ORDER BY updated_at DESC LIMIT 1`, strings.TrimSpace(questID)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.DeliveredApplicationControl{}, false, nil
	}
	if err != nil {
		return domain.DeliveredApplicationControl{}, false, err
	}
	var control domain.DeliveredApplicationControl
	if err = json.Unmarshal([]byte(raw), &control); err != nil {
		return domain.DeliveredApplicationControl{}, false, errors.New("stored application action is invalid")
	}
	return control, true, nil
}

func (s *SQLite) FinishDeliveredAppControlV2(ctx context.Context, idempotencyKey string, result domain.DeliveredApplicationControl) error {
	result.UpdatedAt = time.Now().UTC()
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	updated, err := s.db.ExecContext(ctx, `UPDATE delivered_app_controls_v2 SET response_json=?,updated_at=? WHERE idempotency_key=?`, string(raw), formatTime(result.UpdatedAt), strings.TrimSpace(idempotencyKey))
	if err != nil {
		return err
	}
	affected, _ := updated.RowsAffected()
	if affected != 1 {
		return fmt.Errorf("application action reservation not found")
	}
	return nil
}
