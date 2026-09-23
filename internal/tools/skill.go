package tools

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode"

	"local-agent-workbench/internal/domain"
)

type ReadSkill struct {
	Skills []domain.SkillRuntime
}

func (t ReadSkill) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:        "read_skill",
		Description: "Load the full equipped skill: instructions, references, scripts, and project configuration. Copy its exact canonical id from the equipped_skills list into id.",
		InputSchema: schema(`{"type":"object","properties":{"id":{"type":"string","description":"Exact canonical id from equipped_skills, for example skill-code-review"},"name":{"type":"string","description":"Legacy display-name lookup; prefer the canonical id"}},"additionalProperties":false}`),
	}
}

func (t ReadSkill) Execute(ctx context.Context, raw json.RawMessage) domain.ToolResult {
	started := time.Now()
	var input struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if bad := Decode(raw, &input); bad != nil {
		return *bad
	}
	query := strings.TrimSpace(input.ID)
	if query == "" {
		query = strings.TrimSpace(input.Name)
	}
	if query == "" {
		return logExecute(ctx, "read_skill", started, FailWithHint("invalid_input", "skill id or name is required", "pass id from the equipped_skills list, for example skill-code-review"))
	}
	skill, ok := lookupSkill(t.Skills, query)
	if !ok {
		names := make([]string, 0, len(t.Skills))
		for _, item := range t.Skills {
			names = append(names, item.Name+" ["+item.ID+"]")
		}
		hint := "this run has no equipped skills"
		if len(names) > 0 {
			hint = "use one of: " + strings.Join(names, ", ")
		}
		return logExecute(ctx, "read_skill", started, FailWithHint("skill_not_equipped", "skill is not equipped for this agent", hint), "query", query)
	}
	return logExecute(ctx, "read_skill", started, OK(map[string]any{
		"id": skill.ID, "name": skill.Name, "description": skill.Description,
		"instructions": skill.Instructions, "references": skill.References, "scripts": skill.Scripts,
		"requiredTools": skill.RequiredTools, "configuration": skill.Configuration,
	}), "skill_id", skill.ID, "name", skill.Name)
}

func lookupSkill(skills []domain.SkillRuntime, query string) (domain.SkillRuntime, bool) {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return domain.SkillRuntime{}, false
	}
	for _, skill := range skills {
		if skill.ID == trimmed {
			return skill, true
		}
	}
	folded := foldSkillKey(trimmed)
	for _, skill := range skills {
		if foldSkillKey(skill.ID) == folded || foldSkillKey(skill.Name) == folded {
			return skill, true
		}
	}
	return domain.SkillRuntime{}, false
}

func foldSkillKey(value string) string {
	var builder strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}
