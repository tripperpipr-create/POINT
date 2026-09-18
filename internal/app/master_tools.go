package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/security"
	workbenchtools "local-agent-workbench/internal/tools"
	"local-agent-workbench/internal/workspace"
)

const masterEntityEventLimit = 12

type masterReadStore interface {
	GetExecution(context.Context, string) (domain.ExecutionInstance, error)
	GetExecutionByRunID(context.Context, string) (domain.ExecutionInstance, error)
	ListExecutions(context.Context, string, int) ([]domain.ExecutionInstance, error)
	GetChangeSet(context.Context, string) (domain.ChangeSet, error)
	ListQuests(context.Context, string) ([]domain.Quest, error)
	GetRun(context.Context, string) (domain.Run, error)
	ListByRun(context.Context, string) ([]domain.Event, error)
}

// masterReadTools расширяет файловое чтение Компаньона сведениями Хаба.
// Новые имена перечислены явно: произвольный инструмент не может обойти grants
// базового реестра и превратить расследование Мастера в действие.
type masterReadTools struct {
	base        *companionReadTools
	store       masterReadStore
	workspaceID string
	observe     masterRosterObserver
}

func newMasterReadTools(fs *workspace.FS, store masterReadStore, workspaceID string, observe masterRosterObserver) *masterReadTools {
	var base *companionReadTools
	if fs != nil {
		base = newCompanionReadTools(fs, nil)
	}
	return &masterReadTools{base: base, store: store, workspaceID: workspaceID, observe: observe}
}

func (t *masterReadTools) Definitions() []domain.ToolDefinition {
	var definitions []domain.ToolDefinition
	if t.base != nil {
		definitions = append(definitions, t.base.Definitions()...)
	}
	definitions = append(definitions,
		masterEntityDefinition("read_execution", "Прочитать состояние execution или связанного run по идентификатору, включая ошибку и последние события."),
		masterEntityDefinition("read_changeset", "Прочитать workspace-scoped Change Set, его файлы, состояние execution и последние события."),
		masterEntityDefinition("read_quest", "Прочитать workspace-scoped квест и краткие сведения о его execution и последних событиях."),
		masterRosterDefinition(),
	)
	sort.Slice(definitions, func(i, j int) bool { return definitions[i].Name < definitions[j].Name })
	return definitions
}

func masterEntityDefinition(name, description string) domain.ToolDefinition {
	return domain.ToolDefinition{
		Name: name, Description: description,
		InputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string","description":"Точный идентификатор сущности"}},"required":["id"],"additionalProperties":false}`),
	}
}

func (t *masterReadTools) Execute(ctx context.Context, name string, arguments json.RawMessage) domain.ToolResult {
	switch name {
	case "read_execution":
		return t.readExecution(ctx, arguments)
	case "read_changeset":
		return t.readChangeSet(ctx, arguments)
	case "read_quest":
		return t.readQuest(ctx, arguments)
	case masterRosterToolName:
		return t.readRoster(ctx, arguments)
	default:
		if t.base != nil {
			return t.base.Execute(ctx, name, arguments)
		}
		return workbenchtools.FailWithHint("tool_not_allowed", "Мастеру доступны только читающие инструменты", "предложите действие человеку вместо его выполнения")
	}
}

type masterEntityInput struct {
	ID string `json:"id"`
}

func decodeMasterEntityInput(arguments json.RawMessage) (masterEntityInput, *domain.ToolResult) {
	var input masterEntityInput
	if failure := workbenchtools.Decode(arguments, &input); failure != nil {
		return input, failure
	}
	input.ID = strings.TrimSpace(input.ID)
	if input.ID == "" {
		failure := workbenchtools.Fail("invalid_input", "id is required")
		return input, &failure
	}
	return input, nil
}

func (t *masterReadTools) readExecution(ctx context.Context, arguments json.RawMessage) domain.ToolResult {
	input, failure := decodeMasterEntityInput(arguments)
	if failure != nil {
		return *failure
	}
	if t.store == nil {
		return workbenchtools.Fail("storage_unavailable", "хранилище Хаба недоступно")
	}
	execution, err := t.store.GetExecution(ctx, input.ID)
	if errors.Is(err, sql.ErrNoRows) {
		execution, err = t.store.GetExecutionByRunID(ctx, input.ID)
	}
	if err == nil {
		if execution.WorkspaceID != t.workspaceID {
			return masterEntityNotFound("execution")
		}
		return workbenchtools.OK(t.executionSummary(ctx, execution))
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return workbenchtools.Fail("read_failed", err.Error())
	}
	run, runErr := t.store.GetRun(ctx, input.ID)
	if errors.Is(runErr, sql.ErrNoRows) || runErr == nil && run.WorkspaceID != t.workspaceID {
		return masterEntityNotFound("execution")
	}
	if runErr != nil {
		return workbenchtools.Fail("read_failed", runErr.Error())
	}
	return workbenchtools.OK(map[string]any{
		"id": run.ID, "runId": run.ID, "status": run.Status, "error": boundedMasterReadText(run.Error, 2000),
		"result": boundedMasterReadText(run.Result, 2000), "task": boundedMasterReadText(run.Task, 1200),
		"provider": run.Provider, "model": run.Model, "events": t.eventSnippets(ctx, run.ID),
	})
}

