package storage

import (
	"context"
	"database/sql"
	"local-agent-workbench/internal/domain"
)

func migrationMasterHostV1(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `ALTER TABLE master_conversations ADD COLUMN scope_kind TEXT NOT NULL DEFAULT 'project';
ALTER TABLE master_conversations ADD COLUMN workspace_path TEXT NOT NULL DEFAULT '';
UPDATE master_conversations SET work_mode='auto' WHERE work_mode='execute';
UPDATE master_conversations SET work_mode='fast' WHERE work_mode='agent';
CREATE TABLE host_fast_requests(request_key TEXT PRIMARY KEY, fingerprint TEXT NOT NULL, run_id TEXT NOT NULL);`)
	return err
}

func (s *SQLite) WorkspaceByID(ctx context.Context, id string) (domain.Workspace, error) {
	var w domain.Workspace
	var opened string
	err := s.db.QueryRowContext(ctx, `SELECT id,path,name,opened_at FROM workspaces WHERE id=?`, id).Scan(&w.ID, &w.Path, &w.Name, &opened)
	w.OpenedAt = parseTime(opened)
	return w, err
}
