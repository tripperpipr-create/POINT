package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/egress"
)

type SQLite struct{ db *sql.DB }

func Open(path string) (*SQLite, error) {
	dsn := path
	if !strings.Contains(path, "?") {
		// Fail locked opens instead of hanging forever when another Point/core holds the DB.
		dsn = path + "?_pragma=busy_timeout(5000)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	store := &SQLite{db: db}
	if _, err = db.Exec(`PRAGMA busy_timeout=5000`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite busy_timeout: %w", err)
	}
	if err = store.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err = store.MarkInterrupted(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err = store.InterruptMasterTurns(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err = store.PurgeTemporaryMasterConversations(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *SQLite) Close() error {
	err := s.PurgeTemporaryMasterConversations(context.Background())
	closeErr := s.db.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func (s *SQLite) migrate(ctx context.Context) error {
	const ddl = `
PRAGMA journal_mode=WAL;
PRAGMA foreign_keys=ON;
CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS profiles (
  id TEXT PRIMARY KEY, name TEXT NOT NULL, role_description TEXT NOT NULL, system_prompt TEXT NOT NULL,
  goals TEXT NOT NULL DEFAULT '[]', rules TEXT NOT NULL DEFAULT '[]', provider TEXT NOT NULL, provider_preset TEXT NOT NULL DEFAULT '', base_url TEXT NOT NULL, model TEXT NOT NULL,
  temperature REAL NOT NULL DEFAULT 0.2, max_output_tokens INTEGER NOT NULL DEFAULT 4096, context_window_tokens INTEGER NOT NULL DEFAULT 32768, reasoning_effort TEXT NOT NULL DEFAULT 'medium', allowed_tools TEXT NOT NULL,
  max_steps INTEGER NOT NULL, max_duration_seconds INTEGER NOT NULL, approval_mode TEXT NOT NULL,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS custom_tools (
  id TEXT PRIMARY KEY, kind TEXT NOT NULL, display_name TEXT NOT NULL, description TEXT NOT NULL,
  command TEXT NOT NULL, cwd TEXT NOT NULL, timeout_seconds INTEGER NOT NULL,
  configuration TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS workflows (
  id TEXT PRIMARY KEY, name TEXT NOT NULL, description TEXT NOT NULL, steps TEXT NOT NULL,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS workspaces (
  id TEXT PRIMARY KEY, path TEXT NOT NULL UNIQUE, name TEXT NOT NULL, opened_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS runs (
  id TEXT PRIMARY KEY, agent_id TEXT NOT NULL, profile_id TEXT NOT NULL, workspace_id TEXT NOT NULL,
  task TEXT NOT NULL, context_items TEXT NOT NULL DEFAULT '[]', configuration_snapshot TEXT NOT NULL DEFAULT '{}', provider TEXT NOT NULL, model TEXT NOT NULL, status TEXT NOT NULL, step INTEGER NOT NULL,
  request_count INTEGER NOT NULL, tools_used TEXT NOT NULL, changed_files TEXT NOT NULL, error TEXT NOT NULL,
  result TEXT NOT NULL, started_at TEXT NOT NULL, finished_at TEXT, duration_ms INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS workflow_runs (
  id TEXT PRIMARY KEY, workflow_id TEXT NOT NULL, workspace_id TEXT NOT NULL, task TEXT NOT NULL,
  context_items TEXT NOT NULL, snapshot TEXT NOT NULL, status TEXT NOT NULL, current_step INTEGER NOT NULL,
  step_runs TEXT NOT NULL, error TEXT NOT NULL, result TEXT NOT NULL, started_at TEXT NOT NULL,
  finished_at TEXT, duration_ms INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS events (
  sequence INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE, run_id TEXT NOT NULL, agent_id TEXT NOT NULL,
  execution_id TEXT NOT NULL DEFAULT '', quest_id TEXT NOT NULL DEFAULT '', flow_run_id TEXT NOT NULL DEFAULT '', flow_node_id TEXT NOT NULL DEFAULT '',
  type TEXT NOT NULL, step INTEGER NOT NULL, actor TEXT NOT NULL, data TEXT NOT NULL, created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS events_run_sequence ON events(run_id, sequence);
CREATE TRIGGER IF NOT EXISTS events_no_update BEFORE UPDATE ON events BEGIN SELECT RAISE(ABORT, 'events are immutable'); END;
CREATE TRIGGER IF NOT EXISTS events_no_delete BEFORE DELETE ON events BEGIN SELECT RAISE(ABORT, 'events are immutable'); END;
CREATE TABLE IF NOT EXISTS approvals (
  id TEXT PRIMARY KEY, run_id TEXT NOT NULL, agent_id TEXT NOT NULL, tool_name TEXT NOT NULL, reason TEXT NOT NULL,
  arguments TEXT NOT NULL, status TEXT NOT NULL, created_at TEXT NOT NULL, resolved_at TEXT
);
CREATE TABLE IF NOT EXISTS patches (
  id TEXT PRIMARY KEY, run_id TEXT NOT NULL, approval_id TEXT NOT NULL, source_tool TEXT NOT NULL DEFAULT 'propose_patch', path TEXT NOT NULL,
  original_hash TEXT NOT NULL, original_existed INTEGER NOT NULL DEFAULT 1, original TEXT NOT NULL, proposed TEXT NOT NULL, diff TEXT NOT NULL,
  status TEXT NOT NULL, created_at TEXT NOT NULL
);`
	if _, err := s.db.ExecContext(ctx, ddl); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "runs", "context_items", `ALTER TABLE runs ADD COLUMN context_items TEXT NOT NULL DEFAULT '[]'`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "runs", "configuration_snapshot", `ALTER TABLE runs ADD COLUMN configuration_snapshot TEXT NOT NULL DEFAULT '{}'`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "custom_tools", "configuration", `ALTER TABLE custom_tools ADD COLUMN configuration TEXT NOT NULL DEFAULT '{}'`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "patches", "source_tool", `ALTER TABLE patches ADD COLUMN source_tool TEXT NOT NULL DEFAULT 'propose_patch'`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "profiles", "goals", `ALTER TABLE profiles ADD COLUMN goals TEXT NOT NULL DEFAULT '[]'`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "profiles", "rules", `ALTER TABLE profiles ADD COLUMN rules TEXT NOT NULL DEFAULT '[]'`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "profiles", "provider_preset", `ALTER TABLE profiles ADD COLUMN provider_preset TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "profiles", "temperature", `ALTER TABLE profiles ADD COLUMN temperature REAL NOT NULL DEFAULT 0.2`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "profiles", "max_output_tokens", `ALTER TABLE profiles ADD COLUMN max_output_tokens INTEGER NOT NULL DEFAULT 4096`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "profiles", "context_window_tokens", `ALTER TABLE profiles ADD COLUMN context_window_tokens INTEGER NOT NULL DEFAULT 32768`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "profiles", "reasoning_effort", `ALTER TABLE profiles ADD COLUMN reasoning_effort TEXT NOT NULL DEFAULT 'medium'`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "patches", "original_existed", `ALTER TABLE patches ADD COLUMN original_existed INTEGER NOT NULL DEFAULT 1`); err != nil {
		return err
	}
	for _, column := range []struct {
		name string
		sql  string
	}{
		{"execution_id", `ALTER TABLE events ADD COLUMN execution_id TEXT NOT NULL DEFAULT ''`},
		{"quest_id", `ALTER TABLE events ADD COLUMN quest_id TEXT NOT NULL DEFAULT ''`},
		{"flow_run_id", `ALTER TABLE events ADD COLUMN flow_run_id TEXT NOT NULL DEFAULT ''`},
		{"flow_node_id", `ALTER TABLE events ADD COLUMN flow_node_id TEXT NOT NULL DEFAULT ''`},
	} {
		if err := s.ensureColumn(ctx, "events", column.name, column.sql); err != nil {
			return err
		}
	}
	if _, err := s.db.ExecContext(ctx, `
CREATE INDEX IF NOT EXISTS events_execution_sequence ON events(execution_id, sequence);
CREATE INDEX IF NOT EXISTS events_quest_sequence ON events(quest_id, sequence);
CREATE INDEX IF NOT EXISTS events_flow_node_sequence ON events(flow_run_id, flow_node_id, sequence);`); err != nil {
		return err
	}
	return s.runVersionedMigrations(ctx)
}

func (s *SQLite) ensureColumn(ctx context.Context, tableName, columnName, statement string) error {
	if tableName != "runs" && tableName != "custom_tools" && tableName != "profiles" && tableName != "patches" && tableName != "events" {
		return errors.New("unsupported migration table")
	}
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(`+tableName+`)`)
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err = rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return err
		}
		if name == columnName {
			found = true
		}
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err = rows.Close(); err != nil || found {
		return err
	}
	_, err = s.db.ExecContext(ctx, statement)
	return err
}

func (s *SQLite) MarkInterrupted(ctx context.Context) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	// Leave paused runs with a resumable checkpoint alone so Resume can
	// continue after restart. Running/waiting with a safe checkpoint become
	// paused (not irreversible interrupted). In-flight mutations stay
	// unknown_outcome via journal + non-resumable checkpoint.
	if _, err = tx.ExecContext(ctx, `UPDATE runs SET status=?, error='', finished_at=NULL
WHERE status IN (?, ?) AND id IN (
  SELECT c.run_id FROM run_checkpoints c
  INNER JOIN (
    SELECT run_id, MAX(seq) AS seq FROM run_checkpoints GROUP BY run_id
  ) latest ON latest.run_id=c.run_id AND latest.seq=c.seq
  WHERE c.in_flight_call_id=''
)`, domain.RunPaused, domain.RunRunning, domain.RunWaiting); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE runs SET status=?, error=CASE WHEN error='' THEN 'Application stopped before the run finished' ELSE error END, finished_at=?, duration_ms=CAST((julianday(?) - julianday(started_at))*86400000 AS INTEGER) WHERE status IN (?, ?)`, domain.RunInterrupted, now, now, domain.RunRunning, domain.RunWaiting); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE runs SET status=?, error=CASE WHEN error='' THEN 'Application stopped before the run finished' ELSE error END, finished_at=?, duration_ms=CAST((julianday(?) - julianday(started_at))*86400000 AS INTEGER)
WHERE status=? AND id NOT IN (
  SELECT c.run_id FROM run_checkpoints c
  INNER JOIN (
    SELECT run_id, MAX(seq) AS seq FROM run_checkpoints GROUP BY run_id
  ) latest ON latest.run_id=c.run_id AND latest.seq=c.seq
  WHERE c.in_flight_call_id=''
)`, domain.RunInterrupted, now, now, domain.RunPaused); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE workflow_runs SET status=?, error=CASE WHEN error='' THEN 'Application stopped before the workflow finished' ELSE error END, finished_at=?, duration_ms=CAST((julianday(?) - julianday(started_at))*86400000 AS INTEGER) WHERE status IN (?, ?, ?)`, domain.RunInterrupted, now, now, domain.RunRunning, domain.RunWaiting, domain.RunPaused); err != nil {
		_ = tx.Rollback()
		return err
	}
	// A Flow is resumable when at least one of its executions points at the
	// latest safe checkpoint. Preserve the parent state together with that
	// execution; otherwise the child can be resumed but the scheduler has
	// already irreversibly abandoned its graph.
	if _, err = tx.ExecContext(ctx, `UPDATE flow_runs SET status=?, error='', finished_at=NULL
WHERE status IN (?, ?, ?) AND id IN (
  SELECT DISTINCT execution.flow_run_id FROM executions execution
  INNER JOIN run_checkpoints checkpoint ON checkpoint.run_id=execution.run_id
  INNER JOIN (
    SELECT run_id, MAX(seq) AS seq FROM run_checkpoints GROUP BY run_id
  ) latest ON latest.run_id=checkpoint.run_id AND latest.seq=checkpoint.seq
  WHERE execution.flow_run_id<>'' AND checkpoint.in_flight_call_id=''
)`, domain.RunPaused, domain.RunRunning, domain.RunWaiting, domain.RunPaused); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE flow_runs SET status=?, error=CASE WHEN error='' THEN 'Application stopped before the flow finished' ELSE error END, finished_at=?, duration_ms=CAST((julianday(?) - julianday(started_at))*86400000 AS INTEGER)
WHERE status IN (?, ?, ?) AND id NOT IN (
  SELECT DISTINCT execution.flow_run_id FROM executions execution
  INNER JOIN run_checkpoints checkpoint ON checkpoint.run_id=execution.run_id
  INNER JOIN (
    SELECT run_id, MAX(seq) AS seq FROM run_checkpoints GROUP BY run_id
  ) latest ON latest.run_id=checkpoint.run_id AND latest.seq=checkpoint.seq
  WHERE execution.flow_run_id<>'' AND checkpoint.in_flight_call_id=''
)`, domain.RunInterrupted, now, now, domain.RunRunning, domain.RunWaiting, domain.RunPaused); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE executions SET status=?, error='', finished_at=NULL
WHERE status IN (?, ?, ?) AND run_id IN (
  SELECT c.run_id FROM run_checkpoints c
  INNER JOIN (
    SELECT run_id, MAX(seq) AS seq FROM run_checkpoints GROUP BY run_id
  ) latest ON latest.run_id=c.run_id AND latest.seq=c.seq
  WHERE c.in_flight_call_id=''
)`, domain.RunPaused, domain.RunRunning, domain.RunWaiting, domain.RunPaused); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE executions SET status=?, error=CASE WHEN error='' THEN 'Application stopped before the execution finished' ELSE error END, finished_at=?, duration_ms=CAST((julianday(?) - julianday(started_at))*86400000 AS INTEGER)
WHERE status IN (?, ?, ?) AND (run_id='' OR run_id NOT IN (
  SELECT c.run_id FROM run_checkpoints c
  INNER JOIN (
    SELECT run_id, MAX(seq) AS seq FROM run_checkpoints GROUP BY run_id
  ) latest ON latest.run_id=c.run_id AND latest.seq=c.seq
  WHERE c.in_flight_call_id=''
))`, domain.RunInterrupted, now, now, domain.RunRunning, domain.RunWaiting, domain.RunPaused); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE approvals SET status=?, resolved_at=? WHERE status=?`, domain.ApprovalDenied, now, domain.ApprovalPending); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE patches SET status='rejected' WHERE status='pending'`); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (s *SQLite) SaveSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

