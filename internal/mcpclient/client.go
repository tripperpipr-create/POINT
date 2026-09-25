package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"local-agent-workbench/internal/mcp"
)

const (
	TransportStdio = "stdio"
	TransportHTTP  = "http"

	defaultStartTimeout = 90 * time.Second
	defaultCallTimeout  = 60 * time.Second
	defaultResultBytes  = 4 << 20
	// maxMessageBytes — потолок одного сообщения сервера. Ответ больше этого —
	// не ответ, а сбой: 8 MiB текста ни экран, ни модель не прочтут.
	maxMessageBytes = 8 << 20
	logBytes        = 64 << 10
	maxToolPages    = 20
	maxTools        = 500
)

// supportedVersions — версии протокола, на которых Point готов говорить.
// Предлагаем свою (mcp.ProtocolVersion), соглашаемся на любую из этих.
var supportedVersions = map[string]bool{"2025-03-26": true, "2025-06-18": true, "2025-11-25": true}

// Config — всё, что нужно для одного соединения. Собирает его ядро: команда и
// доверие — из хранилища, секреты — из памяти, окружение — ChildEnvironment.
type Config struct {
	Name      string
	Transport string

	// stdio
	Command string
	Args    []string
	Dir     string
	Env     []string

	// Streamable HTTP
	URL         string
	Headers     map[string]string
	DialContext func(ctx context.Context, network, address string) (net.Conn, error)

	// Secrets — точные значения, вставленные ядром в окружение и заголовки.
	// Ими зачищается всё, что сервер вернёт текстом ошибки или в stderr.
	Secrets []string

	StartTimeout   time.Duration
	CallTimeout    time.Duration
	MaxResultBytes int
	ClientVersion  string
}

// ServerInfo — что сервер сказал о себе при приветствии.
type ServerInfo struct {
	Name             string `json:"name"`
	Version          string `json:"version"`
	ProtocolVersion  string `json:"protocolVersion"`
	Instructions     string `json:"instructions,omitempty"`
	ToolsListChanged bool   `json:"toolsListChanged,omitempty"`
}

