package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"local-agent-workbench/internal/domain"
)

func migrationSandboxVolumeWorkspaceV1(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
ALTER TABLE sandboxes ADD COLUMN storage_mode TEXT NOT NULL DEFAULT 'bind';
ALTER TABLE sandboxes ADD COLUMN workspace_volume TEXT NOT NULL DEFAULT '';
ALTER TABLE sandboxes ADD COLUMN file_rules_version TEXT NOT NULL DEFAULT 'legacy-v1';
ALTER TABLE sandboxes ADD COLUMN sandboxd_digest TEXT NOT NULL DEFAULT '';
CREATE TABLE sandbox_audit_reviews (
 id TEXT NOT NULL PRIMARY KEY,
 sandbox_id TEXT NOT NULL, tree_digest TEXT NOT NULL,
 decision TEXT NOT NULL CHECK(decision IN ('accepted','rejected')), created_at TEXT NOT NULL
);
CREATE INDEX sandbox_audit_reviews_revision ON sandbox_audit_reviews(sandbox_id,tree_digest,created_at DESC);
CREATE TRIGGER sandbox_audit_reviews_no_update BEFORE UPDATE ON sandbox_audit_reviews BEGIN SELECT RAISE(ABORT, 'audit reviews are immutable'); END;
CREATE TRIGGER sandbox_audit_reviews_no_delete BEFORE DELETE ON sandbox_audit_reviews BEGIN SELECT RAISE(ABORT, 'audit reviews are immutable'); END;
`)
	return err
}

func (s *SQLite) ListOpenSandboxes(ctx context.Context, workspaceID string) ([]domain.SandboxRecord, error) {
	query := `SELECT id FROM sandboxes WHERE closed_at IS NULL`
	var args []any
	if workspaceID != "" {
		query += ` AND workspace_id=?`
		args = append(args, workspaceID)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	result := make([]domain.SandboxRecord, 0, len(ids))
	for _, id := range ids {
		r, err := s.GetSandbox(ctx, id)
		if err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, nil
}

type SandboxAuditReview struct {
	ID         string    `json:"id"`
	SandboxID  string    `json:"sandboxId"`
	TreeDigest string    `json:"treeDigest"`
	Decision   string    `json:"decision"`
	CreatedAt  time.Time `json:"createdAt"`
}

func (s *SQLite) SaveSandboxAuditReview(ctx context.Context, r SandboxAuditReview) error {
	if r.SandboxID == "" || r.TreeDigest == "" || (r.Decision != "accepted" && r.Decision != "rejected") {
		return errors.New("invalid audit review")
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now().UTC()
	}
	if r.ID == "" {
		r.ID = domain.NewID("audit-review")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO sandbox_audit_reviews(id,sandbox_id,tree_digest,decision,created_at) VALUES(?,?,?,?,?)`, r.ID, r.SandboxID, r.TreeDigest, r.Decision, formatTime(r.CreatedAt))
	return err
}
func (s *SQLite) SandboxAuditReview(ctx context.Context, id, digest string) (SandboxAuditReview, bool, error) {
	r := SandboxAuditReview{SandboxID: id, TreeDigest: digest}
	var created string
	err := s.db.QueryRowContext(ctx, `SELECT id,decision,created_at FROM sandbox_audit_reviews WHERE sandbox_id=? AND tree_digest=? ORDER BY created_at DESC,id DESC LIMIT 1`, id, digest).Scan(&r.ID, &r.Decision, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return r, false, nil
	}
	r.CreatedAt = parseTime(created)
	return r, err == nil, err
}
