package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
)

func (s *SQLite) ReplaceAgentSelectionBindings(ctx context.Context, workOrderID, conversationID, workspaceID, digest string, revision int, agentIDs []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM agent_selection_bindings WHERE work_order_id=?`, workOrderID); err != nil {
		return err
	}
	now := formatTime(time.Now().UTC())
	for _, agentID := range agentIDs {
		if _, err = tx.ExecContext(ctx, `INSERT INTO agent_selection_bindings(conversation_id,work_order_id,workspace_id,agent_id,selection_digest,revision,created_at) VALUES(?,?,?,?,?,?,?)`,
			conversationID, workOrderID, workspaceID, agentID, digest, revision, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *SQLite) ListAgentSelectionBindings(ctx context.Context, workOrderID string) ([]domain.AgentSelectionBinding, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT conversation_id,work_order_id,workspace_id,agent_id,selection_digest,revision,created_at FROM agent_selection_bindings WHERE work_order_id=? ORDER BY created_at,agent_id`, workOrderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.AgentSelectionBinding
	for rows.Next() {
		var item domain.AgentSelectionBinding
		var created string
		if err = rows.Scan(&item.ConversationID, &item.WorkOrderID, &item.WorkspaceID, &item.AgentID, &item.SelectionDigest, &item.Revision, &created); err != nil {
			return nil, err
		}
		item.CreatedAt = parseTime(created)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *SQLite) ListWorkOrdersForAgentBinding(ctx context.Context, agentID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT work_order_id FROM agent_selection_bindings WHERE agent_id=? ORDER BY revision DESC`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		result = append(result, id)
	}
	return result, rows.Err()
}

func (s *SQLite) RejectedRoleFamiliesForWorkOrder(ctx context.Context, workOrderID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT detail_json FROM agent_lifecycle_events WHERE work_order_id=? AND kind='draft_rejected' ORDER BY created_at`, workOrderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	var result []string
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var detail map[string]any
		if json.Unmarshal([]byte(raw), &detail) != nil {
			continue
		}
		family, _ := detail["roleFamily"].(string)
		if family != "" && !seen[family] {
			seen[family] = true
			result = append(result, family)
		}
	}
	return result, rows.Err()
}

func (s *SQLite) SetProjectAgentStatus(ctx context.Context, id, from, to string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE project_agents SET status=?,updated_at=? WHERE id=? AND status=?`, to, formatTime(time.Now().UTC()), id, from)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return fmt.Errorf("project agent %q is not %s", id, from)
	}
	return nil
}

func (s *SQLite) SaveAgentLifecycleEvent(ctx context.Context, event domain.AgentLifecycleEvent) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO agent_lifecycle_events(id,workspace_id,agent_id,work_order_id,kind,detail_json,created_at) VALUES(?,?,?,?,?,?,?)`,
		event.ID, event.WorkspaceID, event.AgentID, event.WorkOrderID, event.Kind, marshalJSON(event.Detail), formatTime(event.CreatedAt))
	return err
}

