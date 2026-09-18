package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"local-agent-workbench/internal/domain"
)

var ErrToolExecutionApprovalNotPending = errors.New("tool execution approval is not pending")
var ErrToolExecutionApprovalNotUsable = errors.New("tool execution approval is not usable")

func (s *SQLite) SaveToolExecutionApproval(ctx context.Context, approval domain.ToolExecutionApproval) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO tool_execution_approvals(
id,workspace_id,tool_id,tool_digest,arguments_digest,arguments,reason,status,created_at,expires_at,resolved_at,consumed_at
) VALUES(?,?,?,?,?,?,?,?,?,?,NULL,NULL)`, approval.ID, approval.WorkspaceID, approval.ToolID, approval.ToolDigest,
		approval.ArgumentsDigest, string(approval.Arguments), approval.Reason, approval.Status, formatTime(approval.CreatedAt), formatTime(approval.ExpiresAt))
	return err
}

func (s *SQLite) ToolExecutionApproval(ctx context.Context, id string) (domain.ToolExecutionApproval, error) {
	return scanToolExecutionApproval(s.db.QueryRowContext(ctx, `SELECT id,workspace_id,tool_id,tool_digest,arguments_digest,arguments,reason,status,created_at,expires_at,resolved_at,consumed_at FROM tool_execution_approvals WHERE id=?`, id))
}

func (s *SQLite) ResolveToolExecutionApproval(ctx context.Context, id, workspaceID string, allow bool, now time.Time) (domain.ToolExecutionApproval, error) {
	status := domain.ToolExecutionApprovalDenied
	if allow {
		status = domain.ToolExecutionApprovalAllowed
	}
	result, err := s.db.ExecContext(ctx, `UPDATE tool_execution_approvals SET status=?,resolved_at=? WHERE id=? AND workspace_id=? AND status=? AND julianday(expires_at)>julianday(?)`,
		status, formatTime(now), id, workspaceID, domain.ToolExecutionApprovalPending, formatTime(now))
	if err != nil {
		return domain.ToolExecutionApproval{}, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return domain.ToolExecutionApproval{}, err
	}
	if changed != 1 {
		return domain.ToolExecutionApproval{}, ErrToolExecutionApprovalNotPending
	}
	return s.ToolExecutionApproval(ctx, id)
}

// ConsumeToolExecutionApproval atomically validates and spends an allow-once
// decision. Exact digests in the WHERE clause close replay and substitution
// races between review and process creation.
func (s *SQLite) ConsumeToolExecutionApproval(ctx context.Context, id, workspaceID, toolID, toolDigest, argumentsDigest string, now time.Time) (domain.ToolExecutionApproval, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE tool_execution_approvals SET status=?,consumed_at=? WHERE id=? AND workspace_id=? AND tool_id=? AND tool_digest=? AND arguments_digest=? AND status=? AND julianday(expires_at)>julianday(?)`,
		domain.ToolExecutionApprovalConsumed, formatTime(now), id, workspaceID, toolID, toolDigest, argumentsDigest, domain.ToolExecutionApprovalAllowed, formatTime(now))
	if err != nil {
		return domain.ToolExecutionApproval{}, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return domain.ToolExecutionApproval{}, err
	}
	if changed != 1 {
		return domain.ToolExecutionApproval{}, ErrToolExecutionApprovalNotUsable
	}
	return s.ToolExecutionApproval(ctx, id)
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanToolExecutionApproval(row rowScanner) (domain.ToolExecutionApproval, error) {
	var approval domain.ToolExecutionApproval
	var arguments, created, expires string
	var resolved, consumed sql.NullString
	if err := row.Scan(&approval.ID, &approval.WorkspaceID, &approval.ToolID, &approval.ToolDigest, &approval.ArgumentsDigest,
		&arguments, &approval.Reason, &approval.Status, &created, &expires, &resolved, &consumed); err != nil {
		return domain.ToolExecutionApproval{}, err
	}
	approval.Arguments = []byte(arguments)
	approval.CreatedAt = parseTime(created)
	approval.ExpiresAt = parseTime(expires)
	if approval.CreatedAt.IsZero() || approval.ExpiresAt.IsZero() {
		return domain.ToolExecutionApproval{}, fmt.Errorf("tool execution approval %q has invalid timestamps", approval.ID)
	}
	if resolved.Valid {
		value := parseTime(resolved.String)
		approval.ResolvedAt = &value
	}
	if consumed.Valid {
		value := parseTime(consumed.String)
		approval.ConsumedAt = &value
	}
	return approval, nil
}
