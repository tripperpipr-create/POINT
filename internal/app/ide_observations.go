package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"local-agent-workbench/internal/companion"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/security"
)

type IDEObservationBatch struct {
	Kind      string                  `json:"kind"`
	Replace   bool                    `json:"replace,omitempty"`
	FocusPath string                  `json:"focusPath,omitempty"`
	Items     []domain.IDEObservation `json:"items"`
}

func (a *App) RecordIDEObservations(batch IDEObservationBatch) ([]domain.IDEObservation, error) {
	workspace, err := a.requireWorkspace()
	if err != nil {
		return nil, err
	}
	batch.Kind = strings.ToLower(strings.TrimSpace(batch.Kind))
	switch batch.Kind {
	case "diagnostic", "terminal", "task", "debug", "run", "scm":
	default:
		return nil, fmt.Errorf("unsupported IDE observation kind %q", batch.Kind)
	}
	if len(batch.Items) > 200 {
		return nil, errors.New("IDE observation batch exceeds 200 items")
	}
	now := time.Now().UTC()
	focusPath, err := normalizeObservationPath(workspace.Path, batch.FocusPath)
	if err != nil {
		return nil, err
	}
	existingByHash := map[string]domain.IDEObservation{}
	if !batch.Replace {
		prior, listErr := a.store.ListIDEObservations(context.Background(), workspace.ID, 100)
		if listErr == nil {
			for _, item := range prior {
				if item.Kind != batch.Kind {
					continue
				}
				hash := item.NoveltyHash
				if hash == "" {
					hash = companion.ObservationNoveltyHash(item)
				}
				existingByHash[hash] = item
			}
		}
	} else {
		prior, listErr := a.store.ListIDEObservations(context.Background(), workspace.ID, 100)
		if listErr == nil {
			for _, item := range prior {
				if item.Kind != batch.Kind {
					continue
				}
				hash := item.NoveltyHash
				if hash == "" {
					hash = companion.ObservationNoveltyHash(item)
				}
				existingByHash[hash] = item
			}
		}
	}
	items := make([]domain.IDEObservation, 0, len(batch.Items))
	for index, incoming := range batch.Items {
		item := domain.IDEObservation{
			ID: domain.NewID("ideobs"), WorkspaceID: workspace.ID, Kind: batch.Kind,
			Source: strings.TrimSpace(incoming.Source), Level: strings.ToLower(strings.TrimSpace(incoming.Level)),
			Summary: strings.TrimSpace(incoming.Summary), Detail: strings.TrimSpace(incoming.Detail),
			Path: strings.TrimSpace(incoming.Path), Line: incoming.Line,
			Command: strings.TrimSpace(incoming.Command), ExitCode: incoming.ExitCode,
			ObservedAt: now.Add(time.Duration(index) * time.Nanosecond),
			FocusPath:  focusPath,
		}
		if item.Level == "" {
			item.Level = "info"
		}
		if item.Level != "info" && item.Level != "warning" && item.Level != "error" {
			return nil, fmt.Errorf("unsupported IDE observation level %q", item.Level)
		}
		if item.Summary == "" {
			return nil, errors.New("IDE observation summary is required")
		}
		if len([]rune(item.Source)) > 200 || len([]rune(item.Summary)) > 500 || len([]rune(item.Detail)) > 16*1024 || len([]rune(item.Command)) > 4096 {
			return nil, errors.New("IDE observation exceeds a bounded text limit")
		}
		if item.Line < 0 || item.Line > 10_000_000 {
			return nil, errors.New("IDE observation line is invalid")
		}
		item.Path, err = normalizeObservationPath(workspace.Path, item.Path)
		if err != nil {
			return nil, err
		}
		item.Source = security.Redact(item.Source)
		item.Summary = security.Redact(item.Summary)
		item.Detail = security.Redact(item.Detail)
		item.Command = security.Redact(item.Command)
		item.NoveltyHash = companion.ObservationNoveltyHash(item)
		if prev, ok := existingByHash[item.NoveltyHash]; ok {
			item.FirstSeen = prev.FirstSeen
			if item.FirstSeen.IsZero() {
				item.FirstSeen = prev.ObservedAt
			}
			item.Count = prev.Count + 1
			if item.Count < 1 {
				item.Count = 1
			}
		} else {
			item.FirstSeen = item.ObservedAt
			item.Count = 1
		}
		item.LastSeen = item.ObservedAt
		items = append(items, item)
	}
	if err = a.store.SaveIDEObservationBatch(context.Background(), workspace.ID, batch.Kind, batch.Replace, items); err != nil {
		slog.Error("ide observations save failed", "workspace_id", workspace.ID, "kind", batch.Kind, "error", err)
		return nil, err
	}
	levels := map[string]int{}
	for _, item := range items {
		levels[item.Level]++
	}
	slog.Debug("ide observations recorded",
		"workspace_id", workspace.ID,
		"kind", batch.Kind,
		"replace", batch.Replace,
		"count", len(items),
		"levels", levels,
		"focus_path", focusPath,
	)
	return items, nil
}

func normalizeObservationPath(workspaceRoot, value string) (string, error) {
	if value == "" {
		return "", nil
	}
	clean := filepath.Clean(value)
	if filepath.IsAbs(clean) {
		relative, err := filepath.Rel(workspaceRoot, clean)
		if err != nil {
			return "", errors.New("IDE observation path is outside the current workspace")
		}
		clean = relative
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || filepath.IsAbs(clean) {
		return "", errors.New("IDE observation path is outside the current workspace")
	}
	return filepath.ToSlash(clean), nil
}
