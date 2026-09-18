package dbconn

import (
	"context"
	"database/sql"
	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"local-agent-workbench/internal/domain"
	"net"
	"os"
	"strconv"
	"testing"
	"time"
)

// Run only against a disposable database: these fixtures create/drop named objects.
func TestReadOnlyRemoteDatabase(t *testing.T) {
	cases := []struct{ driver, env, createFunction string }{
		{"pgx", "POINT_TEST_POSTGRES_DSN", "CREATE FUNCTION point_audit_write() RETURNS integer LANGUAGE plpgsql AS $$ BEGIN DELETE FROM point_audit_items; RETURN 1; END; $$"},
		{"mysql", "POINT_TEST_MYSQL_DSN", "CREATE FUNCTION point_audit_write() RETURNS INTEGER DETERMINISTIC MODIFIES SQL DATA BEGIN DELETE FROM point_audit_items; RETURN 1; END"},
	}
	for _, tc := range cases {
		t.Run(tc.driver, func(t *testing.T) {
			dsn := os.Getenv(tc.env)
			if dsn == "" {
				t.Skip("set " + tc.env + " to a disposable database")
			}
			db, err := sql.Open(tc.driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			for {
				if err = db.PingContext(ctx); err == nil {
					break
				}
				if ctx.Err() != nil {
					t.Fatal(err)
				}
				time.Sleep(250 * time.Millisecond)
			}
			if _, err = db.ExecContext(ctx, "CREATE TABLE point_audit_items(id INTEGER)"); err != nil {
				t.Fatal(err)
			}
			defer db.Exec("DROP TABLE point_audit_items")
			if _, err = db.ExecContext(ctx, "INSERT INTO point_audit_items VALUES(1)"); err != nil {
				t.Fatal(err)
			}
			if _, err = db.ExecContext(ctx, tc.createFunction); err != nil {
				t.Fatal(err)
			}
			defer db.Exec("DROP FUNCTION point_audit_write")
			config := OpenConfig{ReadOnly: true}
			if tc.driver == "pgx" {
				parsed, parseErr := pgx.ParseConfig(dsn)
				if parseErr != nil {
					t.Fatal(parseErr)
				}
				config.Driver, config.Host, config.Port = domain.DBDriverPostgres, parsed.Host, int(parsed.Port)
				config.Database, config.Username, config.Password, config.SSLMode = parsed.Database, parsed.User, parsed.Password, "disable"
			} else {
				parsed, parseErr := mysql.ParseDSN(dsn)
				if parseErr != nil {
					t.Fatal(parseErr)
				}
				host, port, splitErr := net.SplitHostPort(parsed.Addr)
				if splitErr != nil {
					t.Fatal(splitErr)
				}
				config.Driver, config.Host = domain.DBDriverMySQL, host
				config.Port, _ = strconv.Atoi(port)
				config.Database, config.Username, config.Password = parsed.DBName, parsed.User, parsed.Passwd
			}
			readDB, openErr := Open(ctx, config)
			if openErr != nil {
				t.Fatal(openErr)
			}
			if _, readErr := readDB.ExecContext(ctx, "DELETE FROM point_audit_items"); readErr == nil {
				t.Error("ReadOnly connection accepted a direct write")
			}
			readDB.Close()
			for _, q := range []string{
				"WITH x AS (SELECT 1)\nDELETE FROM point_audit_items RETURNING id",
				"EXPLAIN ANALYZE DELETE FROM point_audit_items",
				"SELECT point_audit_write()",
			} {
				if _, err = RunQuery(ctx, db, QueryRequest{SQL: q}); err == nil {
					t.Errorf("write escaped through %q", q)
				}
				var remaining int
				if err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM point_audit_items").Scan(&remaining); err != nil {
					t.Fatal(err)
				}
				if remaining != 1 {
					t.Fatalf("query %q changed data: %d rows", q, remaining)
				}
			}
			if _, err = RunQuery(ctx, db, QueryRequest{SQL: "SELECT * FROM point_audit_items"}); err != nil {
				t.Fatal(err)
			}
			if _, err = RunQuery(ctx, db, QueryRequest{SQL: "INSERT INTO point_audit_items VALUES(2)", AllowWrite: true}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
