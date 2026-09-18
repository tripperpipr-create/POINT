package app

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestIDEFileSaveAndTerminal(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("old\n"), 0600); err != nil {
		t.Fatal(err)
	}
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(nil)
	if _, err = application.OpenWorkspace(root); err != nil {
		t.Fatal(err)
	}
	if _, err = application.SaveFile("note.txt", "новый текст\n"); err != nil {
		t.Fatal(err)
	}
	read, err := application.ReadFile("note.txt")
	if err != nil || read.Content != "новый текст\n" {
		t.Fatalf("saved content mismatch: %#v, %v", read, err)
	}

	command := "printf terminal-ok"
	if runtime.GOOS == "windows" {
		command = "echo terminal-ok"
	}
	result, err := application.RunTerminalCommand(TerminalCommandRequest{Command: command, TimeoutSeconds: 10})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 || !strings.Contains(result.Stdout, "terminal-ok") {
		t.Fatalf("unexpected terminal result: %#v", result)
	}
}
