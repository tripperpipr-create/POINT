package storage

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

func TestOnlineBackupAndOfflineRestorePreserveRecoveryPoint(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	target := filepath.Join(root, "workbench.db")
	backup := filepath.Join(root, "backup.db")
	store, err := Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.SaveSetting(ctx, "recovery-marker", "before-backup"); err != nil {
		t.Fatal(err)
	}
	report, err := store.Backup(ctx, backup)
	if err != nil {
		t.Fatal(err)
	}
	if report.Integrity != "ok" || report.SHA256 == "" || report.SizeBytes <= 0 {
		t.Fatalf("backup report=%#v", report)
	}
	if err = store.SaveSetting(ctx, "recovery-marker", "after-backup"); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}

	restored, err := RestoreDatabaseOffline(ctx, backup, target)
	if err != nil {
		t.Fatal(err)
	}
	if restored.PreviousPath == "" || restored.Restored.Integrity != "ok" || restored.Backup.SHA256 != report.SHA256 {
		t.Fatalf("restore report=%#v", restored)
	}
	if filepath.Ext(restored.PreviousPath) != ".db" {
		t.Fatalf("recovery point must remain directly usable by point-db: %q", restored.PreviousPath)
	}
	if _, err = VerifyDatabase(ctx, restored.PreviousPath); err != nil {
		t.Fatalf("verify recovery point: %v", err)
	}
	current, err := Open(target)
	if err != nil {
		t.Fatal(err)
	}
	value, err := current.Setting(ctx, "recovery-marker")
	_ = current.Close()
	if err != nil || value != "before-backup" {
		t.Fatalf("restored value=%q err=%v", value, err)
	}
	previous, err := Open(restored.PreviousPath)
	if err != nil {
		t.Fatal(err)
	}
	value, err = previous.Setting(ctx, "recovery-marker")
	_ = previous.Close()
	if err != nil || value != "after-backup" {
		t.Fatalf("recovery-point value=%q err=%v", value, err)
	}
}

func TestReadOnlyDSNSupportsRecoveryNamesAndRejectsWrites(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	target := filepath.Join(root, "workbench.db")
	store, err := Open(target)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.SaveSetting(ctx, "readonly-marker", "preserved"); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	recovery := target + ".pre-restore-20260829T000000Z"
	if err = os.Rename(target, recovery); err != nil {
		t.Fatal(err)
	}
	if _, err = VerifyDatabase(ctx, recovery); err != nil {
		t.Fatalf("verify recovery-style filename: %v", err)
	}
	for _, sidecar := range []string{recovery + "-wal", recovery + "-shm"} {
		if _, statErr := os.Stat(sidecar); !os.IsNotExist(statErr) {
			t.Fatalf("read-only verification left sidecar %q: %v", sidecar, statErr)
		}
	}
	readOnly, err := sql.Open("sqlite", readOnlyDSN(recovery))
	if err != nil {
		t.Fatal(err)
	}
	defer readOnly.Close()
	if _, err = readOnly.ExecContext(ctx, `DELETE FROM settings WHERE key = 'readonly-marker'`); err == nil {
		t.Fatal("read-only maintenance DSN accepted a write")
	}
}

func TestOfflineRestoreFailsClosedForSidecarsAndCorruptBackup(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	target := filepath.Join(root, "workbench.db")
	backup := filepath.Join(root, "backup.db")
	store, err := Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.SaveSetting(ctx, "marker", "safe"); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Backup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(target+"-wal", []byte("active"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = RestoreDatabaseOffline(ctx, backup, target); err == nil || !strings.Contains(err.Error(), "sidecar") {
		t.Fatalf("restore with sidecar err=%v", err)
	}
	if err = os.Remove(target + "-wal"); err != nil {
		t.Fatal(err)
	}
	corrupt := filepath.Join(root, "corrupt.db")
	if err = os.WriteFile(corrupt, []byte("not a sqlite database"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = RestoreDatabaseOffline(ctx, corrupt, target); err == nil {
		t.Fatal("corrupt backup was accepted")
	}
	current, err := Open(target)
	if err != nil {
		t.Fatal(err)
	}
	value, valueErr := current.Setting(ctx, "marker")
	_ = current.Close()
	if valueErr != nil || value != "safe" {
		t.Fatalf("failed restore changed target: value=%q err=%v", value, valueErr)
	}
}

func TestMigrateDatabaseCopyLeavesSourceUntouchedAndReconcilesLegacyData(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	source := filepath.Join(root, "source.db")
	destination := filepath.Join(root, "migrated.db")
	store, err := Open(source)
	if err != nil {
		t.Fatal(err)
	}
	legacy := domain.DefaultProfile()
	legacy.ID, legacy.Name, legacy.Model = "recovery-legacy", "Recovery legacy", "recovery-model"
	legacy.CreatedAt, legacy.UpdatedAt = time.Now().UTC(), time.Now().UTC()
	if err = store.SaveProfile(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.Exec(`DELETE FROM schema_migrations WHERE version=29; DROP TABLE compatibility_usage`); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := fileSHA256(source)
	if err != nil {
		t.Fatal(err)
	}
	report, err := MigrateDatabaseCopy(ctx, source, destination)
	if err != nil {
		t.Fatal(err)
	}
	after, err := fileSHA256(source)
	if err != nil {
		t.Fatal(err)
	}
	if before != after || report.Source.SHA256 != before {
		t.Fatalf("source changed: before=%s after=%s report=%#v", before, after, report.Source)
	}
	// Проверяется, что копия догналась до конца списка, а не до конкретного
	// числа: зашитая цифра требовала правки теста при каждой новой миграции и
	// при этом ничего сверх «список доехал до конца» не утверждала.
	all := hubMigrations()
	last := all[len(all)-1].version
	if len(report.MigrationVersions) != len(all) || report.MigrationVersions[len(report.MigrationVersions)-1] != last || report.Migrated.Integrity != "ok" {
		t.Fatalf("migration report=%#v", report)
	}
	migrated, err := Open(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer migrated.Close()
	blueprints, err := migrated.ListBlueprints(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, blueprint := range blueprints {
		if blueprint.ID == legacy.ID && blueprint.PrimaryModel == legacy.Model {
			found = true
		}
	}
	if !found {
		t.Fatalf("legacy data missing after migrated copy: %#v", blueprints)
	}
}
