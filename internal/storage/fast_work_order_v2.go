package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
)

// FastAgentLaunchV2 is the complete durable half of a one-click launch. The
// physical sandbox is created first; none of its metadata becomes visible
// unless every record below commits together.
type FastAgentLaunchV2 struct {
	Order          domain.WorkOrder
	Snapshots      []domain.SourceSnapshot
	Brief          *domain.TaskBrief
	QuestID        string
	Sandbox        domain.SandboxRecord
	Execution      domain.ExecutionInstance
	Run            domain.Run
	Reservation    domain.BudgetReservation
	BudgetLimits   domain.BudgetReserveLimits
	IdempotencyKey string
}

// CreateApprovedWorkOrderV2 atomically creates a brand-new ready WorkOrder and
// crosses its approval boundary. It is used by one-click launch surfaces where
// a saved draft without its Quest would be an orphan rather than useful state.
func (s *SQLite) CreateApprovedWorkOrderV2(ctx context.Context, order domain.WorkOrder, snapshots []domain.SourceSnapshot, idempotencyKey string) (domain.WorkOrderApproval, error) {
	return s.commitApprovedWorkOrderV2(ctx, FastAgentLaunchV2{Order: order, Snapshots: snapshots, IdempotencyKey: idempotencyKey})
}

// CommitFastAgentLaunchV2 commits the approved WorkOrder and every launch
// record before the model worker is allowed to start.
func (s *SQLite) CommitFastAgentLaunchV2(ctx context.Context, launch FastAgentLaunchV2) (domain.WorkOrderApproval, error) {
	if launch.Brief == nil || !domain.IsTaskBriefApproved(*launch.Brief) {
		return domain.WorkOrderApproval{}, errors.New("approved fast-agent task brief is required")
	}
	if strings.TrimSpace(launch.QuestID) == "" || strings.TrimSpace(launch.Sandbox.ID) == "" || strings.TrimSpace(launch.Execution.ID) == "" || strings.TrimSpace(launch.Run.ID) == "" || strings.TrimSpace(launch.Reservation.ID) == "" {
		return domain.WorkOrderApproval{}, errors.New("fast-agent launch identifiers are required")
	}
	if launch.Sandbox.ExecutionID != launch.Execution.ID || launch.Execution.SandboxID != launch.Sandbox.ID || launch.Execution.QuestID != launch.QuestID || launch.Execution.RunID != launch.Run.ID {
		return domain.WorkOrderApproval{}, errors.New("fast-agent sandbox, execution, run and quest linkage is inconsistent")
	}
	if launch.Reservation.QuestID != launch.QuestID || launch.Reservation.ExecutionID != launch.Execution.ID || launch.Reservation.RunID != launch.Run.ID {
		return domain.WorkOrderApproval{}, errors.New("fast-agent budget reservation linkage is inconsistent")
	}
	workspaceID := strings.TrimSpace(launch.Order.WorkspaceID)
	if workspaceID == "" || launch.Sandbox.WorkspaceID != workspaceID || launch.Execution.WorkspaceID != workspaceID || launch.Run.WorkspaceID != workspaceID || launch.Reservation.WorkspaceID != workspaceID {
		return domain.WorkOrderApproval{}, errors.New("fast-agent launch entities belong to different workspaces")
	}
	return s.commitApprovedWorkOrderV2(ctx, launch)
}

