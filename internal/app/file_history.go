package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
)

// История по файлу.
//
// Хроника прогонов отвечает на вопрос «что делал прогон #47». Человек задаёт
// другой: «что случилось с engine.go». Один и тот же факт, повёрнутый вокруг
// файла, а не вокруг запуска — и без этого среза приходилось перебирать
// прогоны вручную, чтобы понять судьбу одного файла.

type FileHistoryEntry struct {
	ID          string    `json:"id"`
	Source      string    `json:"source"` // patch | change-set
	Operation   string    `json:"operation"`
	Status      string    `json:"status"`
	RunID       string    `json:"runId,omitempty"`
	ChangeSetID string    `json:"changeSetId,omitempty"`
	ExecutionID string    `json:"executionId,omitempty"`
	Title       string    `json:"title,omitempty"`
	Tool        string    `json:"tool,omitempty"`
	Diff        string    `json:"diff,omitempty"`
	Revertible  bool      `json:"revertible"`
	RevertPath  string    `json:"revertPath,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
}

type FileHistory struct {
	Path        string             `json:"path"`
	Entries     []FileHistoryEntry `json:"entries"`
	Total       int                `json:"total"`
	Applied     int                `json:"applied"`
	Reverted    int                `json:"reverted"`
	Pending     int                `json:"pending"`
	GeneratedAt time.Time          `json:"generatedAt"`
}

// samePath сравнивает пути в одной канонической форме. Хранилище пишет их с
// прямыми слэшами, но запрос может прийти в любом виде, и разница в разделителе
// молча дала бы пустую историю вместо ответа.
func samePath(left, right string) bool {
	normalize := func(value string) string {
		value = strings.TrimSpace(value)
		value = filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
		return strings.TrimPrefix(value, "./")
	}
	return normalize(left) == normalize(right)
}

// FileHistory собирает судьбу одного файла из неизменяемых записей: принятых
// патчей и наборов изменений. Порядок обратный хронологическому — историю
// читают с конца, от последнего изменения к первому.
func (a *App) FileHistory(ctx context.Context, path string) (FileHistory, error) {
	a.mu.RLock()
	currentFS := a.currentFS
	a.mu.RUnlock()
	if currentFS == nil {
		return FileHistory{}, errors.New("workspace is not open")
	}
	if strings.TrimSpace(path) == "" {
		return FileHistory{}, errors.New("path is required")
	}
	// Путь проверяется границей рабочей папки, а не доверием к клиенту:
	// `..`, абсолютный путь, исключённый каталог и побег по симлинку
	// отсекаются здесь. Файла может уже не быть — агент мог его удалить,
	// и история такого файла как раз самая интересная.
	if _, err := currentFS.Resolve(path, true); err != nil {
		return FileHistory{}, err
	}

	workspaceID := a.currentWorldID()
	result := FileHistory{
		Path:        filepath.ToSlash(filepath.Clean(filepath.FromSlash(strings.TrimSpace(path)))),
		Entries:     []FileHistoryEntry{},
		GeneratedAt: time.Now().UTC(),
	}

	patches, err := a.store.ListPatchesForWorkspace(ctx, workspaceID, 500)
	if err != nil {
		return result, err
	}
	for _, patch := range patches {
		if !samePath(patch.Path, path) {
			continue
		}
		operation := "modify"
		if !patch.OriginalExisted {
			operation = "create"
		} else if strings.TrimSpace(patch.Proposed) == "" && strings.TrimSpace(patch.Original) != "" {
			operation = "delete"
		}
		entry := FileHistoryEntry{
			ID:         patch.ID,
			Source:     "patch",
			Operation:  operation,
			Status:     patch.Status,
			RunID:      patch.RunID,
			Tool:       patch.SourceTool,
			Diff:       patch.Diff,
			CreatedAt:  patch.CreatedAt,
			Revertible: patch.Status == "applied",
		}
		if entry.Revertible {
			entry.RevertPath = fmt.Sprintf("/api/patches/%s/revert", patch.ID)
		}
		result.Entries = append(result.Entries, entry)
	}

	sets, err := a.store.ListChangeSets(ctx, workspaceID)
	if err != nil {
		return result, err
	}
	for _, set := range sets {
		for _, item := range set.Items {
			if !samePath(item.Path, path) {
				continue
			}
			// Элемент набора, уже опубликованный отдельным патчем, не удваиваем:
			// иначе одна правка выглядела бы как две.
			if item.PatchID != "" {
				continue
			}
			entry := FileHistoryEntry{
				ID:          set.ID + "/" + item.ID,
				Source:      "change-set",
				Operation:   item.Kind,
				Status:      string(set.Status),
				ChangeSetID: set.ID,
				ExecutionID: set.ExecutionID,
				Title:       set.Title,
				Diff:        item.Diff,
				CreatedAt:   set.CreatedAt,
				Revertible:  set.Status == domain.ChangeSetApplied,
			}
			if entry.Revertible {
				entry.RevertPath = fmt.Sprintf("/api/change-sets/%s/revert", set.ID)
			}
			result.Entries = append(result.Entries, entry)
		}
	}

	// Новейшее сверху. При совпадении времени порядок фиксируем по ID, иначе
	// выдача плясала бы между запросами.
	sort.SliceStable(result.Entries, func(i, j int) bool {
		if !result.Entries[i].CreatedAt.Equal(result.Entries[j].CreatedAt) {
			return result.Entries[i].CreatedAt.After(result.Entries[j].CreatedAt)
		}
		return result.Entries[i].ID < result.Entries[j].ID
	})

	result.Total = len(result.Entries)
	for _, entry := range result.Entries {
		switch {
		case entry.Status == "applied":
			result.Applied++
		case entry.Status == "reverted":
			result.Reverted++
		case entry.Status == "pending" || entry.Status == "approved":
			result.Pending++
		}
	}
	return result, nil
}