func (s *SQLite) Setting(ctx context.Context, key string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, key).Scan(&value)
	return value, err
}

func (s *SQLite) SaveProfile(ctx context.Context, p domain.AgentProfile) error {
	tools, _ := json.Marshal(p.AllowedTools)
	goals, _ := json.Marshal(p.Goals)
	rules, _ := json.Marshal(p.Rules)
	_, err := s.db.ExecContext(ctx, `INSERT INTO profiles(id,name,role_description,system_prompt,goals,rules,connection_id,provider,provider_preset,base_url,api_version,model,temperature,max_output_tokens,context_window_tokens,reasoning_effort,allowed_tools,max_steps,max_duration_seconds,approval_mode,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,role_description=excluded.role_description,system_prompt=excluded.system_prompt,goals=excluded.goals,rules=excluded.rules,connection_id=excluded.connection_id,provider=excluded.provider,provider_preset=excluded.provider_preset,base_url=excluded.base_url,api_version=excluded.api_version,model=excluded.model,temperature=excluded.temperature,max_output_tokens=excluded.max_output_tokens,context_window_tokens=excluded.context_window_tokens,reasoning_effort=excluded.reasoning_effort,allowed_tools=excluded.allowed_tools,max_steps=excluded.max_steps,max_duration_seconds=excluded.max_duration_seconds,approval_mode=excluded.approval_mode,updated_at=excluded.updated_at`,
		p.ID, p.Name, p.RoleDescription, p.SystemPrompt, string(goals), string(rules), p.ConnectionID, p.Provider, p.ProviderPreset, p.BaseURL, p.APIVersion, p.Model, p.Temperature, p.MaxOutputTokens, p.ContextWindowTokens, p.ReasoningEffort, string(tools), p.MaxSteps, p.MaxDurationSeconds, p.ApprovalMode, formatTime(p.CreatedAt), formatTime(p.UpdatedAt))
	return err
}

