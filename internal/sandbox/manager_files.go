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

	"local-agent-workbench/internal/osproc"
	"local-agent-workbench/internal/workspace"
)

// File filtering and secret removal belong to the filtered-copy backend.

var skippedDirectories = map[string]struct{}{
	// vendor/ is intentionally NOT skipped: Composer PHP stages inherit the
	// bootstrap tip and must keep installed packages (autoload + libraries).
	".git": {}, "node_modules": {}, ".cache": {}, "dist": {}, "build": {},
	".venv": {}, "venv": {}, "__pycache__": {}, ".idea": {}, ".vscode": {},
}

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

func shouldSkipFile(name string) bool {
	lower := strings.ToLower(name)
	// Linked Git worktrees use a .git *file* instead of a directory. Copying it
	// would make an otherwise isolated child sandbox point back to the parent
	// worktree's administrative directory.
	if lower == ".git" {
		return true
	}
	if strings.HasSuffix(lower, ".exe") || strings.HasSuffix(lower, ".dll") || strings.HasSuffix(lower, ".so") {
		return true
	}
	if strings.HasSuffix(lower, ".png") || strings.HasSuffix(lower, ".jpg") || strings.HasSuffix(lower, ".jpeg") {
		return true
	}
	return isSecretFile(name)
}

// isSecretFile — файл секретов, которому не место в рабочей копии. Список
// тот же, что закрывает файлы для чтения агентом (собственный узкий список
// пропускал .npmrc, credentials и ключи SSH), но ключи SSH узнаются по имени
// целиком: подстрока задела бы исходники вроде id_rsa_parser.go, и сборка
// в песочнице ломалась бы.
func isSecretFile(name string) bool {
	base := strings.ToLower(name)
	if runtime.GOOS == "windows" {
		base = strings.TrimRight(base, ". ")
	}
	// Ключ SSH узнаётся по подстроке (id_rsa_deploy, deploy_id_rsa,
	// id_ed25519_sk). Ключ с любым расширением (id_rsa.pub, id_rsa.txt) —
	// тоже ключ; исключение для исходников действует, только когда ключ —
	// часть более длинного имени: id_rsa_parser.go — код, а не ключ.
	for _, key := range []string{"id_rsa", "id_ed25519", "id_ecdsa", "id_dsa"} {
		if !strings.Contains(base, key) {
			continue
		}
		stem := strings.TrimSuffix(base, filepath.Ext(base))
		if stem == key || stem == key+"_sk" {
			return true
		}
		return !hasSourceExtension(base)
	}
	return workspace.IsSensitive(name)
}

var sourceExtensions = map[string]bool{
	".go": true, ".py": true, ".js": true, ".mjs": true, ".cjs": true, ".ts": true, ".tsx": true, ".jsx": true,
	".java": true, ".kt": true, ".rs": true, ".rb": true, ".php": true, ".cs": true, ".c": true, ".h": true,
	".cpp": true, ".hpp": true, ".swift": true, ".sh": true, ".ps1": true, ".md": true, ".txt": true,
	".json": true, ".yaml": true, ".yml": true, ".toml": true, ".html": true, ".css": true, ".sql": true,
}

func hasSourceExtension(name string) bool {
	return sourceExtensions[strings.ToLower(filepath.Ext(name))]
}

func shouldSkipDirectory(name string) bool {
	_, skip := skippedDirectories[strings.ToLower(name)]
	return skip
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

func listTextFiles(root string) (map[string]string, error) {
	result := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if shouldSkipDirectory(d.Name()) {
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
