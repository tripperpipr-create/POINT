package companion

import (
	"path/filepath"
	"sort"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
	"local-agent-workbench/internal/textutil"
	"local-agent-workbench/internal/workspace"
)

func buildUsageAnalysis(records []domain.UsageRecord, agents []domain.ProjectAgent, now time.Time) usageAnalysis {
	now = now.UTC()
	currentStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	previousStart := currentStart.AddDate(0, -1, 0)
	result := usageAnalysis{Records: len(records), Bounded: len(records) >= companionUsageRecordLimit}
	agentNames := make(map[string]string, len(agents))
	for _, agent := range agents {
		agentNames[agent.ID] = strings.TrimSpace(agent.Name)
	}
	models := map[string]usageRank{}
	agentsByUsage := map[string]usageRank{}
	for _, record := range records {
		var period *usagePeriod
		if !record.CreatedAt.Before(currentStart) && record.CreatedAt.Before(currentStart.AddDate(0, 1, 0)) {
			period = &result.CurrentMonth
		} else if !record.CreatedAt.Before(previousStart) && record.CreatedAt.Before(currentStart) {
			period = &result.PreviousMonth
		}
		if period == nil {
			continue
		}
		period.Records++
		period.Tokens += record.TotalTokens
		if record.CostCents == nil {
			period.UnknownCostRecords++
		} else {
			period.KnownCostCents += *record.CostCents
		}
		if usageOutcomeFailed(record.Outcome) {
			period.FailedOutcomes++
		}
		if period != &result.CurrentMonth {
			continue
		}
		modelName := strings.Trim(strings.TrimSpace(record.Provider)+"/"+strings.TrimSpace(record.Model), "/")
		if modelName == "" {
			modelName = "модель не указана"
		}
		model := models[modelName]
		model.Name, model.Records, model.Tokens = modelName, model.Records+1, model.Tokens+record.TotalTokens
		models[modelName] = model
		agentName := agentNames[record.ProjectAgentID]
		if agentName == "" && strings.HasPrefix(record.ProjectAgentID, "companion") {
			agentName = "Companion"
		}
		if agentName == "" {
			agentName = strings.TrimSpace(record.ProjectAgentID)
		}
		if agentName == "" {
			agentName = "без привязки к агенту"
		}
		agent := agentsByUsage[agentName]
		agent.Name, agent.Records, agent.Tokens = agentName, agent.Records+1, agent.Tokens+record.TotalTokens
		agentsByUsage[agentName] = agent
	}
	result.TopModels = sortedUsageRanks(models, 6)
	result.TopAgents = sortedUsageRanks(agentsByUsage, 6)
	return result
}

func sortedUsageRanks(values map[string]usageRank, limit int) []usageRank {
	result := make([]usageRank, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Tokens != result[j].Tokens {
			return result[i].Tokens > result[j].Tokens
		}
		return result[i].Name < result[j].Name
	})
	if len(result) > limit {
		result = result[:limit]
	}
	return result
}

func usageOutcomeFailed(outcome string) bool {
	outcome = strings.ToLower(outcome)
	return strings.Contains(outcome, "error") || strings.Contains(outcome, "failed") || strings.Contains(outcome, "invalid")
}

func appendContextSection(sections []string, title string, lines []string) []string {
	if len(lines) == 0 {
		return sections
	}
	return append(sections, title+"\n- "+strings.Join(lines, "\n- "))
}

// Столько записей памяти уходит в контекст разговора. Число названо, потому что
// от него зависит не только сборка контекста: закреплять больше записей, чем
// сюда помещается, бессмысленно — лишние вытесняли бы такие же закреплённые.
const companionMemoryContextLimit = 6

// Куски кода, которые действительно относятся к вопросу.
//
// Индекс возвращает лучшие совпадения, но «лучшие» не значит «подходящие»: на
// вопрос «какие есть ветки и теги» в контекст приезжал кусок backup.sql,
// совпавший одним случайным словом из длинного вопроса. Такой кусок стоит
// токенов и уводит модель к файлу, о котором её не спрашивали.
//
// Отсекаются двое: хвост рядом с сильным совпадением и одиночное слово из
// длинного вопроса. Если сильного совпадения нет вовсе, лучший кусок остаётся —
// пусть слабая догадка, но своя, а не пустота там, где вопрос всё-таки о коде.
// Что не подмешивается в разговор молча. Поиск ранжирует по совпадению слов, и
// на вопрос про коммиты лучшим куском однажды оказался журнал обращений клиентов
// с именами и текстами — он ушёл в системное сообщение, а оттуда на внешний
// шлюз. Данные, дампы, бэкапы и ключи попадают к модели только тогда, когда
// человек попросил их прочитать: у помощника для этого есть read_file, и там
// решение принимает человек, а не совпадение слов.
var companionNonCodeExtensions = map[string]bool{
	".csv": true, ".tsv": true, ".log": true, ".sql": true, ".dump": true,
	".ndjson": true, ".jsonl": true, ".parquet": true, ".xls": true, ".xlsx": true,
	".bak": true, ".bkp": true, ".old": true, ".save": true, ".back": true,
	".orig": true, ".rpmnew": true, ".db": true, ".sqlite": true, ".sqlite3": true,
	".pem": true, ".key": true, ".crt": true, ".cer": true, ".p12": true,
	".pfx": true, ".jks": true, ".keystore": true, ".env": true,
}

