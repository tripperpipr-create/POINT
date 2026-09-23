// Package masterskills owns methodology only. Authority and output contracts
// remain in the orchestrator and application, never in a learned revision.
package masterskills

import (
	"embed"
	"fmt"
	"strings"

	"local-agent-workbench/internal/domain"
)

//go:embed builtin/*.md
var resources embed.FS

const (
	Context     = "master-context"
	Intake      = "master-intake"
	Criteria    = "master-criteria"
	Planning    = "master-planning"
	Flow        = "master-flow"
	Recovery    = "master-recovery"
	Explanation = "master-explanation"
)

func Builtins() []domain.SkillDefinition {
	items := []struct{ id, name, description string }{
		{Context, "Исследование контекста", "Поиск фактов и выбор источников"},
		{Intake, "Постановка задания", "Существенные уточнения и согласованные границы"},
		{Criteria, "Критерии результата", "Проверяемые условия готовности"},
		{Planning, "Планирование", "Milestones, зависимости и бюджет"},
		{Flow, "Составление Flow", "Стадии, параллелизм и интеграция"},
		{Recovery, "Разбор сбоев и перепланирование", "Причина остановки и ограниченная коррекция"},
		{Explanation, "Объяснение результата", "Статусы, доказательства и ограничения"},
	}
	result := make([]domain.SkillDefinition, 0, len(items))
	for _, item := range items {
		body, err := resources.ReadFile("builtin/" + item.id + ".md")
		if err != nil {
			panic(err)
		}
		result = append(result, domain.SkillDefinition{ID: item.id, Name: item.name, Description: item.description, Instructions: strings.TrimSpace(string(body)), Configuration: map[string]any{"revision": 1}})
	}
	return result
}

func Runtime(s domain.SkillDefinition) domain.SkillRuntime {
	return domain.SkillRuntime{ID: s.ID, Name: s.Name, Description: s.Description, Instructions: s.Instructions, Configuration: map[string]any{"revision": s.Configuration["revision"]}}
}

// Required routes by the existing workflow, not by another model request or
// keywords in untrusted user/project text. Extra skills are read on demand.
func Required(phase string) []string {
	switch phase {
	case "intake":
		return []string{Intake, Criteria}
	case "planning":
		return []string{Planning, Flow}
	case "recovery":
		return []string{Recovery}
	case "context":
		return []string{Context}
	default:
		return []string{Explanation}
	}
}

func Prompt(skills []domain.SkillRuntime) string {
	var b strings.Builder
	for _, s := range skills {
		fmt.Fprintf(&b, "\n<master_skill id=%q revision=%q>\n%s\n</master_skill>\n", s.ID, fmt.Sprint(s.Configuration["revision"]), s.Instructions)
	}
	return b.String()
}

func Catalog(skills []domain.SkillRuntime) string {
	var b strings.Builder
	b.WriteString("\nДополнительные методики доступны через read_skill(id):\n")
	for _, s := range skills {
		fmt.Fprintf(&b, "%s — %s\n", s.ID, s.Description)
	}
	return b.String()
}
