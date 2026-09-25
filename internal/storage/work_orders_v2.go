package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
)

func (s *SQLite) SaveWorkOrderV2(ctx context.Context, order domain.WorkOrder) (domain.WorkOrder, error) {
	order = domain.NormalizeWorkOrder(order)
	if err := domain.ValidateWorkOrder(order); err != nil {
		return domain.WorkOrder{}, err
	}
	if order.State == "approved" {
		return domain.WorkOrder{}, errors.New("approved work orders can only be written by the approval transaction")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.WorkOrder{}, err
	}
	defer tx.Rollback()
	if err = validateWorkOrderSourcesV2(ctx, tx, order); err != nil {
		return domain.WorkOrder{}, err
	}
	var currentVersion int
	var currentDigest, currentRaw, currentStatus string
	err = tx.QueryRowContext(ctx, `SELECT version,digest,payload_json,status FROM work_order_current_v2 WHERE id=?`, order.ID).Scan(&currentVersion, &currentDigest, &currentRaw, &currentStatus)
	switch {
	case errors.Is(err, sql.ErrNoRows) && order.Version != 1:
		return domain.WorkOrder{}, errors.New("initial work order version must be 1")
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return domain.WorkOrder{}, err
	case err == nil && (order.Version < currentVersion || order.Version > currentVersion+1):
		return domain.WorkOrder{}, errors.New("work order version must remain current or increment by one")
	}
	digest := domain.WorkOrderDigest(order)
	if err == nil && order.Version == currentVersion && digest != currentDigest {
		return domain.WorkOrder{}, errors.New("work order content changed without a new version")
	}
	raw, err := json.Marshal(order)
	if err != nil {
		return domain.WorkOrder{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO work_order_revisions_v2(id,version,workspace_id,digest,payload_json,created_at) VALUES(?,?,?,?,?,?)`,
		order.ID, order.Version, order.WorkspaceID, digest, string(raw), formatTime(order.CreatedAt)); err != nil {
		return domain.WorkOrder{}, err
	}
	if currentStatus == "approved" && order.Version == currentVersion+1 {
		var previous domain.WorkOrder
		if err = json.Unmarshal([]byte(currentRaw), &previous); err != nil {
			return domain.WorkOrder{}, err
		}
		diff := domain.DiffWorkOrders(previous, order)
		if _, err = tx.ExecContext(ctx, `INSERT INTO work_order_revision_diffs_v2(id,work_order_id,from_version,to_version,payload_json,created_at) VALUES(?,?,?,?,?,?)`, diff.ID, diff.WorkOrderID, diff.FromVersion, diff.ToVersion, marshalJSON(diff), formatTime(diff.CreatedAt)); err != nil {
			return domain.WorkOrder{}, err
		}
		var questID string
		if err = tx.QueryRowContext(ctx, `SELECT quest_id FROM work_order_approvals_v2 WHERE work_order_id=? AND version=? AND digest=?`, previous.ID, previous.Version, currentDigest).Scan(&questID); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return domain.WorkOrder{}, err
		}
		if questID != "" {
			now := formatTime(time.Now().UTC())
			var fromStatus string
			if scanErr := tx.QueryRowContext(ctx, `SELECT status FROM quests WHERE id=?`, questID).Scan(&fromStatus); scanErr == nil {
				// Квест, стоящий в статусе своего вердикта, — законченная
				// запись: его не ставят на паузу правки, новая версия пойдёт
				// новым квестом. Перезапущенный после вердикта квест правка
				// останавливает, как любой живой; квест, уже стоящий на паузе,
				// тоже помечается правкой — продолжить прежнюю версию нельзя.
				result, updateErr := tx.ExecContext(ctx, `UPDATE quests SET status='paused',controller_state='scope_revision',updated_at=?,finished_at=NULL WHERE id=? AND status IN ('preflight','running','verifying','applying','awaiting_user','blocked','paused') AND NOT EXISTS (SELECT 1 FROM work_order_completion_gates_v2 gate WHERE gate.quest_id=quests.id AND gate.status=quests.status)`, now, questID)
				if updateErr != nil {
					return domain.WorkOrder{}, updateErr
				}
				if affected, _ := result.RowsAffected(); affected == 1 {
					if _, err = tx.ExecContext(ctx, `INSERT INTO work_order_quest_control_events_v2(id,quest_id,action,from_status,to_status,message,created_at) VALUES(?,?,?,?,?,?,?)`, domain.NewID("control"), questID, "revise", fromStatus, "paused", "scope changed; review work order diff", now); err != nil {
						return domain.WorkOrder{}, err
					}
				}
			}
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO work_order_current_v2(id,version,workspace_id,status,digest,payload_json,updated_at) VALUES(?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET version=excluded.version,workspace_id=excluded.workspace_id,status=excluded.status,digest=excluded.digest,payload_json=excluded.payload_json,updated_at=excluded.updated_at`,
		order.ID, order.Version, order.WorkspaceID, order.State, digest, string(raw), formatTime(order.UpdatedAt)); err != nil {
		return domain.WorkOrder{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.WorkOrder{}, err
	}
	order.Digest = digest
	return order, nil
}

func (s *SQLite) ListWorkOrderDiffsV2(ctx context.Context, id string) ([]domain.WorkOrderRevisionDiff, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT payload_json FROM work_order_revision_diffs_v2 WHERE work_order_id=? ORDER BY to_version DESC`, strings.TrimSpace(id))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.WorkOrderRevisionDiff{}
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var diff domain.WorkOrderRevisionDiff
		if err = json.Unmarshal([]byte(raw), &diff); err != nil {
			return nil, err
		}
		result = append(result, diff)
	}
	return result, rows.Err()
}

func validateWorkOrderSourcesV2(ctx context.Context, tx *sql.Tx, order domain.WorkOrder) error {
	for _, ref := range order.Sources {
		var digest, workspaceID string
		if err := tx.QueryRowContext(ctx, `SELECT digest,workspace_id FROM source_snapshots_v2 WHERE id=?`, ref.ID).Scan(&digest, &workspaceID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("source snapshot %q does not exist", ref.ID)
			}
			return err
		}
		if digest != ref.Digest {
			return fmt.Errorf("source snapshot %q digest mismatch", ref.ID)
		}
		if workspaceID != "" && workspaceID != order.WorkspaceID {
			return fmt.Errorf("source snapshot %q belongs to another project", ref.ID)
		}
	}
	return nil
}

func (s *SQLite) GetWorkOrderV2(ctx context.Context, id string) (domain.WorkOrder, error) {
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT payload_json FROM work_order_current_v2 WHERE id=?`, strings.TrimSpace(id)).Scan(&raw); err != nil {
		return domain.WorkOrder{}, err
	}
	var order domain.WorkOrder
	if err := json.Unmarshal([]byte(raw), &order); err != nil {
		return domain.WorkOrder{}, err
	}
	s.normalizeWorkOrderStaffingV2(ctx, &order)
	order.Digest = domain.WorkOrderDigest(order)
	s.attachWorkOrderRuntimeV2(ctx, &order)
	return order, nil
}

// DeleteWorkOrderV2 убирает наряд вместе с его историей ревизий и расписками.
//
// Утверждённый наряд, чей квест так и не появился или был удалён, оставался в
// ленте навсегда: удаления у наряда не было вовсе, и карточка обещала
// «выполнение отслеживается в квесте», которого нет.
//
// Хроника запусков и улики не трогаются: они принадлежат работе, а не договору.
func (s *SQLite) DeleteWorkOrderV2(ctx context.Context, workspaceID, workOrderID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var found string
	err = tx.QueryRowContext(ctx, `SELECT id FROM work_order_current_v2 WHERE id=? AND workspace_id=?`, workOrderID, workspaceID).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("work order %q not found in the open project", workOrderID)
	}
	if err != nil {
		return err
	}
	if err = deleteWorkOrderRowsTx(ctx, tx, workOrderID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLite) ListWorkOrdersForConversationV2(ctx context.Context, conversationID string) ([]domain.WorkOrder, error) {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return []domain.WorkOrder{}, nil
	}
	// The pool holds a single SQLite connection, so the runtime of each order
	// must be attached after this cursor is closed. Querying while iterating
	// waits for a connection that only this loop can release: the Master feed
	// hung until the client gave up, and the user's own message vanished with
	// the failed reload.
	rows, err := s.db.QueryContext(ctx, `SELECT payload_json FROM work_order_current_v2 ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	result := []domain.WorkOrder{}
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return nil, err
		}
		var order domain.WorkOrder
		if json.Unmarshal([]byte(raw), &order) != nil || order.ConversationID != conversationID {
			continue
		}
		order.Digest = domain.WorkOrderDigest(order)
		result = append(result, order)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	for index := range result {
		s.normalizeWorkOrderStaffingV2(ctx, &result[index])
		s.attachWorkOrderRuntimeV2(ctx, &result[index])
	}
	return result, nil
}

func (s *SQLite) ListWorkOrdersForWorkspaceV2(ctx context.Context, workspaceID string) ([]domain.WorkOrder, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return []domain.WorkOrder{}, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT payload_json FROM work_order_current_v2 WHERE workspace_id=? ORDER BY updated_at DESC`, workspaceID)
	if err != nil {
		return nil, err
	}
	result := []domain.WorkOrder{}
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return nil, err
		}
		var order domain.WorkOrder
		if err = json.Unmarshal([]byte(raw), &order); err != nil {
			rows.Close()
			return nil, err
		}
		order.Digest = domain.WorkOrderDigest(order)
		result = append(result, order)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	for index := range result {
		s.normalizeWorkOrderStaffingV2(ctx, &result[index])
		s.attachWorkOrderRuntimeV2(ctx, &result[index])
	}
	return result, nil
}

