package orchestrator

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
)

// Модель, которая отвечает разными репликами по кругу: так проверяются ходы,
// где первый ответ не годится, а второй годится (или не годится тоже).
type stubbornModel struct {
	replies []string
	at      int
	think   string
}

func (m *stubbornModel) Stream(_ context.Context, _ providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	if m.think != "" {
		if err := emit(providers.ModelEvent{Kind: providers.EventReasoning, Reasoning: &providers.ReasoningBlock{Type: "text", Text: m.think}}); err != nil {
			return err
		}
	}
	reply := ""
	if m.at < len(m.replies) {
		reply = m.replies[m.at]
	} else if len(m.replies) > 0 {
		reply = m.replies[len(m.replies)-1]
	}
	m.at++
	return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: reply})
}

// Ответ модели редко приходит голым JSON. Пока разбор снимал только ограду
// ```, вежливое предисловие и хвост <think> уводили ход на новый круг — и на
// шестом круге человек получал «Модель Мастера не смогла сформировать задание»
// при полностью разборчивом ответе.
func TestTaskIntakeReadsWrappedJSON(t *testing.T) {
	body := `{"intent":"chat","reply":"Готово"}`
	for name, raw := range map[string]string{
		"голый":     body,
		"markdown":  "```json\n" + body + "\n```",
		"с прозой":  "Конечно, вот задание:\n" + body,
		"с мыслью":  "<think>надо вернуть json</think>" + body,
		"с хвостом": body + "\n\nЕсли что-то не так, скажите.",
	} {
		envelope, ok := decodeTaskIntakeEnvelope(raw)
		if !ok || envelope.Reply != "Готово" {
			t.Fatalf("%s: ответ не разобран: %#v", name, envelope)
		}
	}
	if _, ok := decodeTaskIntakeEnvelope("совсем не json"); ok {
		t.Fatal("проза не должна выдаваться за структуру")
	}
	if _, ok := decodeTaskIntakeEnvelope(`{"intent":"chat","reply":"  "}`); ok {
		t.Fatal("ответ без текста принимать нельзя")
	}
}

// Упрямая модель стоит карточки квеста, а не всего разговора.
//
// Шесть кругов подсказок подряд заканчивались тем, что ход падал целиком:
// человек ждал три минуты и получал служебную фразу движка вместо уже
// написанной моделью реплики.
func TestTaskIntakeKeepsReplyWhenBriefNeverComes(t *testing.T) {
	store := newChatStoreStub()
	model := &stubbornModel{replies: []string{`{"intent":"task","reply":"Понял, уточню объём работ.","brief":null}`}}
	service := ChatService{Store: store, ModelFactory: func(providers.Config) (providers.Model, error) { return model, nil }}
	response, err := service.Chat(context.Background(), ChatRequest{
		WorkspaceID: "ws", TaskIntake: true, Message: "Сделай приложение",
		Config: domain.OrchestratorConfig{Provider: domain.ProviderOllama, Model: "model"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.FallbackReason != "" || response.Mode != "model" {
		t.Fatalf("ход упал вместо мягкой уступки: %q / %q", response.Mode, response.FallbackReason)
	}
	if response.Reply != "Понял, уточню объём работ." {
		t.Fatalf("реплика модели потеряна: %q", response.Reply)
	}
	if !strings.Contains(response.Reasoning, "Задание не оформлено") {
		t.Fatalf("причина отсутствия карточки не названа: %q", response.Reasoning)
	}
	// Подсказок ровно две: каждая стоит человеку отдельного запроса к модели.
	if model.at != maxIntakeRepairs+1 {
		t.Fatalf("кругов подсказок %d вместо %d", model.at, maxIntakeRepairs+1)
	}
}

// Ход рассказывает о себе, пока идёт, а не только задним числом.
func TestMasterTurnStreamsLiveTrace(t *testing.T) {
	store := newChatStoreStub()
	brief := domain.TaskBrief{Mode: domain.TaskModePrecise, Goal: "Bounded goal", ResultKind: "code", Criteria: []domain.AcceptanceCriterion{{ID: "c1", Text: "Done", Kind: "manual"}}}
	raw, _ := json.Marshal(taskIntakeEnvelope{Reply: "Готово", Brief: &brief})
	type event struct{ kind, text, detail string }
	var events []event
	service := ChatService{Store: store, ReadTools: readingToolsStub{}, ModelFactory: func(providers.Config) (providers.Model, error) {
		return &thinkingModel{reply: string(raw)}, nil
	}, OnProgress: func(kind, text, detail string) { events = append(events, event{kind, text, detail}) }}
	if _, err := service.Chat(context.Background(), ChatRequest{
		WorkspaceID: "ws", TaskIntake: true, Message: "Prepare the task",
		Config: domain.OrchestratorConfig{Provider: domain.ProviderOllama, Model: "model"},
	}); err != nil {
		t.Fatal(err)
	}
	kinds := map[string]event{}
	for _, item := range events {
		kinds[item.kind] = item
	}
	mind, ok := kinds["reasoning"]
	if !ok {
		t.Fatalf("рассуждение не доехало до ленты: %#v", events)
	}
	if mind.text != "" {
		t.Fatalf("мысль не должна занимать строку ожидания: %q", mind.text)
	}
	// Мысль едет приростом, а не целиком: иначе один ход пишет в журнал
	// мегабайты одного и того же текста.
	var thought struct {
		Delta string `json:"delta"`
		Text  string `json:"text"`
	}
	if json.Unmarshal([]byte(mind.detail), &thought) != nil || !strings.Contains(thought.Delta, "смотрю на ростер") {
		t.Fatalf("подробность мысли пуста или не прирост: %q", mind.detail)
	}
	if thought.Text != "" {
		t.Fatalf("мысль не должна ехать целиком: %q", mind.detail)
	}
	start, ok := kinds["tools"]
	if !ok || start.text != "read_file" {
		t.Fatalf("начало обращения не названо: %#v", start)
	}
	if !strings.Contains(start.detail, "internal/app/app.go") {
		t.Fatalf("аргумент обращения потерян: %q", start.detail)
	}
	done, ok := kinds["tool_result"]
	if !ok {
		t.Fatalf("исход обращения не показан: %#v", events)
	}
	var payload struct {
		Tool   string `json:"tool"`
		Result string `json:"result"`
		Failed bool   `json:"failed"`
	}
	if json.Unmarshal([]byte(done.detail), &payload) != nil || payload.Tool != "read_file" || payload.Failed || payload.Result == "" {
		t.Fatalf("исход обращения описан невнятно: %q", done.detail)
	}
}
