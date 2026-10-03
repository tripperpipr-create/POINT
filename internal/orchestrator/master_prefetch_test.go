package orchestrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/providers"
)

// Подсказка индекса стоит перед репликой человека один раз и не трогает
// снимок мира; медленный поставщик ход не держит.
func TestMasterPrefetchGoesBeforeTheQuestionOnce(t *testing.T) {
	model := &turnModel{rounds: []roundScript{
		{calls: []providers.ToolCall{toolCall("r1", "read_file", map[string]string{"path": "api.php"})}},
		{text: "Маршрут /api/documents ведёт в DocumentsController."},
	}}
	calls := 0
	service := ChatService{Store: newChatStoreStub(), ReadTools: readingToolsStub{}, ModelFactory: model.factory(),
		Prefetch: func(_ context.Context, message string) string {
			calls++
			return "UNTRUSTED PRELIMINARY CODE CONTEXT: api.php for " + message
		}}
	if _, err := service.Chat(context.Background(), intakeRequest("Как работает /api/documents?")); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("поставщик вызван %d раз", calls)
	}
	messages := model.requests[0].Messages
	at := -1
	for index, message := range messages {
		if strings.HasPrefix(message.Content, "UNTRUSTED PRELIMINARY CODE CONTEXT") {
			if at >= 0 {
				t.Fatal("подсказка вставлена дважды")
			}
			at = index
		}
	}
	if at < 1 || !strings.HasPrefix(messages[at-1].Content, "UNTRUSTED PROJECT EVIDENCE") || !strings.Contains(messages[at+1].Content, "/api/documents") {
		t.Fatalf("подсказка не между снимком мира и репликой: %d", at)
	}
}

func TestMasterSlowPrefetchDoesNotHoldTheTurn(t *testing.T) {
	model := &turnModel{rounds: []roundScript{{text: "Ответ."}}}
	release := make(chan struct{})
	defer close(release)
	service := ChatService{Store: newChatStoreStub(), ReadTools: readingToolsStub{}, ModelFactory: model.factory(),
		Prefetch: func(context.Context, string) string {
			<-release
			return "UNTRUSTED PRELIMINARY CODE CONTEXT: late"
		}}
	started := time.Now()
	if _, err := service.Chat(context.Background(), intakeRequest("Как работает /api/documents?")); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > masterPrefetchTimeout+time.Second {
		t.Fatalf("ход ждал подсказку %s", elapsed)
	}
	for _, message := range model.requests[0].Messages {
		if strings.Contains(message.Content, "late") {
			t.Fatal("опоздавшая подсказка попала в ход")
		}
	}
}
