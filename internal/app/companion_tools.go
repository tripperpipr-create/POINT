package app

import (
	"context"
	"encoding/json"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/policy"
	workbenchtools "local-agent-workbench/internal/tools"
	"local-agent-workbench/internal/workspace"
)

// Что компаньону дают в руки — решают его grants, а не список здесь.
//
// Выданы группы чтения, индекса и git: ни один инструмент из них не меняет ни
// файлы, ни репозиторий, ни процессы. `run_command` и `propose_patch` лежат в
// других группах и компаньону не достаются — он живёт в боковой панели,
// отвечает на каждую реплику и ничего не должен трогать без квеста и
// подтверждения.
//
// Группа навыка добавляется только тем помощникам, у кого навыки надеты: без
// них read_skill способен лишь ответить, что грузить нечего.

// companionReadTools — реализация companion.ReadTools поверх общего реестра.
//
// Реестр собирается здесь, а не в компаньоне: рабочая папка живёт в слое
// приложения, и тянуть её в разговор значило бы связать компаньона с файловой
// системой навсегда — включая случаи, когда папки нет вовсе.
type companionReadTools struct {
	registry *workbenchtools.Registry
	grants   policy.Grants
}

func newCompanionReadTools(fs *workspace.FS, skills []domain.SkillRuntime) *companionReadTools {
	grants := policy.CompanionGrants()
	items := []workbenchtools.Tool{
		workbenchtools.ProjectMap{FS: fs},
		workbenchtools.SearchCode{FS: fs},
		workbenchtools.ListFiles{FS: fs},
		workbenchtools.ReadFile{FS: fs},
		workbenchtools.SearchText{FS: fs},
		workbenchtools.GitDiff{FS: fs},
		workbenchtools.GitBranches{FS: fs},
		workbenchtools.GitLog{FS: fs},
		workbenchtools.GitTags{FS: fs},
	}
	if len(skills) > 0 {
		items = append(items, workbenchtools.ReadSkill{Skills: skills})
		grants = grants.WithGroups(policy.SkillGroup)
	}
	return &companionReadTools{registry: workbenchtools.NewRegistry(items...), grants: grants}
}

func (t *companionReadTools) Definitions() []domain.ToolDefinition {
	return t.registry.Definitions(t.grants.ToolNames())
}

func (t *companionReadTools) Execute(ctx context.Context, name string, arguments json.RawMessage) domain.ToolResult {
	// Имя сверяется с grants до похода в реестр. Реестр собран строкой выше и
	// лишнего не содержит, но grants — единственное место, где записано, что
	// компаньону можно, и проверять положено именно по ним: иначе добавленный
	// однажды в реестр `run_command` разошёлся бы с этим правилом молча.
	if !t.grants.Allows(name) {
		return domain.ToolResult{OK: false, Error: &domain.ToolError{
			Code:    "tool_not_allowed",
			Message: "компаньону доступны только читающие инструменты",
			Hint:    "изменения выполняет агент по квесту с подтверждением",
		}}
	}
	tool, ok := t.registry.Get(name)
	if !ok {
		return domain.ToolResult{OK: false, Error: &domain.ToolError{
			Code:    "unknown_tool",
			Message: "инструмент " + name + " не найден",
		}}
	}
	return tool.Execute(ctx, arguments)
}