// Annotations — подсказки сервера о природе инструмента. Недоверенные: они
// только предзаполняют риск, решает владелец.
type Annotations struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    *bool  `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool  `json:"destructiveHint,omitempty"`
	IdempotentHint  *bool  `json:"idempotentHint,omitempty"`
	OpenWorldHint   *bool  `json:"openWorldHint,omitempty"`
}

// Tool — определение инструмента из tools/list.
type Tool struct {
	Name         string          `json:"name"`
	Title        string          `json:"title,omitempty"`
	Description  string          `json:"description,omitempty"`
	InputSchema  json.RawMessage `json:"inputSchema,omitempty"`
	OutputSchema json.RawMessage `json:"outputSchema,omitempty"`
	Annotations  *Annotations    `json:"annotations,omitempty"`
}

// Content — одна часть результата. Point читает текст; картинки и ресурсы
// сохраняются как есть, но в экран не выводятся.
type Content struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	Data     string `json:"data,omitempty"`
}

// CallResult — ответ tools/call.
type CallResult struct {
	Content    []Content       `json:"content"`
	Structured json.RawMessage `json:"structuredContent,omitempty"`
	IsError    bool            `json:"isError,omitempty"`
	// Truncated — текст обрезан по потолку MaxResultBytes.
	Truncated bool `json:"-"`
}

// Text — все текстовые части подряд.
func (r CallResult) Text() string {
	parts := make([]string, 0, len(r.Content))
	for _, item := range r.Content {
		if item.Type == "text" {
			parts = append(parts, item.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// transport доставляет сообщения. Запросы сервера к клиенту и уведомления
// отдаются обработчику клиента; ответ на запрос сервера транспорт отправляет
// сам — тем путём, каким запрос пришёл.
type transport interface {
	call(ctx context.Context, msg message) (message, error)
	notify(ctx context.Context, msg message) error
	negotiated(version string)
	done() <-chan struct{}
	close() error
}

// errSessionExpired — удалённый сервер забыл сессию (404 на Mcp-Session-Id).
// Клиент приветствуется заново один раз и повторяет запрос.
var errSessionExpired = errors.New("mcp session expired")

// Client — одно живое соединение с сервером.
type Client struct {
	cfg    Config
	tr     transport
	log    *ring
	nextID atomic.Int64
	stale  atomic.Bool

	mu   sync.Mutex
	info ServerInfo
}

// Dial поднимает транспорт и приветствует сервер. Ошибка всегда *Error.
func Dial(ctx context.Context, cfg Config) (*Client, error) {
	if cfg.StartTimeout <= 0 {
		cfg.StartTimeout = defaultStartTimeout
	}
	if cfg.CallTimeout <= 0 {
		cfg.CallTimeout = defaultCallTimeout
	}
	if cfg.MaxResultBytes <= 0 {
		cfg.MaxResultBytes = defaultResultBytes
	}
	if cfg.ClientVersion == "" {
		cfg.ClientVersion = "dev"
	}
	client := &Client{cfg: cfg, log: newRing(logBytes, cfg.Secrets)}
	var err error
	switch cfg.Transport {
	case TransportStdio:
		client.tr, err = startStdio(cfg, client.log, client.inbound)
	case TransportHTTP:
		client.tr, err = newHTTPTransport(cfg, client.log, client.inbound)
	default:
		err = newError(KindProtocol, nil, "unknown transport %q", cfg.Transport)
	}
	if err != nil {
		return nil, client.wrap(err)
	}
	startCtx, cancel := context.WithTimeout(ctx, cfg.StartTimeout)
	defer cancel()
	if err := client.initialize(startCtx); err != nil {
		_ = client.tr.close()
		return nil, client.wrap(err)
	}
	return client, nil
}

// Info — что сервер сказал о себе.
func (c *Client) Info() ServerInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.info
}

// Stale — сервер сообщил, что список инструментов изменился.
func (c *Client) Stale() bool { return c.stale.Load() }

// Done закрывается, когда соединение умерло.
func (c *Client) Done() <-chan struct{} { return c.tr.done() }

// Log — зачищенный хвост журнала сервера: stderr процесса, его сообщения
// журнала и отказы Point на запросы сервера.
func (c *Client) Log() string { return c.log.Tail(0) }

// Close завершает соединение: stdin закрывается, процесс получает время выйти.
func (c *Client) Close() error { return c.tr.close() }

func (c *Client) initialize(ctx context.Context) error {
	params := map[string]any{
		"protocolVersion": mcp.ProtocolVersion,
		// Ни корней, ни модели, ни ввода человека Point серверу не даёт:
		// возможностей клиента нет, и запросы за ними получают отказ.
		"capabilities": map[string]any{},
		"clientInfo":   map[string]any{"name": "point", "version": c.cfg.ClientVersion},
	}
	response, err := c.send(ctx, "initialize", params, false)
	if err != nil {
		return err
	}
	var result struct {
		ProtocolVersion string `json:"protocolVersion"`
		Capabilities    struct {
			Tools *struct {
				ListChanged bool `json:"listChanged"`
			} `json:"tools"`
		} `json:"capabilities"`
		ServerInfo struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
		Instructions string `json:"instructions"`
	}
	if err := json.Unmarshal(response, &result); err != nil {
		return newError(KindHandshake, err, "initialize result is not an object")
	}
	if !supportedVersions[result.ProtocolVersion] {
		return newError(KindHandshake, nil, "server speaks protocol %q", c.scrub(result.ProtocolVersion))
	}
	if result.Capabilities.Tools == nil {
		return newError(KindHandshake, nil, "server declares no tools capability")
	}
	c.mu.Lock()
	c.info = ServerInfo{
		Name:             clip(result.ServerInfo.Name, 200),
		Version:          clip(result.ServerInfo.Version, 100),
		ProtocolVersion:  result.ProtocolVersion,
		Instructions:     clip(result.Instructions, 8<<10),
		ToolsListChanged: result.Capabilities.Tools.ListChanged,
	}
	c.mu.Unlock()
	c.tr.negotiated(result.ProtocolVersion)
	note, _ := notification("notifications/initialized", nil)
	return c.tr.notify(ctx, note)
}

// ListTools читает список инструментов по страницам. Потолки — от сервера,
// который отдаёт бесконечный курсор или тысячи инструментов.
func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	ctx, cancel := context.WithTimeout(ctx, c.cfg.CallTimeout)
	defer cancel()
	// Признак «список устарел» снимается до чтения, а не после: уведомление,
	// пришедшее во время чтения или сразу за ответом, должно его пережить.
	c.stale.Store(false)
	var tools []Tool
	cursor := ""
	for page := 0; page < maxToolPages; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := c.send(ctx, "tools/list", params, true)
		if err != nil {
			return nil, c.wrap(err)
		}
		var result struct {
			Tools      []Tool `json:"tools"`
			NextCursor string `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &result); err != nil {
			return nil, newError(KindProtocol, err, "tools/list result is not an object")
		}
		tools = append(tools, result.Tools...)
		if len(tools) > maxTools {
			return nil, newError(KindTooLarge, nil, "server lists more than %d tools", maxTools)
		}
		if result.NextCursor == "" {
			return tools, nil
		}
		cursor = result.NextCursor
	}
	return nil, newError(KindProtocol, nil, "tools/list did not finish in %d pages", maxToolPages)
}

