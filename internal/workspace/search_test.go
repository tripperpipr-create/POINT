package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSearchFixture(t *testing.T) *FS {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"src/app.go":        "package app\n\nfunc Run() error {\n\treturn nil\n}\n",
		"src/deep/util.go":  "package deep\n\nfunc Helper() {}\n",
		"web/index.ts":      "export function Run() {}\n",
		"min.js":            strings.Repeat("x", 70*1024) + "needle\n",
		"node_modules/x.js": "func Run() {}\n",
	}
	for name, content := range files {
		full := filepath.Join(root, filepath.FromSlash(name))
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fs, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	return fs
}

func TestSearchWithRegexContextAndGlob(t *testing.T) {
	fs := writeSearchFixture(t)
	matches, truncated, err := fs.SearchWith(context.Background(), SearchOptions{Query: `func \w+\(\)`, Regex: true, Context: 1, Glob: "**/*.go"})
	if err != nil || truncated {
		t.Fatalf("err=%v truncated=%v", err, truncated)
	}
	if len(matches) != 2 {
		t.Fatalf("matches=%+v", matches)
	}
	for _, match := range matches {
		if !strings.HasSuffix(match.Path, ".go") || len(match.Before) != 1 {
			t.Fatalf("match=%+v", match)
		}
	}
	scoped, _, _ := fs.SearchWith(context.Background(), SearchOptions{Query: "func", Path: "src/deep"})
	if len(scoped) != 1 || scoped[0].Path != "src/deep/util.go" {
		t.Fatalf("scoped=%+v", scoped)
	}
}

func TestSearchWithReportsTruncationAndLongLines(t *testing.T) {
	fs := writeSearchFixture(t)
	matches, truncated, err := fs.SearchWith(context.Background(), SearchOptions{Query: "package", MaxResults: 1})
	if err != nil || !truncated || len(matches) != 1 {
		t.Fatalf("matches=%d truncated=%v err=%v", len(matches), truncated, err)
	}
	long, _, _ := fs.SearchWith(context.Background(), SearchOptions{Query: "needle"})
	if len(long) != 1 || len([]rune(long[0].Text)) > maxMatchLineRunes+1 {
		t.Fatalf("long line not found or not clipped: %d", len(long))
	}
}

func TestNumberedReadKeepsLinesLongerThanScannerBuffer(t *testing.T) {
	fs := writeSearchFixture(t)
	content, err := fs.Read("min.js", true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content.Numbered, "needle") {
		t.Fatal("numbered read dropped a long line")
	}
}
