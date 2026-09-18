package storage

import (
	"context"
	"database/sql"
	"fmt"

	"local-agent-workbench/internal/domain"
)

func migrationDBConnectionsV1(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS db_connections (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL DEFAULT '',
  display_name TEXT NOT NULL,
  driver TEXT NOT NULL,
  host TEXT NOT NULL DEFAULT '',
  port INTEGER NOT NULL DEFAULT 0,
  database_name TEXT NOT NULL,
  username TEXT NOT NULL DEFAULT '',
  secret_ref TEXT NOT NULL DEFAULT '',
  ssl_mode TEXT NOT NULL DEFAULT '',
  read_only_default INTEGER NOT NULL DEFAULT 1,
  status TEXT NOT NULL DEFAULT 'unknown',
  last_error TEXT NOT NULL DEFAULT '',
  last_probe_at TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS db_connections_workspace ON db_connections(workspace_id, updated_at DESC);
`)
	return err
}

func (s *SQLite) SaveDBConnection(ctx context.Context, conn domain.DBConnection) error {
	var probed any
	if conn.LastProbeAt != nil {
		probed = formatTime(*conn.LastProbeAt)
	}
	readOnly := 0
	if conn.ReadOnlyDefault {
		readOnly = 1
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO db_connections(
  id, workspace_id, display_name, driver, host, port, database_name, username, secret_ref, ssl_mode,
  read_only_default, status, last_error, last_probe_at, created_at, updated_at
) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET
  workspace_id=excluded.workspace_id,
  display_name=excluded.display_name,
  driver=excluded.driver,
  host=excluded.host,
  port=excluded.port,
  database_name=excluded.database_name,
  username=excluded.username,
  secret_ref=excluded.secret_ref,
  ssl_mode=excluded.ssl_mode,
  read_only_default=excluded.read_only_default,
  status=excluded.status,
  last_error=excluded.last_error,
  last_probe_at=excluded.last_probe_at,
  updated_at=excluded.updated_at`,
		conn.ID, conn.WorkspaceID, conn.DisplayName, conn.Driver, conn.Host, conn.Port, conn.Database, conn.Username,
		conn.SecretRef, conn.SSLMode, readOnly, conn.Status, conn.LastError, probed,
		formatTime(conn.CreatedAt), formatTime(conn.UpdatedAt))
	return err
}

func (s *SQLite) ListDBConnections(ctx context.Context, workspaceID string) ([]domain.DBConnection, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, workspace_id, display_name, driver, host, port, database_name, username, secret_ref, ssl_mode,
  read_only_default, status, last_error, last_probe_at, created_at, updated_at
FROM db_connections
WHERE workspace_id=? OR workspace_id=''
ORDER BY updated_at DESC`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.DBConnection
	for rows.Next() {
		conn, scanErr := scanDBConnection(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, conn)
	}
	return result, rows.Err()
}

func (s *SQLite) GetDBConnection(ctx context.Context, id string) (domain.DBConnection, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, workspace_id, display_name, driver, host, port, database_name, username, secret_ref, ssl_mode,
  read_only_default, status, last_error, last_probe_at, created_at, updated_at
FROM db_connections WHERE id=?`, id)
	conn, err := scanDBConnection(row)
	if err == sql.ErrNoRows {
		return domain.DBConnection{}, fmt.Errorf("подключение к БД не найдено")
	}
	return conn, err
}

func (s *SQLite) DeleteDBConnection(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM db_connections WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("подключение к БД не найдено")
	}
	return nil
}

type dbScanner interface {
	Scan(dest ...any) error
}

func scanDBConnection(row dbScanner) (domain.DBConnection, error) {
	var conn domain.DBConnection
	var created, updated string
	var probed sql.NullString
	var readOnly int
	if err := row.Scan(
		&conn.ID, &conn.WorkspaceID, &conn.DisplayName, &conn.Driver, &conn.Host, &conn.Port, &conn.Database,
		&conn.Username, &conn.SecretRef, &conn.SSLMode, &readOnly, &conn.Status, &conn.LastError, &probed,
		&created, &updated,
	); err != nil {
		return domain.DBConnection{}, err
	}
	conn.ReadOnlyDefault = readOnly != 0
	conn.CreatedAt, conn.UpdatedAt = parseTime(created), parseTime(updated)
	if probed.Valid && probed.String != "" {
		t := parseTime(probed.String)
		conn.LastProbeAt = &t
	}
	return conn, nil
}
