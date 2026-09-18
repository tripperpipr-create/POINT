package providers

import (
	"context"
	"encoding/json"
	"fmt"

	"local-agent-workbench/internal/domain"
)

type ToolCall struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	Arguments     json.RawMessage `json:"arguments"`
	ArgumentError string          `json:"argumentError,omitempty"`
}

type ImageContent struct {
	MediaType  string `json:"mediaType"`
	DataBase64 string `json:"dataBase64"`
}

type Message struct {
	Role       string         `json:"role"`
	Content    string         `json:"content,omitempty"`
	Images     []ImageContent `json:"images,omitempty"`
	ToolCalls  []ToolCall     `json:"toolCalls,omitempty"`
	ToolCallID string         `json:"toolCallId,omitempty"`
	// Reasoning — блоки размышления, которые провайдер вернул в этом ответе.
	// Хранятся дословно и уходят обратно вместе с ходом: Anthropic отвергает
	// следующий запрос, если при включённом thinking ответ содержал вызов
	// инструмента, а блоки размышления с подписью не вернулись. Провайдеры,
	// которым это не нужно, поле просто не читают.
	Reasoning []ReasoningBlock `json:"reasoning,omitempty"`
}

// ReasoningBlock — блок размышления в том виде, в каком его отдал провайдер.
// Подпись обязательна к возврату: по ней провайдер проверяет, что содержимое
// не подменили. Redacted-блок приходит целиком в Data и текста не имеет.
type ReasoningBlock struct {
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	Signature string `json:"signature,omitempty"`
	Data      string `json:"data,omitempty"`
}

type ModelRequest struct {
	// JSONSchema is an optional native response schema (currently Ollama).
	JSONSchema          json.RawMessage         `json:"jsonSchema,omitempty"`
	Model               string                  `json:"model"`
	Messages            []Message               `json:"messages"`
	Tools               []domain.ToolDefinition `json:"tools"`
	Temperature         float64                 `json:"temperature"`
	ContextWindowTokens int                     `json:"contextWindowTokens,omitempty"`
	MaxOutputTokens     int                     `json:"maxOutputTokens"`
	ReasoningEffort     string                  `json:"reasoningEffort"`
	// DisableThinking просит рантайм не включать «размышление» вовсе — это не
	// то же, что `ReasoningEffort: "none"`. Второе значит «не слать
	// `reasoning_effort`» и остаётся выбором пользователя в профиле агента;
	// первое добавляет в тело `chat_template_kwargs`, которое понимают только
	// самостоятельно поднятые рантаймы. Признаки разведены нарочно: иначе
	// профиль с «none» начал бы ронять запросы к официальному OpenAI.
	DisableThinking bool `json:"disableThinking,omitempty"`
}

type EventKind string

const (
	EventTextDelta EventKind = "text_delta"
	EventToolCall  EventKind = "tool_call"
	EventUsage     EventKind = "usage"
	EventRetry     EventKind = "retry"
	EventReasoning EventKind = "reasoning"
)

type ModelEvent struct {
	Kind         EventKind       `json:"kind"`
	Delta        string          `json:"delta,omitempty"`
	ToolCall     *ToolCall       `json:"toolCall,omitempty"`
	Reasoning    *ReasoningBlock `json:"reasoning,omitempty"`
	InputTokens  int             `json:"inputTokens,omitempty"`
	OutputTokens int             `json:"outputTokens,omitempty"`
	// Кэш и цена приходят не от всех: локальный Claude Code сообщает и то, и
	// другое, и без них расход виден вчетверо меньше настоящего — на замере из
	// 20 132 входных токенов свежими были 10.
	CacheReadTokens  int `json:"cacheReadTokens,omitempty"`
	CacheWriteTokens int `json:"cacheWriteTokens,omitempty"`
	// CostMicroUSD — стоимость обращения в стотысячных долях доллара. Целое
	// вместо дробного намеренно: деньги, сложенные из float, расходятся с суммой
	// по строкам, и объяснить это человеку нечем.
	CostMicroUSD int `json:"costMicroUsd,omitempty"`
	// Пределы модели, о которых сообщил сам исполнитель: окно контекста и
	// потолок ответа. У подписки они и есть тот предел, который видно.
	ContextWindowTokens  int    `json:"contextWindowTokens,omitempty"`
	ModelMaxOutputTokens int    `json:"modelMaxOutputTokens,omitempty"`
	Attempt              int    `json:"attempt,omitempty"`
	DelayMs              int64  `json:"delayMs,omitempty"`
	Message              string `json:"message,omitempty"`
}

type Model interface {
	Stream(context.Context, ModelRequest, func(ModelEvent) error) error
}

type Config struct {
	Kind    domain.ProviderKind
	BaseURL string
	APIKey  string
	// Preset — идентификатор пресета подключения («llmux», «vllm», «openai»).
	// Протоколу он не нужен, но по виду провайдера нельзя узнать ни то, примет
	// ли endpoint поля сверх спецификации, ни то, тарифицирует ли он токены.
	// Пустой пресет трактуется как платный чужой API — безопасная сторона.
	Preset string
	// APIVersion нужна только Azure и живёт отдельно от BaseURL: она передаётся
	// в query, а нормализация адреса query намеренно срезает.
	APIVersion     string
	TimeoutSeconds int
	// HeaderTimeoutSeconds — срок ожидания заголовков ответа для потоковых
	// вызовов. Отдельный от общего он нужен потому, что поток живёт долго по
	// замыслу: общий срок рубил бы модель посреди ответа, а молчание провайдера
	// видно раньше и точнее по заголовкам. Ноль означает прежнее поведение —
	// один срок на всё.
	HeaderTimeoutSeconds int
	// MCPConfigJSON — конфигурация MCP-сервера Point для исполнителей, которые
	// принимают чужие инструменты только так. Внутри адрес и ключ сессии,
	// выданный одной сущности на время одного разговора.
	MCPConfigJSON string
	// MCPTools — имена инструментов Point, которые исполнителю разрешено
	// звать. Пустой список значит «ни одного»: разрешение выдаётся поимённо,
	// потому что права принадлежат сущности, а не серверу.
	MCPTools []string
	// WorkingDir — папка проекта для провайдеров, отвечающих локальным
	// процессом: Claude Code читает проект относительно неё, и без неё он
	// смотрел бы туда, откуда запущено ядро.
	WorkingDir string
}

func New(config Config) (Model, error) {
	switch config.Kind {
	case domain.ProviderOpenAI, domain.ProviderAzureOpenAI:
		return NewOpenAICompatible(config), nil
	case domain.ProviderAnthropic:
		return NewAnthropic(config), nil
	case domain.ProviderOllama:
		return NewOllama(config), nil
	default:
		if domain.IsAgentCLIProvider(config.Kind) {
			return nil, fmt.Errorf("provider %q is removed; Point uses HTTP API providers only", config.Kind)
		}
		return nil, fmt.Errorf("unsupported provider kind %q", config.Kind)
	}
}
