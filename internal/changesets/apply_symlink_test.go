package changesets

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSafeTargetRejectsEscapingSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation may require Windows developer mode")
	}
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.Symlink(secret, filepath.Join(root, "escape.txt")); err != nil {
		t.Skip(err)
	}
	if _, err := safeTarget(root, "escape.txt"); err == nil {
		t.Fatal("expected symlink escape to fail")
	}
}
