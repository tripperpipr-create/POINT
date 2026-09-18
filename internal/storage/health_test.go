package storage

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
)

func TestHealthReportsIntegrityAndExactMigrationSet(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "health.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	report, err := store.Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Integrity != "ok" || report.ForeignKeyViolations != 0 || report.MigrationVersion != LatestMigrationVersion() || len(report.MissingMigrationVersions) != 0 || report.LastMigrationAt.IsZero() {
		t.Fatalf("health=%#v", report)
	}

	if _, err = store.db.Exec(`DELETE FROM schema_migrations WHERE version=29`); err != nil {
		t.Fatal(err)
	}
	report, err = store.Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(report.MissingMigrationVersions, []int{29}) {
		t.Fatalf("missing migrations=%#v", report.MissingMigrationVersions)
	}
}
