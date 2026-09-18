package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

func TestCompatibilityUsageAggregatesExactVersionsAndStaysWorldScoped(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "compatibility.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	first := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	second := first.Add(24 * time.Hour)
	for _, usage := range []domain.CompatibilityUsage{
		{Feature: domain.CompatibilityWorkflowRun, WorkspaceID: "ws-a", ApplicationVersion: "1.2.2", LegacyVersion: "workflow-snapshot-v1", LastSeen: first},
		{Feature: domain.CompatibilityWorkflowRun, WorkspaceID: "ws-a", ApplicationVersion: "1.2.2", LegacyVersion: "workflow-snapshot-v1", LastSeen: second},
		{Feature: domain.CompatibilityWorkflowRun, WorkspaceID: "ws-a", ApplicationVersion: "1.3.0", LegacyVersion: "workflow-snapshot-v1", LastSeen: second},
		{Feature: domain.CompatibilityWorkflowRun, WorkspaceID: "ws-b", ApplicationVersion: "1.2.2", LegacyVersion: "workflow-snapshot-v1", LastSeen: second},
		{Feature: domain.CompatibilityProfileSave, WorkspaceID: "", ApplicationVersion: "1.2.2", LegacyVersion: "profile-api-v1", LastSeen: second},
	} {
		if err = store.RecordCompatibilityUsage(ctx, usage); err != nil {
			t.Fatal(err)
		}
	}
	items, err := store.ListCompatibilityUsage(ctx, "ws-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("workspace-visible usage=%#v", items)
	}
	counts := map[string]int64{}
	for _, item := range items {
		if item.WorkspaceID == "ws-b" {
			t.Fatal("another workspace's compatibility telemetry leaked")
		}
		counts[string(item.Feature)+"/"+item.ApplicationVersion] = item.Count
		if item.Feature == domain.CompatibilityWorkflowRun && item.ApplicationVersion == "1.2.2" {
			if !item.FirstSeen.Equal(first) || !item.LastSeen.Equal(second) {
				t.Fatalf("aggregate time bounds=%#v", item)
			}
		}
	}
	if counts["legacy_workflow_run/1.2.2"] != 2 || counts["legacy_workflow_run/1.3.0"] != 1 || counts["legacy_profile_save/1.2.2"] != 1 {
		t.Fatalf("exact-version counts=%#v", counts)
	}
}

func TestMigration29ReconcilesProfilesWrittenAfterOriginalBridge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile-reconcile.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	legacy := domain.DefaultProfile()
	legacy.ID, legacy.Name, legacy.Model = "late-legacy-profile", "Late legacy profile", "legacy-model"
	if err = store.SaveProfile(context.Background(), legacy); err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.Exec(`DELETE FROM schema_migrations WHERE version=29`); err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.Exec(`DROP TABLE compatibility_usage`); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	blueprints, err := store.ListBlueprints(context.Background())
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
		t.Fatalf("late legacy profile was not reconciled: %#v", blueprints)
	}
	versions, err := store.MigrationVersions(context.Background())
	all := hubMigrations()
	if err != nil || len(versions) != len(all) || versions[len(versions)-1] != all[len(all)-1].version {
		t.Fatalf("migration versions=%#v err=%v", versions, err)
	}
}
