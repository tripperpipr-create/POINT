package changesets

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Все записи в файлы проекта идут одним способом.
//
// Дважды подряд в этом файле находилась одна и та же болезнь, и оба раза —
// в ветке, куда никто не смотрел:
//
//   - ApplyWithContent склеивал путь напрямую (filepath.Join с корнем проекта)
//     вместо safeTarget: «..», абсолютный путь и ссылка наружу не отвергались;
//   - обе ветки писали через os.WriteFile на месте, а не во временный файл с
//     переименованием, и запись уходила по жёсткой ссылке за пределы проекта.
//
// Поведение закрыто отдельными тестами. Эта проверка — про форму: она ловит
// возврат самих приёмов раньше, чем кто-нибудь напишет третью ветку применения
// и снова обойдёт охрану. Поведенческий тест поймал бы это только если автор
// не забудет добавить к новой ветке свой случай.
func TestApplyUsesGuardedWritersOnly(t *testing.T) {
	source, err := os.ReadFile("apply.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)

	// Комментарии не в счёт: объяснение починки называет убранные приёмы.
	withoutComments := regexp.MustCompile(`(?m)^\s*//.*$`).ReplaceAllString(text, "")

	// Защита от холостого хода: если файл вдруг перестанет содержать сами
	// охраняемые вызовы, проверка ниже пройдёт на пустом месте.
	for _, required := range []string{"safeTarget(", "writeFileAtomically("} {
		if !strings.Contains(withoutComments, required) {
			t.Fatalf("в apply.go нет %s — проверка прошла бы вхолостую", required)
		}
	}

	forbidden := []struct {
		шаблон string
		почему string
	}{
		{
			шаблон: "os.WriteFile(",
			почему: "запись на месте идёт по жёсткой ссылке за пределы проекта; используйте writeFileAtomically",
		},
		{
			шаблон: "filepath.Join(workspacePath",
			почему: "путь из набора приходит от агента; используйте safeTarget, иначе «..» и ссылки не отвергаются",
		},
	}
	for _, item := range forbidden {
		if strings.Contains(withoutComments, item.шаблон) {
			t.Errorf("в apply.go вернулось %q: %s", item.шаблон, item.почему)
		}
	}
}
