package app

import (
	"context"
	"encoding/json"
	"strings"

	"local-agent-workbench/internal/domain"
	workbenchtools "local-agent-workbench/internal/tools"
)

// read_roster — чтение ростера под конкретное требование.
//
// Имя начинается с read_ не для красоты: префикс — единственный признак, по
// которому и человек в ленте, и модель в списке инструментов видят, что Мастер
// ничего не делает, а только смотрит. Ростер он всё равно не собирает — состав
// карточки считает сервер; инструмент нужен, чтобы Мастер знал про кандидатов и
// пробелы до того, как пообещает человеку исполнителя.
const masterRosterToolName = "read_roster"

// masterRosterObserver — наблюдатель, переданный замыканием. Хранилище тут ни
// при чём: подбор склеивает готовность, сигналы отбора и каталог чертежей, и
// тянуть всё это в интерфейс чтения значило бы пересказать половину ядра.
type masterRosterObserver func(context.Context, RosterNeed) (RosterObservation, error)

func masterRosterDefinition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name: masterRosterToolName,
		Description: "Посмотреть, кем делать эту работу: готовые исполнители проекта с их готовностью, подходящие чертежи и пробелы по ролям. " +
			"Вход — краткая сводка требований. Ростер наряда собирает сервер; инструмент только показывает картину.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{` +
			`"goal":{"type":"string","description":"Цель работы одной фразой"},` +
			`"scope":{"type":"array","items":{"type":"string"},"maxItems":10,"description":"Что входит в работу"},` +
			`"criteria":{"type":"array","items":{"type":"string"},"maxItems":10,"description":"Критерии готовности"},` +
			`"requiredTools":{"type":"array","items":{"type":"string"},"maxItems":16,"description":"Имена инструментов из каталога; неизвестные отбрасываются"},` +
			`"stackCategory":{"type":"string","description":"Категория результата: web, api, cli, data, desktop-mobile"},` +
			`"allowSubagents":{"type":"boolean","description":"Разрешены ли временные субагенты под готовым исполнителем"},` +
			`"maxAgents":{"type":"integer","minimum":0,"maximum":8,"description":"Бюджет проектных агентов"}` +
			`},"required":["goal"],"additionalProperties":false}`),
	}
}

type masterRosterInput struct {
	Goal           string   `json:"goal"`
	Scope          []string `json:"scope"`
	Criteria       []string `json:"criteria"`
	RequiredTools  []string `json:"requiredTools"`
	StackCategory  string   `json:"stackCategory"`
	AllowSubagents bool     `json:"allowSubagents"`
	MaxAgents      int      `json:"maxAgents"`
}

func (t *masterReadTools) readRoster(ctx context.Context, arguments json.RawMessage) domain.ToolResult {
	var input masterRosterInput
	if failure := workbenchtools.Decode(arguments, &input); failure != nil {
		return *failure
	}
	if strings.TrimSpace(input.Goal) == "" {
		return workbenchtools.Fail("invalid_input", "goal is required")
	}
	if t.observe == nil {
		return workbenchtools.Fail("storage_unavailable", "подбор исполнителей недоступен в этой сессии")
	}
	observation, err := t.observe(ctx, RosterNeed{
		WorkspaceID: t.workspaceID, Goal: input.Goal,
		Scope: input.Scope, Criteria: input.Criteria, RequiredTools: input.RequiredTools,
		StackCategory: input.StackCategory, AllowSubagents: input.AllowSubagents, MaxAgents: input.MaxAgents,
	})
	if err != nil {
		return workbenchtools.Fail("roster_unavailable", err.Error())
	}
	return workbenchtools.OK(masterRosterPayload(observation))
}

// masterRosterPayload — ответ в том виде, в каком его читает модель. Всё уже
// ограничено наблюдателем: вывод инструмента режется на 16 КиБ без всякого
// признака обрезки, и обрезанный JSON модель просто не разберёт.
func masterRosterPayload(observation RosterObservation) map[string]any {
	selected := make([]map[string]any, 0, len(observation.Selected))
	for _, candidate := range observation.Selected {
		selected = append(selected, map[string]any{
			"agentId": candidate.AgentID, "name": candidate.Name, "role": candidate.Role,
			"readiness": candidate.Readiness, "matched": candidate.Matched,
			"blocking": candidate.Blocking, "missingTools": candidate.MissingTools,
		})
	}
	considered := make([]map[string]any, 0, len(observation.Considered))
	for _, candidate := range observation.Considered {
		considered = append(considered, map[string]any{
			"agentId": candidate.AgentID, "name": candidate.Name,
			"readiness": candidate.Readiness, "whyNotSelected": candidate.WhyNot,
		})
	}
	gaps := make([]map[string]any, 0, len(observation.Gaps))
	for _, gap := range observation.Gaps {
		blueprints := make([]map[string]any, 0, len(gap.Blueprints))
		for _, blueprint := range gap.Blueprints {
			blueprints = append(blueprints, map[string]any{
				"blueprintId": blueprint.BlueprintID, "name": blueprint.Name,
				"role": blueprint.Role, "why": blueprint.Why,
			})
		}
		item := map[string]any{
			"kind": gap.Kind, "role": gap.Requirement.Role,
			"responsibility": gap.Requirement.Responsibility,
			"requiredTools":  gap.Requirement.RequiredTools,
			"blueprints":     blueprints,
		}
		if gap.Draft.Name != "" {
			item["draftForm"] = map[string]any{
				"name": gap.Draft.Name, "role": gap.Draft.Role,
				"mission": gap.Draft.Mission, "requiredTools": gap.Draft.RequiredTools,
			}
		}
		if gap.Subagent != nil {
			item["subagent"] = map[string]any{
				"parentAgentId": gap.Subagent.ParentAgentID, "role": gap.Subagent.Role,
				"mission": gap.Subagent.Mission, "requiredTools": gap.Subagent.RequiredTools,
			}
		}
		gaps = append(gaps, item)
	}
	payload := map[string]any{
		"selected": selected, "considered": considered, "gaps": gaps,
		"budget": map[string]any{"maxProjectAgents": observation.MaxAgents, "used": len(observation.Selected)},
		"note":   "Ростер карточки собирает сервер. Учти это в вопросах человеку; уточнить черновик можно необязательным полем hire.",
	}
	if observation.Reason != "" {
		payload["reason"] = observation.Reason
	}
	return payload
}