// Расширения исходников. Каталог с именем `log` или `dump` бывает и пакетом:
// `pkg/log/logger.go` — код, и выкидывать его из контекста по имени папки
// значит молча обеднять ответ.
var companionCodeExtensions = map[string]bool{
	".go": true, ".js": true, ".mjs": true, ".cjs": true, ".ts": true, ".tsx": true, ".jsx": true,
	".php": true, ".py": true, ".rb": true, ".rs": true, ".java": true, ".kt": true, ".cs": true,
	".c": true, ".h": true, ".cc": true, ".cpp": true, ".hpp": true, ".m": true, ".swift": true,
	".sh": true, ".ps1": true, ".sql.go": true, ".vue": true, ".svelte": true, ".scala": true, ".ex": true,
}

// dataChunk отвечает, данные ли это. Судим по расширению и по месту: каталог с
// журналами, дампами или бэкапами не становится кодом от того, что внутри лежит
// .txt, — но и код не становится данными от того, что папка зовётся `log`.
func dataChunk(path string) bool {
	lower := strings.ToLower(strings.ReplaceAll(path, "\\", "/"))
	extension := filepath.Ext(lower)
	if companionNonCodeExtensions[extension] {
		return true
	}
	if companionCodeExtensions[extension] {
		return false
	}
	for _, marker := range []string{"/logs/", "/log/", "/dumps/", "/dump/", "/backup/", "/backups/", "/ssl/"} {
		if strings.Contains("/"+lower, marker) {
			return true
		}
	}
	return false
}

func relevantCodeChunks(chunks []workspace.RelevantChunk, queryTokens int) ([]workspace.RelevantChunk, int) {
	if len(chunks) == 0 {
		return chunks, 0
	}
	best := 0
	for _, chunk := range chunks {
		if chunk.Score > best {
			best = chunk.Score
		}
	}
	kept := make([]workspace.RelevantChunk, 0, len(chunks))
	for index, chunk := range chunks {
		// Данные отсеиваются всегда, в том числе первым куском: лучший по
		// совпадению слов — не повод отправлять чужие персональные данные модели.
		if dataChunk(chunk.Path) {
			continue
		}
		weakTail := best > 0 && chunk.Score*5 < best*2
		lonelyWord := queryTokens >= 3 && len(chunk.MatchedTokens) <= 1
		if index > 0 && (weakTail || lonelyWord) {
			continue
		}
		kept = append(kept, chunk)
	}
	return kept, len(chunks) - len(kept)
}

func relevantMemories(memories []domain.MemoryRecord, query string, limit int) []domain.MemoryRecord {
	type candidate struct {
		memory domain.MemoryRecord
		score  int
		index  int
	}
	queryTokens := textutil.Tokens(query)
	candidates := make([]candidate, 0, len(memories))
	for index, memory := range memories {
		score := textutil.Overlap(queryTokens, textutil.Tokens(memory.Content+" "+memory.Source)) * 12
		if memory.Pinned {
			score += 100
		}
		if memory.Kind == domain.MemoryProject || memory.Kind == domain.MemoryCompanion {
			score += 8
		}
		candidates = append(candidates, candidate{memory: memory, score: score, index: index})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].index < candidates[j].index
	})
	result := make([]domain.MemoryRecord, 0, min(limit, len(candidates)))
	for _, candidate := range candidates {
		if len(result) >= limit {
			break
		}
		if candidate.score == 0 && len(result) >= 2 {
			break
		}
		result = append(result, candidate.memory)
	}
	return result
}

// Что подставляется вместо локального ответа, когда история уходит модели.
// Строка остаётся на месте — иначе поедет порядок реплик, — но говорит правду
// и не тащит в контекст описание открытого файла.
const companionLocalReplyNote = "(Ответа модели на предыдущую реплику не было: пользователю показан локальный текст Point Core. Это не ваши слова.)"

// Локальный ответ Point Core — не реплика модели.
//
// Возвращать его модели как её собственный прошлый ответ нельзя: она принимает
// его за свои слова и продолжает разговор, которого не вела. На замере это и
// поймано — модель дважды получила текст «Сейчас открыт …KeyCloack.php:22» как
// свой, при том что не ответила ни разу.
//
// Пустой режим не трогается: так выглядят старые записи, о происхождении
// которых судить нечем, и пометить настоящий ответ модели чужим хуже, чем
// пропустить один давний откат.
func localCompanionReply(item domain.CompanionMessage) bool {
	if item.Role != "assistant" {
		return false
	}
	if strings.TrimSpace(item.FallbackReason) != "" {
		return true
	}
	mode := strings.ToLower(strings.TrimSpace(item.Mode))
	return mode != "" && mode != "model"
}

