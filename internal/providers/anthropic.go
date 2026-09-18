package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/observability"
	"local-agent-workbench/internal/security"
)

// Anthropic Messages API — не OpenAI-совместимый протокол, и пресетом к
// существующему клиенту он не решается: системное сообщение уходит отдельным
// полем, инструменты приходят блоками tool_use, ответ на них уходит блоком
// tool_result внутри сообщения роли user, картинки описываются base64-блоком, а
// поток событий устроен по-своему. Вся эта разница живёт здесь, за тем же
// интерфейсом Model, поэтому вызывающий код агента о ней не знает.
const anthropicVersion = "2023-06-01"

// max_tokens в Messages API обязателен. Профиль может не задать лимит ответа —
// подставляем осознанный потолок, а не ноль, иначе провайдер откажет.
const anthropicDefaultMaxTokens = 4096

type Anthropic struct {
	config Config
	client *http.Client
}

func NewAnthropic(config Config) *Anthropic {
	return &Anthropic{config: config, client: streamingClient(config)}
}

func anthropicTools(definitions []domain.ToolDefinition) []map[string]any {
	result := make([]map[string]any, 0, len(definitions))
	for _, definition := range definitions {
		var schema any
		_ = json.Unmarshal(definition.InputSchema, &schema)
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		result = append(result, map[string]any{
			"name":         definition.Name,
			"description":  definition.Description,
			"input_schema": schema,
		})
	}
	return result
}

// anthropicMessages переводит общий конверт в блочный формат Anthropic и
// отдельно возвращает системную инструкцию: в messages ей места нет.
func anthropicMessages(messages []Message) ([]map[string]any, string) {
	messages = NormalizeChatMessages(messages)
	system := ""
	result := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		if message.Role == "system" {
			system = message.Content
			continue
		}
		if message.Role == "tool" {
			// Результат инструмента — это блок внутри сообщения пользователя, а
			// не отдельная роль. Соседние результаты складываются в одно
			// сообщение, иначе API отвергнет чередование ролей.
			block := map[string]any{"type": "tool_result", "tool_use_id": message.ToolCallID, "content": message.Content}
			if last := len(result) - 1; last >= 0 && result[last]["role"] == "user" {
				if blocks, ok := result[last]["content"].([]map[string]any); ok && len(blocks) > 0 {
					if first, ok := blocks[0]["type"].(string); ok && first == "tool_result" {
						result[last]["content"] = append(blocks, block)
						continue
					}
				}
			}
			result = append(result, map[string]any{"role": "user", "content": []map[string]any{block}})
			continue
		}
		blocks := make([]map[string]any, 0, len(message.Reasoning)+len(message.Images)+len(message.ToolCalls)+1)
		// Блоки размышления идут первыми и дословно — так их прислал провайдер.
		// Порядок и подпись он проверяет: перестановка или потеря подписи
		// отвергается тем же 400, что и полное отсутствие блоков.
		for _, thought := range message.Reasoning {
			switch thought.Type {
			case "redacted_thinking":
				blocks = append(blocks, map[string]any{"type": "redacted_thinking", "data": thought.Data})
			default:
				blocks = append(blocks, map[string]any{"type": "thinking", "thinking": thought.Text, "signature": thought.Signature})
			}
		}
		if strings.TrimSpace(message.Content) != "" {
			blocks = append(blocks, map[string]any{"type": "text", "text": message.Content})
		}
		for _, image := range message.Images {
			blocks = append(blocks, map[string]any{
				"type":   "image",
				"source": map[string]string{"type": "base64", "media_type": image.MediaType, "data": image.DataBase64},
			})
		}
		for _, call := range message.ToolCalls {
			var input any
			if len(call.Arguments) > 0 && json.Valid(call.Arguments) {
				_ = json.Unmarshal(call.Arguments, &input)
			}
			if input == nil {
				input = map[string]any{}
			}
			blocks = append(blocks, map[string]any{"type": "tool_use", "id": call.ID, "name": call.Name, "input": input})
		}
		if len(blocks) == 0 {
			continue
		}
		result = append(result, map[string]any{"role": message.Role, "content": blocks})
	}
	return result, system
}

