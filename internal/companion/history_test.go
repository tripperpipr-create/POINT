package companion

import (
	"fmt"
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
	"local-agent-workbench/internal/workspace"
)

func TestSummarizeCompanionHistoryKeepsRolesAndBudget(t *testing.T) {
	history := make([]domain.CompanionMessage, 0, 30)
	for index := 0; index < 30; index++ {
		role := "user"
		if index%2 == 1 {
			role = "assistant"
		}
		history = append(history, domain.CompanionMessage{Role: role, Content: fmt.Sprintf("turn-%02d %s", index, strings.Repeat("detail ", 40))})
	}
	summary := summarizeCompanionHistory(history, 1200)
	if len(summary) > 1200 {
		t.Fatalf("summary escaped budget: %d", len(summary))
	}
	if !strings.Contains(summary, "Пользователь:") || !strings.Contains(summary, "Помощник:") {
		t.Fatalf("roles lost from summary: %q", summary)
	}
	if !strings.Contains(summary, "turn-00") {
		t.Fatalf("early conversation was not retained: %q", summary)
	}
}

func TestNormalizeCompanionSpeakerOnlyAllowsOwnedLogChat(t *testing.T) {
	if got := normalizeCompanionSpeaker("companion-log"); got != "companion-log" {
		t.Fatalf("log speaker=%q", got)
	}
	if got := normalizeCompanionSpeaker("master"); got != "companion" {
		t.Fatalf("untrusted speaker escaped isolation: %q", got)
	}
}

// Вопрос, оставшийся без ответа, не должен выглядеть как заданный дважды.
func TestCompanionHistoryNamesTheUnansweredTurn(t *testing.T) {
	messages := alternateCompanionRoles([]providers.Message{
		{Role: "system", Content: "правила"},
		{Role: "user", Content: "Разбери сбой сборки"},
		{Role: "user", Content: "А теперь посмотри тесты"},
	})
	// Человек остановил круг: вопрос сохранён, ответа нет. Две реплики подряд
	// Anthropic отвергает, а прочие модели отвечают на брошенный вопрос заново.
	if len(messages) != 4 {
		t.Fatalf("чередование не восстановлено: %#v", messages)
	}
	if messages[2].Role != "assistant" || messages[2].Content != companionUnansweredNote {
		t.Fatalf("молчание не названо молчанием: %#v", messages[2])
	}
	for i := 1; i < len(messages); i++ {
		if messages[i].Role == messages[i-1].Role {
			t.Fatalf("две реплики одной роли подряд на месте %d: %#v", i, messages)
		}
	}
}

// Два ответа подряд склеиваются: пометка о молчании тут была бы выдумкой.
func TestCompanionHistoryMergesNeighbourAnswers(t *testing.T) {
	messages := alternateCompanionRoles([]providers.Message{
		{Role: "user", Content: "Что в проекте?"},
		{Role: "assistant", Content: "Ветки: master и dev."},
		{Role: "assistant", Content: "Тесты зелёные."},
	})
	if len(messages) != 2 {
		t.Fatalf("соседние ответы не склеены: %#v", messages)
	}
	if !strings.Contains(messages[1].Content, "master") || !strings.Contains(messages[1].Content, "Тесты зелёные") {
		t.Fatalf("склейка потеряла текст: %q", messages[1].Content)
	}
}

// Локальный откат не должен возвращаться модели как её собственный ответ.
func TestModelHistoryHidesLocalReplies(t *testing.T) {
	history := []domain.CompanionMessage{
		{Role: "user", Content: "Какие ветки видишь у проекта"},
		{Role: "assistant", Mode: "deterministic", FallbackReason: "provider error: upstream", Content: "Сейчас открыт KeyCloack.php:22 (php)."},
		{Role: "user", Content: "У самого проекта какие ветки видишь"},
		{Role: "assistant", Mode: "deterministic", Content: "Сейчас открыт KeyCloack.php:22 (php)."},
		{Role: "assistant", Mode: "model", Content: "Настоящий ответ модели"},
		{Role: "assistant", Content: "Запись без режима: происхождение неизвестно"},
	}
	messages := modelHistory(history)
	if len(messages) != len(history) {
		t.Fatalf("порядок реплик поехал: %d вместо %d", len(messages), len(history))
	}
	for _, message := range messages {
		if strings.Contains(message.Content, "KeyCloack") {
			t.Fatalf("локальный текст ушёл модели как её ответ: %q", message.Content)
		}
	}
	if messages[1].Content != companionLocalReplyNote || messages[3].Content != companionLocalReplyNote {
		t.Fatalf("откат не помечен: %q / %q", messages[1].Content, messages[3].Content)
	}
	if messages[4].Content != "Настоящий ответ модели" {
		t.Fatalf("ответ модели подменён: %q", messages[4].Content)
	}
	if messages[5].Content != "Запись без режима: происхождение неизвестно" {
		t.Fatalf("старая запись без режима не должна трогаться: %q", messages[5].Content)
	}
}

