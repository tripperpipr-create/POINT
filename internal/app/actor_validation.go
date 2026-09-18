package app

import (
	"context"
	"fmt"
	"strings"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/policy"
)

// validateActorDefinition owns the structural contract shared by blueprints
// and workspace agents. Readiness is deliberately separate: drafts may be
// incomplete, but they may never contain unknown or malformed capabilities.
func (a *App) validateActorDefinition(ctx context.Context, profile domain.AgentProfile, skillIDs []string) error {
	knownTools := make(map[string]bool)
	for _, item := range domain.BuiltInToolCatalog() {
		knownTools[item.Name] = true
	}
	customTools, err := a.store.ListCustomTools(ctx)
	if err != nil {
		return err
	}
	for _, tool := range customTools {
		knownTools[tool.ID] = true
	}
	seenTools := make(map[string]bool)
	for _, tool := range profile.AllowedTools {
		tool = strings.TrimSpace(tool)
		if !knownTools[tool] {
			return fmt.Errorf("unknown agent tool %q", tool)
		}
		if seenTools[tool] {
			return fmt.Errorf("duplicate agent tool %q", tool)
		}
		seenTools[tool] = true
	}
	for key, value := range profile.ToolPolicies {
		key, value = strings.TrimSpace(key), strings.ToUpper(strings.TrimSpace(value))
		switch {
		case key == "network":
			if value != "" && value != "DENY" && value != "ALLOW" && value != "ALLOWLIST" {
				return fmt.Errorf("invalid network policy %q", value)
			}
		case strings.HasPrefix(strings.ToLower(key), "network:"):
			if value != "ALLOW" && value != "DENY" {
				return fmt.Errorf("invalid network destination policy %q", value)
			}
		case !knownTools[key]:
			return fmt.Errorf("tool policy references unknown tool %q", key)
		case value != "ALLOW" && value != "ASK" && value != "DENY":
			return fmt.Errorf("invalid policy %q for tool %q", value, key)
		}
	}
	if effort := strings.TrimSpace(profile.ReasoningEffort); effort != "" && effort != "none" && effort != "minimal" && effort != "low" && effort != "medium" && effort != "high" {
		return fmt.Errorf("unsupported reasoning effort %q", effort)
	}
	seenFallbacks := make(map[string]bool)
	for _, model := range profile.FallbackModels {
		model = strings.TrimSpace(model)
		if model == "" {
			return fmt.Errorf("fallback model must not be empty")
		}
		if seenFallbacks[model] || model == profile.Model {
			return fmt.Errorf("duplicate fallback model %q", model)
		}
		seenFallbacks[model] = true
	}
	// Навык проверяется на экипировке, а не при запуске.
	//
	// Раньше несовпадение всплывало на подготовке прогона: человек надевал
	// навык, всё выглядело исправным, и квест падал позже — на шаге, где
	// навыку понадобился инструмент, которого агенту не выдали. Отказать надо
	// там, где принимают решение.
	skillByID := map[string]domain.SkillDefinition{}
	if len(skillIDs) > 0 {
		skills, skillErr := a.store.ListSkills(ctx)
		if skillErr != nil {
			return skillErr
		}
		for _, skill := range skills {
			skillByID[skill.ID] = skill
		}
	}
	grants := policy.ProfileGrants(profile)
	seenSkills := make(map[string]bool)
	for _, id := range skillIDs {
		if seenSkills[id] {
			return fmt.Errorf("duplicate skill %q", id)
		}
		seenSkills[id] = true
		skill, known := skillByID[id]
		if !known {
			// Незнакомый идентификатор здесь не ошибка: навык может приехать
			// позже — переносом из другого проекта или продвижением выученного.
			// Проверять нечего, а запрет сломал бы эти пути.
			continue
		}
		for _, tool := range skill.RequiredTools {
			if !grants.Allows(tool) {
				return fmt.Errorf("skill %q requires tool %q that is not granted to this agent", skill.Name, tool)
			}
		}
	}
	if len(profile.SystemPrompt) > 64*1024 || len(profile.RoleDescription) > 4*1024 || len(profile.Name) > 200 {
		return fmt.Errorf("agent fields exceed their size limit")
	}
	if len(profile.Goals) > 32 || len(profile.Rules) > 64 || len(skillIDs) > 64 || len(profile.AllowedTools) > 64 {
		return fmt.Errorf("agent lists exceed their size limit")
	}
	return nil
}
