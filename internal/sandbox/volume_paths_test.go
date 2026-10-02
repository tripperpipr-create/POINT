package sandbox

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVolumeProcessPathsAllowTransientDirectoriesAndRejectEscape(t *testing.T) {
	root := t.TempDir()
	b := NewContainerBackend(t.TempDir())
	cwd, err := b.ResolveProcessDirectory(root, "node_modules/pkg")
	if err != nil {
		t.Fatal(err)
	}
	_, linux, err := volumeContainerPaths(root, cwd)
	if err != nil || linux != "/workspace/node_modules/pkg" {
		t.Fatalf("cwd=%q err=%v", linux, err)
	}
	for _, value := range []string{"../outside", filepath.Join(filepath.Dir(root), "outside")} {
		if _, err = b.ResolveProcessDirectory(root, value); err == nil {
			t.Fatalf("accepted escaped cwd %q", value)
		}
	}
	program, err := workspaceProgram(root, filepath.Join(root, "node_modules", ".bin", "tool"))
	if err != nil || program != "/workspace/node_modules/.bin/tool" {
		t.Fatalf("program=%q err=%v", program, err)
	}
	if program, err = workspaceProgram(root, "python3"); err != nil || program != "python3" {
		t.Fatalf("image program=%q err=%v", program, err)
	}
	if _, err = workspaceProgram(root, root); err == nil {
		t.Fatal("accepted directory as program")
	}
	if err = os.Symlink(t.TempDir(), filepath.Join(root, "link")); err != nil {
		t.Skip("symlink privilege unavailable", err)
	}
	if _, err = b.ResolveProcessDirectory(root, "link/child"); err == nil {
		t.Fatal("accepted symlink cwd")
	}
}
