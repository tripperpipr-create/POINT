package tools

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"local-agent-workbench/internal/domain"
)

// SearchSkills only exposes the operation's pinned catalogue, never mutable storage.
type SearchSkills struct{ Skills []domain.SkillRuntime }

func (t SearchSkills) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{Name: "search_skills", Description: "Find relevant available skills by task or topic. Load exact ids with read_skill; discovery grants no tools or permissions.", InputSchema: schema(`{"type":"object","properties":{"query":{"type":"string"},"limit":{"type":"integer","minimum":1,"maximum":20}},"required":["query"],"additionalProperties":false}`)}
}

func (t SearchSkills) Execute(_ context.Context, raw json.RawMessage) domain.ToolResult {
	var input struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if bad := Decode(raw, &input); bad != nil {
		return *bad
	}
	if strings.TrimSpace(input.Query) == "" {
		return Fail("invalid_input", "query is required")
	}
	if input.Limit <= 0 {
		input.Limit = 8
	}
	if input.Limit > 20 {
		input.Limit = 20
	}
	type match struct {
		skill domain.SkillRuntime
		score int
	}
	var matches []match
	seen := map[string]bool{}
	for _, skill := range t.Skills {
		if seen[skill.ID] {
			continue
		}
		seen[skill.ID] = true
		score := 0
		text := strings.ToLower(skill.ID + " " + skill.Name + " " + skill.Description)
		for _, term := range strings.Fields(strings.ToLower(input.Query)) {
			if strings.Contains(text, term) {
				score++
			}
		}
		if score > 0 {
			matches = append(matches, match{skill, score})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return matches[i].skill.ID < matches[j].skill.ID
	})
	items := []map[string]any{}
	for _, item := range matches {
		if len(items) >= input.Limit {
			break
		}
		description := []rune(item.skill.Description)
		if len(description) > 500 {
			description = description[:500]
		}
		items = append(items, map[string]any{"id": item.skill.ID, "name": item.skill.Name, "description": string(description), "requiredTools": item.skill.RequiredTools, "attribution": domain.SkillRuntimeAttribution(item.skill)})
	}
	return OK(map[string]any{"items": items})
}
