package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
)

func migrationMasterChatBranchV1(ctx context.Context, tx *sql.Tx) error {
	for _, statement := range []string{
		`ALTER TABLE master_conversations ADD COLUMN branch_offer TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE master_conversations ADD COLUMN branch_name TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE master_conversations ADD COLUMN branch_base TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE master_conversations ADD COLUMN branch_commit TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE master_conversations ADD COLUMN branch_path TEXT NOT NULL DEFAULT ''`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLite) SetMasterChatBranchOffer(ctx context.Context, workspaceID, chatID, state string) error {
	if state != "pending" && state != "skipped" {
		return errors.New("неверное состояние предложения ветки")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE master_conversations SET branch_offer=? WHERE workspace_id=? AND id=? AND branch_name=''`, state, workspaceID, chatID)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return sql.ErrNoRows
	}
	return nil
}

// MoveMasterChatToWorktree moves a chat and any unapproved plan drafts.
// The destination lives in Point's managed workspace directory, validated by
// the app before this transaction. A moved plan receives a new revision; the
// older immutable revisions remain as an audit trail in their original world.
func (s *SQLite) MoveMasterChatToWorktree(ctx context.Context, sourceID, chatID, targetPath, branchName, branchBase, commit string) (domain.Workspace, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Workspace{}, err
	}
	defer tx.Rollback()
	var count int
	var plans []domain.WorkOrder
	rows, err := tx.QueryContext(ctx, `SELECT payload_json FROM work_order_current_v2 WHERE workspace_id=?`, sourceID)
	if err != nil {
		return domain.Workspace{}, err
	}
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return domain.Workspace{}, err
		}
		var order domain.WorkOrder
		if json.Unmarshal([]byte(raw), &order) == nil && order.ConversationID == chatID {
			if order.State == "approved" || order.ApprovedVersion > 0 {
				rows.Close()
				return domain.Workspace{}, errors.New("план уже запущен; ветку можно создать только до запуска")
			}
			for _, member := range order.Roster.Permanent {
				if member.Existing {
					rows.Close()
					return domain.Workspace{}, errors.New("в плане уже выбран исполнитель; завершите его в текущей рабочей копии")
				}
			}
			plans = append(plans, order)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return domain.Workspace{}, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM master_turns WHERE workspace_id=? AND conversation_id=? AND status IN ('preparing','waiting','streaming','tools')`, sourceID, chatID).Scan(&count); err != nil {
		return domain.Workspace{}, err
	}
	if count != 0 {
		return domain.Workspace{}, errors.New("дождитесь завершения ответа Мастера")
	}
	var offer string
	if err = tx.QueryRowContext(ctx, `SELECT branch_offer FROM master_conversations WHERE workspace_id=? AND id=?`, sourceID, chatID).Scan(&offer); err != nil {
		return domain.Workspace{}, err
	}
	if offer != "pending" && offer != "skipped" {
		return domain.Workspace{}, errors.New("чат уже привязан к ветке или не ждёт первого плана")
	}
	var target domain.Workspace
	var opened string
	err = tx.QueryRowContext(ctx, `SELECT id,name,opened_at FROM workspaces WHERE path=?`, targetPath).Scan(&target.ID, &target.Name, &opened)
	target.OpenedAt = parseTime(opened)
	if errors.Is(err, sql.ErrNoRows) {
		target = domain.Workspace{ID: domain.NewID("ws"), Path: targetPath, Name: filepath.Base(targetPath), OpenedAt: time.Now().UTC()}
		_, err = tx.ExecContext(ctx, `INSERT INTO workspaces(id,path,name,opened_at) VALUES(?,?,?,?)`, target.ID, target.Path, target.Name, formatTime(target.OpenedAt))
	}
	if err != nil {
		return domain.Workspace{}, err
	}
	target.Path = targetPath
	// The worktree is a new world, so seed the project configuration and roster
	// before moving the chat. Global connections, blueprints and skills remain
	// shared; project agents receive fresh IDs in the isolated world.
	for _, statement := range []string{
		`INSERT INTO orchestrator_config(id,workspace_id,preset,connection_id,provider,provider_preset,base_url,api_version,model,temperature,max_output_tokens,planning_depth,parallelism,approval_strictness,team_preference,created_at,updated_at)
SELECT 'orchestrator-'||?, ?,preset,connection_id,provider,provider_preset,base_url,api_version,model,temperature,max_output_tokens,planning_depth,parallelism,approval_strictness,team_preference,created_at,updated_at FROM orchestrator_config WHERE workspace_id=?`,
		`INSERT INTO workspace_model_routing(workspace_id,coding_connection_id,coding_model,cheap_connection_id,cheap_model,updated_at)
SELECT ?,coding_connection_id,coding_model,cheap_connection_id,cheap_model,updated_at FROM workspace_model_routing WHERE workspace_id=?`,
		`INSERT INTO master_learning_config(workspace_id,enabled) SELECT ?,enabled FROM master_learning_config WHERE workspace_id=?`,
	} {
		args := []any{target.ID, sourceID}
		if strings.HasPrefix(statement, "INSERT INTO orchestrator_config") {
			args = []any{target.ID, target.ID, sourceID}
		}
		if _, err = tx.ExecContext(ctx, statement, args...); err != nil {
			return domain.Workspace{}, err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO master_memory(workspace_id,id,content,source_id,status,updated_at) SELECT ?,id,content,source_id,status,updated_at FROM master_memory WHERE workspace_id=?`, target.ID, sourceID); err != nil {
		return domain.Workspace{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO project_agents(id,workspace_id,blueprint_id,status,role_family,parent_agent_id,owner_quest_id,temporary,name,role_description,personality,mission,system_prompt,goals,rules,constraints_json,project_rules,skill_ids,allowed_tools,tool_policies,connection_id,provider,provider_preset,base_url,primary_model,fallback_models,temperature,max_output_tokens,context_window_tokens,reasoning_effort,max_steps,max_duration_seconds,approval_mode,experience,level,tasks_completed,success_count,created_at,updated_at)
SELECT 'projectagent_'||lower(hex(randomblob(12))),?,blueprint_id,status,role_family,'','',0,name,role_description,personality,mission,system_prompt,goals,rules,constraints_json,project_rules,skill_ids,allowed_tools,tool_policies,connection_id,provider,provider_preset,base_url,primary_model,fallback_models,temperature,max_output_tokens,context_window_tokens,reasoning_effort,max_steps,max_duration_seconds,approval_mode,experience,level,tasks_completed,success_count,created_at,updated_at FROM project_agents WHERE workspace_id=? AND temporary=0 AND status='active'`, target.ID, sourceID); err != nil {
		return domain.Workspace{}, err
	}
	for _, plan := range plans {
		for i, ref := range plan.Sources {
			var raw, storagePath string
			if err = tx.QueryRowContext(ctx, `SELECT payload_json,storage_path FROM source_snapshots_v2 WHERE id=?`, ref.ID).Scan(&raw, &storagePath); err != nil {
				return domain.Workspace{}, err
			}
			var snapshot domain.SourceSnapshot
			if err = json.Unmarshal([]byte(raw), &snapshot); err != nil {
				return domain.Workspace{}, err
			}
			snapshot.ID = domain.NewID("source")
			snapshot.WorkspaceID = target.ID
			snapshot.StoragePath = storagePath
			if err = saveSourceSnapshotV2With(ctx, tx, snapshot); err != nil {
				return domain.Workspace{}, err
			}
			plan.Sources[i].ID = snapshot.ID
		}
		plan.WorkspaceID = target.ID
		plan.Version++
		plan.UpdatedAt = time.Now().UTC()
		plan = domain.NormalizeWorkOrder(plan)
		if err = domain.ValidateWorkOrder(plan); err != nil {
			return domain.Workspace{}, err
		}
		if err = validateWorkOrderSourcesV2(ctx, tx, plan); err != nil {
			return domain.Workspace{}, err
		}
		digest := domain.WorkOrderDigest(plan)
		plan.Digest = digest
		raw, marshalErr := json.Marshal(plan)
		if marshalErr != nil {
			return domain.Workspace{}, marshalErr
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO work_order_revisions_v2(id,version,workspace_id,digest,payload_json,created_at) VALUES(?,?,?,?,?,?)`, plan.ID, plan.Version, target.ID, digest, string(raw), formatTime(plan.UpdatedAt)); err != nil {
			return domain.Workspace{}, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE work_order_current_v2 SET version=?,workspace_id=?,digest=?,payload_json=?,updated_at=? WHERE id=?`, plan.Version, target.ID, digest, string(raw), formatTime(plan.UpdatedAt), plan.ID); err != nil {
			return domain.Workspace{}, err
		}
	}
	for _, table := range []string{"companion_messages", "master_turns", "master_turn_events"} {
		if _, err = tx.ExecContext(ctx, `UPDATE `+table+` SET workspace_id=? WHERE workspace_id=? AND conversation_id=?`, target.ID, sourceID, chatID); err != nil {
			return domain.Workspace{}, err
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE master_conversations SET workspace_id=?,branch_offer='bound',branch_name=?,branch_base=?,branch_commit=?,branch_path=?,updated_at=? WHERE workspace_id=? AND id=?`, target.ID, branchName, branchBase, commit, targetPath, time.Now().UTC().Format(time.RFC3339Nano), sourceID, chatID)
	if err != nil {
		return domain.Workspace{}, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return domain.Workspace{}, sql.ErrNoRows
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, "master.active."+target.ID, chatID); err != nil {
		return domain.Workspace{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE settings SET value='' WHERE key=? AND value=?`, "master.active."+sourceID, chatID); err != nil {
		return domain.Workspace{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.Workspace{}, err
	}
	return target, nil
}
