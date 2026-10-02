package changesets

import (
	"context"
	"fmt"

	"local-agent-workbench/internal/domain"
)

// RevertBlockers — пути, которые не дадут откатить применённые наборы целиком.
// Revert проверяет по одному набору, и откат нескольких мог остановиться на
// середине: часть файлов квеста уже вернулась, часть нет. Проверка до отката
// смотрит на последний набор, тронувший каждый путь: только его состояние
// должно совпадать с текущим файлом.
func (a Applier) RevertBlockers(ctx context.Context, workspacePath string, setIDs []string) ([]string, error) {
	last := map[string]domain.ChangeItem{}
	order := []string{}
	for _, id := range setIDs {
		set, err := a.Store.GetChangeSet(ctx, id)
		if err != nil {
			return nil, err
		}
		if set.Status != domain.ChangeSetApplied {
			continue
		}
		for _, item := range set.Items {
			if item.AppliedOperation == "kept" {
				continue
			}
			if _, seen := last[item.Path]; !seen {
				order = append(order, item.Path)
			}
			last[item.Path] = item
		}
	}
	var blockers []string
	for _, path := range order {
		item := last[path]
		target, err := safeTarget(workspacePath, path)
		if err != nil {
			blockers = append(blockers, path)
			continue
		}
		current, existed, _, err := readFileState(target)
		if err != nil {
			blockers = append(blockers, path)
			continue
		}
		switch item.AppliedOperation {
		case "write", "overwrite", "created":
			if !existed || item.AppliedHash == "" || hashBytes(current) != item.AppliedHash {
				blockers = append(blockers, fmt.Sprintf("%s (изменён после квеста)", path))
			}
		case "delete":
			if existed {
				blockers = append(blockers, fmt.Sprintf("%s (создан заново после квеста)", path))
			}
		default:
			blockers = append(blockers, fmt.Sprintf("%s (нет сведений о применении)", path))
		}
	}
	return blockers, nil
}