// То же в резюме давней части диалога: там строка ужата и без подписи
// неотличима от настоящего ответа.
func TestSummarizeCompanionHistoryMarksLocalReplies(t *testing.T) {
	summary := summarizeCompanionHistory([]domain.CompanionMessage{
		{Role: "user", Content: "Какие ветки видишь"},
		{Role: "assistant", Mode: "deterministic", Content: "Сейчас открыт KeyCloack.php:22 (php)."},
	}, 1200)
	if strings.Contains(summary, "KeyCloack") {
		t.Fatalf("локальный текст попал в резюме: %q", summary)
	}
	if !strings.Contains(summary, "Point Core (модель не ответила)") {
		t.Fatalf("локальный ответ не помечен в резюме: %q", summary)
	}
	if strings.Contains(summary, "Помощник: Сейчас открыт") {
		t.Fatalf("локальный ответ выдан за реплику помощника: %q", summary)
	}
}

// Данные не подмешиваются в разговор молча.
//
// Поиск ранжирует по совпадению слов, и на вопрос про коммиты лучшим куском
// однажды оказался журнал обращений клиентов: имена, идентификаторы и тексты
// ушли в системное сообщение, а оттуда — на внешний шлюз. Прочитать такой файл
// помощник по-прежнему может, но по просьбе человека, а не по совпадению слов.
func TestRelevantCodeChunksKeepDataOutOfContext(t *testing.T) {
	chunks := []workspace.RelevantChunk{
		{IndexedChunk: workspace.IndexedChunk{Path: "docker/logs/logs/vote-feedback.csv"}, Score: 66, MatchedTokens: []string{"коммит", "изменение"}},
		{IndexedChunk: workspace.IndexedChunk{Path: "docker/nginx/ssl/site.crt"}, Score: 60, MatchedTokens: []string{"коммит", "изменение"}},
		{IndexedChunk: workspace.IndexedChunk{Path: "contribute/dev/db/backup.sql"}, Score: 58, MatchedTokens: []string{"коммит", "изменение"}},
		{IndexedChunk: workspace.IndexedChunk{Path: "docker/plat/js/app.js"}, Score: 55, MatchedTokens: []string{"коммит", "изменение"}},
	}
	kept, dropped := relevantCodeChunks(chunks, 3)
	if len(kept) != 1 || kept[0].Path != "docker/plat/js/app.js" {
		t.Fatalf("в контекст ушли не только исходники: %#v", kept)
	}
	if dropped != 3 {
		t.Fatalf("отсеяно %d кусков вместо трёх", dropped)
	}
	// Лучший по совпадению кусок сохраняется всегда — но не когда это данные.
	onlyData := []workspace.RelevantChunk{{IndexedChunk: workspace.IndexedChunk{Path: "docker/logs/logs/vote-feedback.csv"}, Score: 66, MatchedTokens: []string{"коммит"}}}
	if kept, _ := relevantCodeChunks(onlyData, 1); len(kept) != 0 {
		t.Fatalf("журнал ушёл в контекст как лучший кусок: %#v", kept)
	}
}

func TestDataChunkNamesDataByPathAndExtension(t *testing.T) {
	for path, data := range map[string]bool{
		"internal/app/app.go":               false,
		"docker/plat/js/app.js":             false,
		"docker/nginx/sites-available/site": false,
		// Каталог `log` бывает и пакетом, а `dump` — утилитой: исходник в них
		// остаётся исходником, и выкидывать его из контекста по имени папки
		// значит молча обеднять ответ.
		"pkg/log/logger.go":                  false,
		"internal/dump/dump.go":              false,
		"docker/logs/logs/vote-feedback.csv": true,
		"var/log/access.txt":                 true,
		"contribute/dev/db/backup.sql":       true,
		"docker/nginx/ssl/site.crt":          true,
		"config/.env":                        true,
		"docker/plat/js/app.js.bak":          true,
	} {
		if got := dataChunk(path); got != data {
			t.Fatalf("%s: данные=%v, ждали %v", path, got, data)
		}
	}
}
