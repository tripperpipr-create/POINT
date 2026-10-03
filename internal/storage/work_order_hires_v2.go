package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"local-agent-workbench/internal/domain"
)

func migrationWorkOrderHiresV2(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `CREATE TABLE work_order_hires_v2 (
  idempotency_key TEXT PRIMARY KEY,
  work_order_id TEXT NOT NULL,
  draft_id TEXT NOT NULL,
  expected_version INTEGER NOT NULL,
  expected_digest TEXT NOT NULL,
  request_hash TEXT NOT NULL,
  agent_id TEXT NOT NULL,
  result_json TEXT NOT NULL,
  created_at TEXT NOT NULL
); CREATE INDEX work_order_hires_order_v2 ON work_order_hires_v2(work_order_id);`)
	return err
}

type WorkOrderHireResult struct {
	Agent     domain.ProjectAgent `json:"agent"`
	WorkOrder domain.WorkOrder    `json:"workOrder"`
}

func WorkOrderHireRequestHash(draftID, agentID string, agent domain.ProjectAgent) string {
	raw, _ := json.Marshal(struct {
		DraftID string
		AgentID string
		Agent   domain.ProjectAgent
	}{draftID, agentID, agent})
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

func (s *SQLite) WorkOrderHireReplayV2(ctx context.Context, orderID, draftID, key string, version int, digest, requestHash string) (WorkOrderHireResult, bool, error) {
	var savedOrder, savedDraft, savedDigest, savedHash, raw string
	var savedVersion int
	err := s.db.QueryRowContext(ctx, `SELECT work_order_id,draft_id,expected_version,expected_digest,request_hash,result_json FROM work_order_hires_v2 WHERE idempotency_key=?`, key).Scan(&savedOrder, &savedDraft, &savedVersion, &savedDigest, &savedHash, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return WorkOrderHireResult{}, false, nil
	}
	if err != nil {
		return WorkOrderHireResult{}, false, err
	}
	if savedOrder != orderID || savedDraft != draftID || savedVersion != version || savedDigest != digest || savedHash != requestHash {
		return WorkOrderHireResult{}, false, errors.New("ключ повтора относится к другому найму")
	}
	var result WorkOrderHireResult
	if err = json.Unmarshal([]byte(raw), &result); err != nil {
		return WorkOrderHireResult{}, false, err
	}
	return result, true, nil
}

// HireWorkOrderAgentV2 commits the agent, optional blueprint and roster revision
// together. A retry returns the original result without creating a second agent.
func (s *SQLite) HireWorkOrderAgentV2(ctx context.Context, orderID, draftID, key string, version int, digest, requestHash string, agent domain.ProjectAgent, blueprint *domain.AgentBlueprint, existing, runnable bool) (WorkOrderHireResult, error) {
	tx, err := s.beginTx(ctx)
	if err != nil {
		return WorkOrderHireResult{}, err
	}
	defer tx.Rollback()
	var savedRaw, savedOrder, savedDraft, savedDigest, savedAgent, savedHash string
	var savedVersion int
	err = tx.QueryRowContext(ctx, `SELECT work_order_id,draft_id,expected_version,expected_digest,request_hash,agent_id,result_json FROM work_order_hires_v2 WHERE idempotency_key=?`, key).Scan(&savedOrder, &savedDraft, &savedVersion, &savedDigest, &savedHash, &savedAgent, &savedRaw)
	if err == nil {
		if savedOrder != orderID || savedDraft != draftID || savedVersion != version || savedDigest != digest || savedHash != requestHash || (existing && savedAgent != agent.ID) {
			return WorkOrderHireResult{}, errors.New("ключ повтора относится к другому найму")
		}
		var result WorkOrderHireResult
		if err = json.Unmarshal([]byte(savedRaw), &result); err != nil {
			return WorkOrderHireResult{}, err
		}
		return result, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return WorkOrderHireResult{}, err
	}
	var raw, currentDigest, status, workspaceID string
	var currentVersion int
	if err = tx.QueryRowContext(ctx, `SELECT version,digest,payload_json,status,workspace_id FROM work_order_current_v2 WHERE id=?`, orderID).Scan(&currentVersion, &currentDigest, &raw, &status, &workspaceID); err != nil {
		return WorkOrderHireResult{}, err
	}
	if currentVersion != version || currentDigest != digest {
		return WorkOrderHireResult{}, errors.New("наряд изменился; обновите карточку перед наймом")
	}
	if status != "staffing" || workspaceID != agent.WorkspaceID {
		return WorkOrderHireResult{}, errors.New("наряд не принимает исполнителя в этом проекте")
	}
	var order domain.WorkOrder
	if err = json.Unmarshal([]byte(raw), &order); err != nil {
		return WorkOrderHireResult{}, err
	}
	index := -1
	for i, draft := range order.Roster.Permanent {
		if draft.ID == draftID && !draft.Existing {
			index = i
			break
		}
	}
	if index < 0 {
		return WorkOrderHireResult{}, errors.New("черновик исполнителя уже изменён или удалён")
	}
	if existing {
		var agentWorkspace, agentStatus string
		if err = tx.QueryRowContext(ctx, `SELECT workspace_id,status FROM project_agents WHERE id=?`, agent.ID).Scan(&agentWorkspace, &agentStatus); err != nil {
			return WorkOrderHireResult{}, err
		}
		if agentWorkspace != workspaceID || agentStatus != domain.ProjectAgentActive {
			return WorkOrderHireResult{}, errors.New("исполнитель недоступен в этом проекте")
		}
	} else {
		if blueprint != nil {
			if err = saveBlueprint(ctx, tx, *blueprint); err != nil {
				return WorkOrderHireResult{}, err
			}
		}
		if err = saveProjectAgent(ctx, tx, agent); err != nil {
			return WorkOrderHireResult{}, err
		}
	}
	order.Roster.Permanent[index] = domain.AgentDraft{ID: agent.ID, BlueprintID: agent.BlueprintID, Existing: true, Name: agent.Name, Role: agent.RoleDescription, Mission: agent.Mission, RequiredTools: append([]string(nil), agent.AllowedTools...)}
	for i := range order.Roster.Temporary {
		if order.Roster.Temporary[i].ParentAgentID == draftID {
			order.Roster.Temporary[i].ParentAgentID = agent.ID
		}
	}
	order.Roster.AgentIDs = nil
	ready := runnable && len(order.Roster.Permanent) > 0
	for _, draft := range order.Roster.Permanent {
		if !draft.Existing {
			ready = false
			continue
		}
		order.Roster.AgentIDs = append(order.Roster.AgentIDs, draft.ID)
	}
	if ready {
		order.State = "ready"
	}
	order.Version = version + 1
	order = domain.NormalizeWorkOrder(order)
	if err = domain.ValidateWorkOrder(order); err != nil {
		return WorkOrderHireResult{}, fmt.Errorf("обновлённый наряд: %w", err)
	}
	if err = validateWorkOrderSourcesV2(ctx, tx, order); err != nil {
		return WorkOrderHireResult{}, err
	}
	resultDigest := domain.WorkOrderDigest(order)
	orderRaw, err := json.Marshal(order)
	if err != nil {
		return WorkOrderHireResult{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO work_order_revisions_v2(id,version,workspace_id,digest,payload_json,created_at) VALUES(?,?,?,?,?,?)`, order.ID, order.Version, order.WorkspaceID, resultDigest, string(orderRaw), formatTime(order.UpdatedAt)); err != nil {
		return WorkOrderHireResult{}, err
	}
	updated, err := tx.ExecContext(ctx, `UPDATE work_order_current_v2 SET version=?,status=?,digest=?,payload_json=?,updated_at=? WHERE id=? AND version=? AND digest=?`, order.Version, order.State, resultDigest, string(orderRaw), formatTime(order.UpdatedAt), order.ID, version, digest)
	if err != nil {
		return WorkOrderHireResult{}, err
	}
	if count, _ := updated.RowsAffected(); count != 1 {
		return WorkOrderHireResult{}, errors.New("наряд изменился во время найма")
	}
	order.Digest = resultDigest
	result := WorkOrderHireResult{Agent: agent, WorkOrder: order}
	resultRaw, err := json.Marshal(result)
	if err != nil {
		return WorkOrderHireResult{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO work_order_hires_v2(idempotency_key,work_order_id,draft_id,expected_version,expected_digest,request_hash,agent_id,result_json,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, key, orderID, draftID, version, digest, requestHash, agent.ID, string(resultRaw), formatTime(time.Now().UTC())); err != nil {
		return WorkOrderHireResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return WorkOrderHireResult{}, err
	}
	return result, nil
}
