package sandbox

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// TreeDigest — отпечаток того дерева, которое унаследует следующая песочница:
// те же каталоги и файлы, что переносит copyFiltered, с содержимым каждого
// файла. Две песочницы с одним отпечатком неразличимы для команды, запущенной
// в их чистой копии, — на этом стоит переиспользование результата проверки
// приёмкой (internal/app/stage_verification.go).
//
// Фильтр общий с копированием намеренно: отпечаток, считающий node_modules
// или секреты, расходился бы с тем, что приёмка действительно видит. Пустые
// каталоги входят в отпечаток — команда может зависеть от их наличия.
func TreeDigest(root string) (string, error) {
	hash := sha256.New()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if shouldSkipDirectory(d.Name()) {
				return filepath.SkipDir
			}
			hash.Write([]byte("d\x00" + rel + "\x00"))
			return nil
		}
		if d.Type()&(os.ModeSymlink|os.ModeIrregular) != 0 || shouldSkipFile(d.Name()) {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		content := sha256.New()
		_, err = io.Copy(content, file)
		_ = file.Close()
		if err != nil {
			return err
		}
		hash.Write([]byte("f\x00" + rel + "\x00"))
		hash.Write(content.Sum(nil))
		return nil
	})
	if err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

// CopyCarried делает чистую копию песочницы — то же дерево, что получил бы
// следующий этап. Проверка перед приёмкой гоняет критерии в ней, а не в
// рабочей папке агента с его node_modules и сборкой.
func CopyCarried(src, dst string) error { return copyFiltered(src, dst) }
