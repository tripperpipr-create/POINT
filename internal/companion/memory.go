// Память помощника: что он знает о проекте между разговорами.
//
// Файл отделён от разговора нарочно. Разговор решает, что ответить сейчас;
// память живёт дольше него и возвращается контекстом в каждый следующий
// разговор, поэтому правила записи стоят отдельно и читаются целиком.
package companion

import (
	"context"
	"strconv"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/observability"
	"local-agent-workbench/internal/security"
)

// Просьба запомнить — точный сигнал, а не догадка о важности.
//
// Сжатие разговора не выдумывает, что из сказанного важно: догадка о важности
// ошибается молча, а память живёт долго и подмешивается в каждый следующий
// разговор. Зато прямая просьба человека однозначна, и терять её вместе с окном
// разговора нельзя — она и уходит в память помощника, откуда возвращается
// контекстом в следующие разговоры.
// Столько записей помещается в контекст разговора: столько же имеет смысл
// держать закреплёнными. Значение берётся из отбора памяти, а не повторяется
// числом — второй экземпляр разошёлся бы с первым молча.
const companionPinnedMemoryLimit = companionMemoryContextLimit

var companionMemoryPrefixes = []string{
	"запомни:", "запомни,", "запомни ", "запиши:", "запиши,", "запиши ",
	"учти на будущее:", "учти на будущее,", "учти на будущее ",
	"на будущее:", "remember:", "remember that ",
}

// Три просьбы о памяти, которые человек формулирует словами, а не мышью:
// запомнить, забыть и посмотреть, что помнится. Все три однозначны, поэтому
// ходить с ними к модели незачем — она добавила бы задержку и возможность
// ошибиться там, где ошибаться нечем.
func companionMemoryIntent(message string) string {
	if _, ok := companionMemoryRequest(message); ok {
		return "remember"
	}
	if _, ok := companionForgetRequest(message); ok {
		return "forget"
	}
	if companionMemoryListRequest(message) {
		return "list"
	}
	return ""
}

func (s Service) handleMemoryIntent(ctx context.Context, workspaceID, intent, message string) (ChatResponse, error) {
	switch intent {
	case "remember":
		fact, _ := companionMemoryRequest(message)
		return s.rememberFact(ctx, workspaceID, fact)
	case "forget":
		query, _ := companionForgetRequest(message)
		return s.forgetFact(ctx, workspaceID, query)
	default:
		return s.describeMemory(ctx, workspaceID)
	}
}

var companionForgetPrefixes = []string{
	"забудь:", "забудь,", "забудь про ", "забудь о ", "забудь ",
	"удали из памяти:", "удали из памяти ", "убери из памяти ",
	"forget:", "forget that ", "forget ",
}

func companionForgetRequest(message string) (string, bool) {
	trimmed := strings.TrimSpace(message)
	lower := strings.ToLower(trimmed)
	for _, prefix := range companionForgetPrefixes {
		if !strings.HasPrefix(lower, prefix) {
			continue
		}
		query := strings.TrimSpace(trimmed[len(prefix):])
		query = strings.TrimSpace(strings.TrimLeft(query, ":,-— "))
		if len([]rune(query)) < 3 {
			return "", false
		}
		return query, true
	}
	return "", false
}

var companionMemoryQuestions = []string{
	"что ты помнишь", "что помнишь", "что у тебя в памяти", "что в твоей памяти",
	"покажи память", "покажи, что помнишь", "what do you remember",
}

func companionMemoryListRequest(message string) bool {
	lower := strings.ToLower(strings.TrimSpace(message))
	for _, question := range companionMemoryQuestions {
		if strings.Contains(lower, question) {
			return true
		}
	}
	return false
}

