package storage

import (
	"context"
	"database/sql"
	"errors"
	"local-agent-workbench/internal/domain"
)

func (s *SQLite) HostFastReplay(ctx context.Context, key, fingerprint string) (domain.Run, bool, error) {
	var id, hash string
	err := s.db.QueryRowContext(ctx, `SELECT run_id,fingerprint FROM host_fast_requests WHERE request_key=?`, key).Scan(&id, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Run{}, false, nil
	}
	if err != nil {
		return domain.Run{}, false, err
	}
	if hash != fingerprint {
		return domain.Run{}, false, errors.New("requestId already belongs to a different task or conversation")
	}
	r, err := s.GetRun(ctx, id)
	return r, true, err
}
