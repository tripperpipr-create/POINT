package environment

import (
	"strings"

	"local-agent-workbench/internal/sandbox"
)

// Среда для повтора проваленного этапа.
//
// 30.09.2026 приёмка cf-vue-apps упала под npm 12, который блокирует скрипты
// установки, а исполнители того же квеста работали под Node 20 с npm 10.
// Повторить этап в другой среде можно было только новой версией наряда. Список
// закрыт: человек или Мастер выбирают вариант по имени, образ и версии знает
// сервер, произвольный образ из текста модели сюда не попадает.

// RetryRuntimeChoice — вариант среды для повтора этапа.
type RetryRuntimeChoice struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Node  string `json:"node"`
	Image string `json:"image"`
}

var retryRuntimeChoices = []RetryRuntimeChoice{
	{ID: "node20", Label: "Node 20 (npm 10)", Node: "20", Image: Node20ManagedImage},
	{ID: "node22", Label: "Node 22 (npm 10)", Node: "22", Image: Node22ManagedImage},
}

// RetryRuntimeChoices — варианты среды, доступные для повтора.
func RetryRuntimeChoices() []RetryRuntimeChoice {
	return append([]RetryRuntimeChoice(nil), retryRuntimeChoices...)
}

// RetryRuntimeChoiceByID находит вариант по имени; неизвестное имя — отказ.
func RetryRuntimeChoiceByID(id string) (RetryRuntimeChoice, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, choice := range retryRuntimeChoices {
		if choice.ID == id {
			return choice, true
		}
	}
	return RetryRuntimeChoice{}, false
}

// RuntimeRequirementsWithRetryChoice накладывает выбранную среду на требования
// этапа: нужна node заданной версии, первым кандидатом — образ варианта.
// Расхождение версий node в источниках проекта выбор снимает — он и есть
// решение человека или Мастера о версии.
func RuntimeRequirementsWithRetryChoice(requirements sandbox.RuntimeRequirements, choiceID string) sandbox.RuntimeRequirements {
	choice, ok := RetryRuntimeChoiceByID(choiceID)
	if !ok {
		return requirements
	}
	result := requirements
	result.RequiredCommands = appendUnique(append([]string(nil), requirements.RequiredCommands...), "node", "npm")
	versions := map[string]string{}
	for tool, version := range requirements.ToolVersions {
		if tool != "node" && tool != "npm" {
			versions[tool] = version
		}
	}
	versions["node"] = choice.Node
	result.ToolVersions = versions
	result.CandidateImages = append([]string{choice.Image}, requirements.CandidateImages...)
	conflicts := []string{}
	for _, conflict := range requirements.VersionConflicts {
		if tool := conflictTool(conflict); tool != "node" && tool != "npm" {
			conflicts = append(conflicts, conflict)
		}
	}
	result.VersionConflicts = conflicts
	return result
}

func appendUnique(values []string, extra ...string) []string {
	seen := map[string]bool{}
	for _, value := range values {
		seen[value] = true
	}
	for _, value := range extra {
		if !seen[value] {
			seen[value] = true
			values = append(values, value)
		}
	}
	return values
}
