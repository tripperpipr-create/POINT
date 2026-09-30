package domain

import "strings"

// distinctInstructions убирает дополнительные инструкции, если они дословно
// повторяют миссию: агент из наряда v2 рождается с SystemPrompt, равным
// миссии, и промпт платил за одни и те же слова дважды.
func distinctInstructions(mission, instructions string) string {
	if strings.TrimSpace(instructions) == strings.TrimSpace(mission) {
		return ""
	}
	return instructions
}
