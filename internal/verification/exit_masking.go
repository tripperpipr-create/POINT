package verification

import (
	"regexp"
	"strings"
)

// Команда критерия, которая проглатывает код выхода, всегда «проходит»:
// `npm run verify || true` и `npm test; exit 0` дают 0 при упавшей сборке, и
// итог говорит «проверено» там, где проверка провалилась (Q08, E1). Пайпы сюда
// не входят: в песочнице включён pipefail, и `… | tail` код сохраняет.
var exitMaskingPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\|\|\s*(?:true|:|exit\s+0)\s*(?:$|[;&|)])`),
	regexp.MustCompile(`;\s*(?:true|:|exit\s+0)\s*$`),
	regexp.MustCompile(`;\s*echo\b[^;&|]*$`),
	regexp.MustCompile(`(?:^|[;&|]\s*)set\s+\+e\b`),
}

// MasksExitCode сообщает, скрывает ли команда код выхода своей проверки, и
// называет найденный фрагмент — отказ должен объяснять, что именно исправить.
func MasksExitCode(command string) (string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(command))
	for _, pattern := range exitMaskingPatterns {
		if match := pattern.FindString(normalized); match != "" {
			return strings.TrimSpace(match), true
		}
	}
	return "", false
}
