package dbconn

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
)

// QueryRequest runs a single SQL statement with hard safety limits.
type QueryRequest struct {
	SQL        string
	AllowWrite bool
	MaxRows    int
	Timeout    time.Duration
}

// QueryResult is a tabular snapshot suitable for Hub UI and agent tools.
type QueryResult struct {
	Kind         StatementKind `json:"kind"`
	Columns      []string      `json:"columns"`
	Rows         [][]any       `json:"rows"`
	RowCount     int           `json:"rowCount"`
	Truncated    bool          `json:"truncated"`
	DurationMs   int64         `json:"durationMs"`
	RowsAffected int64         `json:"rowsAffected,omitempty"`
	Message      string        `json:"message,omitempty"`
}

func RunQuery(ctx context.Context, db *sql.DB, req QueryRequest) (QueryResult, error) {
	kind := ClassifySQL(req.SQL)
	if kind == StatementUnknown {
		return QueryResult{}, fmt.Errorf("не удалось определить тип SQL — разрешены явные SELECT/SHOW/… или подтверждённые записи")
	}
	if kind == StatementWrite && !req.AllowWrite {
		return QueryResult{}, fmt.Errorf("записывающий SQL заблокирован: подтвердите выполнение (AllowWrite) перед выполнением")
	}
	maxRows := req.MaxRows
	if maxRows <= 0 {
		maxRows = DefaultMaxRows
	}
	if maxRows > HardMaxRows {
		maxRows = HardMaxRows
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	qctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	started := time.Now()
	if kind == StatementRead {
		tx, release, err := beginRead(qctx, db)
		if err != nil {
			return QueryResult{}, err
		}
		defer release()
		rows, err := tx.QueryContext(qctx, req.SQL)
		if err != nil {
			return QueryResult{}, err
		}
		defer rows.Close()
		cols, err := rows.Columns()
		if err != nil {
			return QueryResult{}, err
		}
		result := QueryResult{Kind: kind, Columns: cols, Rows: make([][]any, 0, 32)}
		for rows.Next() {
			if len(result.Rows) >= maxRows {
				result.Truncated = true
				break
			}
			raw := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range raw {
				ptrs[i] = &raw[i]
			}
			if err = rows.Scan(ptrs...); err != nil {
				return QueryResult{}, err
			}
			result.Rows = append(result.Rows, normalizeRow(raw))
		}
		if err = rows.Err(); err != nil {
			return QueryResult{}, err
		}
		result.RowCount = len(result.Rows)
		result.DurationMs = time.Since(started).Milliseconds()
		return result, nil
	}
	res, err := db.ExecContext(qctx, req.SQL)
	if err != nil {
		return QueryResult{}, err
	}
	affected, _ := res.RowsAffected()
	return QueryResult{
		Kind:         kind,
		Columns:      []string{},
		Rows:         [][]any{},
		RowCount:     0,
		RowsAffected: affected,
		DurationMs:   time.Since(started).Milliseconds(),
		Message:      fmt.Sprintf("выполнено, затронуто строк: %d", affected),
	}, nil
}

func normalizeRow(raw []any) []any {
	out := make([]any, len(raw))
	for i, v := range raw {
		switch t := v.(type) {
		case nil:
			out[i] = nil
		case []byte:
			out[i] = string(t)
		case time.Time:
			out[i] = t.UTC().Format(time.RFC3339Nano)
		default:
			out[i] = t
		}
	}
	return out
}

// SchemaSummary lists tables/views for Hub and agent introspection.
type SchemaSummary struct {
	Driver domain.DBDriver `json:"driver"`
	Tables []TableInfo     `json:"tables"`
}

type TableInfo struct {
	Schema  string   `json:"schema,omitempty"`
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Columns []string `json:"columns,omitempty"`
}

func InspectSchema(ctx context.Context, db *sql.DB, driver domain.DBDriver, limit int) (SchemaSummary, error) {
	if limit <= 0 {
		limit = 100
	}
	timeout := DefaultTimeout
	qctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	summary := SchemaSummary{Driver: driver, Tables: []TableInfo{}}
	var err error
	switch driver {
	case domain.DBDriverSQLite:
		err = inspectSQLite(qctx, db, &summary, limit)
	case domain.DBDriverPostgres:
		err = inspectPostgres(qctx, db, &summary, limit)
	case domain.DBDriverMySQL:
		err = inspectMySQL(qctx, db, &summary, limit)
	default:
		err = fmt.Errorf("неизвестный драйвер")
	}
	return summary, err
}

func inspectSQLite(ctx context.Context, db *sql.DB, summary *SchemaSummary, limit int) error {
	rows, err := db.QueryContext(ctx, `SELECT name, type FROM sqlite_master WHERE type IN ('table','view') AND name NOT LIKE 'sqlite_%' ORDER BY name LIMIT ?`, limit)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var info TableInfo
		if err = rows.Scan(&info.Name, &info.Type); err != nil {
			return err
		}
		cols, _ := sqliteColumns(ctx, db, info.Name)
		info.Columns = cols
		summary.Tables = append(summary.Tables, info)
	}
	return rows.Err()
}

