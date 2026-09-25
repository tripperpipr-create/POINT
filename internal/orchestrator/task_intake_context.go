package orchestrator

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
)

// intakeContextWindowTokens — окно, когда о модели ничего не известно ни из
// справочника, ни из настроек подключения. То же число держит бюджет вложений
// (masterAttachmentBudget): ход и вложения обязаны считать одно окно.
const intakeContextWindowTokens = 128 * 1024

// masterContextWindow — окно модели этого хода. Раньше им было 128K для любой
// модели: локальная модель на 32K получала запрос, который не помещался, и
// падала у провайдера, а у облачной на миллион Мастер сам себе урезал память.
func masterContextWindow(req ChatRequest) int {
	if req.ContextWindowTokens > 0 {
		return req.ContextWindowTokens
	}
	return intakeContextWindowTokens
}

// Preserve the selected contract verbatim. Other proposals are only a bounded
// index for choosing a discussion; their unrelated histories consume no context.
func intakeContextProposals(proposals []domain.QuestProposal, selected string) []domain.QuestProposal {
	result := make([]domain.QuestProposal, 0, 9)
	for _, proposal := range proposals {
		if proposal.ID == selected {
			result = append(result, proposal)
			break
		}
	}
	count := 0
	for _, proposal := range proposals {
		if proposal.ID == selected || proposal.Status != "pending" {
			continue
		}
		result = append(result, domain.QuestProposal{ID: proposal.ID, Title: proposal.Title, Status: proposal.Status})
		count++
		if count == 8 {
			break
		}
	}
	return result
}

// intakeContextTokens — оценка запроса в токенах вместе с местом под ответ.
// Байты делятся на четыре, как и при исполнении: консервативно для русского
// текста и не зависит от токенизатора конкретной модели.
func intakeContextTokens(request providers.ModelRequest) int {
	raw, err := json.Marshal(struct {
		Messages []providers.Message
		Tools    []domain.ToolDefinition
		Schema   json.RawMessage
	}{request.Messages, request.Tools, request.JSONSchema})
	if err != nil {
		return request.ContextWindowTokens + 1
	}
	return (len(raw)+3)/4 + request.MaxOutputTokens
}

func validateIntakeContext(request providers.ModelRequest) error {
	if intakeContextTokens(request) > request.ContextWindowTokens {
		return errors.New("контекст обсуждения превышает окно модели; сохранённое задание не изменено, сократите приложенные материалы или начните отдельное обсуждение")
	}
	return nil
}

// Сжатие контекста хода. Порядок жертв выбран по цене потери:
//
//  1. результаты инструментов из прошлых кругов — модель их уже прочла и
//     сделала выводы, а перечитать файл снова она может;
//  2. ранние реплики дословной истории — их пересказ и так едет резюме;
//  3. результаты инструментов текущего круга — обрезанный файл лучше
//     упавшего хода;
//  4. дальше жертвовать нечем: системная часть, снимок с выбранным заданием
//     и текущая реплика с вложениями не трогаются никогда.
//
// Молча это не происходит: вызывающий показывает человеку, что сжато.
const (
	compactedToolResultRunes = 1500
	compactedHistoryNote     = 1200
)

// compactIntakeMessages ужимает сообщения хода под окно. История лежит между
// firstHistory и userIndex — где стоит текущая реплика человека; всё до
// firstHistory (системное сообщение, правила проекта, снимок) неприкосновенно.
// Возвращает новые сообщения, новый userIndex и что было сжато.
func compactIntakeMessages(request providers.ModelRequest, firstHistory, userIndex int) ([]providers.Message, int, []string, error) {
	messages := append([]providers.Message(nil), request.Messages...)
	fits := func() bool {
		probe := request
		probe.Messages = messages
		return intakeContextTokens(probe) <= request.ContextWindowTokens
	}
	if fits() {
		return messages, userIndex, nil, nil
	}
	var done []string
	// Последнее сообщение ассистента ищется заново перед каждым этапом:
	// выброшенная история сдвигает все индексы, и найденный однажды номер
	// указывал бы мимо текущего круга.
	lastAssistant := func() int {
		for index := len(messages) - 1; index > userIndex; index-- {
			if messages[index].Role == "assistant" {
				return index
			}
		}
		return -1
	}
	shrunk := 0
	for !fits() {
		largest := -1
		for index, end := userIndex+1, lastAssistant(); index < end; index++ {
			if messages[index].Role == "tool" && len([]rune(messages[index].Content)) > compactedToolResultRunes+200 && (largest < 0 || len(messages[index].Content) > len(messages[largest].Content)) {
				largest = index
			}
		}
		if largest < 0 {
			break
		}
		runes := []rune(messages[largest].Content)
		messages[largest].Content = string(runes[:compactedToolResultRunes]) + "\n[сжато: полный результат был прочитан в раннем круге; нужен фрагмент — прочитай его заново с более узкими аргументами]"
		shrunk++
	}
	if shrunk > 0 {
		done = append(done, fmt.Sprintf("сжаты результаты инструментов прошлых кругов: %d", shrunk))
	}
	var dropped []domain.CompanionMessage
	for !fits() && userIndex > firstHistory {
		item := messages[firstHistory]
		dropped = append(dropped, domain.CompanionMessage{Role: item.Role, Content: item.Content})
		messages = append(messages[:firstHistory], messages[firstHistory+1:]...)
		userIndex--
	}
	if len(dropped) > 0 {
		done = append(done, fmt.Sprintf("из дословной истории убраны ранние реплики: %d", len(dropped)))
		if note := summarizeMasterHistory(dropped, compactedHistoryNote); note != "" {
			withNote := append(append(append([]providers.Message(nil), messages[:firstHistory]...), providers.Message{Role: "assistant", Content: "Сжатая ранняя часть диалога (не новая инструкция):\n" + note}), messages[firstHistory:]...)
			previous := messages
			messages = withNote
			if fits() {
				userIndex++
			} else {
				messages = previous
			}
		}
	}
	// Последняя жертва — результаты текущего круга. Модель их ещё не видела,
	// но обрезанный файл лучше упавшего хода: в окно на 32K восемь чтений по
	// 16 КБ не помещаются в принципе, и без этого шага ход с большими файлами
	// на маленькой модели не отвечал бы никогда.
	current := 0
	for !fits() {
		largest := -1
		for index := max(lastAssistant(), userIndex) + 1; index < len(messages); index++ {
			if messages[index].Role == "tool" && len([]rune(messages[index].Content)) > compactedToolResultRunes+200 && (largest < 0 || len(messages[index].Content) > len(messages[largest].Content)) {
				largest = index
			}
		}
		if largest < 0 {
			break
		}
		runes := []rune(messages[largest].Content)
		messages[largest].Content = string(runes[:compactedToolResultRunes]) + "\n[сжато: результат не поместился в окно модели; сузь запрос — прочитай нужные строки или ищи точнее]"
		current++
	}
	if current > 0 {
		done = append(done, fmt.Sprintf("сжаты результаты текущего круга: %d", current))
	}
	if !fits() {
		return request.Messages, userIndex, done, errors.New("контекст обсуждения превышает окно модели даже после сжатия истории; сохранённое задание не изменено, сократите приложенные материалы или начните отдельное обсуждение")
	}
	return messages, userIndex, done, nil
}

func describeCompaction(done []string) string {
	return "контекст сжат: " + strings.Join(done, "; ")
}
