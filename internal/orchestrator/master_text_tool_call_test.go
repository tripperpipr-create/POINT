package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/providers"
)

// Вызов инструмента разговора, который модель написала текстом, исполняется,
// а блок не остаётся в ответе человеку.
func TestMasterExecutesToolCallWrittenAsText(t *testing.T) {
	store := newChatStoreStub()
	model := &turnModel{rounds: []roundScript{
		{text: "Запомню.\n<tool_call>\n{\"name\": \"suggest_memory\", \"arguments\": {\"entries\": [\"Предпочитает табы\"]}}\n</tool_call>"},
		{text: "app.go собирает ядро."},
	}}
	var replies []string
	service := ChatService{Store: store, ModelFactory: model.factory(), OnProgress: func(kind, text, _ string) {
		if kind == "reply" {
			replies = append(replies, text)
		}
	}}
	response, err := service.Chat(context.Background(), intakeRequest("Что делает app.go? И запомни: я предпочитаю табы"))
	if err != nil {
		t.Fatal(err)
	}
	if len(response.MemorySuggestions) != 1 {
		t.Fatalf("вызов из текста не исполнен: %#v", response)
	}
	if strings.Contains(response.Reply, "tool_call") || len(replies) == 0 || strings.Contains(replies[len(replies)-1], "tool_call") {
		t.Fatalf("блок вызова остался в ответе: reply=%q stream=%q", response.Reply, replies)
	}
}

// Круг, оборванный провайдером посреди ответа, повторяется, и начатый текст
// не остаётся в ответе.
type droppingMasterModel struct{ calls int }

func (m *droppingMasterModel) Stream(_ context.Context, _ providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.calls++
	if m.calls == 1 {
		_ = emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "Полуотв"})
		return errors.New("read tcp: wsarecv: An established connection was aborted by the software in your host machine.")
	}
	return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "app.go собирает ядро."})
}

func TestMasterRoundSurvivesDroppedStream(t *testing.T) {
	previous := masterRoundRetryDelay
	masterRoundRetryDelay = time.Millisecond
	t.Cleanup(func() { masterRoundRetryDelay = previous })
	model := &droppingMasterModel{}
	var replies []string
	service := ChatService{Store: newChatStoreStub(), ModelFactory: func(providers.Config) (providers.Model, error) { return model, nil }, OnProgress: func(kind, text, _ string) {
		if kind == "reply" {
			replies = append(replies, text)
		}
	}}
	response, err := service.Chat(context.Background(), intakeRequest("Что делает app.go?"))
	if err != nil {
		t.Fatal(err)
	}
	if response.Reply != "app.go собирает ядро." || model.calls != 2 {
		t.Fatalf("reply=%q calls=%d", response.Reply, model.calls)
	}
	if last := replies[len(replies)-1]; strings.Contains(last, "Полуотв") {
		t.Fatalf("stream kept the dropped attempt: %q", last)
	}
}

// Повтор провайдера посреди потока сбрасывает начатый текст круга.
type restartingMasterModel struct{}

func (restartingMasterModel) Stream(_ context.Context, _ providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	for _, event := range []providers.ModelEvent{
		{Kind: providers.EventTextDelta, Delta: "Полуотв"},
		{Kind: providers.EventRetry, Attempt: 2},
		{Kind: providers.EventTextDelta, Delta: "Целый ответ."},
	} {
		if err := emit(event); err != nil {
			return err
		}
	}
	return nil
}

func TestMasterProviderRestartDoesNotDuplicateText(t *testing.T) {
	service := ChatService{Store: newChatStoreStub(), ModelFactory: func(providers.Config) (providers.Model, error) { return restartingMasterModel{}, nil }}
	response, err := service.Chat(context.Background(), intakeRequest("Что делает app.go?"))
	if err != nil {
		t.Fatal(err)
	}
	if response.Reply != "Целый ответ." {
		t.Fatalf("reply=%q", response.Reply)
	}
}
