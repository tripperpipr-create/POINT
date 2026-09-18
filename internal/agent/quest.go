package agent

import "strings"

// ComposeQuestTask builds the canonical quest brief sent to the model and
// completion gate. Empty optional sections are omitted. When only task is set,
// the raw task is returned unchanged for backward compatibility.
func ComposeQuestTask(task, goal string, criteria, constraints []string) string {
	task = strings.TrimSpace(task)
	goal = strings.TrimSpace(goal)
	criteria = cleanLines(criteria)
	constraints = cleanLines(constraints)
	if goal == "" && len(criteria) == 0 && len(constraints) == 0 {
		return task
	}
	parts := make([]string, 0, 4)
	if task != "" {
		parts = append(parts, "ЗАДАЧА:\n"+task)
	}
	if goal != "" {
		parts = append(parts, "ЦЕЛЬ:\n"+goal)
	}
	if len(criteria) > 0 {
		parts = append(parts, "КРИТЕРИИ ГОТОВНОСТИ:\n- "+strings.Join(criteria, "\n- "))
	}
	if len(constraints) > 0 {
		parts = append(parts, "ОГРАНИЧЕНИЯ:\n- "+strings.Join(constraints, "\n- "))
	}
	return strings.Join(parts, "\n\n")
}

func cleanLines(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}
