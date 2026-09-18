package textutil

import "testing"

// Границы русского счёта: 11–14 ведут себя не как 1–4, и именно на них ломаются
// самодельные варианты правила.
func TestPluralCoversRussianBoundaries(t *testing.T) {
	cases := map[int]string{
		0: "файлов", 1: "файл", 2: "файла", 4: "файла", 5: "файлов",
		11: "файлов", 12: "файлов", 14: "файлов", 15: "файлов",
		21: "файл", 22: "файла", 25: "файлов",
		101: "файл", 111: "файлов", 112: "файлов", 121: "файл",
	}
	for number, want := range cases {
		if got := Plural(number, "файл", "файла", "файлов"); got != want {
			t.Fatalf("Plural(%d) = %q, ожидалось %q", number, got, want)
		}
	}
	if got := Count(1, "файл", "файла", "файлов"); got != "1 файл" {
		t.Fatalf("Count(1) = %q", got)
	}
	if got := Count(5, "набор", "набора", "наборов"); got != "5 наборов" {
		t.Fatalf("Count(5) = %q", got)
	}
}

// Два вопроса, которые раньше задавались одним словом containsFold.
func TestContainsFoldFindsSubstring(t *testing.T) {
	values := []string{"Сборка Проекта", "deploy"}
	if !ContainsFold(values, "проект") {
		t.Fatal("подстрока без учёта регистра обязана находиться")
	}
	if ContainsFold(values, "тест") {
		t.Fatal("чужая подстрока не находится")
	}
}

func TestEqualsAnyFoldNeedsWholeValue(t *testing.T) {
	values := []string{"  Сборка Проекта ", "deploy"}
	if !EqualsAnyFold(values, "сборка проекта") {
		t.Fatal("равенство без регистра и лишних пробелов")
	}
	if EqualsAnyFold(values, "проект") {
		t.Fatal("часть значения — не равенство")
	}
}

func TestFirstNonEmptyKeepsValueAsIs(t *testing.T) {
	if got := FirstNonEmpty("", "   ", "ошибка сборки\n", "запасной"); got != "ошибка сборки\n" {
		t.Fatalf("значение возвращается как есть: %q", got)
	}
	if got := FirstNonEmpty("", "  "); got != "" {
		t.Fatalf("нет ни одного непустого: %q", got)
	}
}
