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

	"local-agent-workbench/internal/domain"
)

// Конверт Anthropic отличается от OpenAI в четырёх местах сразу, и каждое из
// них — тихая поломка, если перепутать: системная инструкция уходит своим
// полем, результат инструмента приходит блоком внутри сообщения пользователя,
// картинка описывается base64-источником, а max_tokens обязателен.
func TestAnthropicBuildsMessagesEnvelope(t *testing.T) {
	var raw []byte
	var headers http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		headers = r.Header.Clone()
		if !strings.HasSuffix(r.URL.Path, "/messages") {
			t.Errorf("запрос ушёл на %s вместо /messages", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n")
	}))
	defer server.Close()

	model := NewAnthropic(Config{Kind: domain.ProviderAnthropic, BaseURL: server.URL + "/v1", APIKey: "k-secret", TimeoutSeconds: 5})
	err := model.Stream(context.Background(), ModelRequest{
		Model: "claude-sonnet-4-5",
		Messages: []Message{
			{Role: "system", Content: "Ты Point"},
			{Role: "user", Content: "Что в файле?", Images: []ImageContent{{MediaType: "image/png", DataBase64: "AAAA"}}},
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "tu_1", Name: "read_file", Arguments: json.RawMessage(`{"path":"main.go"}`)}}},
			{Role: "tool", ToolCallID: "tu_1", Content: "package main"},
		},
		Tools:           []domain.ToolDefinition{{Name: "read_file", Description: "чтение", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		MaxOutputTokens: 0,
	}, func(ModelEvent) error { return nil })
	if err != nil {
		t.Fatal(err)
	}

	if headers.Get("x-api-key") != "k-secret" || headers.Get("anthropic-version") == "" {
		t.Fatalf("заголовки авторизации не выставлены: %v", headers)
	}
	var body struct {
		System    string `json:"system"`
		MaxTokens int    `json:"max_tokens"`
		Tools     []struct {
			Name        string         `json:"name"`
			InputSchema map[string]any `json:"input_schema"`
		} `json:"tools"`
		Messages []struct {
			Role    string `json:"role"`
			Content []struct {
				Type      string `json:"type"`
				Text      string `json:"text"`
				ID        string `json:"id"`
				Name      string `json:"name"`
				ToolUseID string `json:"tool_use_id"`
				Source    struct {
					Type      string `json:"type"`
					MediaType string `json:"media_type"`
					Data      string `json:"data"`
				} `json:"source"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err = json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("тело запроса не разобрано: %v (%s)", err, raw)
	}
	if body.System != "Ты Point" {
		t.Fatalf("системная инструкция не вынесена в поле system: %q", body.System)
	}
	for _, message := range body.Messages {
		if message.Role == "system" {
			t.Fatal("системная роль осталась в messages — Anthropic такой запрос отвергнет")
		}
	}
	if body.MaxTokens <= 0 {
		t.Fatalf("max_tokens обязателен, получено %d", body.MaxTokens)
	}
	if len(body.Tools) != 1 || body.Tools[0].InputSchema == nil {
		t.Fatalf("инструменты не переведены в input_schema: %+v", body.Tools)
	}
	if len(body.Messages) != 3 {
		t.Fatalf("ожидалось три сообщения (user, assistant, tool_result), получено %d: %s", len(body.Messages), raw)
	}
	image := body.Messages[0].Content[1]
	if image.Type != "image" || image.Source.Type != "base64" || image.Source.MediaType != "image/png" {
		t.Fatalf("картинка не описана base64-источником: %+v", image)
	}
	use := body.Messages[1].Content[0]
	if use.Type != "tool_use" || use.ID != "tu_1" || use.Name != "read_file" {
		t.Fatalf("вызов инструмента не переведён в tool_use: %+v", use)
	}
	result := body.Messages[2]
	if result.Role != "user" || result.Content[0].Type != "tool_result" || result.Content[0].ToolUseID != "tu_1" {
		t.Fatalf("результат инструмента должен быть блоком в сообщении пользователя: %+v", result)
	}
}

// Поток Anthropic собирает аргументы инструмента по кускам и отдаёт usage
// отдельным событием: без сборки квест получил бы обрезанный JSON, без usage
// расход остался бы неизвестным.
func TestAnthropicStreamCollectsToolCallAndUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, line := range []string{
			`{"type":"message_start","message":{"usage":{"input_tokens":11,"output_tokens":0}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text"}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Смотрю "}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"файл"}}`,
			`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"tu_9","name":"read_file"}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"path\":"}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"main.go\"}"}}`,
			`{"type":"message_delta","usage":{"input_tokens":11,"output_tokens":7}}`,
		} {
			fmt.Fprintf(w, "event: x\ndata: %s\n\n", line)
		}
	}))
	defer server.Close()

	model := NewAnthropic(Config{Kind: domain.ProviderAnthropic, BaseURL: server.URL + "/v1", TimeoutSeconds: 5})
	var text strings.Builder
	var calls []ToolCall
	usage := [2]int{}
	err := model.Stream(context.Background(), ModelRequest{Model: "claude-sonnet-4-5", Messages: []Message{{Role: "user", Content: "п"}}},
		func(event ModelEvent) error {
			switch event.Kind {
			case EventTextDelta:
				text.WriteString(event.Delta)
			case EventToolCall:
				calls = append(calls, *event.ToolCall)
			case EventUsage:
				usage = [2]int{event.InputTokens, event.OutputTokens}
			}
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if text.String() != "Смотрю файл" {
		t.Fatalf("текст собран неверно: %q", text.String())
	}
	if len(calls) != 1 || calls[0].ID != "tu_9" || calls[0].Name != "read_file" {
		t.Fatalf("вызов инструмента не собран: %+v", calls)
	}
	if string(calls[0].Arguments) != `{"path":"main.go"}` || calls[0].ArgumentError != "" {
		t.Fatalf("аргументы собраны неверно: %s (%s)", calls[0].Arguments, calls[0].ArgumentError)
	}
	if usage != [2]int{11, 7} {
		t.Fatalf("расход не зафиксирован: %v", usage)
	}
}

// Размышление возвращается в следующий запрос дословно.
//
// При включённом thinking Anthropic отвергает запрос, если предыдущий ответ
// содержал вызов инструмента, а блоки размышления с подписью не вернулись.
// Разбор потока знал только text_delta и input_json_delta: блоки терялись, и
// любой агент с рассуждением умирал на втором ходу квеста.
func TestAnthropicRoundTripsThinkingBlocks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, line := range []string{
			`{"type":"content_block_start","index":0,"content_block":{"type":"thinking"}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Надо "}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"прочитать файл"}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig-abc"}}`,
			`{"type":"content_block_start","index":1,"content_block":{"type":"redacted_thinking","data":"zzz"}}`,
			`{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"tu_1","name":"read_file"}}`,
			`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{}"}}`,
		} {
			fmt.Fprintf(w, "event: x\ndata: %s\n\n", line)
		}
	}))
	defer server.Close()

	model := NewAnthropic(Config{Kind: domain.ProviderAnthropic, BaseURL: server.URL + "/v1", TimeoutSeconds: 5})
	var thoughts []ReasoningBlock
	var text strings.Builder
	err := model.Stream(context.Background(),
		ModelRequest{Model: "claude-sonnet-4-5", ReasoningEffort: "medium", Messages: []Message{{Role: "user", Content: "п"}}},
		func(event ModelEvent) error {
			switch event.Kind {
			case EventReasoning:
				thoughts = append(thoughts, *event.Reasoning)
			case EventTextDelta:
				text.WriteString(event.Delta)
			}
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if len(thoughts) != 2 {
		t.Fatalf("блоки размышления не собраны: %+v", thoughts)
	}
	if thoughts[0].Type != "thinking" || thoughts[0].Text != "Надо прочитать файл" || thoughts[0].Signature != "sig-abc" {
		t.Fatalf("текст или подпись размышления потеряны: %+v", thoughts[0])
	}
	if thoughts[1].Type != "redacted_thinking" || thoughts[1].Data != "zzz" {
		t.Fatalf("скрытый блок не сохранён как есть: %+v", thoughts[1])
	}
	// Наружу размышление не течёт: это ход модели, а не ответ человеку.
	if text.String() != "" {
		t.Fatalf("размышление утекло в ответ человеку: %q", text.String())
	}

	// И возвращается в следующий запрос первыми блоками — в том же порядке.
	messages, _ := anthropicMessages([]Message{
		{Role: "user", Content: "прочитай main.go"},
		{Role: "assistant", Content: "смотрю", Reasoning: thoughts, ToolCalls: []ToolCall{{ID: "tu_1", Name: "read_file", Arguments: []byte(`{}`)}}},
	})
	blocks, ok := messages[1]["content"].([]map[string]any)
	if !ok || len(blocks) != 4 {
		t.Fatalf("ход собран неверно: %+v", messages[1]["content"])
	}
	if blocks[0]["type"] != "thinking" || blocks[0]["thinking"] != "Надо прочитать файл" || blocks[0]["signature"] != "sig-abc" {
		t.Fatalf("первый блок не размышление с подписью: %+v", blocks[0])
	}
	if blocks[1]["type"] != "redacted_thinking" || blocks[1]["data"] != "zzz" {
		t.Fatalf("скрытый блок не вернулся: %+v", blocks[1])
	}
	if blocks[2]["type"] != "text" || blocks[3]["type"] != "tool_use" {
		t.Fatalf("текст и вызов должны идти после размышления: %+v %+v", blocks[2], blocks[3])
	}
}

// Azure — тот же протокол, но с другим адресом и заголовком. Ошибка здесь
// выглядит как «модель не отвечает», а причина в форме URL.
func TestAzureOpenAIUsesDeploymentPathAndAPIKeyHeader(t *testing.T) {
	var path, query, keyHeader, bearer string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, query = r.URL.Path, r.URL.Query().Get("api-version")
		keyHeader, bearer = r.Header.Get("api-key"), r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()

	model := NewOpenAICompatible(Config{
		Kind: domain.ProviderAzureOpenAI, BaseURL: server.URL, APIKey: "azure-key",
		APIVersion: "2024-10-21", TimeoutSeconds: 5,
	})
	if err := model.Stream(context.Background(), ModelRequest{
		Model: "gpt-4o-prod", Messages: []Message{{Role: "user", Content: "привет"}},
	}, func(ModelEvent) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if path != "/openai/deployments/gpt-4o-prod/chat/completions" {
		t.Fatalf("путь Azure собран неверно: %s", path)
	}
	if query != "2024-10-21" {
		t.Fatalf("api-version не передана: %q", query)
	}
	if keyHeader != "azure-key" || bearer != "" {
		t.Fatalf("Azure авторизуется заголовком api-key, а не Bearer: api-key=%q authorization=%q", keyHeader, bearer)
	}
}

// Пустая api-version — не повод отправить запрос без неё: Azure откажет, и
// причина будет выглядеть как ошибка модели.
func TestAzureFallsBackToPinnedAPIVersion(t *testing.T) {
	var query string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query().Get("api-version")
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	model := NewOpenAICompatible(Config{Kind: domain.ProviderAzureOpenAI, BaseURL: server.URL, TimeoutSeconds: 5})
	if err := model.Stream(context.Background(), ModelRequest{Model: "d", Messages: []Message{{Role: "user", Content: "п"}}},
		func(ModelEvent) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if query != defaultAzureAPIVersion {
		t.Fatalf("подставлена версия %q вместо закреплённой", query)
	}
}

// Адрес Azure — это ресурс, а не путь до /v1: приписанная версия пути сломала
// бы каждый запрос.
func TestNormalizeBaseURLKeepsAzureResourceRootAndAddsV1ToAnthropic(t *testing.T) {
	azure, err := NormalizeCompatibleBaseURL("https://point.openai.azure.com/", domain.ProviderAzureOpenAI)
	if err != nil {
		t.Fatal(err)
	}
	if azure != "https://point.openai.azure.com" {
		t.Fatalf("адрес Azure изменён: %s", azure)
	}
	anthropic, err := NormalizeCompatibleBaseURL("https://api.anthropic.com", domain.ProviderAnthropic)
	if err != nil {
		t.Fatal(err)
	}
	if anthropic != "https://api.anthropic.com/v1" {
		t.Fatalf("голый хост Anthropic не дополнен: %s", anthropic)
	}
}

func TestNewSupportsAnthropicAndAzure(t *testing.T) {
	for _, kind := range []domain.ProviderKind{domain.ProviderAnthropic, domain.ProviderAzureOpenAI} {
		if _, err := New(Config{Kind: kind, BaseURL: "https://example.invalid"}); err != nil {
			t.Fatalf("провайдер %q не собирается: %v", kind, err)
		}
	}
}
