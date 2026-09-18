package storage

import (
	"context"
	"database/sql"
	"encoding/json"

	"local-agent-workbench/internal/domain"
)

func (s *SQLite) SaveExecution(ctx context.Context, exec domain.ExecutionInstance) error {
	return saveExecutionWith(ctx, s.db, exec)
}

type sqlExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func saveExecutionWith(ctx context.Context, db sqlExecer, exec domain.ExecutionInstance) error {
	var finished any
	if exec.FinishedAt != nil {
		finished = formatTime(*exec.FinishedAt)
	}
	_, err := db.ExecContext(ctx, `
INSERT INTO executions(id,workspace_id,project_agent_id,quest_id,flow_run_id,flow_node_id,run_id,sandbox_id,task,status,snapshot,error,result,started_at,finished_at,duration_ms,runtime,runtime_session_id)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET
  quest_id=excluded.quest_id, flow_run_id=excluded.flow_run_id, flow_node_id=excluded.flow_node_id,
  status=excluded.status, run_id=excluded.run_id, sandbox_id=excluded.sandbox_id, task=excluded.task,
  snapshot=excluded.snapshot, error=excluded.error, result=excluded.result, finished_at=excluded.finished_at, duration_ms=excluded.duration_ms,
  runtime=excluded.runtime, runtime_session_id=excluded.runtime_session_id`,
		exec.ID, exec.WorkspaceID, exec.ProjectAgentID, exec.QuestID, exec.FlowRunID, exec.FlowNodeID, exec.RunID,
		exec.SandboxID, exec.Task, exec.Status, marshalJSON(exec.Snapshot), exec.Error, exec.Result,
		formatTime(exec.StartedAt), finished, exec.DurationMs, exec.Runtime, exec.RuntimeSessionID)
	return err
}

func (s *SQLite) GetExecution(ctx context.Context, id string) (domain.ExecutionInstance, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id,workspace_id,project_agent_id,quest_id,flow_run_id,flow_node_id,run_id,sandbox_id,task,status,snapshot,error,result,started_at,finished_at,duration_ms,runtime,runtime_session_id
FROM executions WHERE id=?`, id)
	var exec domain.ExecutionInstance
	var snapshot, started string
	var finished sql.NullString
	err := row.Scan(&exec.ID, &exec.WorkspaceID, &exec.ProjectAgentID, &exec.QuestID, &exec.FlowRunID, &exec.FlowNodeID, &exec.RunID, &exec.SandboxID, &exec.Task, &exec.Status, &snapshot, &exec.Error, &exec.Result, &started, &finished, &exec.DurationMs, &exec.Runtime, &exec.RuntimeSessionID)
	if err != nil {
		return domain.ExecutionInstance{}, err
	}
	_ = json.Unmarshal([]byte(snapshot), &exec.Snapshot)
	exec.StartedAt = parseTime(started)
	if finished.Valid {
		value := parseTime(finished.String)
		exec.FinishedAt = &value
	}
	return exec, nil
}

func (s *SQLite) GetExecutionByRunID(ctx context.Context, runID string) (domain.ExecutionInstance, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id,workspace_id,project_agent_id,quest_id,flow_run_id,flow_node_id,run_id,sandbox_id,task,status,snapshot,error,result,started_at,finished_at,duration_ms,runtime,runtime_session_id
FROM executions WHERE run_id=? ORDER BY started_at DESC LIMIT 1`, runID)
	var exec domain.ExecutionInstance
	var snapshot, started string
	var finished sql.NullString
	err := row.Scan(&exec.ID, &exec.WorkspaceID, &exec.ProjectAgentID, &exec.QuestID, &exec.FlowRunID, &exec.FlowNodeID, &exec.RunID, &exec.SandboxID, &exec.Task, &exec.Status, &snapshot, &exec.Error, &exec.Result, &started, &finished, &exec.DurationMs, &exec.Runtime, &exec.RuntimeSessionID)
	if err != nil {
		return domain.ExecutionInstance{}, err
	}
	_ = json.Unmarshal([]byte(snapshot), &exec.Snapshot)
	exec.StartedAt = parseTime(started)
	if finished.Valid {
		value := parseTime(finished.String)
		exec.FinishedAt = &value
	}
	return exec, nil
}

