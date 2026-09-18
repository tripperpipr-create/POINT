package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

func TestOpenAICompatibleSendsSingleLeadingSystem(t *testing.T) {
	var raw []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	model := NewOpenAICompatible(Config{BaseURL: server.URL + "/v1", TimeoutSeconds: 5})
	err := model.Stream(context.Background(), ModelRequest{
		Model: "Qwen3.5-122B",
		Messages: []Message{
			{Role: "system", Content: "You are Point Companion"},
			{Role: "system", Content: "Untrusted local project context"},
			{Role: "user", Content: "Что сломано?"},
			{Role: "assistant", Content: "Пока пусто"},
			{Role: "user", Content: "Ещё раз"},
		},
	}, func(ModelEvent) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Messages []struct {
			Role string `json:"role"`
		} `json:"messages"`
		Tools any `json:"tools"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body.Tools != nil {
		t.Fatalf("tools should be omitted, body=%s", raw)
	}
	systemCount := 0
	for i, message := range body.Messages {
		if message.Role == "system" {
			systemCount++
			if i != 0 {
				t.Fatalf("system must be first, messages=%s", raw)
			}
		}
	}
	if systemCount != 1 {
		t.Fatalf("expected one system message, body=%s", raw)
	}
}

func TestNormalizeChatMessagesMergesSystemsAndDropsEmpty(t *testing.T) {
	got := NormalizeChatMessages([]Message{
		{Role: "system", Content: "rules"},
		{Role: "system", Content: "context"},
		{Role: "user", Content: ""},
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
		{Role: "system", Content: "late system"},
	})
	if len(got) != 3 {
		t.Fatalf("messages=%#v", got)
	}
	if got[0].Role != "system" || !strings.Contains(got[0].Content, "rules") || !strings.Contains(got[0].Content, "context") || !strings.Contains(got[0].Content, "late system") {
		t.Fatalf("system=%#v", got[0])
	}
	if got[1].Role != "user" || got[1].Content != "hello" || got[2].Role != "assistant" {
		t.Fatalf("rest=%#v", got[1:])
	}
}

func TestNormalizeChatMessagesDoesNotStartWithAssistant(t *testing.T) {
	got := NormalizeChatMessages([]Message{{Role: "assistant", Content: "orphan"}})
	if len(got) != 2 || got[0].Role != "user" || got[1].Role != "assistant" {
		t.Fatalf("messages=%#v", got)
	}
}

func TestOpenAICompatibleOmitsEmptyToolsArray(t *testing.T) {
	var raw []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	model := NewOpenAICompatible(Config{BaseURL: server.URL + "/v1", TimeoutSeconds: 5})
	if err := model.Stream(context.Background(), ModelRequest{Model: "Qwen3.5-122B", Messages: []Message{{Role: "user", Content: "hi"}}, Tools: nil}, func(ModelEvent) error { return nil }); err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["tools"]; ok {
		t.Fatalf("empty tools must be omitted, body=%s", raw)
	}

	raw = nil
	if err := model.Stream(context.Background(), ModelRequest{
		Model: "Qwen3.5-122B", Messages: []Message{{Role: "user", Content: "hi"}},
		Tools: []domain.ToolDefinition{{Name: "read_file", InputSchema: json.RawMessage(`{"type":"object"}`)}},
	}, func(ModelEvent) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	tools, ok := body["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("expected one tool, body=%s", raw)
	}
}

func TestOpenAICompatibleStreamingTextToolAndUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path=%s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("authorization header missing")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		chunks := []any{
			map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "Checking "}}}},
			map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "call_1", "function": map[string]any{"name": "read_file", "arguments": "{\"path\":"}}}}}}},
			map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "function": map[string]any{"arguments": "\"main.go\"}"}}}}}}, "usage": map[string]any{"prompt_tokens": 12, "completion_tokens": 4}},
		}
		for _, chunk := range chunks {
			data, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "data: %s\n\n", data)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	model := NewOpenAICompatible(Config{BaseURL: server.URL + "/v1", APIKey: "test-key", TimeoutSeconds: 5})
	var text string
	var call *ToolCall
	var usage ModelEvent
	err := model.Stream(context.Background(), ModelRequest{Model: "test", Messages: []Message{{Role: "user", Content: "inspect"}}, Tools: []domain.ToolDefinition{{Name: "read_file", InputSchema: json.RawMessage(`{"type":"object"}`)}}}, func(event ModelEvent) error {
		switch event.Kind {
		case EventTextDelta:
			text += event.Delta
		case EventToolCall:
			call = event.ToolCall
		case EventUsage:
			usage = event
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if text != "Checking " {
		t.Fatalf("text=%q", text)
	}
	if call == nil || call.Name != "read_file" || string(call.Arguments) != `{"path":"main.go"}` {
		t.Fatalf("call=%#v", call)
	}
	if usage.InputTokens != 12 || usage.OutputTokens != 4 {
		t.Fatalf("usage=%#v", usage)
	}
}

func TestOpenAICompatibleRetriesTransientStatusWithBoundedBackoff(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		if requests < 3 {
			w.Header().Set("Retry-After", "0")
			http.Error(w, "busy", http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"recovered\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	model := NewOpenAICompatible(Config{BaseURL: server.URL, TimeoutSeconds: 5})
	var retries []ModelEvent
	var text string
	err := model.Stream(context.Background(), ModelRequest{Model: "test", Messages: []Message{{Role: "user", Content: "work"}}}, func(event ModelEvent) error {
		if event.Kind == EventRetry {
			retries = append(retries, event)
		}
		if event.Kind == EventTextDelta {
			text += event.Delta
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if requests != 3 || text != "recovered" || len(retries) != 2 || retries[0].Attempt != 2 || retries[1].Attempt != 3 {
		t.Fatalf("requests=%d text=%q retries=%#v", requests, text, retries)
	}
}

func TestOpenAICompatibleDoesNotRetryPermanentStatus(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer server.Close()
	model := NewOpenAICompatible(Config{BaseURL: server.URL, TimeoutSeconds: 5})
	err := model.Stream(context.Background(), ModelRequest{Model: "test"}, func(ModelEvent) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "400 Bad Request") || requests != 1 {
		t.Fatalf("err=%v requests=%d", err, requests)
	}
}

func TestOpenAICompatibleReturnsMalformedToolArgumentsForModelCorrection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"bad-call\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\"}}]}}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	model := NewOpenAICompatible(Config{BaseURL: server.URL, TimeoutSeconds: 5})
	var call *ToolCall
	err := model.Stream(context.Background(), ModelRequest{Model: "test"}, func(event ModelEvent) error {
		if event.Kind == EventToolCall {
			call = event.ToolCall
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if call == nil || call.Name != "read_file" || string(call.Arguments) != `{}` || !strings.Contains(call.ArgumentError, "not valid JSON") {
		t.Fatalf("malformed call was not normalized for correction: %#v", call)
	}
}

func TestOllamaStreaming(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("path=%s", r.URL.Path)
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request["stream"] != true {
			t.Error("stream flag missing")
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		fmt.Fprintln(w, `{"message":{"role":"assistant","content":"hello "},"done":false}`)
		fmt.Fprintln(w, `{"message":{"role":"assistant","tool_calls":[{"function":{"name":"list_files","arguments":{"maxDepth":2}}}]},"done":false}`)
		fmt.Fprintln(w, `{"message":{"role":"assistant","content":"world"},"done":true,"prompt_eval_count":8,"eval_count":3}`)
	}))
	defer server.Close()
	model := NewOllama(Config{BaseURL: server.URL, TimeoutSeconds: 5})
	var parts []string
	var toolName string
	var inputTokens int
	err := model.Stream(context.Background(), ModelRequest{Model: "test", Messages: []Message{{Role: "user", Content: "inspect"}}}, func(event ModelEvent) error {
		if event.Kind == EventTextDelta {
			parts = append(parts, event.Delta)
		}
		if event.ToolCall != nil {
			toolName = event.ToolCall.Name
		}
		if event.Kind == EventUsage {
			inputTokens = event.InputTokens
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(parts, "") != "hello world" || toolName != "list_files" || inputTokens != 8 {
		t.Fatalf("parts=%v tool=%s tokens=%d", parts, toolName, inputTokens)
	}
}

func TestOllamaRetriesTransientStatus(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		if requests == 1 {
			w.Header().Set("Retry-After", "0")
			http.Error(w, "warming up", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprintln(w, `{"message":{"role":"assistant","content":"ready"},"done":true}`)
	}))
	defer server.Close()
	model := NewOllama(Config{BaseURL: server.URL, TimeoutSeconds: 5})
	var text string
	var retries int
	err := model.Stream(context.Background(), ModelRequest{Model: "test"}, func(event ModelEvent) error {
		if event.Kind == EventRetry {
			retries++
		}
		if event.Kind == EventTextDelta {
			text += event.Delta
		}
		return nil
	})
	if err != nil || requests != 2 || retries != 1 || text != "ready" {
		t.Fatalf("err=%v requests=%d retries=%d text=%q", err, requests, retries, text)
	}
}

func TestProviderMessageEncodingIncludesImages(t *testing.T) {
	messages := []Message{{Role: "user", Content: "Опиши схему", Images: []ImageContent{{MediaType: "image/png", DataBase64: "aGVsbG8="}}}}
	openAI := openAIMessages(messages)
	parts, ok := openAI[0]["content"].([]map[string]any)
	if !ok || len(parts) != 2 || parts[0]["type"] != "text" || parts[1]["type"] != "image_url" {
		t.Fatalf("OpenAI message=%#v", openAI[0])
	}
	imageURL, ok := parts[1]["image_url"].(map[string]string)
	if !ok || imageURL["url"] != "data:image/png;base64,aGVsbG8=" {
		t.Fatalf("OpenAI image part=%#v", parts[1])
	}
	ollama := wireMessages(messages)
	if len(ollama) != 1 || len(ollama[0].Images) != 1 || ollama[0].Images[0] != "aGVsbG8=" {
		t.Fatalf("Ollama messages=%#v", ollama)
	}
}

func TestNormalizeCompatibleBaseURLAddsOpenAIVersion(t *testing.T) {
	normalized, err := NormalizeCompatibleBaseURL("https://llmux.corp.example", domain.ProviderOpenAI)
	if err != nil || normalized != "https://llmux.corp.example/v1" {
		t.Fatalf("bare host=%q err=%v", normalized, err)
	}
	kept, err := NormalizeCompatibleBaseURL("https://llmux.corp.example/openai/v1/", domain.ProviderOpenAI)
	if err != nil || kept != "https://llmux.corp.example/openai/v1" {
		t.Fatalf("explicit path=%q err=%v", kept, err)
	}
	if _, err = NormalizeCompatibleBaseURL("https://user:secret@llmux.example/v1", domain.ProviderOpenAI); err == nil {
		t.Fatal("URL credentials were accepted")
	}
}

func TestDiscoverOpenAICompatibleModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("path=%s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("authorization header missing")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{
			map[string]any{"id": "z-model", "owned_by": "local"},
			map[string]any{"id": "a-model", "owned_by": "vendor"},
			map[string]any{"id": "a-model", "owned_by": "duplicate"},
		}})
	}))
	defer server.Close()
	models, err := DiscoverModels(context.Background(), Config{Kind: domain.ProviderOpenAI, BaseURL: server.URL + "/v1", APIKey: "secret", TimeoutSeconds: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].ID != "a-model" || models[1].ID != "z-model" || models[0].OwnedBy != "vendor" {
		t.Fatalf("models=%#v", models)
	}
}

func TestDiscoverOllamaModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Errorf("path=%s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"models": []any{
			map[string]any{"name": "qwen:7b", "size": 1234, "modified_at": "2026-01-02T03:04:05Z"},
		}})
	}))
	defer server.Close()
	models, err := DiscoverModels(context.Background(), Config{Kind: domain.ProviderOllama, BaseURL: server.URL, TimeoutSeconds: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != "qwen:7b" || models[0].Size != 1234 || models[0].ModifiedAt.IsZero() {
		t.Fatalf("models=%#v", models)
	}
}

func TestDiscoverModelsRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"models":[{"name":"` + strings.Repeat("x", maxDiscoveryResponseBytes) + `"}]}`))
	}))
	defer server.Close()
	if _, err := DiscoverModels(context.Background(), Config{Kind: domain.ProviderOllama, BaseURL: server.URL, TimeoutSeconds: 5}); err == nil || !strings.Contains(err.Error(), "exceeds 2 MiB") {
		t.Fatalf("oversized response error=%v", err)
	}
}