func (a *Anthropic) Stream(ctx context.Context, request ModelRequest, onEvent func(ModelEvent) error) error {
	messages, system := anthropicMessages(request.Messages)
	maxTokens := request.MaxOutputTokens
	if maxTokens <= 0 {
		maxTokens = anthropicDefaultMaxTokens
	}
	body := map[string]any{
		"model":      request.Model,
		"messages":   messages,
		"max_tokens": maxTokens,
		"stream":     true,
	}
	if system != "" {
		body["system"] = system
	}
	if len(request.Tools) > 0 {
		body["tools"] = anthropicTools(request.Tools)
	}
	// Температура и режим рассуждения — взаимоисключающие: при включённом
	// thinking API требует temperature = 1, поэтому её просто не отправляем.
	if effort := strings.TrimSpace(request.ReasoningEffort); effort != "" && effort != "none" {
		body["thinking"] = map[string]any{"type": "enabled", "budget_tokens": anthropicThinkingBudget(effort, maxTokens)}
	} else {
		body["temperature"] = request.Temperature
	}
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	log := observability.From(ctx)
	log.Info("provider anthropic request",
		"host", observability.HostOnly(a.config.BaseURL),
		"model", request.Model,
		"message_count", len(messages),
		"tools", len(request.Tools),
		"body_bytes", len(data),
		"has_api_key", a.config.APIKey != "",
		"max_output_tokens", maxTokens,
	)
	started := time.Now()
	for attempt := 1; attempt <= maxStreamAttempts; attempt++ {
		httpRequest, requestErr := http.NewRequestWithContext(ctx, http.MethodPost, endpoint(a.config.BaseURL, "/messages"), bytes.NewReader(data))
		if requestErr != nil {
			return requestErr
		}
		httpRequest.Header.Set("Content-Type", "application/json")
		httpRequest.Header.Set("Accept", "text/event-stream")
		httpRequest.Header.Set("anthropic-version", anthropicVersion)
		if a.config.APIKey != "" {
			httpRequest.Header.Set("x-api-key", a.config.APIKey)
		}
		response, requestErr := a.client.Do(httpRequest)
		if requestErr != nil {
			log.Warn("provider anthropic transport error",
				"host", observability.HostOnly(a.config.BaseURL),
				"model", request.Model,
				"attempt", attempt,
				"error", security.Redact(requestErr.Error()),
			)
			if delay := retryDelay("", attempt, time.Now()); shouldRetry(ctx, attempt, delay) {
				if retryErr := announceRetry(ctx, onEvent, attempt, delay, "temporary provider connection error"); retryErr != nil {
					return retryErr
				}
				continue
			}
			return fmt.Errorf("Anthropic request failed: %w", requestErr)
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			snippet, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
			_ = response.Body.Close()
			log.Error("provider anthropic http error",
				"host", observability.HostOnly(a.config.BaseURL),
				"model", request.Model,
				"status", response.StatusCode,
				"attempt", attempt,
				"body", observability.Snippet(security.Redact(string(snippet)), 800),
				"duration_ms", time.Since(started).Milliseconds(),
			)
			delay := retryDelay(response.Header.Get("Retry-After"), attempt, time.Now())
			if retryableStatus(response.StatusCode) && shouldRetry(ctx, attempt, delay) {
				if retryErr := announceRetry(ctx, onEvent, attempt, delay, "provider returned "+response.Status); retryErr != nil {
					return retryErr
				}
				continue
			}
			return fmt.Errorf("provider returned %s: %s", response.Status, strings.TrimSpace(string(snippet)))
		}
		streamErr := streamAnthropicResponse(ctx, response.Body, onEvent)
		_ = response.Body.Close()
		if streamErr != nil {
			log.Error("provider anthropic stream failed",
				"host", observability.HostOnly(a.config.BaseURL),
				"model", request.Model,
				"error", security.Redact(streamErr.Error()),
				"duration_ms", time.Since(started).Milliseconds(),
			)
		} else {
			log.Info("provider anthropic stream done",
				"host", observability.HostOnly(a.config.BaseURL),
				"model", request.Model,
				"duration_ms", time.Since(started).Milliseconds(),
			)
		}
		return streamErr
	}
	return errors.New("provider retry limit reached")
}

// Бюджет рассуждения — доля лимита ответа, а не отдельное число из воздуха:
// он обязан помещаться внутрь max_tokens, иначе API откажет.
func anthropicThinkingBudget(effort string, maxTokens int) int {
	share := 0.25
	switch strings.ToLower(effort) {
	case "minimal":
		share = 0.1
	case "low":
		share = 0.2
	case "high":
		share = 0.6
	}
	budget := int(float64(maxTokens) * share)
	if budget < 1024 {
		budget = 1024
	}
	if budget >= maxTokens {
		budget = maxTokens - 1
	}
	return budget
}

