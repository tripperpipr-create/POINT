package textutil

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBoundedCountsRunesNotBytes(t *testing.T) {
	if got := Bounded("привет", 6); got != "привет" {
		t.Fatalf("строка ровно по лимиту не обрезается: %q", got)
	}
	if got := Bounded("привет", 3); got != "при…" {
		t.Fatalf("обрезка по рунам: %q", got)
	}
	if got := Bounded("привет", 0); got != "привет" {
		t.Fatalf("нулевой лимит означает «без ограничения»: %q", got)
	}
}

func TestBoundedPlainKeepsNoEllipsis(t *testing.T) {
	if got := BoundedPlain("привет", 3); got != "при" {
		t.Fatalf("без многоточия: %q", got)
	}
}

// Байтовая резка русского текста разрывала руну пополам: получатель видел
// невалидный UTF-8. Лимит остаётся байтовым — меняется только граница.
func TestBoundedBytesNeverSplitsARune(t *testing.T) {
	value := strings.Repeat("я", 100) // две байты на руну
	for limit := 0; limit <= 2*utf8.RuneLen('я')*100; limit++ {
		got := BoundedBytes(value, limit)
		if !utf8.ValidString(got) {
			t.Fatalf("лимит %d дал невалидный UTF-8: %q", limit, got)
		}
		if len(got) > limit {
			t.Fatalf("лимит %d превышен: %d байт", limit, len(got))
		}
	}
	if got := BoundedBytes("яя", 3); got != "я" {
		t.Fatalf("нечётная граница отступает к началу руны: %q", got)
	}
	if got := BoundedBytes("abc", 10); got != "abc" {
		t.Fatalf("короткая строка не трогается: %q", got)
	}
}