func TestNewRejectsUnsupportedProvider(t *testing.T) {
	if _, err := New(Config{Kind: "unknown-provider"}); err == nil || !strings.Contains(err.Error(), "unsupported provider") {
		t.Fatalf("expected unsupported provider error, got %v", err)
	}
	if _, err := New(Config{Kind: domain.ProviderCursor}); err == nil || !strings.Contains(err.Error(), "removed") {
		t.Fatalf("expected cursor removed error, got %v", err)
	}
	model, err := New(Config{Kind: domain.ProviderOllama, BaseURL: "http://127.0.0.1:11434"})
	if err != nil || model == nil {
		t.Fatalf("ollama provider err=%v model=%v", err, model)
	}
}

// Думающая модель за самостоятельно поднятым рантаймом: рассуждение идёт
// отдельным полем, ответа нет вовсе, а шлюз в конце пишет про «закрытый
// поток». Проверяется, что рассуждение не попало в текст ответа и что наружу
// вышла причина обрыва, а не чужая формулировка.
func TestOpenAICompatibleReportsReasoningTruncation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"reasoning\":\"Here\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\" we go\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"\"},\"finish_reason\":\"length\"}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":1524,\"completion_tokens\":1200}}\n\n")
		fmt.Fprint(w, "data: {\"error\":{\"message\":\"Upstream closed the stream without sending any content\"}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	model := NewOpenAICompatible(Config{BaseURL: server.URL + "/v1", TimeoutSeconds: 5})
	var text, reasoning strings.Builder
	err := model.Stream(context.Background(), ModelRequest{
		Model:    "Qwen3.6-35B-A3B",
		Messages: []Message{{Role: "user", Content: "Привет"}},
	}, func(event ModelEvent) error {
		switch event.Kind {
		case EventTextDelta:
			text.WriteString(event.Delta)
		case EventReasoning:
			reasoning.WriteString(event.Delta)
		}
		return nil
	})
	if err == nil {
		t.Fatal("обрыв по лимиту принят за успешный ответ")
	}
	if !strings.Contains(err.Error(), "1200") || !strings.Contains(err.Error(), "finish_reason=length") {
		t.Fatalf("причина обрыва не названа: %v", err)
	}
	if strings.Contains(err.Error(), "Upstream closed") {
		t.Fatalf("формулировка шлюза не заменена своей: %v", err)
	}
	if text.String() != "" {
		t.Fatalf("рассуждение утекло в текст ответа: %q", text.String())
	}
	if reasoning.String() != "Here we go" {
		t.Fatalf("рассуждение разобрано неверно: %q", reasoning.String())
	}
}

