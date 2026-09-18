package orchestrator

import (
	"encoding/json"
	"strings"

	"local-agent-workbench/internal/domain"
)

// masterToolCallKey — семантический отпечаток вызова: имя + канонические аргументы.
// Повторы одного и того же чтения в одном ходе Мастера ничего нового не дают
// и только крутят пустой workspace по кругу.
func masterToolCallKey(name string, arguments json.RawMessage) string {
	normalized := append(json.RawMessage(nil), arguments...)
	var value any
	if json.Unmarshal(normalized, &value) == nil {
		if encoded, err := json.Marshal(value); err == nil {
			normalized = encoded
		}
	}
	return name + "\n" + string(normalized)
}

func masterDuplicateToolResult() domain.ToolResult {
	return domain.ToolResult{
		OK: false,
		Error: &domain.ToolError{
			Code:    "duplicate_tool_call",
			Message: "этот же вызов уже был в этом ходе; используйте прежний результат или ответьте человеку",
			Hint:    "не повторяйте list_files/project_map с теми же аргументами — задайте уточняющие вопросы",
		},
	}
}

// masterExplorationEmpty — успешный обзор, который показал пустоту. Модель
// склонна повторять его, пока не кончатся раунды; после первого такого ответа
// дальнейшие list_files/project_map в этом ходе лишние.
func masterExplorationEmpty(name string, result domain.ToolResult) bool {
	if !result.OK {
		return false
	}
	switch name {
	case "project_map", "list_files", "list_dir":
	default:
		return false
	}
	raw := strings.TrimSpace(string(result.Output))
	if raw == "" || raw == "[]" || raw == "null" {
		return true
	}
	var asList []any
	if json.Unmarshal(result.Output, &asList) == nil && len(asList) == 0 {
		return true
	}
	var asMap map[string]any
	if json.Unmarshal(result.Output, &asMap) != nil {
		return false
	}
	files, _ := asMap["filesByLanguage"].(map[string]any)
	dirs, _ := asMap["topDirectories"].([]any)
	symbols, _ := asMap["symbols"].([]any)
	if files == nil && dirs == nil && symbols == nil {
		// Не project_map-форма — не считаем пустым обзором.
		if name == "list_files" || name == "list_dir" {
			return false
		}
	}
	emptyFiles := len(files) == 0
	emptyDirs := len(dirs) == 0
	emptySymbols := len(symbols) == 0
	return emptyFiles && emptyDirs && emptySymbols
}

func masterEmptyWorkspaceHint() string {
	return "Рабочая область пуста или каталог пуст. Не повторяйте list_files/project_map — задайте человеку уточняющие вопросы с вариантами ответа."
}

func filterOutExplorationTools(definitions []domain.ToolDefinition) []domain.ToolDefinition {
	if len(definitions) == 0 {
		return definitions
	}
	out := make([]domain.ToolDefinition, 0, len(definitions))
	for _, item := range definitions {
		switch item.Name {
		case "project_map", "list_files", "list_dir":
			continue
		default:
			out = append(out, item)
		}
	}
	return out
}