func (s *SQLite) ListExecutions(ctx context.Context, workspaceID string, limit int) ([]domain.ExecutionInstance, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id,workspace_id,project_agent_id,quest_id,flow_run_id,flow_node_id,run_id,sandbox_id,task,status,snapshot,error,result,started_at,finished_at,duration_ms,runtime,runtime_session_id
FROM executions WHERE workspace_id=? ORDER BY started_at DESC LIMIT ?`, workspaceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.ExecutionInstance
	for rows.Next() {
		var exec domain.ExecutionInstance
		var snapshot, started string
		var finished sql.NullString
		if err = rows.Scan(&exec.ID, &exec.WorkspaceID, &exec.ProjectAgentID, &exec.QuestID, &exec.FlowRunID,
			&exec.FlowNodeID, &exec.RunID, &exec.SandboxID, &exec.Task, &exec.Status, &snapshot, &exec.Error,
			&exec.Result, &started, &finished, &exec.DurationMs, &exec.Runtime, &exec.RuntimeSessionID); err != nil {
			return nil, err
		}
		unmarshalJSON(snapshot, &exec.Snapshot)
		exec.StartedAt = parseTime(started)
		if finished.Valid && finished.String != "" {
			t := parseTime(finished.String)
			exec.FinishedAt = &t
		}
		result = append(result, exec)
	}
	return result, rows.Err()
}

func (s *SQLite) SaveSandbox(ctx context.Context, sandbox domain.SandboxRecord) error {
	return saveSandboxWith(ctx, s.db, sandbox)
}

func saveSandboxWith(ctx context.Context, db sqlExecer, sandbox domain.SandboxRecord) error {
	var closed any
	if sandbox.ClosedAt != nil {
		closed = formatTime(*sandbox.ClosedAt)
	}
	_, err := db.ExecContext(ctx, `
INSERT INTO sandboxes(id,workspace_id,execution_id,kind,backend,backend_version,backend_image,backend_image_digest,path,base_commit,parent_sandbox_id,parent_execution_id,parent_sandbox_ids,parent_execution_ids,baseline_path,baseline_change_set_ids,created_at,closed_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET parent_sandbox_id=excluded.parent_sandbox_id,
  parent_execution_id=excluded.parent_execution_id, parent_sandbox_ids=excluded.parent_sandbox_ids,
  parent_execution_ids=excluded.parent_execution_ids, baseline_path=excluded.baseline_path,
  baseline_change_set_ids=excluded.baseline_change_set_ids, closed_at=excluded.closed_at`,
		sandbox.ID, sandbox.WorkspaceID, sandbox.ExecutionID, sandbox.Kind, sandbox.Backend, sandbox.BackendVersion,
		sandbox.BackendImage, sandbox.BackendImageDigest, sandbox.Path, sandbox.BaseCommit,
		sandbox.ParentSandboxID, sandbox.ParentExecutionID, marshalJSON(sandbox.ParentSandboxIDs), marshalJSON(sandbox.ParentExecutionIDs),
		sandbox.BaselinePath, marshalJSON(sandbox.BaselineChangeSetIDs), formatTime(sandbox.CreatedAt), closed)
	return err
}

func (s *SQLite) GetSandbox(ctx context.Context, id string) (domain.SandboxRecord, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id,workspace_id,execution_id,kind,backend,backend_version,backend_image,backend_image_digest,path,base_commit,parent_sandbox_id,parent_execution_id,parent_sandbox_ids,parent_execution_ids,baseline_path,baseline_change_set_ids,created_at,closed_at FROM sandboxes WHERE id=?`, id)
	var sandbox domain.SandboxRecord
	var created, parentSandboxIDs, parentExecutionIDs, baselineChangeSetIDs string
	var closed sql.NullString
	if err := row.Scan(&sandbox.ID, &sandbox.WorkspaceID, &sandbox.ExecutionID, &sandbox.Kind,
		&sandbox.Backend, &sandbox.BackendVersion, &sandbox.BackendImage, &sandbox.BackendImageDigest, &sandbox.Path,
		&sandbox.BaseCommit, &sandbox.ParentSandboxID, &sandbox.ParentExecutionID, &parentSandboxIDs, &parentExecutionIDs,
		&sandbox.BaselinePath, &baselineChangeSetIDs, &created, &closed); err != nil {
		return domain.SandboxRecord{}, err
	}
	unmarshalJSON(parentSandboxIDs, &sandbox.ParentSandboxIDs)
	unmarshalJSON(parentExecutionIDs, &sandbox.ParentExecutionIDs)
	unmarshalJSON(baselineChangeSetIDs, &sandbox.BaselineChangeSetIDs)
	sandbox.CreatedAt = parseTime(created)
	if closed.Valid && closed.String != "" {
		t := parseTime(closed.String)
		sandbox.ClosedAt = &t
	}
	return sandbox, nil
}