// normalizeWorkOrderStaffingV2 keeps legacy ready records honest without
// rewriting their immutable revision. Draft, missing, or disabled agents make
// the current view staffing; approval therefore cannot race old persisted UI.
func (s *SQLite) normalizeWorkOrderStaffingV2(ctx context.Context, order *domain.WorkOrder) {
	if order == nil || order.State != "ready" {
		return
	}
	if len(order.Roster.Permanent) == 0 {
		order.State = "staffing"
		return
	}
	for _, draft := range order.Roster.Permanent {
		if !draft.Existing || strings.TrimSpace(draft.ID) == "" {
			order.State = "staffing"
			return
		}
		var status string
		if err := s.db.QueryRowContext(ctx, `SELECT status FROM project_agents WHERE id=? AND workspace_id=?`, draft.ID, order.WorkspaceID).Scan(&status); err != nil || status != "" && status != domain.ProjectAgentActive {
			order.State = "staffing"
			return
		}
	}
}

func (s *SQLite) attachWorkOrderRuntimeV2(ctx context.Context, order *domain.WorkOrder) {
	if order == nil || strings.TrimSpace(order.ID) == "" {
		return
	}
	var runtime domain.WorkOrderRuntime
	var controller, response, updated string
	err := s.db.QueryRowContext(ctx, `SELECT quest.id,quest.status,quest.controller_json,quest.flow_id,quest.flow_run_id,quest.updated_at,approval.response_json
FROM work_order_approvals_v2 approval JOIN quests quest ON quest.id=approval.quest_id
WHERE approval.work_order_id=? ORDER BY approval.version DESC LIMIT 1`, order.ID).Scan(
		&runtime.QuestID, &runtime.Status, &controller, &runtime.FlowID, &runtime.FlowRunID, &updated, &response,
	)
	if err != nil {
		return
	}
	// Исполнители создаются транзакцией утверждения. Без них карточка после
	// запуска продолжала обещать «будет создан» — и человек искал создание
	// агента, которого уже создали.
	var approval domain.WorkOrderApproval
	unmarshalJSON(response, &approval)
	runtime.AgentIDs = approval.AgentIDs
	var state map[string]any
	unmarshalJSON(controller, &state)
	if message, _ := state["statusMessage"].(string); message != "" {
		runtime.Message = message
	}
	runtime.LaunchPhase, _ = state["launchPhase"].(string)
	if raw, _ := state["launchStartedAt"].(string); raw != "" {
		if parsed, parseErr := time.Parse(time.RFC3339Nano, raw); parseErr == nil {
			runtime.LaunchStartedAt = &parsed
		}
	}
	if note, _ := state["plannerNote"].(string); note != "" {
		runtime.PlannerNote = note
	}
	runtime.UpdatedAt = parseTime(updated)
	if bundle, evidenceErr := s.GetEvidenceBundle(ctx, runtime.QuestID); evidenceErr == nil {
		evidence := bundle
		runtime.Evidence = &evidence
		runtime.Assurance = bundle.Assurance
		runtime.OutcomeSummary = bundle.OutcomeSummary
		if domain.IsTerminalQuestStatus(runtime.Status) && strings.TrimSpace(bundle.OutcomeSummary) != "" {
			runtime.Message = bundle.OutcomeSummary
		}
		if bundle.DeliveryReceipt != nil {
			receipt := *bundle.DeliveryReceipt
			runtime.DeliveryReceipt = &receipt
		}
	}
	if runtime.Assurance == "" && (runtime.Status == domain.QuestBlocked || runtime.Status == domain.QuestFailed) {
		runtime.Assurance = domain.WorkOrderAssuranceFailed
		if runtime.OutcomeSummary == "" {
			runtime.OutcomeSummary = runtime.Message
		}
	}
	if milestones, milestoneErr := s.ListMilestoneRuntimesV2(ctx, runtime.QuestID, order.Version); milestoneErr == nil {
		runtime.Milestones = milestones
	}
	s.attachWorkOrderFlowStateV2(ctx, &runtime)
	order.Runtime = &runtime
}

