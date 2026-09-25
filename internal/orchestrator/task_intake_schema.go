package orchestrator

import (
	"encoding/json"

	"local-agent-workbench/internal/domain"
)

// Схемы инструментов разговора. Структуру задания держит протокол вызова
// инструментов, а не разбор JSON из текста: прежний конверт заставлял модель
// заворачивать в JSON и обычный ответ на вопрос, и слабая модель теряла на
// этом целые ходы. Проверку готовности, версий и прав по-прежнему делает
// домен — валидная схема не означает утверждения.

func taskBriefJSONSchema() map[string]any {
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
	// Одни имена значений модель читает по-своему: «написать сервис» для неё
	// code, и задание с writeFiles отклонялось сервером. Смысл значения должен
	// ехать вместе с перечислением, а не жить только в проверке домена.
	resultKind := enum("", "code", "report", "workspace_change", "hub_tool")
	resultKind["description"] = "workspace_change — создать или изменить файлы проекта (новый сервис, правка кода, конфигурация); code — код только текстом в ответе, файлы не меняются; report — отчёт; hub_tool — исходник инструмента Point без регистрации и запуска."
	writeFiles := map[string]any{"type": "boolean", "description": "true только при resultKind=workspace_change."}
	return object(map[string]any{
		"mode": enum("precise", "project", "undecided"), "state": enum("discussion", "ready"), "goal": text, "resultKind": resultKind, "audience": text,
		"scope": list, "outOfScope": list, "openQuestions": list,
		"criteria":    map[string]any{"type": "array", "items": criterion},
		"decisions":   map[string]any{"type": "array", "items": object(map[string]any{"topic": text, "decision": text, "source": enum("user", "project", "delegated")}, "topic", "decision", "source")},
		"permissions": object(map[string]any{"writeFiles": writeFiles, "executeCommands": map[string]any{"type": "boolean"}, "provisionProjectAgents": map[string]any{"type": "boolean"}, "networkHosts": list}, "writeFiles", "executeCommands", "provisionProjectAgents", "networkHosts"),
		"budget": object(map[string]any{
			"tokens": map[string]any{"type": "integer"}, "costCents": map[string]any{"type": "integer", "minimum": 0},
			"activeSeconds": map[string]any{"type": "integer"}, "maxParallel": map[string]any{"type": "integer"},
			"maxReplans": map[string]any{"type": "integer"}, "maxAttempts": map[string]any{"type": "integer"},
			"maxProjectAgents": map[string]any{"type": "integer", "minimum": 0, "maximum": 8},
		}, "tokens", "costCents", "activeSeconds", "maxParallel", "maxReplans", "maxAttempts", "maxProjectAgents"),
	}, "mode", "state", "goal", "resultKind", "scope", "outOfScope", "openQuestions", "criteria", "permissions")
}

// Вопрос с выбором обязан принести сам выбор: "options": [] проходило
// required насквозь, и человек получал одиночный выбор без единого чипа.
// Свободный ответ остаётся отдельной ветвью — у него вариантов нет по сути.
func masterClarificationJSONSchema() map[string]any {
	text := map[string]any{"type": "string"}
	enum := func(values ...string) map[string]any { return map[string]any{"type": "string", "enum": values} }
	object := func(properties map[string]any, required ...string) map[string]any {
		return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
	}
	choiceOptions := map[string]any{"type": "array", "items": text, "minItems": 2, "maxItems": 8}
	freeOptions := map[string]any{"type": "array", "items": text, "maxItems": 0}
	choice := object(map[string]any{"text": text, "kind": enum("single", "multiple"), "options": choiceOptions}, "text", "kind", "options")
	free := object(map[string]any{"text": text, "kind": enum("text"), "options": freeOptions}, "text", "kind")
	return map[string]any{"anyOf": []any{choice, free}}
}

func masterActionDefinitions() []domain.ToolDefinition {
	schema := func(value map[string]any) json.RawMessage {
		raw, _ := json.Marshal(value)
		return raw
	}
	return []domain.ToolDefinition{
		{
			Name:        masterActionProposeBrief,
			Description: "Оформить или обновить задание на работу. Зови, когда человек поручает работу или обсуждает её требования, даже если известна лишь цель. Карточку задания человек увидит отдельно, повторять её в тексте не нужно. Сервер проверит задание и вернёт замечания, если оно неполно.",
			InputSchema: schema(map[string]any{
				"type": "object", "additionalProperties": false, "required": []string{"title", "brief"},
				"properties": map[string]any{
					"proposalId": map[string]any{"type": "string", "description": "Идентификатор задания из снимка проекта, если продолжаешь его; для нового задания оставь пустым."},
					"title":      map[string]any{"type": "string", "description": "Короткое название задания, до 72 знаков."},
					"brief":      taskBriefJSONSchema(),
				},
			}),
		},
		{
			Name:        masterActionAskClarifications,
			Description: "Задать человеку до двух существенных уточнений, от которых зависит реализация, границы или проверка. Вопросы покажутся карточкой с вариантами; в тексте ответа их не дублируй.",
			InputSchema: schema(map[string]any{
				"type": "object", "additionalProperties": false, "required": []string{"items"},
				"properties": map[string]any{
					"items": map[string]any{"type": "array", "minItems": 1, "maxItems": 2, "items": masterClarificationJSONSchema()},
				},
			}),
		},
		{
			Name:        masterActionSuggestMemory,
			Description: "Предложить запомнить устойчивое предпочтение или факт о проекте из слов человека. Запись попадёт в память только после его согласия.",
			InputSchema: schema(map[string]any{
				"type": "object", "additionalProperties": false, "required": []string{"entries"},
				"properties": map[string]any{
					"entries": map[string]any{"type": "array", "minItems": 1, "maxItems": 3, "items": map[string]any{"type": "string"}},
				},
			}),
		},
	}
}
