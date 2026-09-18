package storage

import (
	"context"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

func TestSourceSnapshotV2IsImmutable(t *testing.T) {
	store, err := Open(t.TempDir() + "/hub-v2.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	snapshot := domain.SourceSnapshot{ID: "source-1", Kind: "text", Label: "spec", Digest: "sha256:one", ExtractedText: "one", Warnings: []string{}, CreatedAt: time.Now().UTC()}
	if err = store.SaveSourceSnapshotV2(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	snapshot.Digest, snapshot.ExtractedText = "sha256:two", "two"
	if err = store.SaveSourceSnapshotV2(context.Background(), snapshot); err == nil {
		t.Fatal("expected immutable source id collision")
	}
	stored, err := store.GetSourceSnapshotV2(context.Background(), "source-1")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Digest != "sha256:one" || stored.ExtractedText != "one" {
		t.Fatalf("stored snapshot changed: %#v", stored)
	}
}