// attachWorkOrderFlowStateV2 переносит состояние Flow на карточку наряда.
// Наблюдатель опрашивает наряд раз в 2.5 с, поэтому экрану выполнения хватает
// этого ответа и не нужен полный /api/state/runtime.
func (s *SQLite) attachWorkOrderFlowStateV2(ctx context.Context, runtime *domain.WorkOrderRuntime) {
	if runtime == nil || strings.TrimSpace(runtime.FlowRunID) == "" {
		return
	}
	flowRun, err := s.GetFlowRun(ctx, runtime.FlowRunID)
	if err != nil {
		return
	}
	nodes := []domain.FlowNode{}
	if flow, flowErr := s.GetFlow(ctx, runtime.FlowID); flowErr == nil {
		nodes = flow.Nodes
	} else {
		// Flow мог не найтись, а состояние узлов есть: порядок тогда не
		// восстановить, но сами этапы человек всё равно обязан увидеть.
		for nodeID := range flowRun.NodeStates {
			nodes = append(nodes, domain.FlowNode{ID: nodeID})
		}
		sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	}
	stages := make([]domain.WorkOrderStage, 0, len(nodes))
	for _, node := range nodes {
		state := flowRun.NodeStates[node.ID]
		stage := domain.WorkOrderStage{
			ID: node.ID, Name: node.Name, Kind: string(node.Kind),
			Status: state.Status, AgentID: node.AgentID,
			StartedAt: state.StartedAt, FinishedAt: state.FinishedAt,
		}
		stage.ExecutionID, _ = state.Output["executionId"].(string)
		stage.RunID, _ = state.Output["runId"].(string)
		stage.WaitReason, _ = state.Output["waitReason"].(string)
		if state.Status == string(domain.RunFailed) {
			failure, _ := state.Output["error"].(string)
			if strings.TrimSpace(failure) == "" {
				failure = "Этап завершился ошибкой"
			}
			runtime.Stall = &domain.WorkOrderStall{
				NodeID: node.ID, NodeName: node.Name,
				WaitReason: "stage_failed", Error: failure,
			}
		}
		// Flow keeps the node in waiting_agent while its execution is running.
		// The feed must show the execution's actual state and run ID.
		if strings.TrimSpace(stage.ExecutionID) != "" {
			if execution, execErr := s.GetExecution(ctx, stage.ExecutionID); execErr == nil {
				if strings.TrimSpace(stage.RunID) == "" {
					stage.RunID = execution.RunID
				}
				if state.Status == "waiting_agent" && (execution.Status == domain.RunRunning || execution.Status == domain.RunPaused || execution.Status == domain.RunWaiting) {
					stage.Status = string(execution.Status)
					stage.WaitReason = ""
				}
			}
		}
		// Затык — любая причина ожидания, а не только провал запуска. Узел,
		// ждущий ключ подключения, стоит так же намертво, и карточка обязана
		// назвать причину: иначе человек видит «квест выполняется» у работы,
		// которая не движется, и ни одной кнопки рядом.
		if runtime.Stall == nil && strings.TrimSpace(stage.WaitReason) != "" {
			startError, _ := state.Output["startError"].(string)
			runtime.Stall = &domain.WorkOrderStall{
				NodeID: node.ID, NodeName: node.Name,
				WaitReason: stage.WaitReason, Error: startError,
			}
		}
		stages = append(stages, stage)
	}
	runtime.Stages = stages
}

