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

func (s *SQLite) FinalizeWorkOrderQuestV2(ctx context.Context, questID string, bundle domain.EvidenceBundle) (domain.QuestStatus, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.QuestBlocked, err
	}
	defer tx.Rollback()
	var workOrderID, digest, raw string
	var version int
	if err = tx.QueryRowContext(ctx, `SELECT work_order_id,version,digest FROM work_order_approvals_v2 WHERE quest_id=? ORDER BY version DESC LIMIT 1`, questID).Scan(&workOrderID, &version, &digest); err != nil {
		return domain.QuestBlocked, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT payload_json FROM work_order_revisions_v2 WHERE id=? AND version=? AND digest=?`, workOrderID, version, digest).Scan(&raw); err != nil {
		return domain.QuestBlocked, err
	}
	var order domain.WorkOrder
	if err = json.Unmarshal([]byte(raw), &order); err != nil {
		return domain.QuestBlocked, err
	}
	bundle.QuestID = questID
	verdict := domain.WorkOrderEvidenceVerdict(order, bundle)
	if verdict.Err != nil {
		return domain.QuestBlocked, verdict.Err
	}
	status := verdict.Status
	// Причина нетерминального исхода ложится в сам bundle: он сохраняется
	// именно затем, чтобы объяснить, почему работа не принята, а прежде
	// объяснения в нём не было — квест просто становился blocked.
	if status != domain.QuestCompleted && strings.TrimSpace(verdict.Reason) != "" {
		bundle.KnownLimitations = append(bundle.KnownLimitations, "Шлюз доказательств: "+verdict.Reason)
	}
	if bundle.CreatedAt.IsZero() {
		bundle.CreatedAt = time.Now().UTC()
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO evidence_bundles(id,quest_id,payload_json,created_at) VALUES(?,?,?,?)`, bundle.ID, bundle.QuestID, marshalJSON(bundle), formatTime(bundle.CreatedAt)); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.QuestBlocked, err
		}
		return domain.QuestBlocked, err
	}
	now := formatTime(time.Now().UTC())
	if _, err = tx.ExecContext(ctx, `INSERT INTO work_order_completion_gates_v2(quest_id,work_order_id,version,digest,evidence_id,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`,
		questID, workOrderID, version, digest, bundle.ID, status, now, now); err != nil {
		return domain.QuestBlocked, err
	}
	var finished any
	if domain.IsTerminalQuestStatus(status) {
		finished = now
	}
	result, err := tx.ExecContext(ctx, `UPDATE quests SET status=?,controller_state=?,updated_at=?,finished_at=? WHERE id=? AND status IN ('preflight','running','verifying','applying','paused','awaiting_user')`, status, string(status), now, finished, questID)
	if err != nil {
		return domain.QuestBlocked, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return domain.QuestBlocked, errors.New("quest is not in a finalizable state")
	}
	if status == domain.QuestCompleted {
		if _, err = tx.ExecContext(ctx, `UPDATE writer_leases_v2 SET state='released',updated_at=?,released_at=? WHERE quest_id=? AND state='active'`, now, now, questID); err != nil {
			return domain.QuestBlocked, err
		}
	}
	if err = tx.Commit(); err != nil {
		return domain.QuestBlocked, err
	}
	return status, nil
}
