package modeljson

import (
	"encoding/json"
	"strings"
)

// RepairToolArguments возвращает аргументы вызова инструмента как JSON-объект.
//
// Локальные модели и их парсеры на шлюзе портят аргументы предсказуемо:
// оборачивают в ограду кода, дописывают `<think>` или пояснение после
// объекта, лишнюю закрывающую скобку, присылают объект строкой JSON.
// Починка ничего не угадывает: результат обязан быть валидным объектом, иначе
// ok=false, и модель получает прежний отказ «аргументы не JSON».
// repaired=true — аргументы пришлось исправлять.
func RepairToolArguments(raw string) (args json.RawMessage, repaired bool, ok bool) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return json.RawMessage(`{}`), false, true
	}
	if isObject(text) {
		return json.RawMessage(text), false, true
	}
	if payload, err := Payload(text); err == nil && isObject(payload) {
		return json.RawMessage(payload), true, true
	}
	// Объект, присланный строкой JSON: "{\"path\":\"a.go\"}".
	var inner string
	if json.Unmarshal([]byte(text), &inner) == nil && isObject(strings.TrimSpace(inner)) {
		return json.RawMessage(strings.TrimSpace(inner)), true, true
	}
	stripped, _ := Payload(text)
	if object, found := firstObject(stripped); found && isObject(object) {
		return json.RawMessage(object), true, true
	}
	return nil, false, false
}

// TextToolCall — вызов инструмента, который модель написала текстом ответа, а
// не структурой протокола.
type TextToolCall struct {
	Name      string
	Arguments json.RawMessage
}

const (
	toolCallOpen  = "<tool_call>"
	toolCallClose = "</tool_call>"
)

// ExtractTextToolCalls достаёт вызовы вида `<tool_call>{"name":…,
// "arguments":…}</tool_call>` (формат hermes, которым пишут Qwen и их
// производные), когда парсер шлюза не распознал их и отдал текстом.
//
// Берутся только вызовы, чьё имя разрешает allowed, — текст ответа не может
// дать модели инструмент, которого ей не предлагали. Если хоть один блок не
// разобран, ничего не извлекается: половина плана хуже честного текста.
// rest — ответ без извлечённых блоков.
func ExtractTextToolCalls(content string, allowed func(string) bool) (calls []TextToolCall, rest string) {
	if !strings.Contains(content, toolCallOpen) {
		return nil, content
	}
	var kept strings.Builder
	remaining := content
	for {
		start := strings.Index(remaining, toolCallOpen)
		if start < 0 {
			kept.WriteString(remaining)
			break
		}
		kept.WriteString(remaining[:start])
		body := remaining[start+len(toolCallOpen):]
		end := strings.Index(body, toolCallClose)
		next := ""
		if end >= 0 {
			next = body[end+len(toolCallClose):]
			body = body[:end]
		}
		call, ok := parseTextToolCall(body)
		if !ok || allowed == nil || !allowed(call.Name) {
			return nil, content
		}
		calls = append(calls, call)
		if end < 0 {
			break
		}
		remaining = next
	}
	return calls, strings.TrimSpace(kept.String())
}

func parseTextToolCall(body string) (TextToolCall, bool) {
	payload, err := Payload(body)
	if err != nil {
		return TextToolCall{}, false
	}
	object, found := firstObject(payload)
	if !found {
		return TextToolCall{}, false
	}
	var envelope struct {
		Name       string          `json:"name"`
		Arguments  json.RawMessage `json:"arguments"`
		Parameters json.RawMessage `json:"parameters"`
	}
	if json.Unmarshal([]byte(object), &envelope) != nil || strings.TrimSpace(envelope.Name) == "" {
		return TextToolCall{}, false
	}
	rawArgs := envelope.Arguments
	if len(rawArgs) == 0 {
		rawArgs = envelope.Parameters
	}
	args, _, ok := RepairToolArguments(string(rawArgs))
	if !ok {
		return TextToolCall{}, false
	}
	return TextToolCall{Name: strings.TrimSpace(envelope.Name), Arguments: args}, true
}

func isObject(text string) bool {
	return strings.HasPrefix(text, "{") && json.Valid([]byte(text))
}

// firstObject возвращает первый сбалансированный объект верхнего уровня,
// учитывая строки и экранирование: скобка внутри строки объект не закрывает.
func firstObject(text string) (string, bool) {
	start := strings.IndexByte(text, '{')
	if start < 0 {
		return "", false
	}
	depth := 0
	inString, escaped := false, false
	for i := start; i < len(text); i++ {
		c := text[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return text[start : i+1], true
			}
		}
	}
	return "", false
}
