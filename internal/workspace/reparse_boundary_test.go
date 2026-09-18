package workspace

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Выход из рабочей копии через точку повторного разбора.
//
// Тест на симлинк рядом пропускается на Windows целиком, и там граница
// оставалась непроверенной. Между тем junction создаётся **без прав
// администратора**, а filepath.EvalSymlinks его не разворачивает: он возвращает
// сам путь ссылки. Проверка вложенности видела путь «внутри проекта», запись
// шла по ссылке, и файл появлялся за пределами рабочей копии — при том что
// Write возвращал ошибку, то есть отказ был мнимым.
//
// Проверяем не формулировку ошибки, а мир: за пределами копии не должно
// появиться ничего.
func TestReparsePointCannotEscapeWorkspace(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("junction — механизм Windows; для симлинков есть TestResolveRejectsEscapingSymlink")
	}
	root, outside := t.TempDir(), t.TempDir()
	link := filepath.Join(root, "escape")
	if err := exec.Command("cmd", "/c", "mklink", "/J", link, outside).Run(); err != nil {
		t.Skip("junction создать не удалось")
	}
	if strings.HasPrefix(outside, root) {
		t.Fatalf("проба построена неверно: %q внутри %q", outside, root)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("секрет"), 0o600); err != nil {
		t.Fatal(err)
	}

	fs, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}

	// Защита от холостого хода: если ссылка не ведёт наружу, проверять нечего.
	if _, statErr := os.Stat(filepath.Join(link, "secret.txt")); statErr != nil {
		t.Skipf("ссылка не ведёт к внешнему файлу: %v", statErr)
	}

	if _, err = fs.Resolve("escape/secret.txt", true); !errors.Is(err, ErrOutsideWorkspace) {
		t.Errorf("Resolve пропустил путь через ссылку наружу, ошибка: %v", err)
	}
	if _, err = fs.Read("escape/secret.txt", false); !errors.Is(err, ErrOutsideWorkspace) {
		t.Errorf("Read прошёл по ссылке наружу, ошибка: %v", err)
	}
	if _, err = fs.Write("escape/written.txt", "агент записал наружу"); !errors.Is(err, ErrOutsideWorkspace) {
		t.Errorf("Write прошёл по ссылке наружу, ошибка: %v", err)
	}
	if _, err = fs.BuildIndex(context.Background()); err != nil {
		t.Fatal(err)
	}
	if result := fs.LookupIndex("секрет", 8); len(result.Hits) != 0 {
		t.Fatalf("индекс прошёл по junction за границу workspace: %#v", result.Hits)
	}
	if _, statErr := os.Stat(filepath.Join(outside, "written.txt")); statErr == nil {
		t.Fatalf("файл создан за пределами рабочей копии: %s", filepath.Join(outside, "written.txt"))
	}
}
