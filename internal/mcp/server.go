// Package mcp отдаёт инструменты Point по протоколу MCP тем исполнителям,
// которые не умеют принимать их иначе.
//
// Модель, работающая по API, получает инструменты в теле запроса и зовёт их
// через ядро: там доступ проверяется на своей стороне. Локальный CLI устроен
// наоборот — он исполняет инструменты сам и принимает чужие только как
// MCP-сервер. Чтобы у одной сущности не завелось двух разных наборов прав,
// сервер здесь ничего не решает про доступ: он получает готовую связку
// «реестр + права» той сущности, от чьего имени пришёл запрос, и выдаёт ровно
// то, что выдал бы обычный путь.
//
// Кто спрашивает — определяет разовый ключ сессии, а не заголовок с именем:
// имя подделывается, ключ выдаётся ядром на время одного разговора и живёт
// столько же.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/observability"
)

// Executor — то, чем сущность уже пользуется в обычном пути: список доступных
// ей инструментов и их исполнение с проверкой прав. Сервер намеренно не знает
// ни про policy.Grants, ни про реестр инструментов: правило доступа записано
// один раз, и второе его прочтение здесь разошлось бы с первым.
type Executor interface {
	Definitions() []domain.ToolDefinition
	Execute(ctx context.Context, name string, arguments json.RawMessage) domain.ToolResult
}

// Session — доступ одной сущности на время одного разговора. Ключ выдаёт ядро,
// оно же закрывает сессию, когда разговор кончился: ключ, переживший работу,
// становится дырой в правах.
type Session struct {
	Key      string
	Subject  string
	Executor Executor
}

// Registry хранит открытые сессии. Он не чистит их по времени: срок жизни
// сессии — это срок работы, которую она обслуживает, и знает его тот, кто её
// открыл.
type Registry struct {
	mu       sync.RWMutex
	sessions map[string]Session
}

func NewRegistry() *Registry { return &Registry{sessions: make(map[string]Session)} }

// Open заводит сессию под уже выданный ключ. Ключ приходит снаружи, потому что
// его же надо передать исполнителю в конфиге, и генерировать его в двух местах
// значило бы иметь два разных ключа.
func (r *Registry) Open(session Session) error {
	if strings.TrimSpace(session.Key) == "" {
		return errors.New("mcp session key is required")
	}
	if session.Executor == nil {
		return errors.New("mcp session needs an executor")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[session.Key] = session
	return nil
}

func (r *Registry) Close(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sessions, key)
}

func (r *Registry) lookup(key string) (Session, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	session, ok := r.sessions[key]
	return session, ok
}

// Count — сколько сессий открыто. Нужен наблюдаемости: сессия, пережившая
// работу, видна только счётчиком.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.sessions)
}

// Протокол: JSON-RPC 2.0. Полей ровно столько, сколько нужно инструментам —
// приветствие, список и вызов.
type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *responseError  `json:"error,omitempty"`
}

type responseError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type callParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// ProtocolVersion — версия MCP, о которой договариваемся при приветствии.
const ProtocolVersion = "2025-06-18"

// ServerName — под этим именем инструменты Point видны исполнителю: у Claude
// Code они станут mcp__point__<инструмент>, и по этому префиксу их выдают в
// разрешённых.
const ServerName = "point"

// Handler — точка входа MCP. Один маршрут: ключ сессии приходит заголовком,
// тело — JSON-RPC.
func (r *Registry) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, httpRequest *http.Request) {
		session, ok := r.lookup(sessionKey(httpRequest))
		if !ok {
			writeError(w, http.StatusUnauthorized, nil, -32001, "mcp session is unknown or closed")
			return
		}
		var call request
		if err := json.NewDecoder(http.MaxBytesReader(w, httpRequest.Body, 1<<20)).Decode(&call); err != nil {
			writeError(w, http.StatusBadRequest, nil, -32700, "parse error")
			return
		}
		switch call.Method {
		case "initialize":
			writeResult(w, call.ID, map[string]any{
				"protocolVersion": ProtocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": ServerName, "version": "1"},
			})
		case "notifications/initialized":
			// Уведомление ответа не ждёт, но и ошибкой не является.
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeResult(w, call.ID, map[string]any{"tools": toolList(session.Executor.Definitions())})
		case "tools/call":
			var params callParams
			if len(call.Params) > 0 && json.Unmarshal(call.Params, &params) != nil {
				writeError(w, http.StatusOK, call.ID, -32602, "invalid params")
				return
			}
			// Чужой исполнитель зовёт инструменты Point молча: в логе ядра виден
			// только маршрут. Имя инструмента и исход пишутся здесь — иначе о
			// том, что смотрел помощник, узнать неоткуда.
			started := time.Now()
			result := session.Executor.Execute(httpRequest.Context(), params.Name, params.Arguments)
			observability.From(httpRequest.Context()).Info("mcp tool call",
				"subject", session.Subject,
				"tool", params.Name,
				"ok", result.OK,
				"truncated", result.Truncated,
				"duration_ms", time.Since(started).Milliseconds(),
			)
			writeResult(w, call.ID, toolCallResult(result))
		default:
			writeError(w, http.StatusOK, call.ID, -32601, "method not found: "+call.Method)
		}
	}
}

