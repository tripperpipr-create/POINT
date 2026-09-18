package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"local-agent-workbench/internal/domain"
)

func (s *SQLite) WriterLeaseV2(ctx context.Context, workspaceID string) (domain.WriterLease, bool, error) {
	var lease domain.WriterLease
	var acquiredAt, updatedAt string
	var releasedAt sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT workspace_id,quest_id,token,state,acquired_at,updated_at,released_at FROM writer_leases_v2 WHERE workspace_id=?`, strings.TrimSpace(workspaceID)).Scan(
		&lease.WorkspaceID, &lease.QuestID, &lease.Token, &lease.State, &acquiredAt, &updatedAt, &releasedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.WriterLease{}, false, nil
	}
	if err != nil {
		return domain.WriterLease{}, false, err
	}
	lease.AcquiredAt, lease.UpdatedAt = parseTime(acquiredAt), parseTime(updatedAt)
	if releasedAt.Valid {
		value := parseTime(releasedAt.String)
		lease.ReleasedAt = &value
	}
	return lease, true, nil
}
