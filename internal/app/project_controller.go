package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
)

const (
	controllerPlanning            = "planning"
	controllerWaitingPrerequisite = "waiting_prerequisite"
	controllerExecutingWave       = "executing_wave"
	controllerWaitingMerge        = "waiting_merge"
	controllerVerifying           = "verifying"
	controllerReplanning          = "replanning"
	controllerNeedsUser           = "needs_user"
	controllerCompleted           = "completed"
)

func initializeQuestController(quest *domain.Quest) {
	if quest == nil || quest.Brief == nil || quest.Brief.Mode != domain.TaskModeProject {
		return
	}
	quest.Kind = "project"
	quest.ControllerState = controllerExecutingWave
	if quest.Controller == nil {
		quest.Controller = map[string]any{}
	}
	quest.Controller["schemaVersion"] = 1
	quest.Controller["wave"] = 1
	quest.Controller["maxReplans"] = quest.Brief.Budget.MaxReplans
	quest.Controller["updatedAt"] = time.Now().UTC().Format(time.RFC3339Nano)
}

func (a *App) questRequiresIsolatedWorkspace(ctx context.Context, workspaceID, questID string) bool {
	if strings.TrimSpace(questID) == "" {
		return false
	}
	quests, err := a.store.ListQuests(ctx, workspaceID)
	if err != nil {
		return true
	}
	byID := map[string]domain.Quest{}
	for _, quest := range quests {
		byID[quest.ID] = quest
	}
	seen := map[string]bool{}
	for questID != "" {
		if seen[questID] {
			return true
		}
		seen[questID] = true
		quest, ok := byID[questID]
		if !ok {
			return true
		}
		if quest.Brief != nil && quest.Brief.Mode == domain.TaskModeProject {
			return true
		}
		questID = quest.ParentID
	}
	return false
}

// ReconcileProjectController is idempotent and can be called after restart.
// It never starts an action; it only derives whether prerequisites permit the
// current wave to continue.
func (a *App) ReconcileProjectController(ctx context.Context, questID string) (domain.Quest, error) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return domain.Quest{}, err
	}
	quests, err := a.store.ListQuests(ctx, ws.ID)
	if err != nil {
		return domain.Quest{}, err
	}
	var target *domain.Quest
	status := map[string]domain.QuestStatus{}
	for index := range quests {
		status[quests[index].ID] = quests[index].Status
		if quests[index].ID == questID {
			copy := quests[index]
			target = &copy
		}
	}
	if target == nil {
		return domain.Quest{}, errors.New("quest not found")
	}
	if target.Kind != "project" {
		return *target, nil
	}
	for _, prerequisite := range target.PrerequisiteIDs {
		if status[prerequisite] != domain.QuestCompleted {
			target.ControllerState = controllerWaitingPrerequisite
			target.Status = domain.QuestPaused
			target.UpdatedAt = time.Now().UTC()
			_ = a.store.SaveQuest(ctx, *target)
			return *target, nil
		}
	}
	if target.ControllerState == controllerWaitingPrerequisite {
		target.ControllerState = controllerReplanning
		target.Status = domain.QuestActive
		target.UpdatedAt = time.Now().UTC()
		if err = a.store.SaveQuest(ctx, *target); err != nil {
			return domain.Quest{}, err
		}
	}
	if derived := a.deriveProjectControllerState(ctx, ws.ID, *target); derived != "" && derived != target.ControllerState {
		target.ControllerState = derived
		if derived == controllerNeedsUser {
			target.Status = domain.QuestPaused
		}
		target.UpdatedAt = time.Now().UTC()
		_ = a.store.SaveQuest(ctx, *target)
	}
	return *target, nil
}

func (a *App) deriveProjectControllerState(ctx context.Context, workspaceID string, quest domain.Quest) string {
	if quest.ControllerState == controllerCompleted || quest.ControllerState == controllerWaitingPrerequisite {
		return quest.ControllerState
	}
	if quest.Controller != nil {
		if _, blocked := quest.Controller["blockerEventId"]; blocked && quest.ControllerState == controllerReplanning {
			return controllerReplanning
		}
	}
	runs, err := a.store.ListFlowRuns(ctx, workspaceID, 100)
	if err != nil {
		return ""
	}
	for _, run := range runs {
		if run.QuestID != quest.ID {
			continue
		}
		for _, state := range run.NodeStates {
			wait, _ := state.Output["waitReason"].(string)
			if wait == "sandbox_merge_conflict" {
				return controllerNeedsUser
			}
			lineage, _ := state.Output["sandboxLineage"].(string)
			if lineage == "merged_parallel_join" && state.Status != "completed" && state.Status != "failed" {
				return controllerWaitingMerge
			}
		}
	}
	return ""
}

func (a *App) setProjectControllerState(ctx context.Context, questID, state string) {
	if strings.TrimSpace(questID) == "" {
		return
	}
	ws, err := a.requireWorkspace()
	if err != nil {
		return
	}
	quests, err := a.store.ListQuests(ctx, ws.ID)
	if err != nil {
		return
	}
	byID := map[string]domain.Quest{}
	for _, quest := range quests {
		byID[quest.ID] = quest
	}
	quest, ok := byID[questID]
	for ok && quest.Kind != "project" && quest.ParentID != "" {
		quest, ok = byID[quest.ParentID]
	}
	if !ok || quest.Kind != "project" {
		return
	}
	if quest.ControllerState == controllerCompleted {
		return
	}
	quest.ControllerState = state
	if state == controllerNeedsUser {
		quest.Status = domain.QuestPaused
	}
	quest.UpdatedAt = time.Now().UTC()
	_ = a.store.SaveQuest(ctx, quest)
}

func (a *App) projectControllerFinished(ctx context.Context, quest *domain.Quest, flowSucceeded bool) bool {
	if quest == nil || quest.Kind != "project" {
		return flowSucceeded
	}
	quest.ControllerState = controllerVerifying
	verified := false
	if flowSucceeded {
		if outcome, err := a.QuestOutcome(ctx, quest.ID); err == nil {
			verified = outcome.Verified
		}
	}
	if verified {
		quest.ControllerState = controllerCompleted
		quest.Status = domain.QuestCompleted
		return true
	}
	if flowSucceeded {
		quest.ControllerState = controllerNeedsUser
		if taskBriefHasManualCriteria(quest.Brief) {
			quest.Status = domain.QuestNeedsReview
		} else {
			quest.Status = domain.QuestBlocked
		}
	} else {
		quest.ControllerState = controllerReplanning
		quest.Status = domain.QuestPaused
	}
	quest.FinishedAt = nil
	return false
}
