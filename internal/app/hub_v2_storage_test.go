package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestHubV2UsesCleanDatabaseAndLeavesLegacyUntouched(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, legacyDatabaseFile)
	if err := os.WriteFile(legacy, []byte("legacy must remain untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	application, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	application.Shutdown(context.Background())
	got, err := os.ReadFile(legacy)
	if err != nil || string(got) != "legacy must remain untouched" {
		t.Fatalf("legacy database changed: %q, %v", got, err)
	}
	if _, err = os.Stat(filepath.Join(root, hubV2DatabaseFile)); err != nil {
		t.Fatalf("v2 database was not created: %v", err)
	}
}

// TestLegacyHubStaysReachableByRestart keeps the switch back cheap: the old
// database is still on disk, and one environment variable reopens it.
func TestLegacyHubStaysReachableByRestart(t *testing.T) {
	t.Setenv("POINT_AGENT_HUB_V2", "0")
	root := t.TempDir()
	application, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	application.Shutdown(context.Background())
	if _, err = os.Stat(filepath.Join(root, legacyDatabaseFile)); err != nil {
		t.Fatalf("legacy database was not opened: %v", err)
	}
	if _, err = os.Stat(filepath.Join(root, hubV2DatabaseFile)); !os.IsNotExist(err) {
		t.Fatalf("legacy start must not create the v2 database: %v", err)
	}
}
