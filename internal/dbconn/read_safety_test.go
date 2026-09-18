package dbconn

import (
	"context"
	"database/sql"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"local-agent-workbench/internal/domain"
)

func TestReadSQLCannotHideMutations(t *testing.T) {
	cases := []struct {
		sql  string
		want StatementKind
	}{
		{"WITH x AS (SELECT 1)\nDELETE FROM audit_items RETURNING id", StatementWrite},
		{"WITH x AS (DELETE FROM audit_items RETURNING id) SELECT * FROM x", StatementWrite},
		{"WITH x AS (SELECT 1)\tUPDATE audit_items SET id=2", StatementWrite},
		{"EXPLAIN ANALYZE DELETE FROM audit_items", StatementWrite},
		{"SELECT * INTO backup FROM audit_items", StatementWrite},
		{"PRAGMA query_only=OFF", StatementWrite},
		{"PRAGMA table_info(audit_items)", StatementRead},
		{"SELECT '; DELETE FROM audit_items' AS example;", StatementRead},
		{"SELECT 'it''s safe'", StatementRead},
		{"SELECT 1; SELECT 2", StatementUnknown},
		{"SELECT 1 /*! INTO OUTFILE 'x' */", StatementUnknown},
		{"SELECT 1 /* unfinished", StatementUnknown},
		{"SELECT 'unfinished", StatementUnknown},
		{"SELECTED 1", StatementUnknown},
	}
	for _, tc := range cases {
		if got := ClassifySQL(tc.sql); got != tc.want {
			t.Errorf("%q: got %s want %s", tc.sql, got, tc.want)
		}
	}
}

func TestRunQueryRefusesCTEDeleteAndKeepsWritesUsable(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err = db.Exec("CREATE TABLE audit_items(id INTEGER); INSERT INTO audit_items VALUES(1)"); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"WITH x AS (SELECT 1)\nDELETE FROM audit_items RETURNING id", "PRAGMA query_only=OFF", "SELECT 1; DELETE FROM audit_items"} {
		if _, err = RunQuery(ctx, db, QueryRequest{SQL: q}); err == nil {
			t.Errorf("accepted %q", q)
		}
	}
	tx, release, err := beginRead(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM audit_items"); err == nil {
		t.Error("database did not enforce read-only")
	}
	release()
	result, err := RunQuery(ctx, db, QueryRequest{SQL: "SELECT * FROM audit_items"})
	if err != nil || result.RowCount != 1 {
		t.Fatalf("read: %+v %v", result, err)
	}
	if _, err = RunQuery(ctx, db, QueryRequest{SQL: "INSERT INTO audit_items VALUES(2)", AllowWrite: true}); err != nil {
		t.Fatal("read-only leaked into pool:", err)
	}
}

func TestReadOnlyConnectionConfiguration(t *testing.T) {
	password := "space ' quote & slash/"
	dsn, _, err := buildDSN(OpenConfig{Driver: domain.DBDriverPostgres, Database: "audit", Password: password, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	pg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if pg.Password != password || pg.RuntimeParams["default_transaction_read_only"] != "on" {
		t.Fatal("postgres read-only or credentials lost")
	}
	dsn, _, err = buildDSN(OpenConfig{Driver: domain.DBDriverMySQL, Database: "audit", Password: password, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	my, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if my.Passwd != password || my.Params["transaction_read_only"] != "1" {
		t.Fatal("mysql read-only or credentials lost")
	}
}