func (s *SQLite) GetSandboxByExecution(ctx context.Context, executionID string) (domain.SandboxRecord, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id,workspace_id,execution_id,kind,backend,backend_version,backend_image,backend_image_digest,path,base_commit,parent_sandbox_id,parent_execution_id,parent_sandbox_ids,parent_execution_ids,baseline_path,baseline_change_set_ids,created_at,closed_at FROM sandboxes WHERE execution_id=? ORDER BY created_at DESC LIMIT 1`, executionID)
	var sandbox domain.SandboxRecord
	var created, parentSandboxIDs, parentExecutionIDs, baselineChangeSetIDs string
	var closed sql.NullString
	if err := row.Scan(&sandbox.ID, &sandbox.WorkspaceID, &sandbox.ExecutionID, &sandbox.Kind,
		&sandbox.Backend, &sandbox.BackendVersion, &sandbox.BackendImage, &sandbox.BackendImageDigest, &sandbox.Path,
		&sandbox.BaseCommit, &sandbox.ParentSandboxID, &sandbox.ParentExecutionID, &parentSandboxIDs, &parentExecutionIDs,
		&sandbox.BaselinePath, &baselineChangeSetIDs, &created, &closed); err != nil {
		return domain.SandboxRecord{}, err
	}
	unmarshalJSON(parentSandboxIDs, &sandbox.ParentSandboxIDs)
	unmarshalJSON(parentExecutionIDs, &sandbox.ParentExecutionIDs)
	unmarshalJSON(baselineChangeSetIDs, &sandbox.BaselineChangeSetIDs)
	sandbox.CreatedAt = parseTime(created)
	if closed.Valid && closed.String != "" {
		t := parseTime(closed.String)
		sandbox.ClosedAt = &t
	}
	return sandbox, nil
}

func (s *SQLite) SaveChangeSet(ctx context.Context, set domain.ChangeSet) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err = saveChangeSetTx(ctx, tx, set); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// SaveParallelMergeChangeSet atomically publishes the synthetic merge set and
// closes every branch-local set it replaces. This prevents a crash from
// leaving both the aggregate and its source branches independently applicable.
func (s *SQLite) SaveParallelMergeChangeSet(ctx context.Context, merge domain.ChangeSet, sources []domain.ChangeSet) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err = saveChangeSetTx(ctx, tx, merge); err != nil {
		_ = tx.Rollback()
		return err
	}
	for _, source := range sources {
		if err = saveChangeSetTx(ctx, tx, source); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// SaveParallelMergeExecution commits the pending execution, its merged
// sandbox, the aggregate Change Set and every superseded branch set in one
// SQLite transaction. The filesystem is prepared first and removed by the
// caller if this durable commit fails.
func (s *SQLite) SaveParallelMergeExecution(ctx context.Context, exec domain.ExecutionInstance, sandbox domain.SandboxRecord, merge *domain.ChangeSet, sources []domain.ChangeSet) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err = saveSandboxWith(ctx, tx, sandbox); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err = saveExecutionWith(ctx, tx, exec); err != nil {
		_ = tx.Rollback()
		return err
	}
	if merge != nil {
		if err = saveChangeSetTx(ctx, tx, *merge); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	for _, source := range sources {
		if err = saveChangeSetTx(ctx, tx, source); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func saveChangeSetTx(ctx context.Context, tx *sql.Tx, set domain.ChangeSet) error {
	var applied any
	if set.AppliedAt != nil {
		applied = formatTime(*set.AppliedAt)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO change_sets(id,workspace_id,execution_id,quest_id,title,kind,status,resolutions,depends_on,supersedes,superseded_by,created_at,updated_at,applied_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET title=excluded.title, kind=excluded.kind, status=excluded.status, resolutions=excluded.resolutions,
  depends_on=excluded.depends_on, supersedes=excluded.supersedes, superseded_by=excluded.superseded_by,
  updated_at=excluded.updated_at, applied_at=excluded.applied_at`,
		set.ID, set.WorkspaceID, set.ExecutionID, set.QuestID, set.Title, set.Kind, set.Status, marshalJSON(set.Resolutions), marshalJSON(set.DependsOn),
		marshalJSON(set.Supersedes), set.SupersededBy,
		formatTime(set.CreatedAt), formatTime(set.UpdatedAt), applied); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM change_items WHERE change_set_id=?`, set.ID); err != nil {
		return err
	}
	for _, item := range set.Items {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO change_items(id,change_set_id,path,kind,original_hash,proposed_hash,diff,patch_id,original_content,proposed_content,applied_hash,applied_operation)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, item.ID, set.ID, item.Path, item.Kind, item.OriginalHash, item.ProposedHash, item.Diff, item.PatchID,
			item.OriginalContent, item.ProposedContent, item.AppliedHash, item.AppliedOperation); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLite) ListChangeSets(ctx context.Context, workspaceID string) ([]domain.ChangeSet, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id,workspace_id,execution_id,quest_id,title,kind,status,resolutions,depends_on,supersedes,superseded_by,created_at,updated_at,applied_at
FROM change_sets WHERE workspace_id=? ORDER BY updated_at DESC`, workspaceID)
	if err != nil {
		return nil, err
	}
	var result []domain.ChangeSet
	for rows.Next() {
		var set domain.ChangeSet
		var created, updated, resolutions, dependsOn, supersedes string
		var applied sql.NullString
		if err = rows.Scan(&set.ID, &set.WorkspaceID, &set.ExecutionID, &set.QuestID, &set.Title, &set.Kind, &set.Status,
			&resolutions, &dependsOn, &supersedes, &set.SupersededBy, &created, &updated, &applied); err != nil {
			_ = rows.Close()
			return nil, err
		}
		unmarshalJSON(resolutions, &set.Resolutions)
		unmarshalJSON(dependsOn, &set.DependsOn)
		unmarshalJSON(supersedes, &set.Supersedes)
		set.CreatedAt, set.UpdatedAt = parseTime(created), parseTime(updated)
		if applied.Valid && applied.String != "" {
			t := parseTime(applied.String)
			set.AppliedAt = &t
		}
		result = append(result, set)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	// Load items after closing the parent cursor — SQLite is limited to one open query
	// with MaxOpenConns(1), so nested queries while rows are open deadlock.
	for index := range result {
		items, itemErr := s.listChangeItems(ctx, result[index].ID)
		if itemErr != nil {
			return nil, itemErr
		}
		result[index].Items = items
	}
	return result, nil
}