// Что помнится по этому проекту. Ответ строится из записей, а не из пересказа
// модели: спрашивают про содержимое хранилища, и пересказывать его — значит
// однажды пересказать неверно.
func (s Service) describeMemory(ctx context.Context, workspaceID string) (ChatResponse, error) {
	memories, err := s.Store.ListMemories(ctx, workspaceID)
	if err != nil {
		return ChatResponse{}, err
	}
	lines := make([]string, 0, 12)
	for _, memory := range memories {
		if memory.Kind != domain.MemoryCompanion && memory.Kind != domain.MemoryProject {
			continue
		}
		mark := "·"
		if memory.Pinned {
			mark = "★"
		}
		lines = append(lines, mark+" "+trim(strings.Join(strings.Fields(memory.Content), " "), 300)+" (источник: "+trim(memory.Source, 60)+")")
		if len(lines) >= 12 {
			break
		}
	}
	if len(lines) == 0 {
		return ChatResponse{
			Reply: "По этому проекту я пока ничего не запомнил. Скажите «запомни: …» — и факт будет со мной в следующих разговорах.",
			Level: "suggestion", Mode: "local",
		}, nil
	}
	return ChatResponse{
		Reply: "Вот что я помню по этому проекту (★ — закреплённое, попадает в каждый разговор):\n" + strings.Join(lines, "\n") + "\n\nУдалить любую запись можно словами «забудь …» или в разделе «Память».",
		Level: "suggestion", Mode: "local",
	}, nil
}

// Забывание — такое же решение человека, как и запоминание, и делается его
// словами. Запись ищется по вхождению, но снимается только когда найдена одна:
// удалить не то, что просили, хуже, чем переспросить.
func (s Service) forgetFact(ctx context.Context, workspaceID, query string) (ChatResponse, error) {
	memories, err := s.Store.ListMemories(ctx, workspaceID)
	if err != nil {
		return ChatResponse{}, err
	}
	needle := strings.ToLower(strings.Join(strings.Fields(query), " "))
	matched := make([]domain.MemoryRecord, 0, 3)
	for _, memory := range memories {
		if memory.Kind != domain.MemoryCompanion {
			continue
		}
		if strings.Contains(strings.ToLower(strings.Join(strings.Fields(memory.Content), " ")), needle) {
			matched = append(matched, memory)
		}
	}
	switch len(matched) {
	case 0:
		return ChatResponse{
			Reply: "Такого в моей памяти нет — забывать нечего. Посмотреть весь список можно словами «что ты помнишь».",
			Level: "suggestion", Mode: "local",
		}, nil
	case 1:
		if err = s.Store.DeleteMemory(ctx, workspaceID, matched[0].ID); err != nil {
			return ChatResponse{}, err
		}
		return ChatResponse{
			Reply: "Забыл: «" + trim(matched[0].Content, 300) + "».",
			Level: "suggestion", Mode: "local",
		}, nil
	default:
		lines := make([]string, 0, len(matched))
		for _, memory := range matched {
			lines = append(lines, "· "+trim(strings.Join(strings.Fields(memory.Content), " "), 200))
		}
		return ChatResponse{
			Reply: "Под это подходит несколько записей — скажите точнее, какую забыть:\n" + strings.Join(lines, "\n"),
			Level: "suggestion", Mode: "local",
		}, nil
	}
}

func companionMemoryRequest(message string) (string, bool) {
	trimmed := strings.TrimSpace(message)
	lower := strings.ToLower(trimmed)
	for _, prefix := range companionMemoryPrefixes {
		if !strings.HasPrefix(lower, prefix) {
			continue
		}
		fact := strings.TrimSpace(trimmed[len(prefix):])
		fact = strings.TrimLeft(fact, ":,-— ")
		fact = strings.TrimSpace(fact)
		// Пустая просьба «запомни» без факта — не факт, а начало фразы: пусть
		// на неё отвечает модель, а не хранилище.
		if len([]rune(fact)) < 3 {
			return "", false
		}
		return trim(fact, 2000), true
	}
	return "", false
}