// Гашение размышления уходит в тело только по явной просьбе: официальный
// OpenAI отвечает на лишнее поле 400, и включать его всем нельзя.
func TestOpenAICompatibleSendsChatTemplateKwargsOnlyWhenAsked(t *testing.T) {
	for _, disable := range []bool{false, true} {
		var raw []byte
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
		}))
		model := NewOpenAICompatible(Config{BaseURL: server.URL + "/v1", TimeoutSeconds: 5})
		err := model.Stream(context.Background(), ModelRequest{
			Model:           "Qwen3.6-35B-A3B",
			Messages:        []Message{{Role: "user", Content: "Привет"}},
			DisableThinking: disable,
		}, func(ModelEvent) error { return nil })
		server.Close()
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			ChatTemplateKwargs *struct {
				EnableThinking *bool `json:"enable_thinking"`
			} `json:"chat_template_kwargs"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatal(err)
		}
		if !disable {
			if body.ChatTemplateKwargs != nil {
				t.Fatalf("поле ушло без просьбы, body=%s", raw)
			}
			continue
		}
		if body.ChatTemplateKwargs == nil || body.ChatTemplateKwargs.EnableThinking == nil || *body.ChatTemplateKwargs.EnableThinking {
			t.Fatalf("размышление не погашено, body=%s", raw)
		}
	}
}

func TestSelfHostedProviderPreset(t *testing.T) {
	for _, id := range []string{"llmux", "custom", "vllm", "lm-studio", "llama-cpp", "ollama"} {
		if !domain.SelfHostedProviderPreset(id) {
			t.Fatalf("пресет %q должен считаться своим рантаймом", id)
		}
	}
	for _, id := range []string{"openai", "openrouter", "gemini", "groq", "anthropic", "azure-openai", "mistral", "deepseek", "together", "cursor", "", "нет такого"} {
		if domain.SelfHostedProviderPreset(id) {
			t.Fatalf("пресет %q чужой, лишние поля в теле ему слать нельзя", id)
		}
	}
}

// Попытка, которой не хватит времени, — не вторая попытка, а потерянный ответ.
// На живом замере первая попытка сожгла таймаут провайдера (75 с), вторая
// началась на остатке общего ожидания (90 с), умерла вместе с оборванным
// запросом и унесла с собой местный разбор.
func TestRetryStopsWhenDeadlineLeavesNoRoom(t *testing.T) {
	for _, tc := range []struct {
		name    string
		left    time.Duration
		delay   time.Duration
		allowed bool
	}{
		{name: "времени вдоволь", left: 60 * time.Second, delay: 250 * time.Millisecond, allowed: true},
		{name: "остаток меньше запаса", left: 3 * time.Second, delay: 250 * time.Millisecond, allowed: false},
		{name: "пауза съедает остаток", left: 6 * time.Second, delay: 4 * time.Second, allowed: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), tc.left)
			defer cancel()
			if got := shouldRetry(ctx, 1, tc.delay); got != tc.allowed {
				t.Fatalf("повтор разрешён=%v, ждали %v", got, tc.allowed)
			}
		})
	}
	// Ожидание без срока повторам не мешает: край есть не у каждого вызова.
	if !shouldRetry(context.Background(), 1, 250*time.Millisecond) {
		t.Fatal("без срока повтор запрещён")
	}
	// Отменённый контекст и исчерпанные попытки закрывают повтор без разговоров.
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if shouldRetry(cancelled, 1, 0) {
		t.Fatal("повтор разрешён после отмены")
	}
	if shouldRetry(context.Background(), maxStreamAttempts, 0) {
		t.Fatal("повтор разрешён сверх числа попыток")
	}
}

// Молчащий шлюз обязан отвечать отказом по сроку заголовков, а не по общему.
// На живом замере llmux не прислал заголовков и за семьдесят пять секунд: всё
// это время человек ждал ответа, который уже не мог прийти.
func TestStreamGivesUpOnSilentProviderByHeaderTimeout(t *testing.T) {
	// Обработчик молчит дольше срока заголовков и отпускает соединение сам:
	// закрытие сервера ждёт своих обработчиков, и канал здесь дал бы дедлок.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(3 * time.Second)
	}))
	defer server.Close()
	model, err := New(Config{
		Kind: domain.ProviderOpenAI, BaseURL: server.URL, APIKey: "k",
		TimeoutSeconds: 60, HeaderTimeoutSeconds: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	err = model.Stream(context.Background(), ModelRequest{Model: "m", Messages: []Message{{Role: "user", Content: "привет"}}}, func(ModelEvent) error { return nil })
	if err == nil {
		t.Fatal("молчание шлюза выдано за ответ")
	}
	// Три попытки по секунде с паузами — но заведомо меньше общего срока в минуту.
	if elapsed := time.Since(started); elapsed > 30*time.Second {
		t.Fatalf("отказ пришёл через %s — ждали срок заголовков, а не общий", elapsed)
	}
}

// Без отдельного срока заголовков поведение прежнее: один срок на всё.
func TestStreamingClientKeepsSingleTimeoutWhenHeaderBudgetUnset(t *testing.T) {
	plain := streamingClient(Config{TimeoutSeconds: 7})
	if plain.Timeout != 7*time.Second {
		t.Fatalf("общий срок потерян: %s", plain.Timeout)
	}
	split := streamingClient(Config{TimeoutSeconds: 7, HeaderTimeoutSeconds: 3})
	if split.Timeout != 0 {
		t.Fatalf("общий срок рубит поток: %s", split.Timeout)
	}
	transport, ok := split.Transport.(*http.Transport)
	if !ok || transport.ResponseHeaderTimeout != 3*time.Second {
		t.Fatalf("срок заголовков не задан: %#v", split.Transport)
	}
}