func (s *SQLite) GetChangeSet(ctx context.Context, id string) (domain.ChangeSet, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id,workspace_id,execution_id,quest_id,title,kind,status,resolutions,depends_on,supersedes,superseded_by,created_at,updated_at,applied_at FROM change_sets WHERE id=?`, id)
	var set domain.ChangeSet
	var created, updated, resolutions, dependsOn, supersedes string
	var applied sql.NullString
	if err := row.Scan(&set.ID, &set.WorkspaceID, &set.ExecutionID, &set.QuestID, &set.Title, &set.Kind, &set.Status,
		&resolutions, &dependsOn, &supersedes, &set.SupersededBy, &created, &updated, &applied); err != nil {
		return domain.ChangeSet{}, err
	}
	unmarshalJSON(resolutions, &set.Resolutions)
	unmarshalJSON(dependsOn, &set.DependsOn)
	unmarshalJSON(supersedes, &set.Supersedes)
	set.CreatedAt, set.UpdatedAt = parseTime(created), parseTime(updated)
	if applied.Valid && applied.String != "" {
		t := parseTime(applied.String)
		set.AppliedAt = &t
	}
	items, err := s.listChangeItems(ctx, set.ID)
	if err != nil {
		return domain.ChangeSet{}, err
	}
	set.Items = items
	return set, nil
}

func (s *SQLite) listChangeItems(ctx context.Context, changeSetID string) ([]domain.ChangeItem, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,path,kind,original_hash,proposed_hash,diff,patch_id,original_content,proposed_content,applied_hash,applied_operation FROM change_items WHERE change_set_id=?`, changeSetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.ChangeItem
	for rows.Next() {
		var item domain.ChangeItem
		if err = rows.Scan(&item.ID, &item.Path, &item.Kind, &item.OriginalHash, &item.ProposedHash, &item.Diff, &item.PatchID,
			&item.OriginalContent, &item.ProposedContent, &item.AppliedHash, &item.AppliedOperation); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
