package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"local-agent-workbench/internal/storage"
)

func TestMigrateCorpusUsesCopiesAndPreservesEverySource(t *testing.T) {
	ctx := context.Background()
	source := filepath.Join(t.TempDir(), "source")
	output := filepath.Join(t.TempDir(), "migrated")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	hashes := map[string]string{}
	for _, name := range []string{"old-a.db", "old-b.db"} {
		path := filepath.Join(source, name)
		store, err := storage.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if err = store.SaveSetting(ctx, "fixture", name); err != nil {
			t.Fatal(err)
		}
		if err = store.Close(); err != nil {
			t.Fatal(err)
		}
		report, err := storage.VerifyDatabase(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		hashes[name] = report.SHA256
	}
	reports, err := migrateCorpus(ctx, source, output)
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 2 {
		t.Fatalf("corpus reports=%#v", reports)
	}
	for _, name := range []string{"old-a.db", "old-b.db"} {
		sourceReport, err := storage.VerifyDatabase(ctx, filepath.Join(source, name))
		if err != nil {
			t.Fatal(err)
		}
		migratedReport, err := storage.VerifyDatabase(ctx, filepath.Join(output, name))
		if err != nil {
			t.Fatal(err)
		}
		if sourceReport.SHA256 != hashes[name] || migratedReport.Integrity != "ok" {
			t.Fatalf("corpus item %s source=%#v migrated=%#v", name, sourceReport, migratedReport)
		}
	}
}

func TestRestoreRequiresExplicitOfflineConfirmation(t *testing.T) {
	err := run(context.Background(), []string{"restore", "--backup", "backup.db", "--db", "workbench.db"})
	if err == nil || !strings.Contains(err.Error(), "--confirm-offline") {
		t.Fatalf("restore confirmation error=%v", err)
	}
}
