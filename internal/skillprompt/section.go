package skillprompt

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"local-agent-workbench/internal/domain"
)

// MaxInlinedRunes — потолок, после которого навык не вклеивают в промпт
// целиком, а оставляют строку с приглашением загрузить его read_skill.
const MaxInlinedRunes = 1500

// Section собирает секцию <equipped_skills> для системного сообщения.
//
// Живёт отдельно от движка агента, потому что надетые навыки есть не только у
// агента: помощник в боковой панели носит свои. Один сборщик на обоих — иначе
// две секции разъедутся формулировками, и модель будет по-разному понимать
// один и тот же навык в зависимости от того, кто спрашивает.
func Section(items []domain.SkillRuntime) string {
	if len(items) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("<equipped_skills>\n")
	builder.WriteString("These are mandatory local practices for this run. Follow an inlined skill immediately. If a skill says to load it with read_skill, copy the exact canonical id shown in square brackets into the id field. Never call read_skill with a translated, shortened, or invented name. Do not invent skill instructions, scripts, or references.\n")
	for _, skill := range items {
		fmt.Fprintf(&builder, "- %s [%s]", skill.Name, skill.ID)
		if len(skill.RequiredTools) > 0 {
			fmt.Fprintf(&builder, " (tools: %s)", strings.Join(skill.RequiredTools, ", "))
		}
		builder.WriteByte('\n')
		if skill.Description != "" {
			fmt.Fprintf(&builder, "  %s\n", skill.Description)
		}
		if utf8.RuneCountInString(skill.Instructions) > MaxInlinedRunes {
			builder.WriteString("  Load with read_skill before applying this practice.")
			if len(skill.References) > 0 || len(skill.Scripts) > 0 {
				fmt.Fprintf(&builder, " References: %d. Scripts: %d.", len(skill.References), len(skill.Scripts))
			}
			builder.WriteByte('\n')
			continue
		}
		if skill.Instructions != "" {
			builder.WriteString("  Instructions:\n")
			for _, line := range strings.Split(skill.Instructions, "\n") {
				fmt.Fprintf(&builder, "  %s\n", line)
			}
		}
		for _, ref := range skill.References {
			fmt.Fprintf(&builder, "  Reference: %s\n", ref)
		}
		for _, script := range skill.Scripts {
			fmt.Fprintf(&builder, "  Script: %s\n", script)
		}
		if len(skill.Configuration) > 0 {
			encoded, err := json.Marshal(skill.Configuration)
			if err == nil {
				fmt.Fprintf(&builder, "  Configuration: %s\n", encoded)
			}
		}
	}
	builder.WriteString("</equipped_skills>")
	return strings.TrimSpace(builder.String())
}
