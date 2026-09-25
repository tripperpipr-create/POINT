package workspace

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// Windows открывает `.git.` и короткое имя `GIT~1` как `.git`. Проверка
// исключённых каталогов по присланному имени их пропускала, и запись хука в
// `.git/hooks` исполнялась бы следующим `git commit` на машине человека.
func TestResolveRefusesGitDirectoryAliases(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Win32 path aliases")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git", "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "config"), []byte("[core]"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("SECRET=1"), 0o644); err != nil {
		t.Fatal(err)
	}
	fs, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{".git./hooks/pre-commit", ".GIT/hooks/pre-commit", ".git /config", ".git::$INDEX_ALLOCATION/config"} {
		if _, err := fs.Resolve(path, true); err == nil {
			t.Fatalf("%q открыл служебный каталог Git", path)
		}
	}
	if _, statErr := os.Stat(filepath.Join(root, "GIT~1")); statErr == nil {
		for _, path := range []string{"GIT~1/hooks/pre-commit", "GIT~1/config"} {
			if _, err := fs.Resolve(path, true); !errors.Is(err, ErrExcluded) {
				t.Fatalf("короткое имя %q открыло .git: %v", path, err)
			}
		}
	}
	if _, err := fs.Read(".env.", false); !errors.Is(err, ErrSensitive) {
		t.Fatalf("секрет прочитан через хвостовую точку: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "ENV~1")); statErr == nil {
		if _, err := fs.Read("ENV~1", false); !errors.Is(err, ErrSensitive) {
			t.Fatalf("секрет прочитан через короткое имя: %v", err)
		}
	}
}

// Junction внутри проекта, ведущий в `.git`, открывает то же, что `.git`.
func TestResolveRefusesJunctionIntoGitDirectory(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("junction is a Windows reparse point")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git", "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(root, "link"), filepath.Join(root, ".git")).CombinedOutput(); err != nil {
		t.Skipf("mklink /J unavailable: %v (%s)", err, out)
	}
	fs, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Resolve("link/hooks/pre-commit", true); err == nil {
		t.Fatal("junction в .git открыл служебный каталог")
	}
}
