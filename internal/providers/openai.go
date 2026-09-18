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
	"net/url"
	"strconv"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/observability"
	"local-agent-workbench/internal/security"
)

// Версия Azure API закреплена явно: без неё запрос отвергается, а «последней»
// у Azure не бывает — каждая версия живёт своим сроком.
const defaultAzureAPIVersion = "2024-10-21"

type OpenAICompatible struct {
	config Config
	client *http.Client
}

func NewOpenAICompatible(config Config) *OpenAICompatible {
	return &OpenAICompatible{config: config, client: streamingClient(config)}
}

// Azure говорит на том же протоколе, но тремя отличиями: заголовок api-key
// вместо Bearer, путь через deployment и обязательная api-version в query.
// Копировать ради этого весь клиент незачем — расходятся только две функции.
func (o *OpenAICompatible) requestURL(model string) string {
	if o.config.Kind != domain.ProviderAzureOpenAI {
		return endpoint(o.config.BaseURL, "/chat/completions")
	}
	version := strings.TrimSpace(o.config.APIVersion)
	if version == "" {
		version = defaultAzureAPIVersion
	}
	return endpoint(o.config.BaseURL, "/openai/deployments/"+url.PathEscape(model)+"/chat/completions") +
		"?api-version=" + url.QueryEscape(version)
}

func (o *OpenAICompatible) applyAuth(request *http.Request) {
	if o.config.APIKey == "" {
		return
	}
	if o.config.Kind == domain.ProviderAzureOpenAI {
		request.Header.Set("api-key", o.config.APIKey)
		return
	}
	request.Header.Set("Authorization", "Bearer "+o.config.APIKey)
}

func openAIMessages(messages []Message) []map[string]any {
	messages = NormalizeChatMessages(messages)
	result := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		item := map[string]any{"role": message.Role}
		switch {
		case len(message.Images) > 0:
			parts := make([]map[string]any, 0, len(message.Images)+1)
			if message.Content != "" {
				parts = append(parts, map[string]any{"type": "text", "text": message.Content})
			}
			for _, image := range message.Images {
				parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]string{"url": "data:" + image.MediaType + ";base64," + image.DataBase64}})
			}
			item["content"] = parts
		case message.Content != "" || message.Role == "user" || message.Role == "system" || message.Role == "tool":
			item["content"] = message.Content
		case len(message.ToolCalls) > 0:
			item["content"] = nil
		default:
			item["content"] = message.Content
		}
		if message.ToolCallID != "" {
			item["tool_call_id"] = message.ToolCallID
		}
		if len(message.ToolCalls) > 0 {
			calls := make([]map[string]any, 0, len(message.ToolCalls))
			for _, call := range message.ToolCalls {
				arguments := string(call.Arguments)
				if arguments == "" {
					arguments = "{}"
				}
				calls = append(calls, map[string]any{"id": call.ID, "type": "function", "function": map[string]any{"name": call.Name, "arguments": arguments}})
			}
			item["tool_calls"] = calls
		}
		result = append(result, item)
	}
	return result
}

