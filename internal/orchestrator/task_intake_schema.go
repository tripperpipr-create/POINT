package orchestrator

import (
	"encoding/json"
	"strings"
)

// Native constrained output is an additional format guard. Domain validation
// still owns readiness, versioning and authority; valid JSON is not approval.
func taskIntakeJSONSchema() json.RawMessage {
	text := map[string]any{"type": "string"}
	list := map[string]any{"type": "array", "items": text}
	object := func(properties map[string]any, required ...string) map[string]any {
		return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
	}
	enum := func(values ...string) map[string]any { return map[string]any{"type": "string", "enum": values} }
	manual := object(map[string]any{"id": text, "text": text, "kind": enum("manual")}, "id", "text", "kind")
	verification := object(map[string]any{"id": text, "text": text, "kind": enum("verification"), "tool": text, "arguments": map[string]any{"type": "object"}, "expectedExitCode": map[string]any{"type": "integer", "const": 0}}, "id", "text", "kind", "tool", "arguments")
	reproduction := object(map[string]any{"id": text, "text": text, "kind": enum("reproduction"), "tool": text, "arguments": map[string]any{"type": "object"}, "expectedExitCode": map[string]any{"type": "integer", "minimum": 0, "maximum": 255}}, "id", "text", "kind", "tool", "arguments", "expectedExitCode")
	criterion := map[string]any{"anyOf": []any{manual, verification, reproduction}}

	brief := object(map[string]any{
		"mode": enum("precise", "project", "undecided"), "state": enum("discussion", "ready"), "goal": text, "resultKind": enum("", "code", "report", "workspace_change", "hub_tool"), "audience": text,
		"scope": list, "outOfScope": list, "openQuestions": list,
		"criteria":    map[string]any{"type": "array", "items": criterion},
		"decisions":   map[string]any{"type": "array", "items": object(map[string]any{"topic": text, "decision": text, "source": enum("user", "project", "delegated")}, "topic", "decision", "source")},
		"permissions": object(map[string]any{"writeFiles": map[string]any{"type": "boolean"}, "executeCommands": map[string]any{"type": "boolean"}, "provisionProjectAgents": map[string]any{"type": "boolean"}, "networkHosts": list}, "writeFiles", "executeCommands", "provisionProjectAgents", "networkHosts"),
		"budget": object(map[string]any{
			"tokens": map[string]any{"type": "integer"}, "costCents": map[string]any{"type": "integer", "minimum": 0},
			"activeSeconds": map[string]any{"type": "integer"}, "maxParallel": map[string]any{"type": "integer"},
			"maxReplans": map[string]any{"type": "integer"}, "maxAttempts": map[string]any{"type": "integer"},
			"maxProjectAgents": map[string]any{"type": "integer", "minimum": 0, "maximum": 8},
		}, "tokens", "costCents", "activeSeconds", "maxParallel", "maxReplans", "maxAttempts", "maxProjectAgents"),
	}, "mode", "state", "goal", "resultKind", "scope", "outOfScope", "openQuestions", "criteria", "permissions")
	// hire необязателен намеренно. Обязательное поле держало бы карточку на
	// послушности модели, а ровно на этом уже сломалось обещание возвращать
	// proposalId: уточнение приезжало второй карточкой вместо новой версии.
	hire := object(map[string]any{"name": text, "role": text, "mission": text, "requiredTools": list}, "name", "role", "mission")
	properties := map[string]any{"hire": map[string]any{"anyOf": []any{hire, map[string]any{"type": "null"}}}, "conversationSummary": text, "memorySuggestions": map[string]any{"type": "array", "items": text, "maxItems": 3}, "intent": enum("task", "chat"), "reply": text, "questions": map[string]any{"type": "array", "items": text, "maxItems": 2}, "proposalId": text, "title": text, "agentIds": list, "brief": map[string]any{"anyOf": []any{brief, map[string]any{"type": "null"}}}}
	// Вопрос с выбором обязан принести сам выбор: "options": [] проходило
	// required насквозь, и человек получал одиночный выбор без единого чипа.
	// Свободный ответ остаётся отдельной ветвью — у него вариантов нет по сути.
	choiceOptions := map[string]any{"type": "array", "items": text, "minItems": 2, "maxItems": 8}
	freeOptions := map[string]any{"type": "array", "items": text, "maxItems": 0}
	choiceQuestion := object(map[string]any{"id": text, "text": text, "kind": enum("single", "multiple"), "options": choiceOptions}, "id", "text", "kind", "options")
	freeQuestion := object(map[string]any{"id": text, "text": text, "kind": enum("text"), "options": freeOptions}, "id", "text", "kind", "options")
	properties["clarifications"] = map[string]any{"type": "array", "maxItems": 2, "items": map[string]any{"anyOf": []any{choiceQuestion, freeQuestion}}}
	// intent and brief are a discriminated pair. A task with brief=null
	// otherwise satisfies JSON syntax but forces repeated repair rounds.
	taskProperties, chatProperties := map[string]any{}, map[string]any{}
	for key, value := range properties {
		taskProperties[key], chatProperties[key] = value, value
	}
	taskProperties["intent"], taskProperties["brief"] = enum("task"), brief
	chatProperties["intent"], chatProperties["brief"] = enum("chat"), map[string]any{"type": "null"}
	raw, _ := json.Marshal(map[string]any{"anyOf": []any{
		orderedIntakeEnvelope(taskProperties),
		orderedIntakeEnvelope(chatProperties),
	}})
	return raw
}

// Ollama's grammar follows property order. Decide intent before the nullable
// contract; alphabetical map encoding otherwise makes the model choose null
// before it has emitted the semantic classification.
func orderedIntakeEnvelope(properties map[string]any) json.RawMessage {
	keys := []string{"intent", "reply", "questions", "proposalId", "title", "agentIds", "brief", "clarifications", "memorySuggestions", "conversationSummary", "hire"}
	// hire идёт последним и в required не входит: уточнение исполнителя не
	// должно превращаться в обязательный шаг. Схема, требующая поле, заставляет
	// слабую модель придумывать его каждый ход — а подбор и так делает сервер.
	optional := map[string]bool{"hire": true}
	var out strings.Builder
	out.WriteString(`{"type":"object","additionalProperties":false,"properties":{`)
	requiredKeys := make([]string, 0, len(keys))
	for i, key := range keys {
		if i > 0 {
			out.WriteByte(',')
		}
		name, _ := json.Marshal(key)
		value, _ := json.Marshal(properties[key])
		out.Write(name)
		out.WriteByte(':')
		out.Write(value)
		if !optional[key] {
			requiredKeys = append(requiredKeys, key)
		}
	}
	required, _ := json.Marshal(requiredKeys)
	out.WriteString(`},"required":`)
	out.Write(required)
	out.WriteByte('}')
	return json.RawMessage(out.String())
}