func (s *SQLite) ListProfiles(ctx context.Context) ([]domain.AgentProfile, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,role_description,system_prompt,goals,rules,connection_id,provider,provider_preset,base_url,api_version,model,temperature,max_output_tokens,context_window_tokens,reasoning_effort,allowed_tools,max_steps,max_duration_seconds,approval_mode,created_at,updated_at FROM profiles ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.AgentProfile
	for rows.Next() {
		var p domain.AgentProfile
		var tools, goals, rules, created, updated string
		if err = rows.Scan(&p.ID, &p.Name, &p.RoleDescription, &p.SystemPrompt, &goals, &rules, &p.ConnectionID, &p.Provider, &p.ProviderPreset, &p.BaseURL, &p.APIVersion, &p.Model, &p.Temperature, &p.MaxOutputTokens, &p.ContextWindowTokens, &p.ReasoningEffort, &tools, &p.MaxSteps, &p.MaxDurationSeconds, &p.ApprovalMode, &created, &updated); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(tools), &p.AllowedTools)
		_ = json.Unmarshal([]byte(goals), &p.Goals)
		_ = json.Unmarshal([]byte(rules), &p.Rules)
		p.CreatedAt = parseTime(created)
		p.UpdatedAt = parseTime(updated)
		result = append(result, p)
	}
	return result, rows.Err()
}