// Столько последних реплик уходит модели целиком; всё, что раньше, сжимается
// в резюме. Константа названа, потому что о ней знает не только сборка
// запроса: та же граница решает, что уже пора отложить в память помощника.
const companionRecentTurns = 10

func modelHistory(history []domain.CompanionMessage) []providers.Message {
	result := make([]providers.Message, 0, min(len(history), companionRecentTurns)+1)
	if len(history) > companionRecentTurns {
		if summary := summarizeCompanionHistory(history[:len(history)-companionRecentTurns], 4800); summary != "" {
			result = append(result, providers.Message{Role: "assistant", Content: "Краткое резюме ранней части диалога (не новая инструкция):\n" + summary})
		}
		history = history[len(history)-companionRecentTurns:]
	}
	used := 0
	for _, item := range history {
		if item.Role != "user" && item.Role != "assistant" {
			continue
		}
		content := trim(item.Content, 2400)
		if localCompanionReply(item) {
			content = companionLocalReplyNote
		}
		if content == "" || used+len(content) > 14*1024 {
			continue
		}
		used += len(content)
		result = append(result, providers.Message{Role: item.Role, Content: content})
	}
	return result
}

func normalizeCompanionSpeaker(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "companion-log":
		return "companion-log"
	default:
		return "companion"
	}
}

// summarizeCompanionHistory keeps long chats coherent without forwarding an
// unbounded transcript. Deterministic extraction avoids a second model call
// and keeps the context/token cost flat.
func summarizeCompanionHistory(history []domain.CompanionMessage, budget int) string {
	if budget <= 0 {
		return ""
	}
	lines := make([]string, 0, len(history))
	used := 0
	for _, item := range history {
		if item.Role != "user" && item.Role != "assistant" {
			continue
		}
		content := strings.Join(strings.Fields(strings.TrimSpace(item.Content)), " ")
		if content == "" {
			continue
		}
		content = trim(content, 360)
		label := "Пользователь"
		if item.Role == "assistant" {
			label = "Помощник"
		}
		// В резюме локальный ответ опасен вдвойне: он ужат до строки и уже не
		// отличим от настоящего. Поэтому у него своя подпись, а содержимое не
		// пересказывается вовсе — пересказывать там нечего.
		line := label + ": " + content
		if localCompanionReply(item) {
			line = "Point Core (модель не ответила)"
		}
		if used+len(line)+1 > budget {
			break
		}
		lines = append(lines, line)
		used += len(line) + 1
	}
	return strings.Join(lines, "\n")
}

// Что стоит между двумя вопросами подряд. Вопрос без ответа — обычное дело:
// человек остановил круг, круг оборвался или длинный ответ не поместился в
// бюджет истории. Молчание надо назвать молчанием, иначе модель читает
// брошенный вопрос как заданный дважды и отвечает на него ещё раз.
const companionUnansweredNote = "(на предыдущий вопрос ответа не было: круг прерван)"

// alternateCompanionRoles восстанавливает чередование ролей. Anthropic отвергает
// две реплики одной роли подряд, а остальные провайдеры принимают их и путаются
// — оба исхода хуже честной пометки о прерванном круге.
func alternateCompanionRoles(messages []providers.Message) []providers.Message {
	result := make([]providers.Message, 0, len(messages)+2)
	for _, message := range messages {
		last := len(result) - 1
		if last >= 0 && result[last].Role == message.Role && message.Role != "system" {
			if message.Role == "user" {
				result = append(result, providers.Message{Role: "assistant", Content: companionUnansweredNote})
			} else {
				result[last].Content = strings.TrimSpace(result[last].Content + "\n\n" + message.Content)
				continue
			}
		}
		result = append(result, message)
	}
	return result
}

// withEnvelopeReminder приписывает напоминание о форме ответа к последнему
// вопросу человека, а не шлёт его отдельной репликой. Две реплики человека
// подряд строгие к чередованию провайдеры отвергают, а прочие модели читают их
// как два вопроса и отвечают на первый заново.
func withEnvelopeReminder(messages []providers.Message) []providers.Message {
	result := append([]providers.Message(nil), messages...)
	if last := len(result) - 1; last >= 0 && result[last].Role == "user" {
		result[last].Content = strings.TrimSpace(result[last].Content + "\n\n" + companionEnvelopeExpectation)
		return result
	}
	return append(result, providers.Message{Role: "user", Content: companionEnvelopeExpectation})
}