func streamAnthropicResponse(ctx context.Context, body io.Reader, onEvent func(ModelEvent) error) error {
	type partial struct {
		ID, Name, Arguments string
		isTool              bool
		// Размышление копится теми же блоками, что и вызов инструмента: у него
		// свой индекс в потоке, текст приходит порциями, подпись — в конце.
		isThought        bool
		thoughtType      string
		thoughtText      string
		thoughtSignature string
		thoughtData      string
	}
	blocks := map[int]*partial{}
	order := make([]int, 0, 4)
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var chunk struct {
			Type         string `json:"type"`
			Index        int    `json:"index"`
			ContentBlock struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
				Data string `json:"data"`
			} `json:"content_block"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
				Thinking    string `json:"thinking"`
				Signature   string `json:"signature"`
			} `json:"delta"`
			Message struct {
				Usage struct {
					Input  int `json:"input_tokens"`
					Output int `json:"output_tokens"`
				} `json:"usage"`
			} `json:"message"`
			Usage struct {
				Input  int `json:"input_tokens"`
				Output int `json:"output_tokens"`
			} `json:"usage"`
			Error *struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			return fmt.Errorf("decode provider stream: %w", err)
		}
		switch {
		case chunk.Error != nil:
			return fmt.Errorf("provider error: %s", chunk.Error.Message)
		case chunk.Type == "content_block_start":
			isThought := chunk.ContentBlock.Type == "thinking" || chunk.ContentBlock.Type == "redacted_thinking"
			block := &partial{
				ID: chunk.ContentBlock.ID, Name: chunk.ContentBlock.Name,
				isTool:      chunk.ContentBlock.Type == "tool_use",
				isThought:   isThought,
				thoughtType: chunk.ContentBlock.Type,
				thoughtData: chunk.ContentBlock.Data,
			}
			blocks[chunk.Index] = block
			order = append(order, chunk.Index)
		case chunk.Type == "content_block_delta" && chunk.Delta.Type == "text_delta":
			if chunk.Delta.Text != "" {
				if err := onEvent(ModelEvent{Kind: EventTextDelta, Delta: chunk.Delta.Text}); err != nil {
					return err
				}
			}
		case chunk.Type == "content_block_delta" && chunk.Delta.Type == "input_json_delta":
			if block := blocks[chunk.Index]; block != nil {
				block.Arguments += chunk.Delta.PartialJSON
			}
		// Размышление наружу не показываем — это внутренний ход модели, а не
		// ответ человеку. Его задача одна: вернуться в следующий запрос.
		case chunk.Type == "content_block_delta" && chunk.Delta.Type == "thinking_delta":
			if block := blocks[chunk.Index]; block != nil {
				block.thoughtText += chunk.Delta.Thinking
			}
		case chunk.Type == "content_block_delta" && chunk.Delta.Type == "signature_delta":
			if block := blocks[chunk.Index]; block != nil {
				block.thoughtSignature += chunk.Delta.Signature
			}
		case chunk.Type == "message_start" && (chunk.Message.Usage.Input > 0 || chunk.Message.Usage.Output > 0):
			if err := onEvent(ModelEvent{Kind: EventUsage, InputTokens: chunk.Message.Usage.Input, OutputTokens: chunk.Message.Usage.Output}); err != nil {
				return err
			}
		case chunk.Type == "message_delta" && (chunk.Usage.Input > 0 || chunk.Usage.Output > 0):
			if err := onEvent(ModelEvent{Kind: EventUsage, InputTokens: chunk.Usage.Input, OutputTokens: chunk.Usage.Output}); err != nil {
				return err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	// Блоки размышления отдаём раньше вызовов и в том порядке, в каком они
	// пришли: движок кладёт их в ход целиком, чтобы вернуть следующим запросом.
	for _, index := range order {
		block := blocks[index]
		if block == nil || !block.isThought {
			continue
		}
		thought := ReasoningBlock{Type: block.thoughtType, Text: block.thoughtText, Signature: block.thoughtSignature, Data: block.thoughtData}
		if thought.Type == "" {
			thought.Type = "thinking"
		}
		if err := onEvent(ModelEvent{Kind: EventReasoning, Reasoning: &thought}); err != nil {
			return err
		}
	}
	for position, index := range order {
		block := blocks[index]
		if block == nil || !block.isTool {
			continue
		}
		args := json.RawMessage(strings.TrimSpace(block.Arguments))
		call := ToolCall{ID: block.ID, Name: block.Name, Arguments: args}
		if len(args) == 0 {
			call.Arguments = json.RawMessage(`{}`)
		} else if !json.Valid(args) {
			call.Arguments = json.RawMessage(`{}`)
			call.ArgumentError = "tool call " + strconv.Itoa(position) + " returned arguments that are not valid JSON"
		}
		if call.ID == "" {
			call.ID = "call_" + strconv.Itoa(position)
		}
		if err := onEvent(ModelEvent{Kind: EventToolCall, ToolCall: &call}); err != nil {
			return err
		}
	}
	return nil
}
