package storage

import (
	"context"
	"errors"
	"strings"
)

// DeleteExecutionSandbox removes only a launch that never acquired a Run. It
// is guarded so cleanup cannot erase an execution with observable history.
func (s *SQLite) DeleteExecutionSandbox(ctx context.Context, executionID, sandboxID string) error {
	executionID, sandboxID = strings.TrimSpace(executionID), strings.TrimSpace(sandboxID)
	if executionID == "" || sandboxID == "" {
		return errors.New("execution and sandbox ids are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `DELETE FROM executions WHERE id=? AND sandbox_id=? AND run_id=''`, executionID, sandboxID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return errors.New("execution already has a run or changed concurrently")
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM sandboxes WHERE id=? AND execution_id=?`, sandboxID, executionID); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteOrphanSandbox removes the metadata half left when SaveSandbox succeeds
// but the matching execution insert fails.
func (s *SQLite) DeleteOrphanSandbox(ctx context.Context, sandboxID, executionID string) error {
	sandboxID, executionID = strings.TrimSpace(sandboxID), strings.TrimSpace(executionID)
	if sandboxID == "" || executionID == "" {
		return errors.New("sandbox and execution ids are required")
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM sandboxes WHERE id=? AND execution_id=? AND NOT EXISTS (SELECT 1 FROM executions WHERE id=?)`, sandboxID, executionID, executionID)
	return err
}
