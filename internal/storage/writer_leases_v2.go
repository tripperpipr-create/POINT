package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
)

// ReleaseWriterLeaseV2 makes terminal failure paths release the same exclusive
// writer lease as the evidence gate. The update is idempotent so recovery may
// safely repeat it after an interrupted finalization.
func (s *SQLite) ReleaseWriterLeaseV2(ctx context.Context, questID string) error {
	questID = strings.TrimSpace(questID)
	if questID == "" {
		return errors.New("quest id is required")
	}
	now := formatTime(time.Now().UTC())
	_, err := s.db.ExecContext(ctx, `UPDATE writer_leases_v2 SET state='released',updated_at=?,released_at=? WHERE quest_id=? AND state='active'`, now, now, questID)
	return err
}

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
