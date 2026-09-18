package storage

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestDatabaseNeedsMigrationUsesExactReadOnlyMigrationSet(t *testing.T) {
	ctx := context.Background()
	missing := filepath.Join(t.TempDir(), "missing.db")
	needed, err := DatabaseNeedsMigration(ctx, missing)
	if err != nil || needed {
		t.Fatalf("missing database: needed=%v err=%v", needed, err)
	}

	path := filepath.Join(t.TempDir(), "current.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	needed, err = DatabaseNeedsMigration(ctx, path)
	if err != nil || needed {
		t.Fatalf("current database: needed=%v err=%v", needed, err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`DELETE FROM schema_migrations WHERE version=?`, LatestMigrationVersion()); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	needed, err = DatabaseNeedsMigration(ctx, path)
	if err != nil || !needed {
		t.Fatalf("incomplete database: needed=%v err=%v", needed, err)
	}
}

func TestDatabaseNeedsMigrationRecognizesLegacyDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE legacy_data(id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	needed, err := DatabaseNeedsMigration(context.Background(), path)
	if err != nil || !needed {
		t.Fatalf("legacy database: needed=%v err=%v", needed, err)
	}
}
