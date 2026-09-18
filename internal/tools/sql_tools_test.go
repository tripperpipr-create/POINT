package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"local-agent-workbench/internal/dbconn"
	"local-agent-workbench/internal/domain"
)

type stubDBSource struct {
	conn domain.DBConnection
	cfg  dbconn.OpenConfig
}

func (s stubDBSource) List(context.Context) ([]domain.DBConnection, error) {
	return []domain.DBConnection{s.conn}, nil
}

func (s stubDBSource) OpenConfig(context.Context, string, bool) (domain.DBConnection, dbconn.OpenConfig, error) {
	return s.conn, s.cfg, nil
}

func (s stubDBSource) NetworkAllowed(string, []string, string) bool { return true }

func TestDBQueryRejectsWrites(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "demo.db")
	cfg := dbconn.OpenConfig{Driver: domain.DBDriverSQLite, Database: dbPath, Workspace: dir}
	db, err := dbconn.Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE items(id INTEGER PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	src := stubDBSource{
		conn: domain.DBConnection{ID: "c1", Driver: domain.DBDriverSQLite, Database: dbPath, DisplayName: "demo"},
		cfg:  cfg,
	}
	tool := DBQuery{Config: DBToolConfig{Source: src}}
	raw, _ := json.Marshal(map[string]any{"connectionId": "c1", "sql": "DELETE FROM items", "reason": "no"})
	result := tool.Execute(context.Background(), raw)
	if result.OK || result.Error == nil || result.Error.Code != "write_blocked" {
		t.Fatalf("expected write_blocked, got %#v", result)
	}

	raw, _ = json.Marshal(map[string]any{"connectionId": "c1", "sql": "SELECT name FROM items", "reason": "read"})
	result = tool.Execute(context.Background(), raw)
	if !result.OK {
		t.Fatalf("select failed: %#v", result)
	}
}

func TestDBExecRequiresMutatingSQL(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "demo.db")
	cfg := dbconn.OpenConfig{Driver: domain.DBDriverSQLite, Database: dbPath, Workspace: dir}
	db, err := dbconn.Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE items(id INTEGER PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	src := stubDBSource{
		conn: domain.DBConnection{ID: "c1", Driver: domain.DBDriverSQLite, Database: dbPath},
		cfg:  cfg,
	}
	tool := DBExec{Config: DBToolConfig{Source: src}}
	raw, _ := json.Marshal(map[string]any{"connectionId": "c1", "sql": "INSERT INTO items(name) VALUES ('a')", "reason": "seed"})
	result := tool.Execute(context.Background(), raw)
	if !result.OK {
		t.Fatalf("insert failed: %#v", result)
	}
}
