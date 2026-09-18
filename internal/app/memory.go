package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
)

// SaveMemory enforces the scope and owner contract for portable and
// project-bound memories before storage. Memory is intentionally separate from
// Hub composition because its privacy boundary is a domain rule, not a view.
func (a *App) SaveMemory(memory domain.MemoryRecord) (domain.MemoryRecord, error) {
	now := time.Now().UTC()
	ws, err := a.requireWorkspace()
	if err != nil {
		return domain.MemoryRecord{}, err
	}
	if memory.Kind == domain.MemoryProfile {
		if memory.WorkspaceID != "" {
			return domain.MemoryRecord{}, errors.New("profile memory must not belong to a workspace")
		}
	} else {
		if memory.WorkspaceID == "" {
			memory.WorkspaceID = ws.ID
		}
		if memory.WorkspaceID != ws.ID {
			return domain.MemoryRecord{}, errors.New("memory belongs to another workspace")
		}
	}
	memory.Content = strings.TrimSpace(memory.Content)
	memory.Source = strings.TrimSpace(memory.Source)
	memory.OwnerID = strings.TrimSpace(memory.OwnerID)
	if memory.Content == "" {
		return domain.MemoryRecord{}, errors.New("memory content is required")
	}
	if len(memory.Content) > 64*1024 || len(memory.Source) > 4096 || len(memory.OwnerID) > 256 {
		return domain.MemoryRecord{}, errors.New("memory field exceeds its size limit")
	}
	switch memory.Kind {
	case domain.MemoryProject, domain.MemoryCompanion:
		memory.OwnerID = ""
	case domain.MemoryProfile:
		if memory.OwnerID == "" {
			return domain.MemoryRecord{}, errors.New("profile memory owner is required")
		}
		if _, err = a.store.GetBlueprint(context.Background(), memory.OwnerID); err != nil {
			return domain.MemoryRecord{}, fmt.Errorf("memory owner profile: %w", err)
		}
	case domain.MemoryAgent:
		if memory.OwnerID == "" {
			return domain.MemoryRecord{}, errors.New("agent memory owner is required")
		}
		if _, err = a.currentProjectAgent(memory.OwnerID); err != nil {
			return domain.MemoryRecord{}, fmt.Errorf("memory owner agent: %w", err)
		}
	case domain.MemoryQuest:
		if memory.OwnerID == "" {
			return domain.MemoryRecord{}, errors.New("quest memory owner is required")
		}
		quests, listErr := a.store.ListQuests(context.Background(), ws.ID)
		if listErr != nil {
			return domain.MemoryRecord{}, listErr
		}
		found := false
		for _, quest := range quests {
			if quest.ID == memory.OwnerID {
				found = true
				break
			}
		}
		if !found {
			return domain.MemoryRecord{}, errors.New("memory owner quest is not in the current workspace")
		}
	default:
		return domain.MemoryRecord{}, fmt.Errorf("unsupported memory kind %q", memory.Kind)
	}
	if memory.Confidence < 0 || memory.Confidence > 1 {
		return domain.MemoryRecord{}, errors.New("memory confidence must be between 0 and 1")
	}
	if memory.ID == "" {
		memory.ID = domain.NewID("memory")
		memory.CreatedAt = now
	} else {
		memories, listErr := a.store.ListMemories(context.Background(), ws.ID)
		if listErr != nil {
			return domain.MemoryRecord{}, listErr
		}
		found := false
		for _, existing := range memories {
			if existing.ID == memory.ID {
				memory.CreatedAt = existing.CreatedAt
				found = true
				break
			}
		}
		if !found {
			return domain.MemoryRecord{}, errors.New("memory is not in the current workspace")
		}
	}
	if memory.CreatedAt.IsZero() {
		memory.CreatedAt = now
	}
	memory.UpdatedAt = now
	if memory.Confidence == 0 {
		memory.Confidence = 0.5
	}
	if err := a.store.SaveMemory(context.Background(), memory); err != nil {
		return domain.MemoryRecord{}, err
	}
	return memory, nil
}

func (a *App) DeleteMemory(memoryID string) error {
	ws, err := a.requireWorkspace()
	if err != nil {
		return err
	}
	memoryID = strings.TrimSpace(memoryID)
	if memoryID == "" {
		return errors.New("memory id is required")
	}
	memories, err := a.store.ListMemories(context.Background(), ws.ID)
	if err != nil {
		return err
	}
	for _, memory := range memories {
		if memory.ID == memoryID {
			return a.store.DeleteMemory(context.Background(), memory.WorkspaceID, memoryID)
		}
	}
	return errors.New("memory is not in the current workspace or shared profile library")
}
