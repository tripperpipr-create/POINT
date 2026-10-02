package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// 02.10.2026: поиск «/api/documents» в cf-bitrix читал 343 тысячи файлов
// томов Docker 15 минут и съел весь ход Мастера. Поиск — как у ripgrep: в
// репозитории только то, что git не игнорирует, без двоичных, и со своим
// сроком, после которого возвращается найденное.
func TestSearchRespectsGitIgnoreAndSkipsBinary(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	files := map[string]string{
		".gitignore":              "docker/data/\n*.log\n",
		"source/api.php":          "route('/api/documents', 'DocumentsController');\n",
		"source/new.php":          "// untracked but not ignored: /api/documents\n",
		"docker/data/mysql.ibd":   "/api/documents\n",
		"docker/logger.log":       "GET /api/documents 200\n",
		"docker/compose.yml":      "# /api/documents proxied\n",
		"source/blob.bin":         "\x00\x01/api/documents",
		"nested/front/.gitkeep":   "",
		"source/vendor/lib/x.php": "/api/documents in vendor\n",
	}
	for name, text := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "source/api.php", ".gitignore", "docker/compose.yml", "source/vendor/lib/x.php"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	fs, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	matches, truncated, stats, err := fs.SearchWithStats(context.Background(), SearchOptions{Query: "/api/documents"})
	if err != nil || truncated || stats.TimedOut {
		t.Fatalf("search: %v truncated=%v stats=%+v", err, truncated, stats)
	}
	found := map[string]bool{}
	for _, match := range matches {
		found[match.Path] = true
	}
	for _, want := range []string{"source/api.php", "source/new.php", "docker/compose.yml"} {
		if !found[want] {
			t.Fatalf("%s not found: %+v", want, matches)
		}
	}
	for _, unwanted := range []string{"docker/data/mysql.ibd", "docker/logger.log", "source/blob.bin"} {
		if found[unwanted] {
			t.Fatalf("%s must be skipped: %+v", unwanted, matches)
		}
	}
}

func TestSearchStopsOnItsBudgetWithoutError(t *testing.T) {
	root := t.TempDir()
	for index := 0; index < 50; index++ {
		if err := os.WriteFile(filepath.Join(root, "f"+string(rune('a'+index%26))+string(rune('a'+index/26))+".txt"), []byte("needle\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fs, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	_, truncated, stats, err := fs.SearchWithStats(context.Background(), SearchOptions{Query: "needle", TimeBudget: time.Nanosecond})
	if err != nil || !stats.TimedOut || !truncated {
		t.Fatalf("budget exhaustion must be a truncated result, not an error: err=%v truncated=%v stats=%+v", err, truncated, stats)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, _, err = fs.SearchWithStats(ctx, SearchOptions{Query: "needle"}); err == nil {
		t.Fatal("cancelled caller must still get an error")
	}
}
