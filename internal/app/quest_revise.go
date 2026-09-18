package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
)

const QuestProposalRevise QuestProposalAction = "revise"

// ReviseActiveQuestBrief changes the goal of an already started structured
// quest only after every related run is paused. The new version must be
// approved; running configuration snapshots are not mutated in place.
func (a *App) ReviseActiveQuestBrief(ctx context.Context, questID string, brief domain.TaskBrief, expectedVersion, approveVersion int) (domain.Quest, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ws, err := a.requireWorkspace()
	if err != nil {
		return domain.Quest{}, err
	}
	quests, err := a.store.ListQuests(ctx, ws.ID)
	if err != nil {
		return domain.Quest{}, err
	}
	var quest domain.Quest
	found := false
	for _, item := range quests {
		if item.ID == questID {
			quest, found = item, true
			break
		}
	}
	if !found {
		return domain.Quest{}, errors.New("quest not found")
	}
	if quest.Brief == nil {
		return domain.Quest{}, errors.New("quest has no structured brief to revise")
	}
	if quest.Status != domain.QuestActive && quest.Status != "running" && quest.Status != "paused" {
		return domain.Quest{}, fmt.Errorf("quest status %s cannot revise mid-flight", quest.Status)
	}
	if expectedVersion != quest.Brief.Version {
		return domain.Quest{}, fmt.Errorf("версия задания изменилась: ожидалась %d", quest.Brief.Version)
	}
	if err = a.requireQuestRunsPaused(ctx, quest.ID); err != nil {
		return domain.Quest{}, err
	}
	updated := domain.NormalizeTaskBrief(brief)
	updated.Version = quest.Brief.Version
	if domain.TaskBriefDigest(updated) != domain.TaskBriefDigest(*quest.Brief) {
		updated.Version++
	} else {
		updated = *quest.Brief
	}
	if err = domain.ValidateTaskBrief(updated); err != nil {
		return domain.Quest{}, err
	}
	if approveVersion != updated.Version {
		return domain.Quest{}, errors.New("утвердите новую версию задания перед продолжением")
	}
	approved, err := domain.ApproveTaskBrief(updated)
	if err != nil {
		return domain.Quest{}, err
	}
	quest.Brief = &approved
	quest.Title = firstNonEmpty(strings.TrimSpace(quest.Title), approved.Goal)
	quest.Description = approved.Goal
	quest.Objectives = append([]string(nil), approved.Scope...)
	quest.Constraints = append([]string(nil), approved.OutOfScope...)
	quest.DefinitionOfDone = nil
	for _, c := range approved.Criteria {
		quest.DefinitionOfDone = append(quest.DefinitionOfDone, c.Text)
	}
	quest.BudgetTokens = approved.Budget.Tokens
	quest.UpdatedAt = time.Now().UTC()
	if err = a.store.SaveQuest(ctx, quest); err != nil {
		return domain.Quest{}, err
	}
	return quest, nil
}

func (a *App) requireQuestRunsPaused(ctx context.Context, questID string) error {
	executions, err := a.store.ListExecutions(ctx, a.currentWorldID(), 500)
	if err != nil {
		return err
	}
	for _, exec := range executions {
		if exec.QuestID != questID {
			continue
		}
		switch exec.Status {
		case domain.RunRunning, domain.RunWaiting, domain.RunPending:
			return fmt.Errorf("pause execution %s before revising the goal (status %s)", exec.ID, exec.Status)
		}
		if exec.RunID == "" {
			continue
		}
		run, runErr := a.store.GetRun(ctx, exec.RunID)
		if runErr != nil {
			continue
		}
		switch run.Status {
		case domain.RunRunning, domain.RunWaiting:
			return fmt.Errorf("pause run %s before revising the goal", run.ID)
		}
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
