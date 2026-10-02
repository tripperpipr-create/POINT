package sandbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"local-agent-workbench/internal/filepolicy"
	"local-agent-workbench/internal/osproc"
)

// File filtering and secret removal belong to the filtered-copy backend.

func copyFiltered(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		base := d.Name()
		if d.IsDir() {
			if shouldSkipDirectory(base) {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		// Junction на Windows — ModeIrregular, а не ModeSymlink: копировать
		// или читать его нельзя, чтение падает и роняет весь Diff.
		if d.Type()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			return nil
		}
		if shouldSkipFile(base) {
			return nil
		}
		return copyFile(path, filepath.Join(dst, rel))
	})
}

// Legacy delegates only after TestSharedPolicyPreservesEveryLegacySecretVariant
// proves the original SSH-key and secret filename checks are identical.
func shouldSkipFile(name string) bool {
	return filepolicy.SkipFileLegacy(name, runtime.GOOS == "windows")
}

func isSecretFile(name string) bool {
	return filepolicy.CopySensitiveLegacy(name, runtime.GOOS == "windows")
}

func shouldSkipDirectory(name string) bool {
	return filepolicy.SkipDirectory(filepolicy.Legacy, name, false)
}

// stripWorktreeSecrets убирает секреты из свежего worktree и прячет их
// удаление от Git. Удаление отслеживаемого файла Git покажет как изменение, и
// `git_diff` выдал бы модели его содержимое строками удаления; skip-worktree
// живёт в индексе этого worktree и проект человека не затрагивает. Помечаются
// только отслеживаемые файлы — неотслеживаемый секрет (например, созданный
// хуком post-checkout) Git пометить не может, — а пути идут через stdin:
// тысяча сертификатов-фикстур не помещается в командную строку.
func stripWorktreeSecrets(ctx context.Context, worktree string) error {
	removed, err := removeSensitiveFiles(worktree)
	if err != nil || len(removed) == 0 {
		return err
	}
	listed, err := osproc.CommandContext(ctx, "git", "-C", worktree, "ls-files", "-z").Output()
	if err != nil {
		return fmt.Errorf("git ls-files: %w", err)
	}
	// На Windows каталоги `Keys` и `keys` из индекса ложатся в один каталог
	// на диске: путь с диска сопоставляется с написанием из индекса без учёта
	// регистра, иначе файл остаётся без skip-worktree и его содержимое снова
	// видно в `git diff`.
	tracked := map[string]string{}
	folded := map[string]string{}
	for _, path := range strings.Split(string(listed), "\x00") {
		if path != "" {
			tracked[path] = path
			folded[strings.ToLower(path)] = path
		}
	}
	var input strings.Builder
	for _, path := range removed {
		indexPath, ok := tracked[path]
		if !ok && runtime.GOOS == "windows" {
			indexPath, ok = folded[strings.ToLower(path)]
		}
		if ok {
			input.WriteString(indexPath)
			input.WriteByte(0)
		}
	}
	if input.Len() == 0 {
		return nil
	}
	cmd := osproc.CommandContext(ctx, "git", "-C", worktree, "update-index", "-z", "--skip-worktree", "--stdin")
	cmd.Stdin = strings.NewReader(input.String())
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git update-index --skip-worktree: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// removeSensitiveFiles убирает из рабочей копии файлы секретов и называет
// их пути относительно root.
func removeSensitiveFiles(root string) ([]string, error) {
	var removed []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Секреты ищутся и в каталогах, которые копия пропускает целиком
			// (build, .vscode, .venv): worktree приносит их вместе с checkout.
			// Пропускается только сам Git и, ради скорости, node_modules.
			name := strings.ToLower(d.Name())
			if path != root && (name == ".git" || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			return nil
		}
		if !isSecretFile(d.Name()) {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if removeErr := os.Remove(path); removeErr != nil {
			return removeErr
		}
		removed = append(removed, filepath.ToSlash(rel))
		return nil
	})
	return removed, err
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	mode := fs.FileMode(0o644)
	if info, statErr := in.Stat(); statErr == nil && info.Mode().IsRegular() {
		mode = info.Mode().Perm()
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err = io.Copy(out, in); err != nil {
		return err
	}
	return os.Chmod(dst, mode)
}

func listTextFilesWithRules(root, rules string) (map[string]string, error) {
	result := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			if rel != "." && filepolicy.SkipDirectoryPath(rules, filepath.ToSlash(rel), false) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&(os.ModeSymlink|os.ModeIrregular) != 0 || shouldSkipFile(d.Name()) {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !isMostlyText(data) {
			return nil
		}
		result[rel] = string(data)
		return nil
	})
	return result, err
}

func isMostlyText(data []byte) bool {
	if len(data) == 0 {
		return true
	}
	sample := data
	if len(sample) > 2048 {
		sample = sample[:2048]
	}
	nul := 0
	for _, b := range sample {
		if b == 0 {
			nul++
		}
	}
	return nul == 0
}

func hashText(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