func (s *SQLite) DeleteProfile(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM profiles WHERE id=?`, id)
	return err
}

func (s *SQLite) SaveCustomTool(ctx context.Context, tool domain.CustomTool) error {
	configuration, _ := json.Marshal(struct {
		Program              string                       `json:"program,omitempty"`
		Arguments            []string                     `json:"arguments,omitempty"`
		Parameters           []domain.CustomToolParameter `json:"parameters,omitempty"`
		ProvidesVerification bool                         `json:"providesVerification,omitempty"`
		Revision             int                          `json:"revision,omitempty"`
		TrustedRuns          int                          `json:"trustedRuns,omitempty"`
	}{Program: tool.Program, Arguments: tool.Arguments, Parameters: tool.Parameters, ProvidesVerification: tool.ProvidesVerification, Revision: tool.Revision, TrustedRuns: tool.TrustedRuns})
	_, err := s.db.ExecContext(ctx, `INSERT INTO custom_tools(id,kind,display_name,description,command,cwd,timeout_seconds,configuration,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET kind=excluded.kind,display_name=excluded.display_name,description=excluded.description,command=excluded.command,cwd=excluded.cwd,timeout_seconds=excluded.timeout_seconds,configuration=excluded.configuration,updated_at=excluded.updated_at`,
		tool.ID, tool.Kind, tool.DisplayName, tool.Description, tool.Command, tool.CWD, tool.TimeoutSeconds, string(configuration), formatTime(tool.CreatedAt), formatTime(tool.UpdatedAt))
	return err
}

func (s *SQLite) ListCustomTools(ctx context.Context) ([]domain.CustomTool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,kind,display_name,description,command,cwd,timeout_seconds,configuration,created_at,updated_at FROM custom_tools ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.CustomTool
	for rows.Next() {
		var tool domain.CustomTool
		var configuration, created, updated string
		if err = rows.Scan(&tool.ID, &tool.Kind, &tool.DisplayName, &tool.Description, &tool.Command, &tool.CWD, &tool.TimeoutSeconds, &configuration, &created, &updated); err != nil {
			return nil, err
		}
		var config struct {
			Program              string                       `json:"program,omitempty"`
			Arguments            []string                     `json:"arguments,omitempty"`
			Parameters           []domain.CustomToolParameter `json:"parameters,omitempty"`
			ProvidesVerification bool                         `json:"providesVerification,omitempty"`
			Revision             int                          `json:"revision,omitempty"`
			TrustedRuns          int                          `json:"trustedRuns,omitempty"`
		}
		_ = json.Unmarshal([]byte(configuration), &config)
		tool.Program, tool.Arguments, tool.Parameters, tool.ProvidesVerification = config.Program, config.Arguments, config.Parameters, config.ProvidesVerification
		tool.Revision, tool.TrustedRuns = config.Revision, config.TrustedRuns
		tool.CreatedAt, tool.UpdatedAt = parseTime(created), parseTime(updated)
		result = append(result, tool)
	}
	return result, rows.Err()
}

func (s *SQLite) DeleteCustomTool(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM custom_tools WHERE id=?`, id)
	return err
}

func (s *SQLite) SaveWorkflow(ctx context.Context, workflow domain.AgentWorkflow) error {
	steps, _ := json.Marshal(workflow.Steps)
	_, err := s.db.ExecContext(ctx, `INSERT INTO workflows(id,name,description,steps,created_at,updated_at)
VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,description=excluded.description,steps=excluded.steps,updated_at=excluded.updated_at`,
		workflow.ID, workflow.Name, workflow.Description, string(steps), formatTime(workflow.CreatedAt), formatTime(workflow.UpdatedAt))
	return err
}

func (s *SQLite) ListWorkflows(ctx context.Context) ([]domain.AgentWorkflow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,description,steps,created_at,updated_at FROM workflows ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.AgentWorkflow
	for rows.Next() {
		var workflow domain.AgentWorkflow
		var steps, created, updated string
		if err = rows.Scan(&workflow.ID, &workflow.Name, &workflow.Description, &steps, &created, &updated); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(steps), &workflow.Steps)
		workflow.CreatedAt, workflow.UpdatedAt = parseTime(created), parseTime(updated)
		result = append(result, workflow)
	}
	return result, rows.Err()
}

func (s *SQLite) DeleteWorkflow(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM workflows WHERE id=?`, id)
	return err
}

func (s *SQLite) SaveWorkflowRun(ctx context.Context, run domain.WorkflowRun) error {
	contextItems, _ := json.Marshal(run.ContextItems)
	snapshot, _ := json.Marshal(run.Snapshot)
	stepRuns, _ := json.Marshal(run.StepRuns)
	var finished any
	if run.FinishedAt != nil {
		finished = formatTime(*run.FinishedAt)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO workflow_runs(id,workflow_id,workspace_id,task,context_items,snapshot,status,current_step,step_runs,error,result,started_at,finished_at,duration_ms)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET status=excluded.status,current_step=excluded.current_step,step_runs=excluded.step_runs,error=excluded.error,result=excluded.result,finished_at=excluded.finished_at,duration_ms=excluded.duration_ms`,
		run.ID, run.WorkflowID, run.WorkspaceID, run.Task, string(contextItems), string(snapshot), run.Status, run.CurrentStep, string(stepRuns), run.Error, run.Result, formatTime(run.StartedAt), finished, run.DurationMs)
	return err
}

