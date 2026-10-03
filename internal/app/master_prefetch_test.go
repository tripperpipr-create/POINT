package app

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"local-agent-workbench/internal/workspace"
)

func TestMasterCodeTermsTakeOnlyCode(t *testing.T) {
	got := masterCodeTerms("Как работает метод /api/documents и DocumentService::list? Посмотри the README и app.handlers.Get")
	want := []string{"/api/documents", "DocumentService::list", "README", "app.handlers.Get"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("термины %q, ждали %q", got, want)
	}
	if terms := masterCodeTerms("Почему падает сборка?"); len(terms) != 0 {
		t.Fatalf("в русской реплике нашлись термины %q", terms)
	}
}

// Подсказка — только по готовому индексу: без него пусто и без сборки.
func TestMasterPrefetchUsesOnlyReadyIndex(t *testing.T) {
	root := t.TempDir()
	write := func(name, text string) {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("src/documents.php", "<?php\nclass DocumentsController {\n  public function listDocuments() { return route('/api/documents'); }\n}\n")
	write("src/other.php", "<?php\nfunction unrelated() {}\n")
	fs, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if block := masterPrefetchBlock(fs, "Как работает DocumentsController?"); block != "" {
		t.Fatalf("подсказка без готового индекса: %q", block)
	}
	if _, err = fs.BuildIndex(context.Background()); err != nil {
		t.Fatal(err)
	}
	block := masterPrefetchBlock(fs, "Как работает DocumentsController?")
	if !strings.Contains(block, "src/documents.php") || strings.Contains(block, "src/other.php") {
		t.Fatalf("подсказка = %q", block)
	}
}
