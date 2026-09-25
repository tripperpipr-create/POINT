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
	// Flow callbacks are at-least-once. Once the atomic gate exists, replay is
	// a read: it must not insert a second bundle or finalize milestones twice.
	// Replay is only valid for the verdict of the same approved version: a
	// verdict of an earlier version is not evidence for this one.
	var existingStatus domain.QuestStatus
	var existingVersion int
	var existingDigest string
	if existingErr := tx.QueryRowContext(ctx, `SELECT status,version,digest FROM work_order_completion_gates_v2 WHERE quest_id=?`, questID).Scan(&existingStatus, &existingVersion, &existingDigest); existingErr == nil {
		if existingVersion != version || existingDigest != digest {
			return domain.QuestBlocked, fmt.Errorf("вердикт квеста %s вынесен версии %d наряда, а утверждена версия %d: чужой вердикт не повторяется", questID, existingVersion, version)
		}
		if err = reconcileCompletedWorkOrderGateV2Tx(ctx, tx, questID, existingStatus, time.Now().UTC()); err != nil {
			return domain.QuestBlocked, err
		}
		if err = tx.Commit(); err != nil {
			return domain.QuestBlocked, err
		}
		return existingStatus, nil
	} else if !errors.Is(existingErr, sql.ErrNoRows) {
		return domain.QuestBlocked, existingErr
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
	bundle.Assurance = verdict.Assurance
	if strings.TrimSpace(bundle.OutcomeSummary) == "" {
		switch verdict.Assurance {
		case domain.WorkOrderAssuranceVerified:
			bundle.OutcomeSummary = "Работа доставлена, все доступные проверки выполнены успешно."
		case domain.WorkOrderAssurancePartial:
			bundle.OutcomeSummary = "Работа доставлена с ограничениями проверки."
		default:
			bundle.OutcomeSummary = "Работа заблокирована проверкой или доставкой."
		}
	}
	// Причина нетерминального исхода ложится в сам bundle: он сохраняется
	// именно затем, чтобы объяснить, почему работа не принята, а прежде
	// объяснения в нём не было — квест просто становился blocked.
	if strings.TrimSpace(verdict.Reason) != "" {
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
	if err = finalizeMilestoneRuntimesV2Tx(ctx, tx, questID, workOrderID, version, order, status, time.Now().UTC()); err != nil {
		return domain.QuestBlocked, err
	}
	if domain.IsTerminalQuestStatus(status) {
		if _, err = tx.ExecContext(ctx, `UPDATE writer_leases_v2 SET state='released',updated_at=?,released_at=? WHERE quest_id=? AND state='active'`, now, now, questID); err != nil {
			return domain.QuestBlocked, err
		}
	}
	if err = tx.Commit(); err != nil {
		return domain.QuestBlocked, err
	}
	return status, nil
}

// reconcileCompletedWorkOrderGateV2Tx repairs the only state allowed to lag
// behind an immutable gate. A synchronous Flow can finish while its launch
// goroutine still holds an older quest copy; old builds then wrote `running`
// after the gate had committed `blocked`. Replaying finalization must restore
// the authoritative gate instead of returning before doing any work.
func reconcileCompletedWorkOrderGateV2Tx(ctx context.Context, tx *sql.Tx, questID string, status domain.QuestStatus, now time.Time) error {
	formatted := formatTime(now)
	var finished any
	if domain.IsTerminalQuestStatus(status) {
		finished = formatted
	}
	if _, err := tx.ExecContext(ctx, `UPDATE quests SET status=?,controller_state=?,updated_at=?,finished_at=? WHERE id=? AND status<>?`,
		status, string(status), formatted, finished, questID, status); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT work_order_id,work_order_version,milestone_id,payload_json FROM milestone_runtimes_v2 WHERE quest_id=?`, questID)
	if err != nil {
		return err
	}
	type milestoneRow struct {
		workOrderID string
		version     int
		milestoneID string
		runtime     domain.MilestoneRuntime
	}
	var updates []milestoneRow
	for rows.Next() {
		var row milestoneRow
		var raw string
		if err = rows.Scan(&row.workOrderID, &row.version, &row.milestoneID, &raw); err != nil {
			rows.Close()
			return err
		}
		if err = json.Unmarshal([]byte(raw), &row.runtime); err != nil {
			rows.Close()
			return err
		}
		switch row.runtime.Status {
		case domain.QuestRunning, domain.QuestVerifying, domain.QuestApplying:
			row.runtime.Status = status
			row.runtime.UpdatedAt = now
			row.runtime.FinishedAt = &now
			updates = append(updates, row)
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for _, row := range updates {
		if _, err = tx.ExecContext(ctx, `UPDATE milestone_runtimes_v2 SET payload_json=?,updated_at=? WHERE quest_id=? AND work_order_id=? AND work_order_version=? AND milestone_id=?`,
			marshalJSON(row.runtime), formatted, questID, row.workOrderID, row.version, row.milestoneID); err != nil {
			return err
		}
	}
	if domain.IsTerminalQuestStatus(status) {
		if _, err = tx.ExecContext(ctx, `UPDATE writer_leases_v2 SET state='released',updated_at=?,released_at=? WHERE quest_id=? AND state='active'`, formatted, formatted, questID); err != nil {
			return err
		}
	}
	return nil
}

func finalizeMilestoneRuntimesV2Tx(ctx context.Context, tx *sql.Tx, questID, workOrderID string, version int, order domain.WorkOrder, status domain.QuestStatus, now time.Time) error {
	rows, err := tx.QueryContext(ctx, `SELECT milestone_id,payload_json FROM milestone_runtimes_v2 WHERE quest_id=? AND work_order_id=? AND work_order_version=? ORDER BY rowid`, questID, workOrderID, version)
	if err != nil {
		return err
	}
	type item struct {
		id      string
		runtime domain.MilestoneRuntime
	}
	items := make([]item, 0, len(order.Milestones))
	for rows.Next() {
		var id, raw string
		if err = rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return err
		}
		var runtime domain.MilestoneRuntime
		if err = json.Unmarshal([]byte(raw), &runtime); err != nil {
			rows.Close()
			return err
		}
		if strings.TrimSpace(runtime.MilestoneID) == "" || runtime.MilestoneID != id {
			rows.Close()
			return errors.New("milestone runtime identity is invalid")
		}
		items = append(items, item{id: id, runtime: runtime})
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	if len(items) != len(order.Milestones) {
		return errors.New("milestone runtime set is incomplete")
	}
	for _, value := range items {
		runtime := value.runtime
		switch runtime.Status {
		case domain.QuestRunning, domain.QuestVerifying, domain.QuestApplying:
			runtime.Status = status
			runtime.UpdatedAt = now
			runtime.FinishedAt = &now
		case domain.QuestDraft:
			if (status == domain.QuestCompleted || status == domain.QuestNeedsReview) && len(order.Milestones) != 1 {
				return errors.New("cannot finalize a work order with pending milestones")
			}
			if status != domain.QuestCompleted && status != domain.QuestNeedsReview {
				continue
			}
			runtime.Status = status
			runtime.UpdatedAt = now
			runtime.FinishedAt = &now
		case domain.QuestCompleted, domain.QuestNeedsReview, domain.QuestBlocked, domain.QuestCancelled:
			continue
		default:
			return errors.New("milestone runtime has an invalid status")
		}
		result, updateErr := tx.ExecContext(ctx, `UPDATE milestone_runtimes_v2 SET payload_json=?,updated_at=? WHERE quest_id=? AND work_order_id=? AND work_order_version=? AND milestone_id=?`,
			marshalJSON(runtime), formatTime(runtime.UpdatedAt), questID, workOrderID, version, value.id)
		if updateErr != nil {
			return updateErr
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return sql.ErrNoRows
		}
	}
	return nil
}