func inspectPostgres(ctx context.Context, db *sql.DB, summary *SchemaSummary, limit int) error {
	rows, err := db.QueryContext(ctx, `
SELECT table_schema, table_name, table_type
FROM information_schema.tables
WHERE table_schema NOT IN ('pg_catalog','information_schema')
ORDER BY table_schema, table_name
LIMIT $1`, limit)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var info TableInfo
		if err = rows.Scan(&info.Schema, &info.Name, &info.Type); err != nil {
			return err
		}
		colRows, colErr := db.QueryContext(ctx, `
SELECT column_name FROM information_schema.columns
WHERE table_schema=$1 AND table_name=$2 ORDER BY ordinal_position`, info.Schema, info.Name)
		if colErr == nil {
			for colRows.Next() {
				var col string
				if scanErr := colRows.Scan(&col); scanErr == nil {
					info.Columns = append(info.Columns, col)
				}
			}
			_ = colRows.Close()
		}
		summary.Tables = append(summary.Tables, info)
	}
	return rows.Err()
}

func inspectMySQL(ctx context.Context, db *sql.DB, summary *SchemaSummary, limit int) error {
	rows, err := db.QueryContext(ctx, `
SELECT table_schema, table_name, table_type
FROM information_schema.tables
WHERE table_schema = DATABASE()
ORDER BY table_name
LIMIT ?`, limit)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var info TableInfo
		if err = rows.Scan(&info.Schema, &info.Name, &info.Type); err != nil {
			return err
		}
		colRows, colErr := db.QueryContext(ctx, `
SELECT column_name FROM information_schema.columns
WHERE table_schema=? AND table_name=? ORDER BY ordinal_position`, info.Schema, info.Name)
		if colErr == nil {
			for colRows.Next() {
				var col string
				if scanErr := colRows.Scan(&col); scanErr == nil {
					info.Columns = append(info.Columns, col)
				}
			}
			_ = colRows.Close()
		}
		summary.Tables = append(summary.Tables, info)
	}
	return rows.Err()
}

func sqliteColumns(ctx context.Context, db *sql.DB, table string) ([]string, error) {
	// PRAGMA does not accept bound parameters for the table name; allow only safe identifiers.
	if table == "" || strings.ContainsAny(table, "\"';`--/*\\") {
		return nil, fmt.Errorf("небезопасное имя таблицы")
	}
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(`+quoteIdent(table)+`)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt any
		if err = rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return cols, err
		}
		cols = append(cols, name)
	}
	return cols, rows.Err()
}

func quoteIdent(name string) string {
	escaped := strings.ReplaceAll(name, `"`, `""`)
	return `"` + escaped + `"`
}
