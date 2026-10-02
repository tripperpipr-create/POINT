package workspace

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"local-agent-workbench/internal/filepolicy"
	"local-agent-workbench/internal/sandboxsync"
)

func TestManifestAndBindAuditParity(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"text.txt", "binary.bin", "large.txt"} {
		data := []byte("before\r\n")
		if name == "binary.bin" {
			data = []byte{0, 255, 1}
		}
		if name == "large.txt" {
			data = bytes.Repeat([]byte("x"), maxSnapshotFileBytes+1)
		}
		if err := os.WriteFile(filepath.Join(root, name), data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	fs, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	fs.FileRules = filepolicy.Current
	before, err := fs.CaptureTextSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "text.txt"), []byte("после\r\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(root, "binary.bin")); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "large.txt"), bytes.Repeat([]byte("y"), maxSnapshotFileBytes+1), 0644); err != nil {
		t.Fatal(err)
	}
	bind, err := fs.CaptureTextSnapshotFrom(context.Background(), &before)
	if err != nil {
		t.Fatal(err)
	}
	m, err := sandboxsync.Scan(context.Background(), root, filepolicy.Current)
	if err != nil {
		t.Fatal(err)
	}
	volume, err := fs.CaptureManifestSnapshot(context.Background(), m, &before)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(DiffTextSnapshots(before, bind), DiffTextSnapshots(before, volume)) {
		t.Fatal("audit bytes or non-revertible flags differ")
	}
}
