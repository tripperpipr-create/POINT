package providers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Попадание в кэш префикса видно в расходе. InputTokens остаётся всем
// промптом — по нему Point сверяет окно и бюджет, — а CachedInputTokens
// говорит, сколько из него провайдер не пересчитывал.
func TestUsageReportsCachedPromptTokens(t *testing.T) {
	anthropic := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"type":"message_start","message":{"usage":{"input_tokens":20,"cache_read_input_tokens":900,"cache_creation_input_tokens":80,"output_tokens":0}}}`+"\n\n")
		fmt.Fprint(w, `data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`+"\n\n")
		fmt.Fprint(w, `data: {"type":"message_stop"}`+"\n\n")
	}))
	defer anthropic.Close()
	events, err := collectStream(t, func(onEvent func(ModelEvent) error) error {
		return NewAnthropic(Config{BaseURL: anthropic.URL, APIKey: "k"}).Stream(context.Background(), ModelRequest{Model: "claude", Messages: []Message{{Role: "user", Content: "hi"}}}, onEvent)
	})
	if err != nil {
		t.Fatal(err)
	}
	if usage := firstUsage(events); usage.InputTokens != 1000 || usage.CachedInputTokens != 900 {
		t.Fatalf("anthropic usage = %+v, want input 1000 with 900 cached", usage)
	}

	openai := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`+"\n\n")
		fmt.Fprint(w, `data: {"choices":[],"usage":{"prompt_tokens":2000,"completion_tokens":3,"prompt_tokens_details":{"cached_tokens":1536}}}`+"\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer openai.Close()
	events, err = collectStream(t, func(onEvent func(ModelEvent) error) error {
		return NewOpenAICompatible(Config{BaseURL: openai.URL + "/v1"}).Stream(context.Background(), ModelRequest{Model: "m", Messages: []Message{{Role: "user", Content: "hi"}}}, onEvent)
	})
	if err != nil {
		t.Fatal(err)
	}
	if usage := firstUsage(events); usage.InputTokens != 2000 || usage.CachedInputTokens != 1536 {
		t.Fatalf("openai usage = %+v, want input 2000 with 1536 cached", usage)
	}
}

func firstUsage(events []ModelEvent) ModelEvent {
	for _, event := range events {
		if event.Kind == EventUsage {
			return event
		}
	}
	return ModelEvent{}
}