func (s *SQLite) WorkOrderWorkspaceOwnerV2(ctx context.Context, path string) (string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,payload_json FROM work_order_current_v2`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	clean := filepath.Clean(strings.TrimSpace(path))
	for rows.Next() {
		var id, raw string
		if err = rows.Scan(&id, &raw); err != nil {
			return "", err
		}
		var order domain.WorkOrder
		if json.Unmarshal([]byte(raw), &order) == nil && filepath.Clean(order.Workspace.Path) == clean {
			return id, nil
		}
	}
	return "", rows.Err()
}

func (s *SQLite) WorkOrderApprovalReplayV2(ctx context.Context, idempotencyKey string) (domain.WorkOrderApproval, bool, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT response_json FROM work_order_approvals_v2 WHERE idempotency_key=?`, strings.TrimSpace(idempotencyKey)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.WorkOrderApproval{}, false, nil
	}
	if err != nil {
		return domain.WorkOrderApproval{}, false, err
	}
	var approval domain.WorkOrderApproval
	if err = json.Unmarshal([]byte(raw), &approval); err != nil {
		return domain.WorkOrderApproval{}, false, err
	}
	approval.Replayed = true
	return approval, true, nil
}

func (s *SQLite) WorkOrderApprovalByQuestV2(ctx context.Context, questID string) (domain.WorkOrderApproval, error) {
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT response_json FROM work_order_approvals_v2 WHERE quest_id=? ORDER BY version DESC LIMIT 1`, strings.TrimSpace(questID)).Scan(&raw); err != nil {
		return domain.WorkOrderApproval{}, err
	}
	var approval domain.WorkOrderApproval
	if err := json.Unmarshal([]byte(raw), &approval); err != nil {
		return domain.WorkOrderApproval{}, err
	}
	return approval, nil
}

func (s *SQLite) WorkOrderHasApprovalV2(ctx context.Context, id string) (bool, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM work_order_approvals_v2 WHERE work_order_id=?`, strings.TrimSpace(id)).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *SQLite) ApproveWorkOrderV2(ctx context.Context, id string, version int, digest, idempotencyKey string) (domain.WorkOrderApproval, error) {
	id, digest, idempotencyKey = strings.TrimSpace(id), strings.TrimSpace(digest), strings.TrimSpace(idempotencyKey)
	if id == "" || version <= 0 || digest == "" || idempotencyKey == "" {
		return domain.WorkOrderApproval{}, errors.New("work order id, version, digest and idempotencyKey are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.WorkOrderApproval{}, err
	}
	defer tx.Rollback()
	var replayRaw, replayOrderID, replayDigest string
	var replayVersion int
	if err = tx.QueryRowContext(ctx, `SELECT work_order_id,version,digest,response_json FROM work_order_approvals_v2 WHERE idempotency_key=?`, idempotencyKey).Scan(&replayOrderID, &replayVersion, &replayDigest, &replayRaw); err == nil {
		if replayOrderID != id || replayVersion != version || replayDigest != digest {
			return domain.WorkOrderApproval{}, errors.New("idempotency key belongs to a different approval")
		}
		var replay domain.WorkOrderApproval
		if json.Unmarshal([]byte(replayRaw), &replay) != nil {
			return domain.WorkOrderApproval{}, errors.New("stored approval is invalid")
		}
		replay.Replayed = true
		return replay, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return domain.WorkOrderApproval{}, err
	}
	var currentVersion int
	var currentDigest, raw string
	if err = tx.QueryRowContext(ctx, `SELECT version,digest,payload_json FROM work_order_current_v2 WHERE id=?`, id).Scan(&currentVersion, &currentDigest, &raw); err != nil {
		return domain.WorkOrderApproval{}, err
	}
	if currentVersion != version || currentDigest != digest {
		return domain.WorkOrderApproval{}, errors.New("work order changed; review the current version before approval")
	}
	var order domain.WorkOrder
	if err = json.Unmarshal([]byte(raw), &order); err != nil {
		return domain.WorkOrderApproval{}, err
	}
	if err = validateWorkOrderSourcesV2(ctx, tx, order); err != nil {
		return domain.WorkOrderApproval{}, err
	}
	approved, err := domain.ApproveWorkOrder(order)
	if err != nil {
		return domain.WorkOrderApproval{}, err
	}
	questID := ""
	_ = tx.QueryRowContext(ctx, `SELECT quest_id FROM work_order_approvals_v2 WHERE work_order_id=? ORDER BY version DESC LIMIT 1`, order.ID).Scan(&questID)
	// Повторное утверждение продолжает квест прошлой версии, только пока у
	// него нет вердикта шлюза. Вердикт и улики неизменяемы и принадлежат
	// одному квесту: новая версия на том же квесте наследовала бы вердикт
	// прошлой и не смогла бы сохранить свои улики. После вердикта новая
	// версия идёт новым квестом; ростер не пересоздаётся, как и при правке
	// до вердикта, а аренда рабочей папки у закрытого квеста снимается.
	followUp := questID != ""
	isRevisionApproval := followUp
	if followUp {
		var verdict string
		err = tx.QueryRowContext(ctx, `SELECT status FROM work_order_completion_gates_v2 WHERE quest_id=?`, questID).Scan(&verdict)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return domain.WorkOrderApproval{}, err
		}
		if err == nil {
			// Квест прошлой версии возвращается к своему вердикту, в каком
			// бы живом статусе он ни был после перезапуска, — в том числе в
			// паузе человека: иначе его можно было продолжить рядом с новым
			// квестом того же наряда. Аренда рабочей папки переходит к новому.
			closedAt := formatTime(time.Now().UTC())
			if _, err = tx.ExecContext(ctx, `UPDATE quests SET status=?,controller_state=?,updated_at=?,finished_at=? WHERE id=? AND status IN ('preflight','running','verifying','applying','awaiting_user','paused')`, verdict, verdict, closedAt, closedAt, questID); err != nil {
				return domain.WorkOrderApproval{}, err
			}
			if _, err = tx.ExecContext(ctx, `UPDATE writer_leases_v2 SET state='released',updated_at=?,released_at=? WHERE quest_id=? AND state='active'`, closedAt, closedAt, questID); err != nil {
				return domain.WorkOrderApproval{}, err
			}
			isRevisionApproval = false
		}
		err = nil
	}
	if !isRevisionApproval {
		questID = domain.NewID("quest")
	}
	response := domain.WorkOrderApproval{WorkOrder: approved, QuestID: questID, Status: "preflight"}
	approvedRaw, _ := json.Marshal(approved)
	now := formatTime(time.Now().UTC())
	if _, err = tx.ExecContext(ctx, `INSERT INTO workspaces(id,path,name,opened_at) VALUES(?,?,?,?) ON CONFLICT(path) DO UPDATE SET opened_at=excluded.opened_at`,
		order.WorkspaceID, order.Workspace.Path, filepath.Base(order.Workspace.Path), now); err != nil {
		return domain.WorkOrderApproval{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE work_order_current_v2 SET status='approved',payload_json=?,updated_at=? WHERE id=? AND version=? AND digest=?`, string(approvedRaw), now, id, version, digest)
	if err != nil {
		return domain.WorkOrderApproval{}, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return domain.WorkOrderApproval{}, fmt.Errorf("work order approval lost a concurrent update")
	}
	// Исполнители — те, что утверждены в этой версии. При повторном
	// утверждении агенты, уже созданные прошлым утверждением, берутся как
	// есть, а новые — создаются: иначе запуск шёл бы составом прошлой версии
	// мимо ростера, который человек только что утвердил.
	{
		response.AgentIDs, err = materializeWorkOrderRosterV2(ctx, tx, approved, parseTime(now), followUp)
		if err != nil {
			return domain.WorkOrderApproval{}, err
		}
	}
	writerLeaseToken := ""
	if order.WorkspaceID != "" {
		writerLeaseToken = domain.NewID("writerlease")
		result, leaseErr := tx.ExecContext(ctx, `INSERT INTO writer_leases_v2(workspace_id,quest_id,token,state,acquired_at,updated_at,released_at)
VALUES(?,?,?,'active',?,?,NULL)
ON CONFLICT(workspace_id) DO UPDATE SET quest_id=excluded.quest_id,token=excluded.token,state='active',acquired_at=excluded.acquired_at,updated_at=excluded.updated_at,released_at=NULL
WHERE writer_leases_v2.state='released' OR writer_leases_v2.quest_id=excluded.quest_id`, order.WorkspaceID, questID, writerLeaseToken, now, now)
		if leaseErr != nil {
			return domain.WorkOrderApproval{}, leaseErr
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return domain.WorkOrderApproval{}, errors.New("another writer quest holds the workspace lease")
		}
	}
	objectives := append([]string(nil), order.Scope...)
	constraints := append([]string(nil), order.OutOfScope...)
	constraints = append(constraints, order.Assumptions...)
	definitionOfDone := make([]string, 0, len(order.Criteria))
	for _, criterion := range order.Criteria {
		definitionOfDone = append(definitionOfDone, criterion.Text)
	}
	controller := map[string]any{
		"source":           "work_order_v2",
		"workOrderId":      approved.ID,
		"approvedVersion":  approved.ApprovedVersion,
		"approvedDigest":   approved.ApprovedDigest,
		"workspacePlan":    approved.Workspace,
		"deliveryPolicy":   approved.Delivery,
		"modelRouting":     approved.Routing,
		"networkAllowlist": approved.Network,
		"writerLeaseToken": writerLeaseToken,
	}
	if isRevisionApproval {
		result, updateErr := tx.ExecContext(ctx, `UPDATE quests SET title=?,description=?,objectives=?,constraints_json=?,definition_of_done=?,status='preflight',controller_state='preflight',controller_json=?,flow_id='',flow_run_id='',budget_tokens=?,budget_cents=?,updated_at=?,finished_at=NULL WHERE id=?`,
			order.Goal, order.Goal, marshalJSON(objectives), marshalJSON(constraints), marshalJSON(definitionOfDone), marshalJSON(controller), order.Budget.Tokens, order.Budget.CostCents, now, questID)
		if updateErr != nil {
			return domain.WorkOrderApproval{}, updateErr
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return domain.WorkOrderApproval{}, errors.New("revised work order quest does not exist")
		}
	} else if _, err = tx.ExecContext(ctx, `INSERT INTO quests(id,workspace_id,parent_id,title,description,objectives,constraints_json,definition_of_done,importance,status,team_id,flow_id,flow_run_id,flow_node_id,assigned_agent_id,budget_tokens,budget_cents,created_at,updated_at,finished_at,brief_json,kind,controller_state,controller_json,prerequisite_ids_json)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		questID, order.WorkspaceID, "", order.Goal, order.Goal, marshalJSON(objectives), marshalJSON(constraints), marshalJSON(definitionOfDone),
		domain.QuestNormal, domain.QuestPreflight, "", "", "", "", "", order.Budget.Tokens, order.Budget.CostCents,
		now, now, nil, nil, "project", "preflight", marshalJSON(controller), marshalJSON([]string{})); err != nil {
		return domain.WorkOrderApproval{}, err
	}
	if err = initializeMilestoneRuntimesV2(ctx, tx, questID, approved, parseTime(now)); err != nil {
		return domain.WorkOrderApproval{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO work_order_approvals_v2(idempotency_key,work_order_id,version,digest,quest_id,response_json,created_at) VALUES(?,?,?,?,?,?,?)`,
		idempotencyKey, id, version, digest, questID, marshalJSON(response), now); err != nil {
		return domain.WorkOrderApproval{}, err
	}
	if strings.TrimSpace(approved.ProposalID) != "" {
		result, proposalErr := tx.ExecContext(ctx, `UPDATE quest_proposals SET status='started' WHERE id=? AND workspace_id=? AND status IN ('pending','modified','started')`, approved.ProposalID, approved.WorkspaceID)
		if proposalErr != nil {
			return domain.WorkOrderApproval{}, proposalErr
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return domain.WorkOrderApproval{}, errors.New("linked quest proposal is missing or no longer approvable")
		}
	}
	if err = tx.Commit(); err != nil {
		return domain.WorkOrderApproval{}, err
	}
	return response, nil
}
