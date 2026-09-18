package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"local-agent-workbench/internal/workspace"
)

func TestReadFileLineRangeAndPartialDigest(t *testing.T) {
	root := t.TempDir()
	body := "package main\n\nfunc One() {}\nfunc Two() {}\nfunc Three() {}\n"
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	fs, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	result := ReadFile{FS: fs}.Execute(t.Context(), json.RawMessage(`{"path":"main.go","startLine":3,"endLine":4}`))
	if !result.OK {
		t.Fatalf("read failed: %#v", result.Error)
	}
	var output struct {
		Path      string `json:"path"`
		Content   string `json:"content"`
		SHA256    string `json:"sha256"`
		Truncated bool   `json:"truncated"`
		StartLine int    `json:"startLine"`
		EndLine   int    `json:"endLine"`
		Hint      string `json:"hint"`
	}
	if err = json.Unmarshal(result.Output, &output); err != nil {
		t.Fatal(err)
	}
	if output.Path != "main.go" || output.StartLine != 3 || output.EndLine != 4 || output.SHA256 != "" || !output.Truncated {
		t.Fatalf("partial read=%#v", output)
	}
	if !strings.Contains(output.Content, "func One") || !strings.Contains(output.Content, "func Two") || strings.Contains(output.Content, "func Three") {
		t.Fatalf("line slice=%q", output.Content)
	}
	if !strings.Contains(output.Hint, "not a complete-file inspection") {
		t.Fatalf("missing partial hint: %q", output.Hint)
	}
}

func TestReadFileAcceptsAbsolutePathInsideWorkspace(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fs, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"path": filepath.Join(root, "main.go")})
	result := ReadFile{FS: fs}.Execute(t.Context(), payload)
	if !result.OK {
		t.Fatalf("absolute inside workspace failed: %#v", result.Error)
	}
	var output struct {
		Path string `json:"path"`
	}
	if err = json.Unmarshal(result.Output, &output); err != nil {
		t.Fatal(err)
	}
	if output.Path != "main.go" {
		t.Fatalf("path=%q", output.Path)
	}
}

func TestListFilesSubdirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "root.txt"), []byte("root\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "main.go"), []byte("package main\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fs, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	result := ListFiles{FS: fs}.Execute(t.Context(), json.RawMessage(`{"path":"src","maxDepth":2}`))
	if !result.OK {
		t.Fatalf("list failed: %#v", result.Error)
	}
	var tree []struct {
		Path string `json:"path"`
	}
	if err = json.Unmarshal(result.Output, &tree); err != nil {
		t.Fatal(err)
	}
	if len(tree) != 1 || tree[0].Path != "src/main.go" {
		t.Fatalf("tree=%#v", tree)
	}
}

func TestSliceNumberedLines(t *testing.T) {
	numbered := "     1 | a\n     2 | b\n     3 | c\n"
	got, start, end, partial := sliceNumberedLines(numbered, 2, 2)
	if !partial || start != 2 || end != 2 || !strings.Contains(got, "b") || strings.Contains(got, " | a") {
		t.Fatalf("got=%q start=%d end=%d partial=%v", got, start, end, partial)
	}
	full, start, end, partial := sliceNumberedLines(numbered, 0, 0)
	if partial || start != 1 || end != 3 || full != numbered {
		t.Fatalf("full=%q start=%d end=%d partial=%v", full, start, end, partial)
	}
}
