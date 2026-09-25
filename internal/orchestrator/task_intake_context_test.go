package orchestrator

import (
	"context"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
	"strings"
	"testing"
)

func TestIntakeContextPreservesSelectedContractAndBoundsUnrelatedTasks(t *testing.T) {
	brief := &domain.TaskBrief{SourceRequest: "original", Decisions: []domain.BriefDecision{{Topic: "output", Decision: "preserve", Source: "user"}}}
	proposals := []domain.QuestProposal{}
	for i := 0; i < 100; i++ {
		proposals = append(proposals, domain.QuestProposal{ID: strings.Repeat("x", i+1), Status: "pending", Task: strings.Repeat("unrelated", 10000)})
	}
	proposals = append(proposals, domain.QuestProposal{ID: "selected", Status: "pending", Brief: brief})
	got := intakeContextProposals(proposals, "selected")
	if len(got) != 9 || got[0].ID != "selected" || got[0].Brief != brief {
		t.Fatal("selected agreements lost or unrelated proposals unbounded")
	}
	for _, p := range got[1:] {
		if p.Task != "" || p.Brief != nil {
			t.Fatal("unrelated full task leaked into context")
		}
	}
	if proposals[0].Task == "" {
		t.Fatal("stored data mutated")
	}
}

func TestIntakeContextRejectsOversizedInputWithoutTruncation(t *testing.T) {
	// Оценка токенов ≈ len/4; окно 128K, ответ 4K → нужен текст заметно больше ~500 КиБ.
	oversized := strings.Repeat("agreement", 80_000) // 720_000 рун ASCII → ~180K токенов
	request := providers.ModelRequest{ContextWindowTokens: intakeContextWindowTokens, MaxOutputTokens: 4096, Messages: []providers.Message{{Role: "user", Content: oversized}}}
	if validateIntakeContext(request) == nil {
		t.Fatal("oversized contract accepted")
	}
	if len(request.Messages[0].Content) != len(oversized) {
		t.Fatal("agreement truncated")
	}
	request.Messages[0].Content = "complete precise request"
	if err := validateIntakeContext(request); err != nil {
		t.Fatal(err)
	}
}

// Сжатие жертвует сначала прочитанным в прошлых кругах, потом ранней
// историей — и никогда системной частью, снимком с заданием, текущей
// репликой и последним кругом.
func TestCompactIntakeMessagesSacrificesInOrder(t *testing.T) {
	big := strings.Repeat("x", 6000)
	messages := []providers.Message{
		{Role: "system", Content: "system"},
		{Role: "user", Content: "world with selected brief"},
		{Role: "user", Content: "old question " + strings.Repeat("h", 3000)},
		{Role: "assistant", Content: "old answer " + strings.Repeat("h", 3000)},
		{Role: "user", Content: "current question"},
		{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: "1", Name: "read_file"}}},
		{Role: "tool", ToolCallID: "1", Content: big},
		{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: "2", Name: "read_file"}}},
		{Role: "tool", ToolCallID: "2", Content: big},
	}
	request := providers.ModelRequest{Messages: messages, MaxOutputTokens: 100}
	full := intakeContextTokens(request)

	// Окно, в которое влезает всё, кроме полного старого результата.
	request.ContextWindowTokens = full - 1000
	got, userIndex, done, err := compactIntakeMessages(request, 2, 4)
	if err != nil || len(done) != 1 || userIndex != 4 {
		t.Fatalf("первой жертвой должен стать старый результат: %v %q", err, done)
	}
	if len(got[6].Content) >= len(big) || got[8].Content != big {
		t.Fatal("сжат не тот результат: последний круг неприкосновенен")
	}
	if messages[6].Content != big {
		t.Fatal("сжатие испортило исходные сообщения")
	}

	// Окно, в которое не влезает и ранняя история.
	request.ContextWindowTokens = full - 2000
	got, userIndex, done, err = compactIntakeMessages(request, 2, 4)
	if err != nil || len(done) != 2 {
		t.Fatalf("история не пожертвована: %v %q", err, done)
	}
	if got[0].Content != "system" || got[1].Content != "world with selected brief" || got[userIndex].Content != "current question" {
		t.Fatalf("задета неприкосновенная часть: %#v", got[:userIndex+1])
	}
	for _, message := range got[:userIndex] {
		if strings.HasPrefix(message.Content, "old question") {
			t.Fatal("ранняя реплика осталась дословно")
		}
	}
}

// Текущую реплику с вложениями урезать нельзя: если не влезает даже она, ход
// честно отказывает, а не отправляет модели обрубок просьбы.
func TestCompactIntakeMessagesRefusesWhenCurrentRequestAloneOverflows(t *testing.T) {
	request := providers.ModelRequest{MaxOutputTokens: 100, ContextWindowTokens: 1000, Messages: []providers.Message{
		{Role: "system", Content: "system"},
		{Role: "user", Content: "world"},
		{Role: "user", Content: strings.Repeat("attachment", 2000)},
	}}
	got, _, _, err := compactIntakeMessages(request, 2, 2)
	if err == nil {
		t.Fatal("непомещающаяся реплика принята")
	}
	if got[2].Content != request.Messages[2].Content {
		t.Fatal("реплика человека урезана")
	}
}

// Ход с длинной историей на маленьком окне отвечает, а не падает, и говорит
// человеку, что контекст сжат.
func TestMasterTurnCompactsHistoryForSmallWindow(t *testing.T) {
	store := newChatStoreStub()
	for i := 0; i < 20; i++ {
		store.messages = append(store.messages,
			domain.CompanionMessage{Speaker: "master", Role: "user", Content: "вопрос " + strings.Repeat("о", 800)},
			domain.CompanionMessage{Speaker: "master", Role: "assistant", Content: "ответ " + strings.Repeat("т", 800)},
		)
	}
	model := &turnModel{rounds: []roundScript{{text: "Отвечаю по сжатому контексту."}}}
	var compacted []string
	service := ChatService{Store: store, ModelFactory: model.factory(), OnProgress: func(kind, text, _ string) {
		if kind == "retry" {
			compacted = append(compacted, text)
		}
	}}
	// Сначала ход на просторном окне — узнать, сколько он весит целиком.
	req := intakeRequest("Что дальше?")
	if _, err := service.Chat(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	full := intakeContextTokens(model.requests[0])
	compacted = nil
	service.ModelFactory = model.factory()
	req.ContextWindowTokens = full - 3000
	response, err := service.Chat(context.Background(), req)
	if err != nil {
		t.Fatalf("длинная история уронила ход: %v", err)
	}
	if response.Reply != "Отвечаю по сжатому контексту." {
		t.Fatalf("ответ потерян: %q", response.Reply)
	}
	if len(compacted) != 1 || !strings.Contains(compacted[0], "контекст сжат") {
		t.Fatalf("сжатие не показано человеку: %q", compacted)
	}
	if got := intakeContextTokens(model.requests[0]); got > req.ContextWindowTokens {
		t.Fatalf("запрос не влез в окно: %d > %d", got, req.ContextWindowTokens)
	}
}
