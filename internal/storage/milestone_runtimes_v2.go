package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"local-agent-workbench/internal/domain"
)

func initializeMilestoneRuntimesV2(ctx context.Context, tx *sql.Tx, questID string, order domain.WorkOrder, now time.Time) error {
	for _, milestone := range order.Milestones {
		runtime := domain.MilestoneRuntime{MilestoneID: milestone.ID, Status: domain.QuestDraft, UpdatedAt: now}
		if _, err := tx.ExecContext(ctx, `INSERT INTO milestone_runtimes_v2(quest_id,work_order_id,work_order_version,milestone_id,payload_json,updated_at) VALUES(?,?,?,?,?,?)`,
			questID, order.ID, order.Version, milestone.ID, marshalJSON(runtime), formatTime(now)); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLite) ListMilestoneRuntimesV2(ctx context.Context, questID string, version int) ([]domain.MilestoneRuntime, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT payload_json FROM milestone_runtimes_v2 WHERE quest_id=? AND work_order_version=? ORDER BY rowid`, questID, version)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.MilestoneRuntime{}
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var runtime domain.MilestoneRuntime
		if err = json.Unmarshal([]byte(raw), &runtime); err != nil {
			return nil, err
		}
		result = append(result, runtime)
	}
	return result, rows.Err()
}

func (s *SQLite) SaveMilestoneRuntimeV2(ctx context.Context, questID, workOrderID string, version int, runtime domain.MilestoneRuntime) error {
	runtime.UpdatedAt = time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `UPDATE milestone_runtimes_v2 SET payload_json=?,updated_at=? WHERE quest_id=? AND work_order_id=? AND work_order_version=? AND milestone_id=?`,
		marshalJSON(runtime), formatTime(runtime.UpdatedAt), questID, workOrderID, version, runtime.MilestoneID)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}
