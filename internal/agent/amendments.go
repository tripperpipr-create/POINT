package agent

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"local-agent-workbench/internal/domain"
	workbenchtools "local-agent-workbench/internal/tools"
	"local-agent-workbench/internal/workspace"
)

func normalizeAmendmentPath(path string) string {
	path = strings.TrimSpace(path)
	path = filepath.ToSlash(path)
	return strings.TrimPrefix(path, "./")
}

func pathsForbiddenMatch(target, forbidden string) bool {
	target = normalizeAmendmentPath(target)
	forbidden = normalizeAmendmentPath(forbidden)
	if target == "" || forbidden == "" {
		return false
	}
	if target == forbidden {
		return true
	}
	return strings.HasPrefix(target, forbidden+"/") || strings.HasPrefix(forbidden, target+"/")
}

func toolCallTargetPath(name string, arguments json.RawMessage) string {
	switch name {
	case "propose_patch", "read_file":
	default:
		return ""
	}
	var payload struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(arguments, &payload); err != nil {
		return ""
	}
	return payload.Path
}

// These tools can inspect or mutate arbitrary workspace paths without a
// structured target path that the engine can filter safely. Once a user
// forbids any path, they are conservatively unavailable for that execution.
//
// Признак объявлен вместе с инструментом: перечисление здесь пришлось бы
// править при каждом добавлении, и забытая строка отдавала бы запрещённый путь
// молча.
func toolHasWorkspaceWideAccess(name string) bool {
	if item, ok := domain.ToolCatalogEntry(name); ok {
		return item.WorkspaceWide
	}
	// Пользовательский инструмент запускает заранее настроенную команду: какие
	// пути она тронет, из объявления не видно.
	return strings.HasPrefix(name, "customtool_")
}

func relativizeToolPath(fs *workspace.FS, path string) string {
	if fs == nil || strings.TrimSpace(path) == "" {
		return path
	}
	abs, err := fs.Resolve(path, true)
	if err != nil {
		return path
	}
	rel, err := filepath.Rel(fs.Root(), abs)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}

func forbiddenPathFailure(path string) domain.ToolResult {
	return workbenchtools.FailWithHint("file_forbidden", "user forbids tool access to path: "+path, "choose a different workspace-relative file that is not blocked for this run")
}

func applyContextAmendments(items *[]domain.RunContextItem, amendments []domain.ContextAmendment) {
	if len(amendments) == 0 {
		return
	}
	for _, amend := range amendments {
		switch amend.Action {
		case domain.ContextAmendAdd:
			if amend.Item == nil || strings.TrimSpace(amend.Item.ID) == "" {
				continue
			}
			duplicate := false
			for _, item := range *items {
				if item.ID == amend.Item.ID {
					duplicate = true
					break
				}
			}
			if !duplicate {
				item := *amend.Item
				item.Amendable = false
				item.Pending = false
				*items = append(*items, item)
			}
		case domain.ContextAmendPin, domain.ContextAmendUnpin:
			for index := range *items {
				if (*items)[index].ID != amend.ItemID {
					continue
				}
				(*items)[index].Pinned = amend.Action == domain.ContextAmendPin
			}
		case domain.ContextAmendRemove:
			filtered := (*items)[:0]
			for _, item := range *items {
				if item.ID == amend.ItemID {
					continue
				}
				filtered = append(filtered, item)
			}
			*items = filtered
		}
	}
}

func EstimateContextItemTokens(item domain.RunContextItem) int {
	bytes := len(item.Label) + len(item.Path) + len(item.Content) + len(item.Format) + len(item.MediaType) + 32
	if bytes == 0 {
		return 0
	}
	return (bytes + 3) / 4
}