func (s *SQLite) commitApprovedWorkOrderV2(ctx context.Context, launch FastAgentLaunchV2) (domain.WorkOrderApproval, error) {
	order := launch.Order
	snapshots := launch.Snapshots
	idempotencyKey := strings.TrimSpace(launch.IdempotencyKey)
	if idempotencyKey == "" {
		return domain.WorkOrderApproval{}, errors.New("idempotency key is required")
	}
	order = domain.NormalizeWorkOrder(order)
	if order.Version != 1 {
		return domain.WorkOrderApproval{}, errors.New("atomic work order launch requires initial version 1")
	}
	if err := domain.ValidateWorkOrder(order); err != nil {
		return domain.WorkOrderApproval{}, err
	}
	approved, err := domain.ApproveWorkOrder(order)
	if err != nil {
		return domain.WorkOrderApproval{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.WorkOrderApproval{}, err
	}
	defer tx.Rollback()
	var replayRaw, replayOrderID string
	if replayErr := tx.QueryRowContext(ctx, `SELECT work_order_id,response_json FROM work_order_approvals_v2 WHERE idempotency_key=?`, idempotencyKey).Scan(&replayOrderID, &replayRaw); replayErr == nil {
		if replayOrderID != order.ID {
			return domain.WorkOrderApproval{}, errors.New("idempotency key belongs to a different work order")
		}
		var replay domain.WorkOrderApproval
		if json.Unmarshal([]byte(replayRaw), &replay) != nil {
			return domain.WorkOrderApproval{}, errors.New("stored approval is invalid")
		}
		if launch.Brief != nil && replay.QuestID != launch.QuestID {
			return domain.WorkOrderApproval{}, errors.New("atomic fast-agent launch replay has different durable identifiers")
		}
		replay.Replayed = true
		return replay, nil
	} else if !errors.Is(replayErr, sql.ErrNoRows) {
		return domain.WorkOrderApproval{}, replayErr
	}
	for _, snapshot := range snapshots {
		if err = saveSourceSnapshotV2With(ctx, tx, snapshot); err != nil {
			return domain.WorkOrderApproval{}, fmt.Errorf("save source snapshot: %w", err)
		}
	}
	if err = validateWorkOrderSourcesV2(ctx, tx, order); err != nil {
		return domain.WorkOrderApproval{}, err
	}
	digest := domain.WorkOrderDigest(order)
	readyRaw, err := json.Marshal(order)
	if err != nil {
		return domain.WorkOrderApproval{}, err
	}
	approvedRaw, err := json.Marshal(approved)
	if err != nil {
		return domain.WorkOrderApproval{}, err
	}
	now := time.Now().UTC()
	nowText := formatTime(now)
	if _, err = tx.ExecContext(ctx, `INSERT INTO work_order_revisions_v2(id,version,workspace_id,digest,payload_json,created_at) VALUES(?,?,?,?,?,?)`,
		order.ID, order.Version, order.WorkspaceID, digest, string(readyRaw), formatTime(order.CreatedAt)); err != nil {
		return domain.WorkOrderApproval{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO work_order_current_v2(id,version,workspace_id,status,digest,payload_json,updated_at) VALUES(?,?,?,?,?,?,?)`,
		order.ID, order.Version, order.WorkspaceID, "approved", digest, string(approvedRaw), nowText); err != nil {
		return domain.WorkOrderApproval{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO workspaces(id,path,name,opened_at) VALUES(?,?,?,?) ON CONFLICT(path) DO UPDATE SET opened_at=excluded.opened_at`,
		order.WorkspaceID, order.Workspace.Path, filepath.Base(order.Workspace.Path), nowText); err != nil {
		return domain.WorkOrderApproval{}, err
	}
	agentIDs, err := materializeWorkOrderRosterV2(ctx, tx, approved, now, false)
	if err != nil {
		return domain.WorkOrderApproval{}, err
	}
	questID := strings.TrimSpace(launch.QuestID)
	if questID == "" {
		questID = domain.NewID("quest")
	}
	leaseToken := domain.NewID("writerlease")
	result, err := tx.ExecContext(ctx, `INSERT INTO writer_leases_v2(workspace_id,quest_id,token,state,acquired_at,updated_at,released_at)
VALUES(?,?,?,'active',?,?,NULL)
ON CONFLICT(workspace_id) DO UPDATE SET quest_id=excluded.quest_id,token=excluded.token,state='active',acquired_at=excluded.acquired_at,updated_at=excluded.updated_at,released_at=NULL
WHERE writer_leases_v2.state='released'`, order.WorkspaceID, questID, leaseToken, nowText, nowText)
	if err != nil {
		return domain.WorkOrderApproval{}, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return domain.WorkOrderApproval{}, errors.New("another writer quest holds the workspace lease")
	}
	objectives := append([]string(nil), order.Scope...)
	constraints := append(append([]string(nil), order.OutOfScope...), order.Assumptions...)
	done := make([]string, 0, len(order.Criteria))
	for _, criterion := range order.Criteria {
		done = append(done, criterion.Text)
	}
	controller := map[string]any{
		"source": "work_order_v2", "workOrderId": approved.ID,
		"approvedVersion": approved.ApprovedVersion, "approvedDigest": approved.ApprovedDigest,
		"workspacePlan": approved.Workspace, "deliveryPolicy": approved.Delivery,
		"modelRouting": approved.Routing, "networkAllowlist": approved.Network,
		"writerLeaseToken": leaseToken,
	}
	questStatus := domain.QuestPreflight
	controllerState := "preflight"
	var briefJSON any
	if launch.Brief != nil {
		questStatus = domain.QuestRunning
		controllerState = "running"
		briefJSON = marshalJSON(*launch.Brief)
		controller["launchMode"] = "fast_agent_v2"
		controller["statusMessage"] = "FastAgent выполняет утверждённый короткий WorkOrder"
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO quests(id,workspace_id,parent_id,title,description,objectives,constraints_json,definition_of_done,importance,status,team_id,flow_id,flow_run_id,flow_node_id,assigned_agent_id,budget_tokens,budget_cents,created_at,updated_at,finished_at,brief_json,kind,controller_state,controller_json,prerequisite_ids_json)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		questID, order.WorkspaceID, "", order.Goal, order.Goal, marshalJSON(objectives), marshalJSON(constraints), marshalJSON(done),
		domain.QuestNormal, questStatus, "", "", "", "", "", order.Budget.Tokens, order.Budget.CostCents,
		nowText, nowText, nil, briefJSON, "project", controllerState, marshalJSON(controller), marshalJSON([]string{})); err != nil {
		return domain.WorkOrderApproval{}, err
	}
	if err = initializeMilestoneRuntimesV2(ctx, tx, questID, approved, now); err != nil {
		return domain.WorkOrderApproval{}, err
	}
	if launch.Brief != nil {
		if len(approved.Milestones) != 1 {
			return domain.WorkOrderApproval{}, errors.New("fast-agent launch requires exactly one milestone runtime")
		}
		startedAt := now
		runtime := domain.MilestoneRuntime{MilestoneID: approved.Milestones[0].ID, Status: domain.QuestRunning, StartedAt: &startedAt, UpdatedAt: now}
		result, updateErr := tx.ExecContext(ctx, `UPDATE milestone_runtimes_v2 SET payload_json=?,updated_at=? WHERE quest_id=? AND work_order_id=? AND work_order_version=? AND milestone_id=?`,
			marshalJSON(runtime), nowText, questID, approved.ID, approved.ApprovedVersion, runtime.MilestoneID)
		if updateErr != nil {
			return domain.WorkOrderApproval{}, updateErr
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return domain.WorkOrderApproval{}, errors.New("fast-agent launch requires exactly one milestone runtime")
		}
		if err = saveSandboxWith(ctx, tx, launch.Sandbox); err != nil {
			return domain.WorkOrderApproval{}, fmt.Errorf("save launch sandbox: %w", err)
		}
		if err = saveExecutionWith(ctx, tx, launch.Execution); err != nil {
			return domain.WorkOrderApproval{}, fmt.Errorf("save launch execution: %w", err)
		}
		if err = saveRunWith(ctx, tx, launch.Run); err != nil {
			return domain.WorkOrderApproval{}, fmt.Errorf("save launch run: %w", err)
		}
		if err = reserveBudgetWith(ctx, tx, &launch.Reservation, launch.BudgetLimits); err != nil {
			return domain.WorkOrderApproval{}, fmt.Errorf("reserve launch budget: %w", err)
		}
	}
	response := domain.WorkOrderApproval{WorkOrder: approved, QuestID: questID, AgentIDs: agentIDs, Status: string(questStatus)}
	if _, err = tx.ExecContext(ctx, `INSERT INTO work_order_approvals_v2(idempotency_key,work_order_id,version,digest,quest_id,response_json,created_at) VALUES(?,?,?,?,?,?,?)`,
		idempotencyKey, order.ID, order.Version, digest, questID, marshalJSON(response), nowText); err != nil {
		return domain.WorkOrderApproval{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.WorkOrderApproval{}, err
	}
	return response, nil
}