func (s *SQLite) ListWorkflowRunsForWorkspace(ctx context.Context, workspaceID string, limit int) ([]domain.WorkflowRun, error) {
	if strings.TrimSpace(workspaceID) == "" {
		return []domain.WorkflowRun{}, nil
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,workflow_id,workspace_id,task,context_items,snapshot,status,current_step,step_runs,error,result,started_at,finished_at,duration_ms FROM workflow_runs WHERE workspace_id=? ORDER BY started_at DESC LIMIT ?`, workspaceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.WorkflowRun
	for rows.Next() {
		run, scanErr := scanWorkflowRun(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, run)
	}
	if result == nil {
		result = []domain.WorkflowRun{}
	}
	return result, rows.Err()
}

func (s *SQLite) GetWorkflowRun(ctx context.Context, id string) (domain.WorkflowRun, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id,workflow_id,workspace_id,task,context_items,snapshot,status,current_step,step_runs,error,result,started_at,finished_at,duration_ms FROM workflow_runs WHERE id=?`, id)
	return scanWorkflowRun(row)
}

func scanWorkflowRun(row scanner) (domain.WorkflowRun, error) {
	var run domain.WorkflowRun
	var contextItems, snapshot, stepRuns, started string
	var finished sql.NullString
	err := row.Scan(&run.ID, &run.WorkflowID, &run.WorkspaceID, &run.Task, &contextItems, &snapshot, &run.Status, &run.CurrentStep, &stepRuns, &run.Error, &run.Result, &started, &finished, &run.DurationMs)
	if err != nil {
		return run, err
	}
	_ = json.Unmarshal([]byte(contextItems), &run.ContextItems)
	_ = json.Unmarshal([]byte(snapshot), &run.Snapshot)
	_ = json.Unmarshal([]byte(stepRuns), &run.StepRuns)
	run.StartedAt = parseTime(started)
	if finished.Valid {
		value := parseTime(finished.String)
		run.FinishedAt = &value
	}
	return run, nil
}

func (s *SQLite) SaveWorkspace(ctx context.Context, w domain.Workspace) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO workspaces(id,path,name,opened_at) VALUES(?,?,?,?) ON CONFLICT(path) DO UPDATE SET name=excluded.name,opened_at=excluded.opened_at`, w.ID, w.Path, w.Name, formatTime(w.OpenedAt))
	return err
}

func (s *SQLite) WorkspaceByPath(ctx context.Context, path string) (domain.Workspace, error) {
	var w domain.Workspace
	var opened string
	err := s.db.QueryRowContext(ctx, `SELECT id,path,name,opened_at FROM workspaces WHERE path=?`, path).Scan(&w.ID, &w.Path, &w.Name, &opened)
	w.OpenedAt = parseTime(opened)
	return w, err
}

func (s *SQLite) SaveRun(ctx context.Context, r domain.Run) error {
	return saveRunWith(ctx, s.db, r)
}

func (s *SQLite) ListRunsForWorkspace(ctx context.Context, workspaceID string, limit int) ([]domain.Run, error) {
	if strings.TrimSpace(workspaceID) == "" {
		return []domain.Run{}, nil
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,agent_id,profile_id,workspace_id,task,context_items,configuration_snapshot,provider,model,status,step,request_count,tools_used,changed_files,error,result,started_at,finished_at,duration_ms,controller_json FROM runs WHERE workspace_id=? ORDER BY started_at DESC LIMIT ?`, workspaceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.Run
	for rows.Next() {
		r, scanErr := scanRun(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, r)
	}
	if result == nil {
		result = []domain.Run{}
	}
	return result, rows.Err()
}

func (s *SQLite) GetRun(ctx context.Context, id string) (domain.Run, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id,agent_id,profile_id,workspace_id,task,context_items,configuration_snapshot,provider,model,status,step,request_count,tools_used,changed_files,error,result,started_at,finished_at,duration_ms,controller_json FROM runs WHERE id=?`, id)
	return scanRun(row)
}

type scanner interface{ Scan(...any) error }

func scanRun(row scanner) (domain.Run, error) {
	var r domain.Run
	var contextItems, configurationSnapshot, tools, files, started, controller string
	var finished sql.NullString
	err := row.Scan(&r.ID, &r.AgentID, &r.ProfileID, &r.WorkspaceID, &r.Task, &contextItems, &configurationSnapshot, &r.Provider, &r.Model, &r.Status, &r.Step, &r.RequestCount, &tools, &files, &r.Error, &r.Result, &started, &finished, &r.DurationMs, &controller)
	if err != nil {
		return r, err
	}
	_ = json.Unmarshal([]byte(contextItems), &r.ContextItems)
	_ = json.Unmarshal([]byte(configurationSnapshot), &r.ConfigurationSnapshot)
	_ = json.Unmarshal([]byte(tools), &r.ToolsUsed)
	_ = json.Unmarshal([]byte(files), &r.ChangedFiles)
	r.Controller = decodeControllerJSON(controller)
	r.StartedAt = parseTime(started)
	if r.ConfigurationSnapshot.SchemaVersion == 0 {
		r.ConfigurationSnapshot = domain.RunConfigurationSnapshot{
			Profile: domain.AgentProfile{ID: r.ProfileID, Provider: domain.ProviderKind(r.Provider), Model: r.Model},
		}
	}
	if finished.Valid {
		t := parseTime(finished.String)
		r.FinishedAt = &t
	}
	return r, nil
}

func (s *SQLite) Append(ctx context.Context, e domain.Event) error {
	if e.WorkspaceID == "" && e.RunID != "" {
		_ = s.db.QueryRowContext(ctx, `SELECT workspace_id FROM runs WHERE id=?`, e.RunID).Scan(&e.WorkspaceID)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO events(id,workspace_id,run_id,agent_id,execution_id,quest_id,flow_run_id,flow_node_id,type,step,actor,data,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		e.ID, e.WorkspaceID, e.RunID, e.AgentID, e.ExecutionID, e.QuestID, e.FlowRunID, e.FlowNodeID, e.Type, e.Step, e.Actor, string(e.Data), formatTime(e.CreatedAt))
	return err
}

func (s *SQLite) ListByRun(ctx context.Context, runID string) ([]domain.Event, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,workspace_id,run_id,agent_id,execution_id,quest_id,flow_run_id,flow_node_id,type,step,actor,data,created_at FROM events WHERE run_id=? ORDER BY sequence`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.Event
	for rows.Next() {
		var e domain.Event
		var data, created string
		if err = rows.Scan(&e.ID, &e.WorkspaceID, &e.RunID, &e.AgentID, &e.ExecutionID, &e.QuestID, &e.FlowRunID, &e.FlowNodeID, &e.Type, &e.Step, &e.Actor, &data, &created); err != nil {
			return nil, err
		}
		e.Data = json.RawMessage(data)
		e.CreatedAt = parseTime(created)
		result = append(result, e)
	}
	return result, rows.Err()
}

