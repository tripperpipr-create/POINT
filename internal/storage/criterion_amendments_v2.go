package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
)

// Поправки команд машинных критериев лежат рядом с неизменяемым нарядом, как
// решения по ручным критериям: не переписываются и не удаляются. Действующая
// — последняя по критерию; прежние остаются следом того, что меняли и когда.
func migrationWorkOrderCriterionAmendmentsV2(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
CREATE TABLE work_order_criterion_amendments_v2 (
  quest_id TEXT NOT NULL,
  criterion_id TEXT NOT NULL,
  previous_command TEXT NOT NULL,
  command TEXT NOT NULL,
  reason TEXT NOT NULL DEFAULT '',
  proposed_by TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  PRIMARY KEY(quest_id, criterion_id, created_at)
);
CREATE TRIGGER work_order_criterion_amendments_v2_no_update BEFORE UPDATE ON work_order_criterion_amendments_v2 BEGIN SELECT RAISE(ABORT, 'criterion amendments are immutable'); END;
CREATE TRIGGER work_order_criterion_amendments_v2_no_delete BEFORE DELETE ON work_order_criterion_amendments_v2 BEGIN SELECT RAISE(ABORT, 'criterion amendments are immutable'); END;
`)
	return err
}

// SaveCriterionAmendmentV2 записывает разрешённую поправку.
func (s *SQLite) SaveCriterionAmendmentV2(ctx context.Context, amendment domain.CriterionAmendment) error {
	if strings.TrimSpace(amendment.QuestID) == "" || strings.TrimSpace(amendment.CriterionID) == "" || strings.TrimSpace(amendment.Command) == "" {
		return errors.New("поправке нужны квест, критерий и команда")
	}
	if amendment.CreatedAt.IsZero() {
		amendment.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO work_order_criterion_amendments_v2(quest_id,criterion_id,previous_command,command,reason,proposed_by,created_at) VALUES(?,?,?,?,?,?,?)`,
		amendment.QuestID, amendment.CriterionID, amendment.PreviousCommand, strings.TrimSpace(amendment.Command),
		strings.TrimSpace(amendment.Reason), strings.TrimSpace(amendment.ProposedBy), formatTime(amendment.CreatedAt))
	return err
}

// CriterionAmendmentsV2 — все поправки квеста по времени.
func (s *SQLite) CriterionAmendmentsV2(ctx context.Context, questID string) ([]domain.CriterionAmendment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT criterion_id,previous_command,command,reason,proposed_by,created_at FROM work_order_criterion_amendments_v2 WHERE quest_id=? ORDER BY created_at, criterion_id`, questID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []domain.CriterionAmendment
	for rows.Next() {
		item := domain.CriterionAmendment{QuestID: questID}
		var created string
		if err = rows.Scan(&item.CriterionID, &item.PreviousCommand, &item.Command, &item.Reason, &item.ProposedBy, &created); err != nil {
			return nil, err
		}
		item.CreatedAt = parseTime(created)
		items = append(items, item)
	}
	return items, rows.Err()
}
