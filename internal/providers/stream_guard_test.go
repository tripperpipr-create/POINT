package providers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func collectStream(t *testing.T, stream func(func(ModelEvent) error) error) ([]ModelEvent, error) {
	t.Helper()
	var events []ModelEvent
	err := stream(func(event ModelEvent) error {
		events = append(events, event)
		return nil
	})
	return events, err
}

func toolCallsOf(events []ModelEvent) []ToolCall {
	var calls []ToolCall
	for _, event := range events {
		if event.Kind == EventToolCall && event.ToolCall != nil {
			calls = append(calls, *event.ToolCall)
		}
	}
	return calls
}

func streamOpenAIText(t *testing.T, sse string) ([]ModelEvent, error) {
	t.Helper()
	return collectStream(t, func(onEvent func(ModelEvent) error) error {
		return streamOpenAIResponse(context.Background(), strings.NewReader(sse), onEvent)
	})
}

// Шлюз нумерует вызовы с единицы: прежний обход 0…len-1 терял их все.
func TestOpenAIStreamKeepsToolCallsWithGappedIndexes(t *testing.T) {
	sse := `data: {"choices":[{"delta":{"tool_calls":[{"index":1,"id":"a","function":{"name":"read_file","arguments":"{\"path\":"}}]}}]}
data: {"choices":[{"delta":{"tool_calls":[{"index":1,"function":{"arguments":"\"a.go\"}"}}]}}]}
data: {"choices":[{"delta":{"tool_calls":[{"index":3,"id":"b","function":{"name":"list_files","arguments":"{}"}}]}}]}
data: [DONE]
`
	events, err := streamOpenAIText(t, sse)
	if err != nil {
		t.Fatal(err)
	}
	calls := toolCallsOf(events)
	if len(calls) != 2 || calls[0].Name != "read_file" || string(calls[0].Arguments) != `{"path":"a.go"}` || calls[1].Name != "list_files" {
		t.Fatalf("calls=%+v", calls)
	}
}

// Часть шлюзов повторяет id и имя в каждом куске.
func TestOpenAIStreamDoesNotConcatenateRepeatedIDAndName(t *testing.T) {
	sse := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"read_file","arguments":"{\"path\""}}]}}]}
data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"read_file","arguments":":\"a.go\"}"}}]}}]}
data: [DONE]
`
	events, err := streamOpenAIText(t, sse)
	if err != nil {
		t.Fatal(err)
	}
	calls := toolCallsOf(events)
	if len(calls) != 1 || calls[0].ID != "call_1" || calls[0].Name != "read_file" || calls[0].ArgumentError != "" {
		t.Fatalf("calls=%+v", calls)
	}
}

// Параллельные вызовы под одним index 0 с разными id — два вызова, не один.
func TestOpenAIStreamSplitsParallelCallsSharingIndexZero(t *testing.T) {
	sse := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"a","function":{"name":"read_file","arguments":"{\"path\":\"a\"}"}}]}}]}
data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"b","function":{"name":"read_file","arguments":"{\"path\":\"b\"}"}}]}}]}
data: [DONE]
`
	events, err := streamOpenAIText(t, sse)
	if err != nil {
		t.Fatal(err)
	}
	calls := toolCallsOf(events)
	if len(calls) != 2 || string(calls[1].Arguments) != `{"path":"b"}` || calls[0].ArgumentError != "" || calls[1].ArgumentError != "" {
		t.Fatalf("calls=%+v", calls)
	}
}

// Инструмент без параметров: llama.cpp шлёт пустую строку аргументов.
func TestOpenAIStreamTreatsEmptyArgumentsAsEmptyObject(t *testing.T) {
	sse := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"a","function":{"name":"project_map","arguments":""}}]}}]}
data: [DONE]
`
	events, err := streamOpenAIText(t, sse)
	if err != nil {
		t.Fatal(err)
	}
	calls := toolCallsOf(events)
	if len(calls) != 1 || string(calls[0].Arguments) != `{}` || calls[0].ArgumentError != "" {
		t.Fatalf("calls=%+v", calls)
	}
}

func TestOpenAIStreamExplainsToolCallCutByOutputLimit(t *testing.T) {
	sse := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"a","function":{"name":"propose_patch","arguments":"{\"path\":\"a.go\",\"content\":\"pack"}}]}}]}
data: {"choices":[{"delta":{},"finish_reason":"length"}]}
data: [DONE]
`
	events, err := streamOpenAIText(t, sse)
	if err != nil {
		t.Fatal(err)
	}
	calls := toolCallsOf(events)
	if len(calls) != 1 || !strings.Contains(calls[0].ArgumentError, "cut off by the output token limit") {
		t.Fatalf("calls=%+v", calls)
	}
}