// RejectProjectAgentDraft removes the draft and every descendant atomically.
// The lifecycle event survives the deletion and is the audit/reselection input.
func (s *SQLite) RejectProjectAgentDraft(ctx context.Context, id string, event domain.AgentLifecycleEvent) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status, workspaceID string
	if err = tx.QueryRowContext(ctx, `SELECT status,workspace_id FROM project_agents WHERE id=?`, id).Scan(&status, &workspaceID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("project agent %q does not exist", id)
		}
		return err
	}
	if status != domain.ProjectAgentDraft {
		return fmt.Errorf("project agent %q is not a draft", id)
	}
	if event.ID == "" {
		event.ID = domain.NewID("agentlife")
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	event.WorkspaceID, event.AgentID, event.Kind = workspaceID, id, "draft_rejected"
	if _, err = tx.ExecContext(ctx, `INSERT INTO agent_lifecycle_events(id,workspace_id,agent_id,work_order_id,kind,detail_json,created_at) VALUES(?,?,?,?,?,?,?)`,
		event.ID, event.WorkspaceID, event.AgentID, event.WorkOrderID, event.Kind, marshalJSON(event.Detail), formatTime(event.CreatedAt)); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `WITH RECURSIVE descendants(id) AS (
  SELECT id FROM project_agents WHERE id=?
  UNION ALL SELECT p.id FROM project_agents p JOIN descendants d ON p.parent_agent_id=d.id
) DELETE FROM agent_selection_bindings WHERE agent_id IN (SELECT id FROM descendants)`, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `WITH RECURSIVE descendants(id) AS (
  SELECT id FROM project_agents WHERE id=?
  UNION ALL SELECT p.id FROM project_agents p JOIN descendants d ON p.parent_agent_id=d.id
) DELETE FROM project_agents WHERE id IN (SELECT id FROM descendants)`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteSupersededProjectAgentDraft removes a selector-owned draft when a
// materially changed brief produces a new selection digest. Unlike rejection,
// it does not blacklist the family for the new brief revision.
func (s *SQLite) DeleteSupersededProjectAgentDraft(ctx context.Context, id string, event domain.AgentLifecycleEvent) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status, workspaceID string
	var temporary bool
	if err = tx.QueryRowContext(ctx, `SELECT status,workspace_id,temporary FROM project_agents WHERE id=?`, id).Scan(&status, &workspaceID, &temporary); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	if status != domain.ProjectAgentDraft || temporary {
		return nil
	}
	if event.ID == "" {
		event.ID = domain.NewID("agentlife")
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	event.WorkspaceID, event.AgentID, event.Kind = workspaceID, id, "draft_superseded"
	if _, err = tx.ExecContext(ctx, `INSERT INTO agent_lifecycle_events(id,workspace_id,agent_id,work_order_id,kind,detail_json,created_at) VALUES(?,?,?,?,?,?,?)`,
		event.ID, event.WorkspaceID, event.AgentID, event.WorkOrderID, event.Kind, marshalJSON(event.Detail), formatTime(event.CreatedAt)); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM agent_selection_bindings WHERE agent_id=?`, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM project_agents WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteTemporaryProjectAgent removes a quest-scoped specialist and its
// descendants while retaining an immutable lifecycle event. This is used only
// after evaluation or an explicit Blueprint decision.
func (s *SQLite) DeleteTemporaryProjectAgent(ctx context.Context, id string, event domain.AgentLifecycleEvent) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var workspaceID string
	var temporary bool
	if err = tx.QueryRowContext(ctx, `SELECT workspace_id,temporary FROM project_agents WHERE id=?`, id).Scan(&workspaceID, &temporary); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	if !temporary {
		return fmt.Errorf("project agent %q is not temporary", id)
	}
	if event.ID == "" {
		event.ID = domain.NewID("agentlife")
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	event.WorkspaceID, event.AgentID = workspaceID, id
	if strings.TrimSpace(event.Kind) == "" {
		event.Kind = "temporary_agent_deleted"
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO agent_lifecycle_events(id,workspace_id,agent_id,work_order_id,kind,detail_json,created_at) VALUES(?,?,?,?,?,?,?)`,
		event.ID, event.WorkspaceID, event.AgentID, event.WorkOrderID, event.Kind, marshalJSON(event.Detail), formatTime(event.CreatedAt)); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `WITH RECURSIVE descendants(id) AS (
  SELECT id FROM project_agents WHERE id=?
  UNION ALL SELECT p.id FROM project_agents p JOIN descendants d ON p.parent_agent_id=d.id
) DELETE FROM agent_selection_bindings WHERE agent_id IN (SELECT id FROM descendants)`, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `WITH RECURSIVE descendants(id) AS (
  SELECT id FROM project_agents WHERE id=?
  UNION ALL SELECT p.id FROM project_agents p JOIN descendants d ON p.parent_agent_id=d.id
) DELETE FROM project_agents WHERE id IN (SELECT id FROM descendants)`, id); err != nil {
		return err
	}
	return tx.Commit()
}
