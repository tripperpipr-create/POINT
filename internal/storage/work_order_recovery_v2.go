package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
)

// ListBrokenWorkOrderFinalizationsV2 is deliberately narrow. It finds only
// the historic shape produced by the root-as-child bug and never treats a user
// cancellation as recoverable.
func (s *SQLite) ListBrokenWorkOrderFinalizationsV2(ctx context.Context) ([]domain.WorkOrderApproval, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT approval.response_json
FROM work_order_approvals_v2 approval
JOIN quests quest ON quest.id=approval.quest_id
JOIN flow_runs flow_run ON flow_run.quest_id=quest.id AND flow_run.id=quest.flow_run_id
WHERE quest.status='cancelled'
  AND flow_run.status='completed'
  AND approval.version=(SELECT MAX(latest.version) FROM work_order_approvals_v2 latest WHERE latest.quest_id=quest.id)
  AND NOT EXISTS (SELECT 1 FROM work_order_completion_gates_v2 gate WHERE gate.quest_id=quest.id)
  AND NOT EXISTS (SELECT 1 FROM work_order_quest_control_events_v2 control WHERE control.quest_id=quest.id AND control.action='cancel')
ORDER BY quest.updated_at,quest.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.WorkOrderApproval{}
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var approval domain.WorkOrderApproval
		if err = json.Unmarshal([]byte(raw), &approval); err != nil {
			return nil, err
		}
		result = append(result, approval)
	}
	return result, rows.Err()
}

// ListTerminalFlowPendingWorkOrdersV2 finds the complementary crash/race
// shape: the Flow is already terminal but the root WorkOrder was left in an
// active state and has no evidence gate.  It is safe to replay finalization
// because the failed path never performs delivery and the gate is idempotent.
func (s *SQLite) ListTerminalFlowPendingWorkOrdersV2(ctx context.Context) ([]domain.WorkOrderApproval, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT approval.response_json
FROM work_order_approvals_v2 approval
JOIN quests quest ON quest.id=approval.quest_id
JOIN flow_runs flow_run ON flow_run.quest_id=quest.id AND flow_run.id=quest.flow_run_id
WHERE quest.status IN ('preflight','running','verifying','applying','paused')
  AND flow_run.status IN ('failed','cancelled')
  AND approval.version=(SELECT MAX(latest.version) FROM work_order_approvals_v2 latest WHERE latest.quest_id=quest.id)
  AND NOT EXISTS (SELECT 1 FROM work_order_completion_gates_v2 gate WHERE gate.quest_id=quest.id)
  AND NOT EXISTS (SELECT 1 FROM work_order_quest_control_events_v2 control WHERE control.quest_id=quest.id AND control.action IN ('cancel','pause'))
ORDER BY quest.updated_at,quest.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.WorkOrderApproval{}
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var approval domain.WorkOrderApproval
		if err = json.Unmarshal([]byte(raw), &approval); err != nil {
			return nil, err
		}
		result = append(result, approval)
	}
	return result, rows.Err()
}

// PrepareBrokenWorkOrderFinalizationV2 performs the sole exceptional
// cancelled -> verifying transition, guarded by the same predicates as the
// scanner. It does not run agents, commands or Change Set application.
func (s *SQLite) PrepareBrokenWorkOrderFinalizationV2(ctx context.Context, approval domain.WorkOrderApproval) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	now := formatTime(time.Now().UTC())
	result, err := tx.ExecContext(ctx, `
UPDATE quests SET status='verifying',controller_state='verifying',updated_at=?,finished_at=NULL
WHERE id=? AND status='cancelled'
  AND EXISTS (SELECT 1 FROM flow_runs flow_run WHERE flow_run.id=quests.flow_run_id AND flow_run.quest_id=quests.id AND flow_run.status='completed')
  AND EXISTS (SELECT 1 FROM work_order_approvals_v2 approval WHERE approval.quest_id=quests.id)
  AND NOT EXISTS (SELECT 1 FROM work_order_completion_gates_v2 gate WHERE gate.quest_id=quests.id)
  AND NOT EXISTS (SELECT 1 FROM work_order_quest_control_events_v2 control WHERE control.quest_id=quests.id AND control.action='cancel')`, now, approval.QuestID)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected == 0 {
		return false, err
	}
	proposalID := strings.TrimSpace(approval.WorkOrder.ProposalID)
	if proposalID == "" && strings.HasPrefix(approval.WorkOrder.ID, "workorder-") {
		proposalID = strings.TrimPrefix(approval.WorkOrder.ID, "workorder-")
	}
	if proposalID != "" {
		if _, err = tx.ExecContext(ctx, `UPDATE quest_proposals SET status='started' WHERE id=? AND workspace_id=? AND status IN ('pending','modified','started')`, proposalID, approval.WorkOrder.WorkspaceID); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return false, err
		}
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
