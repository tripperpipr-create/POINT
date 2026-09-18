package storage

import (
	"context"
	"errors"
	"time"
)

func (s *SQLite) ReplaceMasterMemory(ctx context.Context, w, proposedID, targetID string) error {
	if proposedID == targetID {
		return errors.New("выберите другую запись памяти")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var content, source string
	if err = tx.QueryRowContext(ctx, `SELECT content,source_id FROM master_memory WHERE workspace_id=? AND id=? AND status='proposed'`, w, proposedID).Scan(&content, &source); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE master_memory SET content=?,source_id=?,updated_at=? WHERE workspace_id=? AND id=? AND status='accepted'`, content, source, time.Now().UTC().Format(time.RFC3339Nano), w, targetID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("запись для замены не найдена")
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM master_memory WHERE workspace_id=? AND id=?`, w, proposedID); err != nil {
		return err
	}
	return tx.Commit()
}
