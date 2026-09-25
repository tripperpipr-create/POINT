package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"local-agent-workbench/internal/domain"
)

// migrationMCPIntegrationsV1 — MCP-серверы владельца, снимки их инструментов,
// привязка папки к проекту GitLab и журнал действий во внешних сервисах.
//
// Карты и списки конфигурации хранятся JSON-строками: по ним не ищут, их
// читают целиком вместе с сервером. Значений секретов здесь нет — только
// имена и ссылки secretRef на SecretStorage IDE.
func migrationMCPIntegrationsV1(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS mcp_servers (
  id TEXT PRIMARY KEY,
  display_name TEXT NOT NULL,
  kind TEXT NOT NULL DEFAULT 'custom',
  transport TEXT NOT NULL,
  command TEXT NOT NULL DEFAULT '',
  args_json TEXT NOT NULL DEFAULT '[]',
  dir TEXT NOT NULL DEFAULT '',
  env_json TEXT NOT NULL DEFAULT '{}',
  secret_env_json TEXT NOT NULL DEFAULT '{}',
  url TEXT NOT NULL DEFAULT '',
  headers_json TEXT NOT NULL DEFAULT '{}',
  secret_headers_json TEXT NOT NULL DEFAULT '{}',
  allow_private_host TEXT NOT NULL DEFAULT '',
  settings_json TEXT NOT NULL DEFAULT '{}',
  trust_digest TEXT NOT NULL DEFAULT '',
  trusted_at TEXT,
  resolved_command TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'unknown',
  last_error TEXT NOT NULL DEFAULT '',
  last_probe_at TEXT,
  server_name TEXT NOT NULL DEFAULT '',
  server_version TEXT NOT NULL DEFAULT '',
  protocol_version TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS mcp_server_tools (
  server_id TEXT NOT NULL REFERENCES mcp_servers(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  title TEXT NOT NULL DEFAULT '',
  description TEXT NOT NULL DEFAULT '',
  input_schema TEXT NOT NULL DEFAULT '',
  annotations TEXT NOT NULL DEFAULT '',
  digest TEXT NOT NULL,
  approved_digest TEXT NOT NULL DEFAULT '',
  enabled INTEGER NOT NULL DEFAULT 0,
  state TEXT NOT NULL DEFAULT 'new',
  risk TEXT NOT NULL DEFAULT 'HIGH',
  updated_at TEXT NOT NULL,
  PRIMARY KEY (server_id, name)
);
CREATE TABLE IF NOT EXISTS gitlab_bindings (
  workspace_id TEXT PRIMARY KEY,
  server_id TEXT NOT NULL DEFAULT '',
  mode TEXT NOT NULL DEFAULT 'auto',
  project_path TEXT NOT NULL DEFAULT '',
  username TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS integration_actions (
  id TEXT PRIMARY KEY,
  actor TEXT NOT NULL,
  server_id TEXT NOT NULL,
  tool TEXT NOT NULL,
  target TEXT NOT NULL DEFAULT '',
  outcome TEXT NOT NULL,
  error TEXT NOT NULL DEFAULT '',
  body_sha256 TEXT NOT NULL DEFAULT '',
  body_length INTEGER NOT NULL DEFAULT 0,
  at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS integration_actions_at ON integration_actions(at DESC);
`)
	return err
}

func (s *SQLite) SaveMCPServer(ctx context.Context, server domain.MCPServer) error {
	args, _ := json.Marshal(nonNilStrings(server.Args))
	encode := func(values map[string]string) string {
		if values == nil {
			values = map[string]string{}
		}
		raw, _ := json.Marshal(values)
		return string(raw)
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO mcp_servers(
  id, display_name, kind, transport, command, args_json, dir, env_json, secret_env_json, url, headers_json,
  secret_headers_json, allow_private_host, settings_json, trust_digest, trusted_at, resolved_command, status,
  last_error, last_probe_at, server_name, server_version, protocol_version, created_at, updated_at
) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET
  display_name=excluded.display_name, kind=excluded.kind, transport=excluded.transport, command=excluded.command,
  args_json=excluded.args_json, dir=excluded.dir, env_json=excluded.env_json, secret_env_json=excluded.secret_env_json,
  url=excluded.url, headers_json=excluded.headers_json, secret_headers_json=excluded.secret_headers_json,
  allow_private_host=excluded.allow_private_host, settings_json=excluded.settings_json,
  trust_digest=excluded.trust_digest, trusted_at=excluded.trusted_at, resolved_command=excluded.resolved_command,
  status=excluded.status, last_error=excluded.last_error, last_probe_at=excluded.last_probe_at,
  server_name=excluded.server_name, server_version=excluded.server_version,
  protocol_version=excluded.protocol_version, updated_at=excluded.updated_at`,
		server.ID, server.DisplayName, server.Kind, server.Transport, server.Command, string(args), server.Dir,
		encode(server.Env), encode(server.SecretEnv), server.URL, encode(server.Headers), encode(server.SecretHeaders),
		server.AllowPrivateHost, encode(server.Settings), server.TrustDigest, optionalTime(server.TrustedAt),
		server.ResolvedCommand, server.Status, server.LastError, optionalTime(server.LastProbeAt), server.ServerName,
		server.ServerVersion, server.ProtocolVersion, formatTime(server.CreatedAt), formatTime(server.UpdatedAt))
	return err
}

const mcpServerColumns = `id, display_name, kind, transport, command, args_json, dir, env_json, secret_env_json, url,
  headers_json, secret_headers_json, allow_private_host, settings_json, trust_digest, trusted_at, resolved_command,
  status, last_error, last_probe_at, server_name, server_version, protocol_version, created_at, updated_at`

func (s *SQLite) ListMCPServers(ctx context.Context) ([]domain.MCPServer, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+mcpServerColumns+` FROM mcp_servers ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.MCPServer
	for rows.Next() {
		server, scanErr := scanMCPServer(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, server)
	}
	return result, rows.Err()
}

func (s *SQLite) GetMCPServer(ctx context.Context, id string) (domain.MCPServer, error) {
	server, err := scanMCPServer(s.db.QueryRowContext(ctx, `SELECT `+mcpServerColumns+` FROM mcp_servers WHERE id=?`, id))
	if err == sql.ErrNoRows {
		return domain.MCPServer{}, fmt.Errorf("MCP-сервер не найден")
	}
	return server, err
}

func (s *SQLite) DeleteMCPServer(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `DELETE FROM mcp_server_tools WHERE server_id=?`, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE gitlab_bindings SET server_id='' WHERE server_id=?`, id); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM mcp_servers WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("MCP-сервер не найден")
	}
	return tx.Commit()
}

func scanMCPServer(row dbScanner) (domain.MCPServer, error) {
	var server domain.MCPServer
	var args, env, secretEnv, headers, secretHeaders, settings, created, updated string
	var trusted, probed sql.NullString
	if err := row.Scan(&server.ID, &server.DisplayName, &server.Kind, &server.Transport, &server.Command, &args,
		&server.Dir, &env, &secretEnv, &server.URL, &headers, &secretHeaders, &server.AllowPrivateHost, &settings,
		&server.TrustDigest, &trusted, &server.ResolvedCommand, &server.Status, &server.LastError, &probed,
		&server.ServerName, &server.ServerVersion, &server.ProtocolVersion, &created, &updated); err != nil {
		return domain.MCPServer{}, err
	}
	_ = json.Unmarshal([]byte(args), &server.Args)
	server.Env = decodeStringMap(env)
	server.SecretEnv = decodeStringMap(secretEnv)
	server.Headers = decodeStringMap(headers)
	server.SecretHeaders = decodeStringMap(secretHeaders)
	server.Settings = decodeStringMap(settings)
	server.TrustedAt = parseOptionalTime(trusted)
	server.LastProbeAt = parseOptionalTime(probed)
	server.CreatedAt, server.UpdatedAt = parseTime(created), parseTime(updated)
	return server, nil
}

// ReplaceMCPTools записывает свежий снимок инструментов сервера целиком.
func (s *SQLite) ReplaceMCPTools(ctx context.Context, serverID string, tools []domain.MCPTool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `DELETE FROM mcp_server_tools WHERE server_id=?`, serverID); err != nil {
		return err
	}
	for _, tool := range tools {
		enabled := 0
		if tool.Enabled {
			enabled = 1
		}
		if _, err = tx.ExecContext(ctx, `
INSERT INTO mcp_server_tools(server_id, name, title, description, input_schema, annotations, digest,
  approved_digest, enabled, state, risk, updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
			serverID, tool.Name, tool.Title, tool.Description, string(tool.InputSchema), string(tool.Annotations),
			tool.Digest, tool.ApprovedDigest, enabled, tool.State, tool.Risk, formatTime(tool.UpdatedAt)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *SQLite) ListMCPTools(ctx context.Context, serverID string) ([]domain.MCPTool, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT server_id, name, title, description, input_schema, annotations, digest, approved_digest, enabled, state, risk, updated_at
FROM mcp_server_tools WHERE server_id=? ORDER BY name`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.MCPTool
	for rows.Next() {
		var tool domain.MCPTool
		var schema, annotations, updated string
		var enabled int
		if err = rows.Scan(&tool.ServerID, &tool.Name, &tool.Title, &tool.Description, &schema, &annotations,
			&tool.Digest, &tool.ApprovedDigest, &enabled, &tool.State, &tool.Risk, &updated); err != nil {
			return nil, err
		}
		if schema != "" {
			tool.InputSchema = json.RawMessage(schema)
		}
		if annotations != "" {
			tool.Annotations = json.RawMessage(annotations)
		}
		tool.Enabled = enabled != 0
		tool.UpdatedAt = parseTime(updated)
		result = append(result, tool)
	}
	return result, rows.Err()
}

func (s *SQLite) AppendIntegrationAction(ctx context.Context, action domain.IntegrationAction) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO integration_actions(id, actor, server_id, tool, target, outcome, error, body_sha256, body_length, at)
VALUES(?,?,?,?,?,?,?,?,?,?)`, action.ID, action.Actor, action.ServerID, action.Tool, action.Target, action.Outcome,
		action.Error, action.BodySHA256, action.BodyLength, formatTime(action.At))
	return err
}

func (s *SQLite) ListIntegrationActions(ctx context.Context, limit int) ([]domain.IntegrationAction, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, actor, server_id, tool, target, outcome, error, body_sha256, body_length, at
FROM integration_actions ORDER BY at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.IntegrationAction
	for rows.Next() {
		var action domain.IntegrationAction
		var at string
		if err = rows.Scan(&action.ID, &action.Actor, &action.ServerID, &action.Tool, &action.Target, &action.Outcome,
			&action.Error, &action.BodySHA256, &action.BodyLength, &at); err != nil {
			return nil, err
		}
		action.At = parseTime(at)
		result = append(result, action)
	}
	return result, rows.Err()
}

// GetGitLabBinding — привязка папки; sql.ErrNoRows, если её нет.
func (s *SQLite) GetGitLabBinding(ctx context.Context, workspaceID string) (domain.GitLabBinding, error) {
	var binding domain.GitLabBinding
	var mode, updated string
	err := s.db.QueryRowContext(ctx, `
SELECT workspace_id, server_id, mode, project_path, username, updated_at FROM gitlab_bindings WHERE workspace_id=?`,
		workspaceID).Scan(&binding.WorkspaceID, &binding.ServerID, &mode, &binding.ProjectPath, &binding.Username, &updated)
	if err != nil {
		return domain.GitLabBinding{}, err
	}
	binding.Mode, binding.UpdatedAt = domain.GitLabBindMode(mode), parseTime(updated)
	return binding, nil
}

func (s *SQLite) SaveGitLabBinding(ctx context.Context, binding domain.GitLabBinding) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO gitlab_bindings(workspace_id, server_id, mode, project_path, username, updated_at) VALUES(?,?,?,?,?,?)
ON CONFLICT(workspace_id) DO UPDATE SET server_id=excluded.server_id, mode=excluded.mode,
  project_path=excluded.project_path, username=excluded.username, updated_at=excluded.updated_at`,
		binding.WorkspaceID, binding.ServerID, string(binding.Mode), binding.ProjectPath, binding.Username, formatTime(binding.UpdatedAt))
	return err
}

func decodeStringMap(raw string) map[string]string {
	values := map[string]string{}
	_ = json.Unmarshal([]byte(raw), &values)
	if len(values) == 0 {
		return nil
	}
	return values
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func optionalTime(value *time.Time) any {
	if value == nil || value.IsZero() {
		return nil
	}
	return formatTime(*value)
}

func parseOptionalTime(value sql.NullString) *time.Time {
	if !value.Valid || value.String == "" {
		return nil
	}
	parsed := parseTime(value.String)
	return &parsed
}
