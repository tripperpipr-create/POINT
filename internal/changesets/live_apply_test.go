package changesets_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"local-agent-workbench/internal/changesets"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/storage"
)

func TestApplyKeepsModifyWhenLiveAlreadyMatchesProposed(t *testing.T) {
	ctx := context.Background()
	workspacePath := t.TempDir()
	content := []byte("already written by agent")
	if err := os.WriteFile(filepath.Join(workspacePath, "a.txt"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	hash := hex.EncodeToString(sum[:])
	db, err := storage.Open(filepath.Join(t.TempDir(), "changes.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	set := domain.ChangeSet{
		ID: "cs-live", WorkspaceID: "ws", ExecutionID: "exec", Title: "live",
		Status: domain.ChangeSetPending, Items: []domain.ChangeItem{{
			ID: "item", Path: "a.txt", Kind: "modify",
			OriginalHash: "deadbeef", ProposedHash: hash,
			OriginalContent: "old", ProposedContent: string(content),
		}},
	}
	if err := db.SaveChangeSet(ctx, set); err != nil {
		t.Fatal(err)
	}
	applier := changesets.Applier{Store: db}
	result, err := applier.Apply(ctx, workspacePath, set.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.ChangeSet.Status != domain.ChangeSetApplied {
		t.Fatalf("status=%s conflicts=%v", result.ChangeSet.Status, result.Conflicts)
	}
	if len(result.ChangeSet.Items) != 1 || result.ChangeSet.Items[0].AppliedOperation != "kept" {
		t.Fatalf("items=%#v", result.ChangeSet.Items)
	}
	got, _ := os.ReadFile(filepath.Join(workspacePath, "a.txt"))
	if string(got) != string(content) {
		t.Fatalf("content changed: %q", got)
	}
}
