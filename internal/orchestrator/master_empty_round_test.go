package orchestrator

import (
	"context"
	"strings"
	"testing"

	"local-agent-workbench/internal/providers"
)

// E5: после чтения круг закрылся без текста и вызова, и ход сохранился
// completed с одной фразой «посмотрю…». Теперь пустому кругу даётся один круг
// «ответь по собранному» без размышления и без чтения.
func TestMasterEmptyRoundAfterResearchGetsAnswerRound(t *testing.T) {
	model := &turnModel{rounds: []roundScript{
		{text: "Посмотрю файлы.", calls: []providers.ToolCall{toolCall("r1", "read_file", map[string]string{"path": "app.go"})}},
		// Быстрый круг после чтения решил ответить — ответ пишет круг с
		// размышлением, и пустым оказывается уже он (форма E5).
		{text: "Ответ без раздумий."},
		{think: "думаю"},
		{text: "Причина в app.go: поток закрывается раньше записи."},
	}}
	var retries []string
	service := ChatService{Store: newChatStoreStub(), ReadTools: readingToolsStub{}, ModelFactory: model.factory(),
		OnProgress: func(kind, _, detail string) {
			if kind == "retry" {
				retries = append(retries, detail)
			}
		}}
	response, err := service.Chat(context.Background(), intakeRequest("Почему падает?"))
	if err != nil {
		t.Fatal(err)
	}
	if response.Reply != "Посмотрю файлы.\n\nПричина в app.go: поток закрывается раньше записи." {
		t.Fatalf("ответ по собранному не получен: %q", response.Reply)
	}
	if len(model.requests) != 4 {
		t.Fatalf("кругов %d вместо 4", len(model.requests))
	}
	if !model.requests[1].DisableThinking || model.requests[2].DisableThinking {
		t.Fatal("круг после чтения идёт без размышления, его переигровка — с размышлением")
	}
	last := model.requests[3]
	if !last.DisableThinking {
		t.Fatal("восстановительный круг должен идти без размышления")
	}
	for _, tool := range last.Tools {
		if !IsMasterActionTool(tool.Name) {
			t.Fatalf("восстановительному кругу предложено чтение: %s", tool.Name)
		}
	}
	if len(retries) != 1 || !strings.Contains(retries[0], "empty_round") {
		t.Fatalf("пустой круг не назван в ленте: %q", retries)
	}
}

// Повторная пустота — честная ошибка, а не completed с одной вводной фразой.
func TestMasterSecondEmptyRoundIsAnError(t *testing.T) {
	model := &turnModel{rounds: []roundScript{
		{text: "Посмотрю файлы.", calls: []providers.ToolCall{toolCall("r1", "read_file", map[string]string{"path": "app.go"})}},
		{text: "Ответ без раздумий."},
		{think: "думаю"},
	}}
	service := ChatService{Store: newChatStoreStub(), ReadTools: readingToolsStub{}, ModelFactory: model.factory()}
	response, err := service.Chat(context.Background(), intakeRequest("Почему падает?"))
	if err != nil {
		t.Fatal(err)
	}
	// Ошибка хода идёт тем же путём, что и молчание модели: отказ с причиной,
	// а не ответ модели.
	if response.Mode != "deterministic" || response.FallbackReason != errMasterEmptyAnswer.Error() {
		t.Fatalf("ход с одним обещанием принят как ответ: mode=%q reason=%q reply=%q", response.Mode, response.FallbackReason, response.Reply)
	}
	if len(model.requests) != 4 {
		t.Fatalf("восстановительный круг должен быть один: кругов %d", len(model.requests))
	}
}

// loopingThinker повторяет один абзац размышления, пока его не остановят,
// и отвечает, когда размышление погашено.
type loopingThinker struct {
	requests []providers.ModelRequest
	emitted  int
}

const loopingParagraph = "Нужно проверить, как app.go закрывает поток: сначала посмотрю обработчик, потом запись в журнал, затем сверю порядок вызовов с тем, что пишет тест, и только после этого отвечу человеку. "

func (m *loopingThinker) Stream(_ context.Context, req providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.requests = append(m.requests, req)
	if len(m.requests) == 1 {
		call := toolCall("r1", "read_file", map[string]string{"path": "app.go"})
		if err := emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "Посмотрю файл."}); err != nil {
			return err
		}
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &call})
	}
	if req.DisableThinking {
		return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "Поток закрывается раньше записи."})
	}
	// Абзац идёт кусками, как токены: петля на 17 повторов, как в E5.
	for repeat := 0; repeat < 17; repeat++ {
		for _, word := range strings.SplitAfter(loopingParagraph, " ") {
			m.emitted++
			if err := emit(providers.ModelEvent{Kind: providers.EventReasoning, Reasoning: &providers.ReasoningBlock{Type: "text", Text: word}}); err != nil {
				return err
			}
		}
	}
	return emit(providers.ModelEvent{Kind: providers.EventFinish, FinishReason: "stop"})
}

func TestMasterReasoningLoopIsCutAndAnswered(t *testing.T) {
	model := &loopingThinker{}
	var retries []string
	service := ChatService{Store: newChatStoreStub(), ReadTools: readingToolsStub{}, ModelFactory: func(providers.Config) (providers.Model, error) { return model, nil },
		OnProgress: func(kind, _, detail string) {
			if kind == "retry" {
				retries = append(retries, detail)
			}
		}}
	response, err := service.Chat(context.Background(), intakeRequest("Почему падает?"))
	if err != nil {
		t.Fatal(err)
	}
	if response.Reply != "Посмотрю файл.\n\nПоток закрывается раньше записи." {
		t.Fatalf("после петли нет ответа: %q", response.Reply)
	}
	words := len(strings.SplitAfter(loopingParagraph, " "))
	if model.emitted >= 17*words {
		t.Fatal("петля размышления дошла до конца потока, а должна быть оборвана")
	}
	if len(retries) != 1 || !strings.Contains(retries[0], "reasoning_loop") {
		t.Fatalf("петля не названа в ленте: %q", retries)
	}
}

func TestReasoningLoopGuardIgnoresOrdinaryReasoning(t *testing.T) {
	var guard reasoningLoopGuard
	var text strings.Builder
	for step := 0; step < 400; step++ {
		text.WriteString("Шаг ")
		text.WriteString(strings.Repeat("ab", step%7+1))
		text.WriteString(" проверяю файл номер ")
		text.WriteString(string(rune('A' + step%26)))
		text.WriteString(string(rune('a' + step/26)))
		text.WriteString(". ")
	}
	for _, word := range strings.SplitAfter(text.String(), " ") {
		if guard.looped(word) {
			t.Fatalf("обычное размышление принято за петлю на %d байтах", guard.size())
		}
	}
}