// CallTool вызывает инструмент. Ошибка инструмента (isError) — не ошибка
// вызова: результат возвращается, и решает вызывающий.
func (c *Client) CallTool(ctx context.Context, name string, arguments json.RawMessage) (CallResult, error) {
	if len(arguments) == 0 {
		arguments = json.RawMessage(`{}`)
	}
	callCtx, cancel := context.WithTimeout(ctx, c.cfg.CallTimeout)
	defer cancel()
	id := c.nextID.Add(1)
	msg, err := request(id, "tools/call", map[string]any{"name": name, "arguments": arguments})
	if err != nil {
		return CallResult{}, newError(KindProtocol, err, "encode arguments")
	}
	raw, err := c.roundTrip(callCtx, msg, true)
	if err != nil {
		if callCtx.Err() != nil {
			// Сервер работает дальше, пока ему не скажут: без отмены долгий
			// вызов продолжал бы ходить в GitLab уже никому не нужным.
			c.cancelRequest(id)
		}
		return CallResult{}, c.wrap(err)
	}
	var result CallResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return CallResult{}, newError(KindProtocol, err, "tools/call result is not an object")
	}
	result.Truncated = limitContent(&result, c.cfg.MaxResultBytes)
	return result, nil
}

func (c *Client) cancelRequest(id int64) {
	note, err := notification("notifications/cancelled", map[string]any{"requestId": id, "reason": "timeout"})
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = c.tr.notify(ctx, note)
}

func (c *Client) send(ctx context.Context, method string, params any, retry bool) (json.RawMessage, error) {
	msg, err := request(c.nextID.Add(1), method, params)
	if err != nil {
		return nil, newError(KindProtocol, err, "encode %s", method)
	}
	return c.roundTrip(ctx, msg, retry)
}

func (c *Client) roundTrip(ctx context.Context, msg message, retry bool) (json.RawMessage, error) {
	response, err := c.tr.call(ctx, msg)
	if errors.Is(err, errSessionExpired) && retry {
		if err = c.initialize(ctx); err == nil {
			response, err = c.tr.call(ctx, msg)
		}
	}
	if err != nil {
		if ctx.Err() != nil && KindOf(err) == "" {
			return nil, newError(KindTimeout, err, "%s: no answer in time", msg.Method)
		}
		return nil, err
	}
	if response.Error != nil {
		return nil, newError(KindTool, nil, "%s: %s (code %d)", msg.Method, c.scrub(clip(response.Error.Message, 2000)), response.Error.Code)
	}
	return response.Result, nil
}

// inbound отвечает на то, что сервер прислал сам.
func (c *Client) inbound(msg message) *message {
	switch {
	case msg.isRequest():
		if msg.Method == "ping" {
			reply := resultFor(msg.ID, struct{}{})
			return &reply
		}
		// sampling/createMessage, roots/list, elicitation/create и всё
		// незнакомое: модель, файлы и человек Point серверу не отдаются.
		_, _ = fmt.Fprintf(c.log, "\n[point] refused server request %s\n", clip(msg.Method, 100))
		reply := errorFor(msg.ID, codeMethodNotFound, "Point does not provide "+clip(msg.Method, 100))
		return &reply
	case msg.isNotification():
		switch msg.Method {
		case "notifications/tools/list_changed":
			c.stale.Store(true)
		case "notifications/message":
			var entry struct {
				Level string          `json:"level"`
				Data  json.RawMessage `json:"data"`
			}
			if json.Unmarshal(msg.Params, &entry) == nil {
				_, _ = fmt.Fprintf(c.log, "\n[%s] %s\n", clip(entry.Level, 20), clip(string(entry.Data), 2000))
			}
		}
	}
	return nil
}

// wrap дописывает к сбою хвост журнала и зачищает текст.
func (c *Client) wrap(err error) error {
	if err == nil {
		return nil
	}
	var target *Error
	if !errors.As(err, &target) {
		target = newError(KindNetwork, err, "%s", err.Error())
	}
	target.Detail = c.scrub(target.Detail)
	if target.Kind == KindExited || target.Kind == KindSpawn || target.Kind == KindHandshake {
		target.Stderr = c.log.Tail(4 << 10)
	}
	return target
}

func (c *Client) scrub(text string) string { return c.log.scrub(text) }

func limitContent(result *CallResult, limit int) bool {
	used := 0
	truncated := false
	for index := range result.Content {
		item := &result.Content[index]
		size := len(item.Text) + len(item.Data)
		if used+size <= limit {
			used += size
			continue
		}
		room := limit - used
		if room < 0 {
			room = 0
		}
		if len(item.Text) > room {
			item.Text = clip(item.Text, room)
		}
		item.Data = ""
		used = limit
		truncated = true
	}
	if len(result.Structured) > limit {
		result.Structured = nil
		truncated = true
	}
	return truncated
}

// clip режет по границе символа: русский текст в UTF-8 иначе ломался бы
// посередине буквы.
func clip(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	for limit > 0 && !utf8.RuneStart(text[limit]) {
		limit--
	}
	return text[:limit]
}
