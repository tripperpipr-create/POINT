package companion

import (
	"testing"

	"local-agent-workbench/internal/workspace"
)

func chunkAt(path string, score int, matched ...string) workspace.RelevantChunk {
	chunk := workspace.RelevantChunk{Score: score, MatchedTokens: matched}
	chunk.Path = path
	return chunk
}

// Индекс возвращает лучшие совпадения, но «лучшее» не значит «подходящее»: на
// вопрос про ветки и теги в контекст приезжал кусок backup.sql, совпавший одним
// случайным словом. Он стоит токенов и уводит модель к чужому файлу.
func TestRelevantCodeChunksDropsOffTopicTail(t *testing.T) {
	chunks := []workspace.RelevantChunk{
		chunkAt("internal/git/branches.go", 90, "ветки", "теги", "git"),
		chunkAt("internal/git/tags.go", 60, "теги", "git"),
		chunkAt("contribute/dev/db/backup.sql", 12, "проект"),
	}
	kept, dropped := relevantCodeChunks(chunks, 5)
	if dropped != 1 {
		t.Fatalf("отсеяно %d кусков вместо одного: %#v", dropped, kept)
	}
	for _, chunk := range kept {
		if chunk.Path == "contribute/dev/db/backup.sql" {
			t.Fatal("случайное совпадение осталось в контексте")
		}
	}
}

// Одно слово из длинного вопроса — не повод показывать файл, даже если счёт
// рядом с лучшим: длинный вопрос совпадает случайно чаще короткого.
func TestRelevantCodeChunksDropsLonelyWordMatch(t *testing.T) {
	chunks := []workspace.RelevantChunk{
		chunkAt("internal/billing/invoice.go", 70, "счёт", "оплата", "инвойс"),
		chunkAt("docs/changelog.md", 64, "оплата"),
	}
	kept, dropped := relevantCodeChunks(chunks, 4)
	if dropped != 1 || len(kept) != 1 {
		t.Fatalf("одиночное совпадение не отсеяно: kept=%#v dropped=%d", kept, dropped)
	}
}

// Когда сильного совпадения нет вовсе, лучший кусок остаётся: слабая догадка
// полезнее пустоты там, где вопрос всё-таки про код.
func TestRelevantCodeChunksKeepsBestWhenAllWeak(t *testing.T) {
	chunks := []workspace.RelevantChunk{
		chunkAt("internal/app/state.go", 10, "состояние"),
		chunkAt("internal/app/view.go", 8, "вид"),
	}
	kept, dropped := relevantCodeChunks(chunks, 4)
	if len(kept) != 1 || dropped != 1 {
		t.Fatalf("лучший кусок не сохранён: kept=%#v dropped=%d", kept, dropped)
	}
	if kept[0].Path != "internal/app/state.go" {
		t.Fatalf("сохранён не лучший кусок: %q", kept[0].Path)
	}
}

// Короткий вопрос совпадает одним словом законно: «покажи main.go» — это одно
// слово и точное попадание.
func TestRelevantCodeChunksKeepsShortQueryMatches(t *testing.T) {
	chunks := []workspace.RelevantChunk{
		chunkAt("cmd/server/main.go", 80, "main"),
		chunkAt("cmd/point-db/main.go", 76, "main"),
	}
	kept, dropped := relevantCodeChunks(chunks, 2)
	if dropped != 0 || len(kept) != 2 {
		t.Fatalf("короткий запрос потерял совпадения: kept=%#v dropped=%d", kept, dropped)
	}
}
