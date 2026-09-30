package orchestrator

import (
	"context"
	"strings"
	"testing"
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
