package companion

import (
	"path/filepath"
	"strings"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/textutil"
)

// PreferLeanGather is true when IDE focus is enough and the user is not asking
// for guild-wide status, usage, or entity creation. Lean mode keeps latency low.
func PreferLeanGather(message string, focus ChatFocus) bool {
	focus = sanitizeChatFocus(focus)
	if focus.Empty() {
		return false
	}
	lower := strings.ToLower(strings.TrimSpace(message))
	if isUsageRequest(lower) || isSkillCreationRequest(lower) || isTeamCreationRequest(lower) ||
		isAgentCreationRequest(lower) || isFlowCreationRequest(lower) {
		return false
	}
	guildMarkers := []string{
		"статус гильдии", "состояние гильдии", "сводка гильдии", "обзор гильдии",
		"guild status", "project status", "статус проекта", "состояние проекта",
		"какие агенты", "какие квесты", "какие флоу", "какие flow", "roster", "ростер",
	}
	for _, marker := range guildMarkers {
		if strings.Contains(lower, marker) {
			return false
		}
	}
	if focus.File != "" || focus.Snippet != "" || focus.Failure != "" || focus.Run != "" || focus.Diagnostics > 0 || isHereRequest(lower) {
		return true
	}
	return false
}

func focusSearchQuery(query string, focus ChatFocus) string {
	parts := []string{strings.TrimSpace(focus.File)}
	if focus.Selection && strings.TrimSpace(focus.Snippet) != "" {
		tokens := textutil.Tokens(focus.Snippet)
		if len(tokens) > 8 {
			tokens = tokens[:8]
		}
		if len(tokens) > 0 {
			parts = append(parts, strings.Join(tokens, " "))
		}
	}
	parts = append(parts, strings.TrimSpace(query))
	return strings.TrimSpace(strings.Join(parts, " "))
}

func observationMatchesFocus(item domain.IDEObservation, focus ChatFocus) bool {
	file := filepath.ToSlash(strings.TrimSpace(focus.File))
	path := filepath.ToSlash(strings.TrimSpace(item.Path))
	if file == "" || path == "" {
		return false
	}
	fileLower, pathLower := strings.ToLower(file), strings.ToLower(path)
	return fileLower == pathLower || strings.HasSuffix(pathLower, "/"+fileLower) || strings.EqualFold(filepath.Base(path), filepath.Base(file))
}

func prioritizeIDEObservations(items []domain.IDEObservation, focus ChatFocus) []domain.IDEObservation {
	if strings.TrimSpace(focus.File) == "" || len(items) < 2 {
		return items
	}
	matched := make([]domain.IDEObservation, 0, len(items))
	rest := make([]domain.IDEObservation, 0, len(items))
	for _, item := range items {
		if observationMatchesFocus(item, focus) {
			matched = append(matched, item)
		} else {
			rest = append(rest, item)
		}
	}
	return append(matched, rest...)
}

func mergeCompanionQuestions(existing, extras []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, 4)
	for _, question := range append(append([]string{}, existing...), extras...) {
		question = strings.TrimSpace(question)
		key := strings.ToLower(question)
		if question == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, question)
		if len(out) >= 4 {
			break
		}
	}
	return out
}

func ideFollowUpQuestions(message string, focus ChatFocus) []string {
	if focus.Empty() {
		return nil
	}
	lower := strings.ToLower(message)
	out := make([]string, 0, 3)
	// IDE focus is context, not a standing to-do list. Offer a fix only when
	// Problems actually has something actionable.
	if label := focus.Label(); focus.Diagnostics > 0 && label != "" && !isPlanningRequest(lower) {
		out = append(out, "Исправь ошибки в "+label)
	}
	if focus.Failure != "" && !strings.Contains(lower, "сбой") && !strings.Contains(lower, strings.ToLower(focus.Failure)) {
		out = append(out, "Разбери сбой «"+trim(focus.Failure, 80)+"»")
	}
	if len(out) > 3 {
		return out[:3]
	}
	return out
}
