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
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/observability"
	"local-agent-workbench/internal/security"
)

type Ollama struct {
	config Config
	client *http.Client
}

func NewOllama(config Config) *Ollama {
	return &Ollama{config: config, client: streamingClient(config)}
}

type wireMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content,omitempty"`
	Images     []string       `json:"images,omitempty"`
	ToolCalls  []wireToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}
type wireToolCall struct {
	ID       string `json:"id,omitempty"`
	Function struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
}

func apiTools(definitions []domain.ToolDefinition) []apiTool {
	result := make([]apiTool, 0, len(definitions))
	for _, definition := range definitions {
		var parameters any
		_ = json.Unmarshal(definition.InputSchema, &parameters)
		item := apiTool{Type: "function"}
		item.Function.Name = definition.Name
		item.Function.Description = definition.Description
		item.Function.Parameters = parameters
		result = append(result, item)
	}
	return result
}

func wireMessages(messages []Message) []wireMessage {
	messages = NormalizeChatMessages(messages)
	result := make([]wireMessage, 0, len(messages))
	for _, message := range messages {
		item := wireMessage{Role: message.Role, Content: message.Content, ToolCallID: message.ToolCallID}
		for _, image := range message.Images {
			item.Images = append(item.Images, image.DataBase64)
		}
		for _, call := range message.ToolCalls {
			wire := wireToolCall{ID: call.ID}
			wire.Function.Name = call.Name
			wire.Function.Arguments = call.Arguments
			if len(wire.Function.Arguments) == 0 {
				wire.Function.Arguments = json.RawMessage(`{}`)
			}
			item.ToolCalls = append(item.ToolCalls, wire)
		}
		result = append(result, item)
	}
	return result
}

func (o *Ollama) Stream(ctx context.Context, request ModelRequest, onEvent func(ModelEvent) error) error {
	request.Messages = NormalizeChatMessages(request.Messages)
	roles := make([]string, 0, len(request.Messages))
	for _, message := range request.Messages {
		roles = append(roles, message.Role)
	}
	body := map[string]any{"model": request.Model, "messages": wireMessages(request.Messages), "stream": true}
	if len(request.Tools) > 0 {
		body["tools"] = apiTools(request.Tools)
	}
	if len(request.JSONSchema) > 0 {
		if !json.Valid(request.JSONSchema) {
			return errors.New("invalid response JSON schema")
		}
		body["format"] = request.JSONSchema
	}
	if request.DisableThinking {
		body["think"] = false
	}
	options := map[string]any{"temperature": request.Temperature, "num_predict": request.MaxOutputTokens}
	if request.ContextWindowTokens > 0 {
		options["num_ctx"] = request.ContextWindowTokens
	}
	body["options"] = options
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	log := observability.From(ctx)
	log.Info("provider ollama request",
		"host", observability.HostOnly(o.config.BaseURL),
		"model", request.Model,
		"message_count", len(request.Messages),
		"roles", observability.RoleSummary(roles),
		"tools", len(request.Tools),
		"body_bytes", len(data),
	)
	// The serialized request can contain source code, secrets and private chat
	// text. Metadata above is enough even at DEBUG; never persist the payload.
	started := time.Now()
	for attempt := 1; attempt <= maxStreamAttempts; attempt++ {
		httpRequest, requestErr := http.NewRequestWithContext(ctx, http.MethodPost, endpoint(o.config.BaseURL, "/api/chat"), bytes.NewReader(data))
		if requestErr != nil {
			return requestErr
		}
		httpRequest.Header.Set("Content-Type", "application/json")
		response, requestErr := o.client.Do(httpRequest)
		if requestErr != nil {
			log.Warn("provider ollama transport error", "host", observability.HostOnly(o.config.BaseURL), "model", request.Model, "attempt", attempt, "error", security.Redact(requestErr.Error()))
			if delay := retryDelay("", attempt, time.Now()); shouldRetry(ctx, attempt, delay) {
				if retryErr := announceRetry(ctx, onEvent, attempt, delay, "temporary Ollama connection error"); retryErr != nil {
					return retryErr
				}
				continue
			}
			return fmt.Errorf("ollama request failed: %w", requestErr)
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			snippet, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
			_ = response.Body.Close()
			log.Error("provider ollama http error",
				"host", observability.HostOnly(o.config.BaseURL),
				"model", request.Model,
				"status", response.StatusCode,
				"attempt", attempt,
				"body", observability.Snippet(security.Redact(string(snippet)), 800),
				"duration_ms", time.Since(started).Milliseconds(),
			)
			delay := retryDelay(response.Header.Get("Retry-After"), attempt, time.Now())
			if retryableStatus(response.StatusCode) && shouldRetry(ctx, attempt, delay) {
				if retryErr := announceRetry(ctx, onEvent, attempt, delay, "Ollama returned "+response.Status); retryErr != nil {
					return retryErr
				}
				continue
			}
			return fmt.Errorf("ollama returned %s: %s", response.Status, strings.TrimSpace(string(snippet)))
		}
		log.Info("provider ollama stream start",
			"host", observability.HostOnly(o.config.BaseURL),
			"model", request.Model,
			"status", response.StatusCode,
			"attempt", attempt,
			"duration_ms", time.Since(started).Milliseconds(),
		)
		streamErr := streamOllamaResponse(ctx, response.Body, onEvent)
		_ = response.Body.Close()
		if streamErr != nil {
			log.Error("provider ollama stream failed",
				"host", observability.HostOnly(o.config.BaseURL),
				"model", request.Model,
				"error", security.Redact(streamErr.Error()),
				"duration_ms", time.Since(started).Milliseconds(),
			)
		} else {
			log.Info("provider ollama stream done",
				"host", observability.HostOnly(o.config.BaseURL),
				"model", request.Model,
				"duration_ms", time.Since(started).Milliseconds(),
			)
		}
		return streamErr
	}
	return errors.New("ollama retry limit reached")
}

func streamOllamaResponse(ctx context.Context, body io.Reader, onEvent func(ModelEvent) error) error {
	var err error
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		if err = ctx.Err(); err != nil {
			return err
		}
		var chunk struct {
			Message         wireMessage `json:"message"`
			Done            bool        `json:"done"`
			PromptEvalCount int         `json:"prompt_eval_count"`
			EvalCount       int         `json:"eval_count"`
			Error           string      `json:"error"`
		}
		if err = json.Unmarshal(scanner.Bytes(), &chunk); err != nil {
			return fmt.Errorf("decode ollama stream: %w", err)
		}
		if chunk.Error != "" {
			return errors.New(chunk.Error)
		}
		if chunk.Message.Content != "" {
			if err = onEvent(ModelEvent{Kind: EventTextDelta, Delta: chunk.Message.Content}); err != nil {
				return err
			}
		}
		for _, call := range chunk.Message.ToolCalls {
			args := call.Function.Arguments
			if len(args) == 0 {
				args = json.RawMessage(`{}`)
			}
			normalized := ToolCall{ID: call.ID, Name: call.Function.Name, Arguments: args}
			if normalized.ID == "" {
				normalized.ID = domain.NewID("call")
			}
			if err = onEvent(ModelEvent{Kind: EventToolCall, ToolCall: &normalized}); err != nil {
				return err
			}
		}
		if chunk.Done && (chunk.PromptEvalCount > 0 || chunk.EvalCount > 0) {
			if err = onEvent(ModelEvent{Kind: EventUsage, InputTokens: chunk.PromptEvalCount, OutputTokens: chunk.EvalCount}); err != nil {
				return err
			}
		}
	}
	return scanner.Err()
}
