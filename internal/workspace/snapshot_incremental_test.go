package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Файл, изменённый задолго до прежнего снимка и не тронутый с тех пор, берётся
// из прежнего снимка без чтения. Проверяется это подменой содержимого того
// же размера с восстановленным mtime: перечитанный снимок увидел бы новое.
func TestIncrementalSnapshotReusesUnchangedFiles(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "a.txt")
	if err := os.WriteFile(path, []byte("old!"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	fs, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	first, err := fs.CaptureTextSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("new!"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err = os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	second, err := fs.CaptureTextSnapshotFrom(context.Background(), &first)
	if err != nil {
		t.Fatal(err)
	}
	if second.Files["a.txt"].Content != "old!" {
		t.Fatalf("unchanged metadata must reuse the previous content, got %q", second.Files["a.txt"].Content)
	}
	fresh, _ := fs.CaptureTextSnapshot(context.Background())
	if fresh.Files["a.txt"].Content != "new!" {
		t.Fatal("a fresh snapshot must read the file")
	}
}

// Файл, записанный почти одновременно с прежним снимком, перечитывается, даже
// если размер и mtime совпали: время файловой системы бывает грубым.
func TestIncrementalSnapshotRereadsRacyFiles(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "a.txt")
	if err := os.WriteFile(path, []byte("old!"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	fs, _ := Open(root)
	first, _ := fs.CaptureTextSnapshot(context.Background())
	if err := os.WriteFile(path, []byte("new!"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	second, _ := fs.CaptureTextSnapshotFrom(context.Background(), &first)
	if second.Files["a.txt"].Content != "new!" {
		t.Fatalf("racy file must be re-read, got %q", second.Files["a.txt"].Content)
	}
	if changes := DiffTextSnapshots(first, second); len(changes) != 1 {
		t.Fatalf("changes=%+v", changes)
	}
}

func TestIncrementalSnapshotSeesSizeChangeAndDeletion(t *testing.T) {
	root := t.TempDir()
	old := time.Now().Add(-time.Hour)
	for name, content := range map[string]string{"keep.txt": "same", "grow.txt": "a", "gone.txt": "bye"} {
		path := filepath.Join(root, name)
		_ = os.WriteFile(path, []byte(content), 0o644)
		_ = os.Chtimes(path, old, old)
	}
	fs, _ := Open(root)
	first, _ := fs.CaptureTextSnapshot(context.Background())
	_ = os.WriteFile(filepath.Join(root, "grow.txt"), []byte("abc"), 0o644)
	_ = os.Remove(filepath.Join(root, "gone.txt"))
	second, _ := fs.CaptureTextSnapshotFrom(context.Background(), &first)
	changes := DiffTextSnapshots(first, second)
	if len(changes) != 2 || second.Files["grow.txt"].Content != "abc" || second.Files["keep.txt"].Content != "same" {
		t.Fatalf("changes=%+v", changes)
	}
}
