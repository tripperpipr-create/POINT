package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTextSnapshotReportsFileCountLimit(t *testing.T) {
	root := t.TempDir()
	for index := 0; index < maxSnapshotFiles+1; index++ {
		writeSnapshotFixture(t, root, fmt.Sprintf("file-%05d.txt", index), "x")
	}
	fs, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := fs.CaptureTextSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Complete || snapshot.ScannedFiles != maxSnapshotFiles || len(snapshot.Files) != maxSnapshotFiles || len(snapshot.SkippedPaths) == 0 {
		t.Fatalf("bounded snapshot=%#v files=%d", snapshot, len(snapshot.Files))
	}
}

func TestTextSnapshotDetectsRevertibleCreateModifyDelete(t *testing.T) {
	root := t.TempDir()
	writeSnapshotFixture(t, root, "modify.txt", "before")
	writeSnapshotFixture(t, root, "delete.txt", "remove me")
	fs, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	before, err := fs.CaptureTextSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	writeSnapshotFixture(t, root, "modify.txt", "after")
	writeSnapshotFixture(t, root, "create.txt", "new")
	if err = os.Remove(filepath.Join(root, "delete.txt")); err != nil {
		t.Fatal(err)
	}
	after, err := fs.CaptureTextSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	changes := DiffTextSnapshots(before, after)
	if len(changes) != 3 {
		t.Fatalf("changes=%#v", changes)
	}
	byPath := make(map[string]SnapshotChange)
	for _, change := range changes {
		byPath[change.Path] = change
	}
	if change := byPath["modify.txt"]; !change.Revertible || change.Original != "before" || change.Proposed != "after" || !change.OriginalExisted {
		t.Fatalf("modify=%#v", change)
	}
	if change := byPath["create.txt"]; !change.Revertible || change.OriginalExisted || change.Proposed != "new" {
		t.Fatalf("create=%#v", change)
	}
	if change := byPath["delete.txt"]; !change.Revertible || !change.OriginalExisted || change.Original != "remove me" || change.Proposed != "" {
		t.Fatalf("delete=%#v", change)
	}
}

func TestTextSnapshotReportsBinaryAndOversizedChangesWithoutContent(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "binary.bin"), []byte{0, 1, 2}, 0600); err != nil {
		t.Fatal(err)
	}
	writeSnapshotFixture(t, root, "large.txt", strings.Repeat("x", maxSnapshotFileBytes+1))
	fs, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	before, err := fs.CaptureTextSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "binary.bin"), []byte{0, 1, 3}, 0600); err != nil {
		t.Fatal(err)
	}
	writeSnapshotFixture(t, root, "large.txt", strings.Repeat("y", maxSnapshotFileBytes+1))
	after, err := fs.CaptureTextSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	changes := DiffTextSnapshots(before, after)
	if len(changes) != 2 {
		t.Fatalf("changes=%#v", changes)
	}
	for _, change := range changes {
		if change.Revertible || change.Original != "" || change.Proposed != "" || change.Reason == "" {
			t.Fatalf("opaque change=%#v", change)
		}
	}
}

func TestTextSnapshotHashesSensitiveAndExcludesGeneratedDirectories(t *testing.T) {
	root := t.TempDir()
	writeSnapshotFixture(t, root, ".env", "secret")
	writeSnapshotFixture(t, root, ".cache/generated.txt", "cache")
	writeSnapshotFixture(t, root, "src/main.go", "package main")
	fs, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := fs.CaptureTextSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Files) != 2 || !snapshot.Files["src/main.go"].Revertible || snapshot.Files[".env"].Revertible || snapshot.Files[".env"].Content != "" {
		t.Fatalf("snapshot=%#v", snapshot)
	}
	before := snapshot
	writeSnapshotFixture(t, root, ".env", "changed secret")
	after, err := fs.CaptureTextSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	changes := DiffTextSnapshots(before, after)
	if len(changes) != 1 || changes[0].Path != ".env" || changes[0].Revertible || changes[0].Original != "" || changes[0].Proposed != "" {
		t.Fatalf("sensitive changes=%#v", changes)
	}
}

func writeSnapshotFixture(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}
