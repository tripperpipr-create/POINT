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

// Ход рассказывает о себе, пока идёт, а не только задним числом.
func TestMasterTurnStreamsLiveTrace(t *testing.T) {
	store := newChatStoreStub()
	type event struct{ kind, text, detail string }
	var events []event
	service := ChatService{Store: store, ReadTools: readingToolsStub{}, ModelFactory: func(providers.Config) (providers.Model, error) {
		return &thinkingModel{reply: "Готово"}, nil
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