func (s *SQLite) SaveApproval(ctx context.Context, a domain.Approval) error {
	var resolved any
	if a.ResolvedAt != nil {
		resolved = formatTime(*a.ResolvedAt)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO approvals(id,run_id,agent_id,tool_name,reason,arguments,status,created_at,resolved_at) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET status=excluded.status,resolved_at=excluded.resolved_at`, a.ID, a.RunID, a.AgentID, a.ToolName, a.Reason, string(a.Arguments), a.Status, formatTime(a.CreatedAt), resolved)
	return err
}

func (s *SQLite) GetApproval(ctx context.Context, id string) (domain.Approval, error) {
	var approval domain.Approval
	var arguments, created string
	var resolved sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT id,run_id,agent_id,tool_name,reason,arguments,status,created_at,resolved_at FROM approvals WHERE id=?`, id).
		Scan(&approval.ID, &approval.RunID, &approval.AgentID, &approval.ToolName, &approval.Reason, &arguments, &approval.Status, &created, &resolved)
	if err != nil {
		return domain.Approval{}, err
	}
	approval.Arguments = json.RawMessage(arguments)
	approval.CreatedAt = parseTime(created)
	if resolved.Valid {
		value := parseTime(resolved.String)
		approval.ResolvedAt = &value
	}
	return approval, nil
}

func (s *SQLite) ApprovalsByRun(ctx context.Context, runID string) ([]domain.Approval, error) {
	return s.approvalsByRun(ctx, runID, false)
}

func (s *SQLite) approvalsByRun(ctx context.Context, runID string, pendingOnly bool) ([]domain.Approval, error) {
	query := `SELECT id,run_id,agent_id,tool_name,reason,arguments,status,created_at,resolved_at FROM approvals WHERE run_id=?`
	args := []any{runID}
	if pendingOnly {
		query += ` AND status=?`
		args = append(args, domain.ApprovalPending)
	}
	query += ` ORDER BY created_at`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.Approval
	for rows.Next() {
		var a domain.Approval
		var args, created string
		var resolved sql.NullString
		if err = rows.Scan(&a.ID, &a.RunID, &a.AgentID, &a.ToolName, &a.Reason, &args, &a.Status, &created, &resolved); err != nil {
			return nil, err
		}
		a.Arguments = json.RawMessage(args)
		a.CreatedAt = parseTime(created)
		if resolved.Valid {
			t := parseTime(resolved.String)
			a.ResolvedAt = &t
		}
		result = append(result, a)
	}
	return result, rows.Err()
}