func (t *masterReadTools) executionSummary(ctx context.Context, execution domain.ExecutionInstance) map[string]any {
	summary := map[string]any{
		"id": execution.ID, "runId": execution.RunID, "questId": execution.QuestID,
		"projectAgentId": execution.ProjectAgentID, "status": execution.Status,
		"error": boundedMasterReadText(execution.Error, 2000), "result": boundedMasterReadText(execution.Result, 2000),
		"task": boundedMasterReadText(execution.Task, 1200), "startedAt": execution.StartedAt,
		"finishedAt": execution.FinishedAt, "durationMs": execution.DurationMs,
	}
	if execution.RunID != "" {
		summary["events"] = t.eventSnippets(ctx, execution.RunID)
	}
	return summary
}

func (t *masterReadTools) readChangeSet(ctx context.Context, arguments json.RawMessage) domain.ToolResult {
	input, failure := decodeMasterEntityInput(arguments)
	if failure != nil {
		return *failure
	}
	if t.store == nil {
		return workbenchtools.Fail("storage_unavailable", "хранилище Хаба недоступно")
	}
	set, err := t.store.GetChangeSet(ctx, input.ID)
	if errors.Is(err, sql.ErrNoRows) || err == nil && set.WorkspaceID != t.workspaceID {
		return masterEntityNotFound("Change Set")
	}
	if err != nil {
		return workbenchtools.Fail("read_failed", err.Error())
	}
	items := make([]map[string]any, 0, min(len(set.Items), 20))
	for index, item := range set.Items {
		if index >= 20 {
			break
		}
		items = append(items, map[string]any{
			"path": item.Path, "kind": item.Kind, "appliedOperation": item.AppliedOperation,
			"diffSnippet": boundedMasterReadText(item.Diff, 1200),
		})
	}
	summary := map[string]any{
		"id": set.ID, "executionId": set.ExecutionID, "questId": set.QuestID, "title": set.Title,
		"kind": set.Kind, "status": set.Status, "items": items, "itemCount": len(set.Items),
		"dependsOn": set.DependsOn, "supersedes": set.Supersedes, "supersededBy": set.SupersededBy,
		"resolutions": set.Resolutions, "createdAt": set.CreatedAt, "updatedAt": set.UpdatedAt, "appliedAt": set.AppliedAt,
	}
	if set.ExecutionID != "" {
		if execution, executionErr := t.store.GetExecution(ctx, set.ExecutionID); executionErr == nil && execution.WorkspaceID == t.workspaceID {
			summary["execution"] = t.executionSummary(ctx, execution)
		}
	}
	return workbenchtools.OK(summary)
}

func (t *masterReadTools) readQuest(ctx context.Context, arguments json.RawMessage) domain.ToolResult {
	input, failure := decodeMasterEntityInput(arguments)
	if failure != nil {
		return *failure
	}
	if t.store == nil {
		return workbenchtools.Fail("storage_unavailable", "хранилище Хаба недоступно")
	}
	quests, err := t.store.ListQuests(ctx, t.workspaceID)
	if err != nil {
		return workbenchtools.Fail("read_failed", err.Error())
	}
	var quest *domain.Quest
	for index := range quests {
		if quests[index].ID == input.ID {
			quest = &quests[index]
			break
		}
	}
	if quest == nil {
		return masterEntityNotFound("квест")
	}
	executions, err := t.store.ListExecutions(ctx, t.workspaceID, 500)
	if err != nil {
		return workbenchtools.Fail("read_failed", err.Error())
	}
	executionSummaries := make([]map[string]any, 0)
	for _, execution := range executions {
		if execution.QuestID != quest.ID {
			continue
		}
		executionSummaries = append(executionSummaries, t.executionSummary(ctx, execution))
		if len(executionSummaries) >= 20 {
			break
		}
	}
	return workbenchtools.OK(map[string]any{
		"id": quest.ID, "parentId": quest.ParentID, "title": quest.Title, "description": boundedMasterReadText(quest.Description, 2000),
		"status": quest.Status, "objectives": quest.Objectives, "constraints": quest.Constraints,
		"definitionOfDone": quest.DefinitionOfDone, "teamId": quest.TeamID, "flowId": quest.FlowID,
		"createdAt": quest.CreatedAt, "updatedAt": quest.UpdatedAt, "finishedAt": quest.FinishedAt,
		"executions": executionSummaries,
	})
}

func (t *masterReadTools) eventSnippets(ctx context.Context, runID string) []map[string]any {
	run, err := t.store.GetRun(ctx, runID)
	if err != nil || run.WorkspaceID != t.workspaceID {
		return []map[string]any{}
	}
	events, err := t.store.ListByRun(ctx, runID)
	if err != nil {
		return []map[string]any{}
	}
	if len(events) > masterEntityEventLimit {
		events = events[len(events)-masterEntityEventLimit:]
	}
	result := make([]map[string]any, 0, len(events))
	for _, event := range events {
		result = append(result, map[string]any{
			"type": event.Type, "step": event.Step, "actor": event.Actor,
			"dataSnippet": boundedMasterReadText(string(event.Data), 1200), "createdAt": event.CreatedAt,
		})
	}
	return result
}

func masterEntityNotFound(kind string) domain.ToolResult {
	return workbenchtools.Fail("not_found", kind+" не найден в текущем workspace")
}

func boundedMasterReadText(value string, limit int) string {
	value = strings.TrimSpace(security.Redact(value))
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return value
}
