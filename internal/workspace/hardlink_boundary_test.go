package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// Жёсткая ссылка не должна выносить содержимое за пределы проекта.
//
// Проверка пути здесь бессильна по построению: путь честно лежит внутри
// рабочей копии, а данные — снаружи, потому что это один и тот же файл на
// диске. Защищает другое: запись идёт во временный файл рядом и переименование
// поверх, а это подменяет запись в каталоге и разрывает ссылку — внешний файл
// сохраняет прежнее содержимое.
//
// Защита побочная: она следует из способа записи, а не из проверки. Поэтому её
// нужно закрепить — «оптимизация» до записи на месте (os.WriteFile по abs)
// откроет обход молча, и ни один другой тест этого не заметит.
func TestHardlinkCannotCarryWritesOutside(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	const original = "исходное содержимое"
	if err := os.WriteFile(secret, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "inside.txt")
	if runtime.GOOS == "windows" {
		if err := exec.Command("cmd", "/c", "mklink", "/H", link, secret).Run(); err != nil {
			t.Skip("жёсткая ссылка недоступна:", err)
		}
	} else if err := os.Link(secret, link); err != nil {
		t.Skip("жёсткая ссылка недоступна:", err)
	}

	// Защита от холостого хода: ссылка обязана быть настоящей, иначе проверка
	// ниже пройдёт просто потому, что связи между файлами нет.
	if err := os.WriteFile(secret, []byte("проверка связи"), 0o600); err != nil {
		t.Fatal(err)
	}
	probe, err := os.ReadFile(link)
	if err != nil || string(probe) != "проверка связи" {
		t.Skipf("ссылка не разделяет содержимое (%q, %v) — проверять нечего", string(probe), err)
	}
	if err := os.WriteFile(secret, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	fs, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fs.Write("inside.txt", "агент переписал наружу"); err != nil {
		t.Fatalf("запись внутри проекта не прошла: %v", err)
	}

	after, err := os.ReadFile(secret)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != original {
		t.Fatalf("содержимое внешнего файла переписано через жёсткую ссылку: %q", string(after))
	}
}