func (o *OpenAICompatible) Stream(ctx context.Context, request ModelRequest, onEvent func(ModelEvent) error) error {
	request.Messages = NormalizeChatMessages(request.Messages)
	roles := make([]string, 0, len(request.Messages))
	for _, message := range request.Messages {
		roles = append(roles, message.Role)
	}
	body := map[string]any{"model": request.Model, "messages": openAIMessages(request.Messages), "stream": true, "stream_options": map[string]any{"include_usage": true}}
	if len(request.Tools) > 0 {
		body["tools"] = apiTools(request.Tools)
	}
	if request.MaxOutputTokens > 0 {
		body["max_completion_tokens"] = request.MaxOutputTokens
	}
	if request.ReasoningEffort != "" && request.ReasoningEffort != "none" {
		body["reasoning_effort"] = request.ReasoningEffort
	} else {
		body["temperature"] = request.Temperature
	}
	// Гашение «размышления» у моделей вроде Qwen3: рантайм подставляет его в
	// шаблон чата, а не в параметры выборки, поэтому ключ идёт отдельным полем
	// тела. Ставится только там, где вызывающий уже убедился, что endpoint
	// принимает поля сверх спецификации, — официальный OpenAI отвечает на них
	// 400. Без этого думающая модель тратит весь лимит вывода на размышление и
	// не отдаёт ни одного токена ответа.
	if request.DisableThinking {
		body["chat_template_kwargs"] = map[string]any{"enable_thinking": false}
	}
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	log := observability.From(ctx)
	log.Info("provider openai request",
		"host", observability.HostOnly(o.config.BaseURL),
		"model", request.Model,
		"message_count", len(request.Messages),
		"roles", observability.RoleSummary(roles),
		"tools", len(request.Tools),
		"body_bytes", len(data),
		"has_api_key", o.config.APIKey != "",
		"max_output_tokens", request.MaxOutputTokens,
	)
	// Prompts and tool arguments may contain source code, secrets, or private
	// conversation text. Request metadata above is enough for diagnostics; never
	// copy the serialized provider payload into the default DEBUG log.
	started := time.Now()
	for attempt := 1; attempt <= maxStreamAttempts; attempt++ {
		httpRequest, requestErr := http.NewRequestWithContext(ctx, http.MethodPost, o.requestURL(request.Model), bytes.NewReader(data))
		if requestErr != nil {
			return requestErr
		}
		httpRequest.Header.Set("Content-Type", "application/json")
		o.applyAuth(httpRequest)
		response, requestErr := o.client.Do(httpRequest)
		if requestErr != nil {
			log.Warn("provider openai transport error",
				"host", observability.HostOnly(o.config.BaseURL),
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
			return fmt.Errorf("OpenAI-compatible request failed: %w", requestErr)
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			snippet, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
			_ = response.Body.Close()
			log.Error("provider openai http error",
				"host", observability.HostOnly(o.config.BaseURL),
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
		log.Info("provider openai stream start",
			"host", observability.HostOnly(o.config.BaseURL),
			"model", request.Model,
			"status", response.StatusCode,
			"attempt", attempt,
			"duration_ms", time.Since(started).Milliseconds(),
		)
		streamErr := streamOpenAIResponse(ctx, response.Body, onEvent)
		_ = response.Body.Close()
		if streamErr != nil {
			log.Error("provider openai stream failed",
				"host", observability.HostOnly(o.config.BaseURL),
				"model", request.Model,
				"error", security.Redact(streamErr.Error()),
				"duration_ms", time.Since(started).Milliseconds(),
			)
			if isTransientEmptyStreamError(streamErr) {
				delay := retryDelay("", attempt, time.Now())
				if shouldRetry(ctx, attempt, delay) {
					if retryErr := announceRetry(ctx, onEvent, attempt, delay, "temporary provider stream interrupt"); retryErr != nil {
						return retryErr
					}
					continue
				}
			}
		} else {
			log.Info("provider openai stream done",
				"host", observability.HostOnly(o.config.BaseURL),
				"model", request.Model,
				"duration_ms", time.Since(started).Milliseconds(),
			)
		}
		return streamErr
	}
	return errors.New("provider retry limit reached")
}

// IsTransientProviderError reports empty/interrupted upstream streams that are
// safe to retry or fall back from without treating them as assignment failures.
func IsTransientProviderError(err error) bool {
	return isTransientEmptyStreamError(err) || IsTruncatedReasoningError(err)
}

// Empty upstream closes from LLMux/vLLM with no answer are transient and safe to
// retry at the HTTP layer when the stream delivered nothing usable yet.
func isTransientEmptyStreamError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	markers := []string{
		"upstream closed the stream without sending any content",
		"stream closed without sending any content",
		"connection reset",
		"unexpected eof",
		"http2: server sent goaway",
	}
	for _, marker := range markers {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// Ответ, которого не было: весь лимит вывода ушёл в размышление. Сообщение
// называет и причину, и число, чтобы читающий знал, что делать, — поднять
// лимит или погасить размышление, — а не гадал по чужой фразе про закрытый
// поток.
func truncatedReasoningError(tokens int) error {
	if tokens > 0 {
		return fmt.Errorf("model returned no answer: the entire output budget of %d tokens went to reasoning (finish_reason=length)", tokens)
	}
	return errors.New("model returned no answer: the output budget ran out during reasoning (finish_reason=length)")
}

// IsTruncatedReasoningError is an empty assignment turn: the model spent the
// whole output budget thinking and never produced text or a tool call.
func IsTruncatedReasoningError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "went to reasoning") || strings.Contains(msg, "ran out during reasoning")
}

func streamOpenAIResponse(ctx context.Context, body io.Reader, onEvent func(ModelEvent) error) error {
	var err error
	type partial struct{ ID, Name, Arguments string }
	calls := map[int]*partial{}
	// Чем кончился поток и было ли в нём хоть слово ответа. По этой паре
	// отличается «модель промолчала, потому что упёрлась в лимит» от настоящей
	// ошибки провайдера — снаружи они выглядели одинаково.
	var sawContent bool
	var finishReason string
	var completionTokens int
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		if err = ctx.Err(); err != nil {
			return err
		}
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
					// Думающие модели шлют рассуждение отдельным полем, и имя у
					// него не одно: vLLM пишет `reasoning`, часть шлюзов —
					// `reasoning_content`. Разбираются оба: пропустить их
					// значит смотреть на пустой поток там, где модель работает.
					Reasoning        string `json:"reasoning"`
					ReasoningContent string `json:"reasoning_content"`
					ToolCalls        []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage struct {
				Prompt     int `json:"prompt_tokens"`
				Completion int `json:"completion_tokens"`
			} `json:"usage"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err = json.Unmarshal([]byte(payload), &chunk); err != nil {
			return fmt.Errorf("decode provider stream: %w", err)
		}
		if chunk.Error != nil {
			// Ошибку шлюза перебивает свой разбор, если он точнее: llmux
			// сообщает «upstream closed the stream without sending any
			// content» и там, где поток закрылся штатно — с usage и [DONE], —
			// а ответа не было потому, что весь лимит съело размышление.
			if !sawContent && finishReason == "length" {
				return truncatedReasoningError(completionTokens)
			}
			return fmt.Errorf("provider error: %s", chunk.Error.Message)
		}
		for _, choice := range chunk.Choices {
			if choice.FinishReason != "" {
				finishReason = choice.FinishReason
			}
			if reasoning := choice.Delta.Reasoning + choice.Delta.ReasoningContent; reasoning != "" {
				if err = onEvent(ModelEvent{Kind: EventReasoning, Delta: reasoning, Reasoning: &ReasoningBlock{Type: "text", Text: reasoning}}); err != nil {
					return err
				}
			}
			if choice.Delta.Content != "" {
				sawContent = true
				if err = onEvent(ModelEvent{Kind: EventTextDelta, Delta: choice.Delta.Content}); err != nil {
					return err
				}
			}
			for _, part := range choice.Delta.ToolCalls {
				call := calls[part.Index]
				if call == nil {
					call = &partial{}
					calls[part.Index] = call
				}
				call.ID += part.ID
				call.Name += part.Function.Name
				call.Arguments += part.Function.Arguments
			}
		}
		if chunk.Usage.Prompt > 0 || chunk.Usage.Completion > 0 {
			completionTokens = chunk.Usage.Completion
			if err = onEvent(ModelEvent{Kind: EventUsage, InputTokens: chunk.Usage.Prompt, OutputTokens: chunk.Usage.Completion}); err != nil {
				return err
			}
		}
	}
	if err = scanner.Err(); err != nil {
		return err
	}
	// Поток мог закончиться и без строки ошибки — просто [DONE] после обрыва по
	// лимиту. Молча вернуть «успех» здесь нельзя: наверху пустой ответ станет
	// откатом с причиной «модель вернула пустое», а причина другая и чинится
	// иначе — лимитом или гашением размышления.
	if !sawContent && finishReason == "length" {
		return truncatedReasoningError(completionTokens)
	}
	for index := 0; index < len(calls); index++ {
		call := calls[index]
		if call == nil {
			continue
		}
		args := json.RawMessage(call.Arguments)
		normalized := ToolCall{ID: call.ID, Name: call.Name, Arguments: args}
		if !json.Valid(args) {
			normalized.Arguments = json.RawMessage(`{}`)
			normalized.ArgumentError = "tool call " + strconv.Itoa(index) + " returned arguments that are not valid JSON"
		}
		if normalized.ID == "" {
			normalized.ID = "call_" + strconv.Itoa(index)
		}
		if err = onEvent(ModelEvent{Kind: EventToolCall, ToolCall: &normalized}); err != nil {
			return err
		}
	}
	return nil
}