func TestOpenAIStreamSkipsSingleUndecodableLine(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\ndata: {broken\ndata: {\"choices\":[{\"delta\":{\"content\":\"b\"}}]}\ndata: [DONE]\n"
	events, err := streamOpenAIText(t, sse)
	if err != nil {
		t.Fatal(err)
	}
	text := ""
	for _, event := range events {
		text += event.Delta
	}
	if text != "ab" {
		t.Fatalf("text=%q", text)
	}
}

func TestTransientStreamErrorRecognisesWindowsAndPosixDrops(t *testing.T) {
	cases := []error{
		errors.New("read tcp 10.0.0.2:5123->10.0.0.1:443: wsarecv: An established connection was aborted by the software in your host machine."),
		fmt.Errorf("read: %w", wsaeConnAborted),
		fmt.Errorf("read: %w", syscall.ECONNRESET),
		errStreamStalled,
		errors.New("provider error (overloaded_error): Overloaded"),
	}
	for _, err := range cases {
		if !isTransientStreamError(err) || !IsTransientProviderError(err) {
			t.Fatalf("not transient: %v", err)
		}
	}
	for _, err := range []error{context.Canceled, context.DeadlineExceeded, errors.New("provider returned 400 Bad Request")} {
		if isTransientStreamError(err) {
			t.Fatalf("must not be transient: %v", err)
		}
	}
}

// Поток начался и замолчал: сторож тишины рвёт его, HTTP-слой повторяет
// запрос, а потребитель получает EventRetry и сбрасывает начатое.
func TestOpenAIRetriesStalledStreamAfterFirstBytes(t *testing.T) {
	var requests atomic.Int32
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if requests.Add(1) == 1 {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hal\"}}]}\n\n")
			w.(http.Flusher).Flush()
			select {
			case <-release:
			case <-r.Context().Done():
			}
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	defer close(release)
	model := NewOpenAICompatible(Config{BaseURL: server.URL + "/v1", HeaderTimeoutSeconds: 5, StreamIdleSeconds: 1})
	var text strings.Builder
	retries := 0
	err := model.Stream(context.Background(), ModelRequest{Model: "m", Messages: []Message{{Role: "user", Content: "hi"}}}, func(event ModelEvent) error {
		switch event.Kind {
		case EventRetry:
			retries++
			text.Reset()
		case EventTextDelta:
			text.WriteString(event.Delta)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if retries != 1 || text.String() != "hello" || requests.Load() != 2 {
		t.Fatalf("retries=%d text=%q requests=%d", retries, text.String(), requests.Load())
	}
}

func TestIdleReaderDoesNotFireBeforeFirstByte(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		// Долгий prefill: первый байт позже срока тишины.
		time.Sleep(1500 * time.Millisecond)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	model := NewOpenAICompatible(Config{BaseURL: server.URL + "/v1", HeaderTimeoutSeconds: 5, StreamIdleSeconds: 1})
	events, err := collectStream(t, func(onEvent func(ModelEvent) error) error {
		return model.Stream(context.Background(), ModelRequest{Model: "m", Messages: []Message{{Role: "user", Content: "hi"}}}, onEvent)
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Kind == EventRetry {
			t.Fatalf("prefill must not count as a stall: %+v", events)
		}
	}
}

// Сервер отдал заголовки и замолчал навсегда: прежде сторож тишины ждал первого
// байта, и прогон висел 6,5 часа. Теперь срок первого байта рвёт обращение, и
// тот же запрос повторяется.
func TestSilentStreamBeforeFirstByteIsRetried(t *testing.T) {
	var requests atomic.Int32
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		if requests.Add(1) == 1 {
			select {
			case <-release:
			case <-r.Context().Done():
			}
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	defer close(release)
	model := NewOpenAICompatible(Config{BaseURL: server.URL + "/v1", HeaderTimeoutSeconds: 5, StreamIdleSeconds: 1, StreamFirstByteSeconds: 1})
	events, err := collectStream(t, func(onEvent func(ModelEvent) error) error {
		return model.Stream(context.Background(), ModelRequest{Model: "m", Messages: []Message{{Role: "user", Content: "hi"}}}, onEvent)
	})
	if err != nil {
		t.Fatal(err)
	}
	retried := false
	for _, event := range events {
		retried = retried || event.Kind == EventRetry
	}
	if !retried || requests.Load() != 2 {
		t.Fatalf("silent stream was not cut: requests=%d events=%+v", requests.Load(), events)
	}
}

func TestAnthropicRetriesOverloadedStreamAndReportsStopReason(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if requests.Add(1) == 1 {
			fmt.Fprint(w, "data: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}\n\n")
			return
		}
		fmt.Fprint(w, "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\"}}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"done\"}}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":3}}\n\n")
	}))
	defer server.Close()
	model := NewAnthropic(Config{BaseURL: server.URL + "/v1", APIKey: "k", HeaderTimeoutSeconds: 5})
	events, err := collectStream(t, func(onEvent func(ModelEvent) error) error {
		return model.Stream(context.Background(), ModelRequest{Model: "claude", MaxOutputTokens: 100, Messages: []Message{{Role: "user", Content: "hi"}}}, onEvent)
	})
	if err != nil {
		t.Fatal(err)
	}
	var finish string
	retried := false
	for _, event := range events {
		if event.Kind == EventFinish {
			finish = event.FinishReason
		}
		if event.Kind == EventRetry {
			retried = true
		}
	}
	if !retried || finish != "stop" || requests.Load() != 2 {
		t.Fatalf("retried=%v finish=%q requests=%d events=%+v", retried, finish, requests.Load(), events)
	}
}

func TestAnthropicReportsReasoningTruncationAtMaxTokens(t *testing.T) {
	sse := "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\"}}\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"hm\"}}\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"max_tokens\"},\"usage\":{\"output_tokens\":2048}}\n"
	_, err := collectStream(t, func(onEvent func(ModelEvent) error) error {
		return streamAnthropicResponse(context.Background(), strings.NewReader(sse), onEvent)
	})
	if !IsTruncatedReasoningError(err) {
		t.Fatalf("err=%v", err)
	}
}

func TestOllamaKeepsModelLoadedAndReadsThinking(t *testing.T) {
	var body string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(raw)
		body = string(raw)
		fmt.Fprintln(w, `{"message":{"role":"assistant","thinking":"plan"},"done":false}`)
		fmt.Fprintln(w, `{"message":{"role":"assistant","content":"ok"},"done":true,"done_reason":"stop"}`)
	}))
	defer server.Close()
	model := NewOllama(Config{BaseURL: server.URL, TimeoutSeconds: 5})
	events, err := collectStream(t, func(onEvent func(ModelEvent) error) error {
		return model.Stream(context.Background(), ModelRequest{Model: "qwen", Messages: []Message{{Role: "user", Content: "hi"}}}, onEvent)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, `"keep_alive":"30m"`) || strings.Contains(body, "num_predict") {
		t.Fatalf("body=%s", body)
	}
	sawThinking := false
	for _, event := range events {
		if event.Kind == EventReasoning && event.Reasoning != nil && event.Reasoning.Text == "plan" {
			sawThinking = true
		}
	}
	if !sawThinking {
		t.Fatalf("events=%+v", events)
	}
}

func TestBusyStatusWaitsSecondsWithoutRetryAfter(t *testing.T) {
	if got := statusRetryDelay(http.StatusServiceUnavailable, "", 1, time.Now()); got != 2*time.Second {
		t.Fatalf("503 first delay=%s", got)
	}
	if got := statusRetryDelay(http.StatusTooManyRequests, "", 2, time.Now()); got != 4*time.Second {
		t.Fatalf("429 second delay=%s", got)
	}
	if got := statusRetryDelay(http.StatusBadGateway, "", 1, time.Now()); got != baseRetryDelay {
		t.Fatalf("502 delay=%s", got)
	}
}

func TestStreamingClientsShareTransportPerHeaderTimeout(t *testing.T) {
	first := streamingClient(Config{HeaderTimeoutSeconds: 45})
	second := streamingClient(Config{HeaderTimeoutSeconds: 45})
	if first.Transport != second.Transport {
		t.Fatal("clients with the same header timeout must share one connection pool")
	}
}
