package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
)

func (s *SQLite) SaveEgressAsk(ctx context.Context, ask domain.EgressAsk) error {
	resolved := ""
	if !ask.ResolvedAt.IsZero() {
		resolved = ask.ResolvedAt.UTC().Format(time.RFC3339Nano)
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO egress_asks(id, workspace_id, quest_id, run_id, kind, target, reason, risk, status, created_at, resolved_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  status=excluded.status,
  resolved_at=excluded.resolved_at,
  reason=excluded.reason,
  risk=excluded.risk,
  run_id=excluded.run_id,
  quest_id=excluded.quest_id
`, ask.ID, ask.WorkspaceID, ask.QuestID, ask.RunID, string(ask.Kind), ask.Target, ask.Reason, ask.Risk, string(ask.Status),
		ask.CreatedAt.UTC().Format(time.RFC3339Nano), resolved)
	if err != nil {
		return fmt.Errorf("save egress ask: %w", err)
	}
	return nil
}

func (s *SQLite) GetEgressAsk(ctx context.Context, id string) (domain.EgressAsk, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, workspace_id, quest_id, run_id, kind, target, reason, risk, status, created_at, resolved_at
FROM egress_asks WHERE id = ?`, id)
	return scanEgressAsk(row)
}

func (s *SQLite) ListPendingEgressAsks(ctx context.Context, workspaceID string) ([]domain.EgressAsk, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, workspace_id, quest_id, run_id, kind, target, reason, risk, status, created_at, resolved_at
FROM egress_asks WHERE workspace_id = ? AND status = ? ORDER BY created_at ASC`, workspaceID, string(domain.EgressAskPending))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.EgressAsk
	for rows.Next() {
		ask, err := scanEgressAsk(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ask)
	}
	return out, rows.Err()
}

func (s *SQLite) ListEgressAsksForWorkspace(ctx context.Context, workspaceID string) ([]domain.EgressAsk, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, workspace_id, quest_id, run_id, kind, target, reason, risk, status, created_at, resolved_at
FROM egress_asks WHERE workspace_id = ? ORDER BY created_at ASC`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.EgressAsk
	for rows.Next() {
		ask, err := scanEgressAsk(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ask)
	}
	return out, rows.Err()
}

func (s *SQLite) FindPendingEgressAsk(ctx context.Context, workspaceID string, kind domain.EgressAskKind, target string) (domain.EgressAsk, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, workspace_id, quest_id, run_id, kind, target, reason, risk, status, created_at, resolved_at
FROM egress_asks WHERE workspace_id = ? AND kind = ? AND target = ? AND status = ? LIMIT 1`,
		workspaceID, string(kind), strings.TrimSpace(target), string(domain.EgressAskPending))
	return scanEgressAsk(row)
}

type egressScanner interface {
	Scan(dest ...any) error
}

func scanEgressAsk(row egressScanner) (domain.EgressAsk, error) {
	var ask domain.EgressAsk
	var kind, status, created, resolved string
	err := row.Scan(&ask.ID, &ask.WorkspaceID, &ask.QuestID, &ask.RunID, &kind, &ask.Target, &ask.Reason, &ask.Risk, &status, &created, &resolved)
	if err == sql.ErrNoRows {
		return domain.EgressAsk{}, fmt.Errorf("egress ask: %w", err)
	}
	if err != nil {
		return domain.EgressAsk{}, err
	}
	ask.Kind = domain.EgressAskKind(kind)
	ask.Status = domain.EgressAskStatus(status)
	ask.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	if resolved != "" {
		ask.ResolvedAt, _ = time.Parse(time.RFC3339Nano, resolved)
	}
	return ask, nil
}