func (s *SQLite) SavePatch(ctx context.Context, p domain.PatchProposal) error {
	if p.SourceTool == "" {
		p.SourceTool = "propose_patch"
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO patches(id,run_id,approval_id,source_tool,path,original_hash,original_existed,original,proposed,diff,status,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET approval_id=excluded.approval_id,source_tool=excluded.source_tool,status=excluded.status`, p.ID, p.RunID, p.ApprovalID, p.SourceTool, p.Path, p.OriginalHash, p.OriginalExisted, p.Original, p.Proposed, p.Diff, p.Status, formatTime(p.CreatedAt))
	return err
}

func (s *SQLite) PatchesByRun(ctx context.Context, runID string) ([]domain.PatchProposal, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,run_id,approval_id,source_tool,path,original_hash,original_existed,original,proposed,diff,status,created_at FROM patches WHERE run_id=? ORDER BY created_at`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.PatchProposal
	for rows.Next() {
		var p domain.PatchProposal
		var created string
		if err = rows.Scan(&p.ID, &p.RunID, &p.ApprovalID, &p.SourceTool, &p.Path, &p.OriginalHash, &p.OriginalExisted, &p.Original, &p.Proposed, &p.Diff, &p.Status, &created); err != nil {
			return nil, err
		}
		p.CreatedAt = parseTime(created)
		result = append(result, p)
	}
	return result, rows.Err()
}

func (s *SQLite) ListPatchesForWorkspace(ctx context.Context, workspaceID string, limit int) ([]domain.PatchProposal, error) {
	if strings.TrimSpace(workspaceID) == "" {
		return []domain.PatchProposal{}, nil
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `SELECT p.id,p.run_id,p.approval_id,p.source_tool,p.path,p.original_hash,p.original_existed,p.original,p.proposed,p.diff,p.status,p.created_at FROM patches p INNER JOIN runs r ON r.id = p.run_id WHERE r.workspace_id=? ORDER BY p.created_at DESC LIMIT ?`, workspaceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.PatchProposal
	for rows.Next() {
		var p domain.PatchProposal
		var created string
		if err = rows.Scan(&p.ID, &p.RunID, &p.ApprovalID, &p.SourceTool, &p.Path, &p.OriginalHash, &p.OriginalExisted, &p.Original, &p.Proposed, &p.Diff, &p.Status, &created); err != nil {
			return nil, err
		}
		p.CreatedAt = parseTime(created)
		result = append(result, p)
	}
	if result == nil {
		result = []domain.PatchProposal{}
	}
	return result, rows.Err()
}

func (s *SQLite) GetPatch(ctx context.Context, id string) (domain.PatchProposal, error) {
	var p domain.PatchProposal
	var created string
	err := s.db.QueryRowContext(ctx, `SELECT id,run_id,approval_id,source_tool,path,original_hash,original_existed,original,proposed,diff,status,created_at FROM patches WHERE id=?`, id).Scan(&p.ID, &p.RunID, &p.ApprovalID, &p.SourceTool, &p.Path, &p.OriginalHash, &p.OriginalExisted, &p.Original, &p.Proposed, &p.Diff, &p.Status, &created)
	p.CreatedAt = parseTime(created)
	return p, err
}

func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
func parseTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339Nano, value)
	return t
}

func IsNotFound(err error) bool { return errors.Is(err, sql.ErrNoRows) }

func ValidateProfile(p domain.AgentProfile, additionalTools ...string) error {
	if strings.TrimSpace(p.Name) == "" || strings.TrimSpace(p.Model) == "" {
		return errors.New("profile name and model are required")
	}
	if domain.IsAgentCLIProvider(p.Provider) {
		return errors.New("CLI providers are removed; use an HTTP API provider")
	}
	if !domain.IsHTTPAPIProvider(p.Provider) {
		return errors.New("unsupported provider")
	}
	parsed, err := url.Parse(p.BaseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return errors.New("base URL must be an absolute HTTP or HTTPS URL")
	}
	if parsed.User != nil {
		return errors.New("credentials in the provider URL are not allowed")
	}
	if p.ApprovalMode != domain.ApprovalSafe && p.ApprovalMode != domain.ApprovalAlways {
		return errors.New("unsupported approval mode")
	}
	// Что можно записать в профиль, решает каталог. Ручной список здесь уже
	// успел ослепнуть: git-инструменты и read_skill были в реестре, но профиль
	// с ними не сохранялся, и длинные скиллы не грузились вовсе.
	allowed := map[string]bool{}
	for _, item := range domain.BuiltInToolCatalog() {
		allowed[item.Name] = true
	}
	for _, tool := range additionalTools {
		allowed[tool] = true
	}
	seen := make(map[string]bool)
	for _, tool := range p.AllowedTools {
		if !allowed[tool] {
			return fmt.Errorf("unsupported tool %q", tool)
		}
		if seen[tool] {
			return fmt.Errorf("duplicate tool %q", tool)
		}
		seen[tool] = true
	}
	if len(p.SystemPrompt) > 64*1024 || len(p.RoleDescription) > 4*1024 || len(p.Name) > 200 {
		return errors.New("profile fields exceed their size limit")
	}
	if len(p.Goals) > 32 || len(p.Rules) > 64 {
		return errors.New("profile has too many goals or rules")
	}
	for _, value := range append(append([]string{}, p.Goals...), p.Rules...) {
		if strings.TrimSpace(value) == "" || len(value) > 2048 {
			return errors.New("goals and rules must be non-empty and under 2048 bytes each")
		}
	}
	if p.MaxSteps < 1 || p.MaxSteps > 100 {
		return errors.New("max steps must be between 1 and 100")
	}
	if p.MaxDurationSeconds < 1 || p.MaxDurationSeconds > 3600 {
		return errors.New("max duration must be between 1 and 3600 seconds")
	}
	if p.Temperature < 0 || p.Temperature > 2 {
		return errors.New("temperature must be between 0 and 2")
	}
	if p.MaxOutputTokens != 0 && (p.MaxOutputTokens < 1 || p.MaxOutputTokens > 131072) {
		return errors.New("max output tokens must be between 1 and 131072")
	}
	if p.ContextWindowTokens != 0 && (p.ContextWindowTokens < 4096 || p.ContextWindowTokens > 1048576) {
		return errors.New("context window tokens must be between 4096 and 1048576")
	}
	if p.ContextWindowTokens > 0 && p.MaxOutputTokens > 0 && p.ContextWindowTokens-p.MaxOutputTokens < 1024 {
		return errors.New("context window must reserve at least 1024 tokens for model input")
	}
	if p.ReasoningEffort != "" && p.ReasoningEffort != "none" && p.ReasoningEffort != "minimal" && p.ReasoningEffort != "low" && p.ReasoningEffort != "medium" && p.ReasoningEffort != "high" {
		return errors.New("unsupported reasoning effort")
	}
	if _, err := egress.CompileToolPolicies(p.ToolPolicies); err != nil {
		return fmt.Errorf("invalid controlled egress policy: %w", err)
	}
	return nil
}

var customToolID = regexp.MustCompile(`^customtool_[0-9a-f]{24}$`)
var customToolParameterName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
var customToolPlaceholder = regexp.MustCompile(`\{\{([a-z][a-z0-9_]{0,63})\}\}`)
var windowsDrivePath = regexp.MustCompile(`^[A-Za-z]:`)
var workflowID = regexp.MustCompile(`^workflow_[0-9a-f]{24}$`)
var workflowStepID = regexp.MustCompile(`^step_[0-9a-f]{24}$`)

func ValidateWorkflow(workflow domain.AgentWorkflow, profileIDs ...string) error {
	if !workflowID.MatchString(workflow.ID) {
		return errors.New("invalid workflow ID")
	}
	if strings.TrimSpace(workflow.Name) == "" || len(workflow.Name) > 200 || len(workflow.Description) > 4*1024 {
		return errors.New("workflow name is required and fields must stay within size limits")
	}
	if len(workflow.Steps) < 1 || len(workflow.Steps) > 12 {
		return errors.New("workflow must contain between 1 and 12 steps")
	}
	profiles := make(map[string]bool, len(profileIDs))
	for _, id := range profileIDs {
		profiles[id] = true
	}
	seen := make(map[string]bool, len(workflow.Steps))
	for index, step := range workflow.Steps {
		if !workflowStepID.MatchString(step.ID) {
			return fmt.Errorf("workflow step %d has an invalid ID", index+1)
		}
		if seen[step.ID] {
			return fmt.Errorf("duplicate workflow step ID %q", step.ID)
		}
		seen[step.ID] = true
		if strings.TrimSpace(step.Name) == "" || len(step.Name) > 200 || len(step.Instruction) > 16*1024 {
			return fmt.Errorf("workflow step %d fields exceed their limits", index+1)
		}
		kind := step.Kind
		if kind != "" && kind != "agent" && kind != "cursor" && kind != "manual" {
			return fmt.Errorf("workflow step %d has unsupported kind", index+1)
		}
		if step.OnFailure != "" && step.OnFailure != "stop" && step.OnFailure != "skip" {
			return fmt.Errorf("workflow step %d has unsupported onFailure", index+1)
		}
		if step.Condition != nil && step.Condition.Type != "always" && step.Condition.Type != "previous_status" {
			return fmt.Errorf("workflow step %d has unsupported condition", index+1)
		}
		if kind != "manual" && !profiles[step.ProfileID] {
			return fmt.Errorf("workflow step %d references an unknown profile", index+1)
		}
	}
	return nil
}

func ValidateCustomTool(tool domain.CustomTool) error {
	if !customToolID.MatchString(tool.ID) {
		return errors.New("invalid custom tool ID")
	}
	if tool.Kind != domain.CustomToolCommand && tool.Kind != domain.CustomToolProcess {
		return errors.New("unsupported custom tool kind")
	}
	if strings.TrimSpace(tool.DisplayName) == "" || strings.TrimSpace(tool.Description) == "" {
		return errors.New("tool name and description are required")
	}
	if len(tool.DisplayName) > 120 || len(tool.Description) > 4*1024 || len(tool.Command) > 32*1024 || len(tool.Program) > 4*1024 || len(tool.CWD) > 4*1024 {
		return errors.New("custom tool fields exceed their size limit")
	}
	if tool.TimeoutSeconds < 1 || tool.TimeoutSeconds > 600 {
		return errors.New("custom tool timeout must be between 1 and 600 seconds")
	}
	if tool.Kind == domain.CustomToolCommand {
		if strings.TrimSpace(tool.Command) == "" {
			return errors.New("fixed command is required")
		}
		return nil
	}
	if strings.TrimSpace(tool.Program) == "" || strings.ContainsAny(tool.Program, "\x00\r\n") {
		return errors.New("process program is required and must be one line")
	}
	if filepath.IsAbs(tool.Program) || strings.HasPrefix(tool.Program, "/") || windowsDrivePath.MatchString(tool.Program) || strings.HasPrefix(tool.Program, `\\`) {
		return errors.New("process program must be a PATH name or workspace-relative path")
	}
	if len(tool.Arguments) > 64 || len(tool.Parameters) > 16 {
		return errors.New("process tool exceeds 64 arguments or 16 parameters")
	}
	parameters := make(map[string]bool, len(tool.Parameters))
	for index, parameter := range tool.Parameters {
		if !customToolParameterName.MatchString(parameter.Name) || parameters[parameter.Name] {
			return fmt.Errorf("process parameter %d has an invalid or duplicate name", index+1)
		}
		parameters[parameter.Name] = true
		if strings.TrimSpace(parameter.DisplayName) == "" || strings.TrimSpace(parameter.Description) == "" || len(parameter.DisplayName) > 120 || len(parameter.Description) > 1024 {
			return fmt.Errorf("process parameter %q requires bounded display name and description", parameter.Name)
		}
		switch parameter.Type {
		case domain.CustomToolParameterString, domain.CustomToolParameterWorkspacePath:
			if parameter.MaxLength < 0 || parameter.MaxLength > 16*1024 {
				return fmt.Errorf("process parameter %q maxLength is invalid", parameter.Name)
			}
		case domain.CustomToolParameterInteger:
			if len(parameter.EnumValues) > 0 {
				return fmt.Errorf("integer parameter %q cannot have enum values", parameter.Name)
			}
		case domain.CustomToolParameterEnum:
			if len(parameter.EnumValues) < 1 || len(parameter.EnumValues) > 100 {
				return fmt.Errorf("enum parameter %q requires 1–100 values", parameter.Name)
			}
			seen := make(map[string]bool, len(parameter.EnumValues))
			for _, value := range parameter.EnumValues {
				if strings.TrimSpace(value) == "" || len(value) > 1024 || seen[value] {
					return fmt.Errorf("enum parameter %q has an invalid or duplicate value", parameter.Name)
				}
				seen[value] = true
			}
		default:
			return fmt.Errorf("process parameter %q has unsupported type", parameter.Name)
		}
	}
	referenced := make(map[string]bool, len(parameters))
	totalArgumentBytes := 0
	for index, argument := range tool.Arguments {
		totalArgumentBytes += len(argument)
		if len(argument) > 4*1024 || strings.IndexByte(argument, 0) >= 0 {
			return fmt.Errorf("process argument %d exceeds its limit", index+1)
		}
		matches := customToolPlaceholder.FindAllStringSubmatch(argument, -1)
		remaining := customToolPlaceholder.ReplaceAllString(argument, "")
		if strings.Contains(remaining, "{{") || strings.Contains(remaining, "}}") {
			return fmt.Errorf("process argument %d contains an invalid placeholder", index+1)
		}
		for _, match := range matches {
			if !parameters[match[1]] {
				return fmt.Errorf("process argument %d references unknown parameter %q", index+1, match[1])
			}
			referenced[match[1]] = true
		}
	}
	if totalArgumentBytes > 32*1024 {
		return errors.New("process arguments exceed 32 KiB")
	}
	for name := range parameters {
		if !referenced[name] {
			return fmt.Errorf("process parameter %q is not used by any argument", name)
		}
	}
	return nil
}