// Запись факта в память помощника. Дубликат не заводится: одна и та же просьба
// на третий раз превратила бы память в свалку, а показать человеку три
// одинаковые строки — то же самое, что не показать ничего.
func (s Service) rememberFact(ctx context.Context, workspaceID, fact string) (ChatResponse, error) {
	existing, err := s.Store.ListMemories(ctx, workspaceID)
	if err != nil {
		return ChatResponse{}, err
	}
	normalized := strings.ToLower(strings.Join(strings.Fields(fact), " "))
	for _, memory := range existing {
		if memory.Kind != domain.MemoryCompanion {
			continue
		}
		if strings.ToLower(strings.Join(strings.Fields(memory.Content), " ")) == normalized {
			return ChatResponse{
				Reply: "Это уже в моей памяти по проекту: «" + memory.Content + "». Изменить или удалить запись можно в разделе «Память».",
				Level: "suggestion", Mode: "local",
			}, nil
		}
	}
	// Закрепление, а не просто запись. В контекст разговора память отбирается по
	// совпадению слов с вопросом: «релиз собираем через make release» всплыл бы
	// на слово «релиз» и промолчал на «как выкатывать». Человек просил помнить
	// это всегда, поэтому просьба закрепляется — закреплённое попадает в каждый
	// разговор.
	//
	// Но не больше, чем помещается: в контекст уходит шесть записей, и седьмая
	// закреплённая вытесняла бы уже не случайную заметку, а такую же просьбу.
	// Тогда факт сохраняется обычным — и об этом говорится прямо.
	pinned := 0
	for _, memory := range existing {
		if memory.Kind == domain.MemoryCompanion && memory.Pinned {
			pinned++
		}
	}
	now := time.Now().UTC()
	record := domain.MemoryRecord{
		ID: domain.NewID("memory"), WorkspaceID: workspaceID, Kind: domain.MemoryCompanion,
		Content: fact, Source: "просьба в чате", Confidence: 0.9,
		Pinned:    pinned < companionPinnedMemoryLimit,
		CreatedAt: now, UpdatedAt: now,
	}
	if err = s.Store.SaveMemory(ctx, record); err != nil {
		return ChatResponse{}, err
	}
	if !record.Pinned {
		return ChatResponse{
			Reply: "Запомнил по этому проекту: «" + fact + "». Закреплённых записей уже " + strconv.Itoa(companionPinnedMemoryLimit) + ", поэтому эта будет всплывать, когда окажется к месту. Открепить лишние можно в разделе «Память».",
			Level: "suggestion", Mode: "local",
		}, nil
	}
	return ChatResponse{
		Reply: "Запомнил и закрепил по этому проекту: «" + fact + "». Буду держать это в виду в каждом разговоре; запись видна и удаляется в разделе «Память».",
		Level: "suggestion", Mode: "local",
	}, nil
}

// Конспект того, что уже не помещается в окно разговора.
//
// Сжатие отбрасывает раннюю часть: в запрос она уходит короткой выжимкой и
// исчезает вместе с ним, а после перезапуска или нового чата не остаётся
// ничего. Та же выжимка ложится в память помощника — одной записью на проект,
// которая обновляется, а не копится, и видна человеку в разделе «Память»
// наравне с остальными. Догадок о важности здесь нет: сохраняется ровно то,
// что и так уходило модели.
//
// Отказ записи разговор не роняет: конспект — удобство, а не условие ответа.
func (s Service) rememberHistoryDigest(ctx context.Context, workspaceID, speaker string, history []domain.CompanionMessage) {
	if s.Store == nil || len(history) <= companionRecentTurns {
		return
	}
	digest := summarizeCompanionHistory(history[:len(history)-companionRecentTurns], 1800)
	if strings.TrimSpace(digest) == "" {
		return
	}
	now := time.Now().UTC()
	if err := s.Store.SaveMemory(ctx, domain.MemoryRecord{
		// Идентификатор постоянный: запись обновляется на месте, иначе каждая
		// реплика длинного разговора добавляла бы в память ещё один конспект.
		ID:          "memory_companiondigest_" + workspaceID + "_" + speaker,
		WorkspaceID: workspaceID, Kind: domain.MemoryCompanion,
		Content:    "Конспект ранней части разговора с помощником:\n" + digest,
		Source:     "сжатие разговора",
		Confidence: 0.5, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		observability.From(ctx).Warn("companion history digest not saved",
			"workspace_id", workspaceID,
			"error", security.Redact(err.Error()),
		)
	}
}
