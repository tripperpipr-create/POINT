package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"local-agent-workbench/internal/domain"
)

type SQLite struct {
	db *sql.DB
	// shared: базу делят ядра нескольких проектов. Восстановление после
	// остановки тогда делает каждое ядро для своего мира (RecoverAbandonedWork):
	// общий проход при открытии ставил на паузу работу, живую в соседнем ядре.
	shared bool
}

// Open открывает базу одного процесса и сразу восстанавливает всё, что
// осталось от прежнего: так работают инструменты и проверки с собственной базой.
func Open(path string) (*SQLite, error) { return open(path, false) }

// OpenShared открывает базу, которую делят ядра проектов. Общего
// восстановления нет: ядро восстанавливает свой мир через RecoverAbandonedWork.
func OpenShared(path string) (*SQLite, error) { return open(path, true) }

func open(path string, shared bool) (*SQLite, error) {
	dsn := path
	if !strings.Contains(path, "?") {
		// Fail locked opens instead of hanging forever when another Point/core holds the DB.
		dsn = path + "?_pragma=busy_timeout(5000)"
	}
	db := openQuietSQLite(dsn)
	db.SetMaxOpenConns(1)
	store := &SQLite{db: db, shared: shared}
	var err error
	if _, err = db.Exec(`PRAGMA busy_timeout=5000`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite busy_timeout: %w", err)
	}
	if err = store.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	if shared {
		return store, nil
	}
	if err = store.MarkInterrupted(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err = store.RecoverMasterLearning(context.Background()); err != nil {
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
	var err error
	if !s.shared {
		err = s.PurgeTemporaryMasterConversations(context.Background())
	}
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

func (s *SQLite) SaveSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

func (s *SQLite) Setting(ctx context.Context, key string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, key).Scan(&value)
	return value, err
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