func sessionKey(httpRequest *http.Request) string {
	header := strings.TrimSpace(httpRequest.Header.Get("Authorization"))
	if after, ok := strings.CutPrefix(header, "Bearer "); ok {
		return strings.TrimSpace(after)
	}
	return ""
}

// toolList переводит определения Point в форму MCP. Схема аргументов идёт как
// есть: она уже описана JSON Schema, и переписывать её значило бы завести
// второе описание одного предмета.
func toolList(definitions []domain.ToolDefinition) []map[string]any {
	tools := make([]map[string]any, 0, len(definitions))
	for _, definition := range definitions {
		schema := definition.InputSchema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		tools = append(tools, map[string]any{
			"name":        definition.Name,
			"description": definition.Description,
			"inputSchema": schema,
		})
	}
	return tools
}

// toolCallResult отдаёт результат в форме MCP. Отказ по правам — это ответ с
// пометкой isError, а не транспортная ошибка: исполнитель должен прочитать
// причину и объяснить её человеку, а не свалиться.
func toolCallResult(result domain.ToolResult) map[string]any {
	if !result.OK {
		message := "инструмент недоступен"
		if result.Error != nil {
			message = result.Error.Message
			if result.Error.Hint != "" {
				message += " " + result.Error.Hint
			}
		}
		return map[string]any{
			"isError": true,
			"content": []map[string]any{{"type": "text", "text": message}},
		}
	}
	text := strings.TrimSpace(string(result.Output))
	if text == "" {
		text = "{}"
	}
	return map[string]any{"content": []map[string]any{{"type": "text", "text": text}}}
}

func writeResult(w http.ResponseWriter, id json.RawMessage, result any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response{JSONRPC: "2.0", ID: id, Result: result})
}

func writeError(w http.ResponseWriter, status int, id json.RawMessage, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(response{JSONRPC: "2.0", ID: id, Error: &responseError{Code: code, Message: message}})
}

// ClientConfig — то, что получает исполнитель: адрес сервера и ключ сессии.
// Формат конфига MCP-клиента общий, поэтому собирается он здесь, а не в
// провайдере: провайдеров станет больше одного.
func ClientConfig(baseURL, key string) (string, error) {
	if strings.TrimSpace(baseURL) == "" || strings.TrimSpace(key) == "" {
		return "", errors.New("mcp client config needs a base URL and a session key")
	}
	config := map[string]any{
		"mcpServers": map[string]any{
			ServerName: map[string]any{
				"type":    "http",
				"url":     strings.TrimRight(baseURL, "/") + "/mcp",
				"headers": map[string]string{"Authorization": "Bearer " + key},
			},
		},
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("encode mcp client config: %w", err)
	}
	return string(encoded), nil
}

// ToolPattern — как инструмент Point зовётся у исполнителя. Разрешения ему
// выдаются по этому имени, и собирать строку в двух местах значило бы однажды
// разойтись на дефисе.
func ToolPattern(name string) string {
	return "mcp__" + ServerName + "__" + name
}

// StreamRefusal отвечает на попытку открыть поток событий. MCP допускает
// транспорт без потока: вызовы идут запрос-ответом на тот же адрес, и клиенту
// надо сказать об этом словами протокола, а не пустым отказом маршрутизатора.
func (r *Registry) StreamRefusal() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusMethodNotAllowed, nil, -32000, "this MCP endpoint answers POST requests only; server-sent events are not offered")
	}
}
