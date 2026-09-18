package backup

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"local-agent-workbench/internal/storage"
)

func TestManagerCreatesVerifiedOnlineBackupAndAppliesRetention(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, err := storage.Open(filepath.Join(root, "workbench.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.SaveSetting(ctx, "backup-marker", "preserved"); err != nil {
		t.Fatal(err)
	}

	manager := NewManager(store, filepath.Join(root, "backups"), Policy{Daily: 2, Weekly: 1, MaxBytes: 1 << 30})
	dates := []time.Time{
		time.Date(2026, 8, 23, 10, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC),
	}
	for index, at := range dates {
		manager.now = func() time.Time { return at }
		if _, err = manager.Create(ctx, "history"); err != nil {
			t.Fatalf("create snapshot %d: %v", index, err)
		}
	}
	items, err := manager.listLocked()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("expected two daily snapshots after retention, got %d", len(items))
	}
	latest, err := manager.Latest(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if latest.Reason != "history" || latest.CreatedAt != dates[2] || latest.Database.Integrity != "ok" || latest.Database.SHA256 == "" {
		t.Fatalf("unexpected latest snapshot: %+v", latest)
	}
	for _, item := range items {
		if _, err = storage.VerifyDatabase(ctx, item.path); err != nil {
			t.Fatalf("retained snapshot %s: %v", item.name, err)
		}
	}
	listed, err := manager.List(ctx)
	if err != nil || len(listed) != 2 || listed[0].Database.SHA256 == "" || listed[1].Database.Integrity != "ok" {
		t.Fatalf("verified list: items=%+v err=%v", listed, err)
	}
}

func TestManagerSpaceRetentionKeepsNewestAndUnknownFiles(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, err := storage.Open(filepath.Join(root, "workbench.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	backupDir := filepath.Join(root, "backups")
	manager := NewManager(store, backupDir, Policy{Daily: 10, Weekly: 10, MaxBytes: 1 << 30})
	manager.now = func() time.Time { return time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC) }
	first, err := manager.Create(ctx, "space")
	if err != nil {
		t.Fatal(err)
	}
	unknown := filepath.Join(backupDir, "imported.db")
	if err = os.WriteFile(unknown, []byte("not managed"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager.policy.MaxBytes = first.Database.SizeBytes + first.Database.SizeBytes/2
	manager.now = func() time.Time { return time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC) }
	if _, err = manager.Create(ctx, "space"); err != nil {
		t.Fatal(err)
	}
	items, err := manager.listLocked()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].createdAt.Day() != 8 {
		t.Fatalf("space retention did not keep only newest: %+v", items)
	}
	if _, err = os.Stat(unknown); err != nil {
		t.Fatalf("unmanaged file was changed: %v", err)
	}
}

func TestCreatePreMigrationPreservesSourceAndSkipsCurrentDatabase(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	path := filepath.Join(root, "workbench.db")
	store, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.SaveSetting(ctx, "migration-marker", "preserved"); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	backupDir := filepath.Join(root, "backups")
	if snapshot, createErr := CreatePreMigration(ctx, path, backupDir, time.Now()); createErr != nil || snapshot != nil {
		t.Fatalf("current database should not be snapshotted: snapshot=%+v err=%v", snapshot, createErr)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`DELETE FROM schema_migrations WHERE version=?`, storage.LatestMigrationVersion()); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := storage.VerifyDatabase(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := CreatePreMigration(ctx, path, backupDir, time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if snapshot == nil || snapshot.Reason != "pre-migration" || snapshot.Database.Integrity != "ok" {
		t.Fatalf("unexpected migration snapshot: %+v", snapshot)
	}
	after, err := storage.VerifyDatabase(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if before.SHA256 != after.SHA256 {
		t.Fatalf("pre-migration snapshot mutated source: %s != %s", before.SHA256, after.SHA256)
	}
	if _, err = storage.VerifyDatabase(ctx, snapshot.Database.Path); err != nil {
		t.Fatal(err)
	}
}

func TestLatestReportsMissingWithoutCreatingDirectory(t *testing.T) {
	manager := NewManager(nil, filepath.Join(t.TempDir(), "missing"), DefaultPolicy())
	_, err := manager.Latest(context.Background())
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected os.ErrNotExist, got %v", err)
	}
}
