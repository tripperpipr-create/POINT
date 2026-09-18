// Откат сделанного и учёт потраченного.
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
)

func (a *App) recordUsageFromEvent(event domain.Event) {
	if event.Type != domain.EventModelUsage {
		return
	}
	var payload struct {
		BudgetReservationID string `json:"budgetReservationId"`
		Usage               struct {
			InputTokens  int `json:"inputTokens"`
			OutputTokens int `json:"outputTokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(event.Data, &payload); err != nil {
		return
	}
	// Agent-engine requests are reconciled once, after the provider stream
	// closes. Some providers emit partial usage more than once, so recording
	// each event here would double-count both tokens and cost.
	if payload.BudgetReservationID != "" {
		return
	}
	run, runErr := a.store.GetRun(context.Background(), event.RunID)
	if runErr != nil {
		return
	}
	workspaceID := event.WorkspaceID
	if workspaceID == "" {
		workspaceID = run.WorkspaceID
	}
	if workspaceID == "" {
		return
	}
	total := int64(payload.Usage.InputTokens + payload.Usage.OutputTokens)
	latencyMs := int64(0)
	if events, listErr := a.store.ListByRun(context.Background(), event.RunID); listErr == nil {
		for index := len(events) - 1; index >= 0; index-- {
			candidate := events[index]
			if candidate.Type != domain.EventModelRequested || candidate.Step != event.Step || candidate.CreatedAt.After(event.CreatedAt) {
				continue
			}
			latencyMs = event.CreatedAt.Sub(candidate.CreatedAt).Milliseconds()
			break
		}
	}
	record := domain.UsageRecord{
		ID: domain.NewID("usage"), WorkspaceID: workspaceID,
		ExecutionID: event.ExecutionID, QuestID: event.QuestID,
		ProjectAgentID: run.ProfileID, Provider: run.Provider, Model: run.Model,
		InputTokens: int64(payload.Usage.InputTokens), OutputTokens: int64(payload.Usage.OutputTokens),
		TotalTokens: total, LatencyMs: latencyMs, Outcome: "usage_reported", CreatedAt: event.CreatedAt,
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now().UTC()
	}
	if err := a.store.InsertUsageRecord(context.Background(), record); err != nil {
		slog.Warn("usage record not stored", "execution_id", record.ExecutionID, "quest_id", record.QuestID, "total_tokens", record.TotalTokens, "error", err)
	}
}

func (a *App) RevertExecution(executionID string) (map[string]any, error) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return nil, err
	}
	executions, err := a.store.ListExecutions(context.Background(), ws.ID, 500)
	if err != nil {
		return nil, err
	}
	var exec *domain.ExecutionInstance
	for index := range executions {
		if executions[index].ID == executionID {
			exec = &executions[index]
			break
		}
	}
	if exec == nil {
		return nil, fmt.Errorf("execution %s not found", executionID)
	}
	sets, err := a.store.ListChangeSets(context.Background(), ws.ID)
	if err != nil {
		return nil, err
	}
	reverted := 0
	hasChangeSet := false
	for _, set := range sets {
		if set.ExecutionID != exec.ID {
			continue
		}
		hasChangeSet = true
		if set.Status == domain.ChangeSetApplied {
			result, revertErr := a.RevertChangeSet(set.ID)
			if revertErr != nil {
				return nil, revertErr
			}
			reverted += len(result.Applied)
		} else if set.Status == domain.ChangeSetPending || set.Status == domain.ChangeSetApproved || set.Status == domain.ChangeSetConflict {
			if _, rejectErr := a.RejectChangeSet(set.ID); rejectErr != nil {
				return nil, rejectErr
			}
		}
	}
	// Compatibility fallback for pre-Hub executions that wrote directly to the
	// live workspace and therefore have no ChangeSet transaction.
	if !hasChangeSet && exec.RunID != "" {
		patches, patchErr := a.store.PatchesByRun(context.Background(), exec.RunID)
		if patchErr == nil {
			for _, patch := range patches {
				if patch.Status != "applied" {
					continue
				}
				if _, err = a.RevertPatch(patch.ID); err == nil {
					reverted++
				}
			}
		}
	}
	return map[string]any{"executionId": exec.ID, "revertedFiles": reverted, "revertedPatches": reverted}, nil
}

func (a *App) RevertFlowNode(flowRunID, nodeID string) (map[string]any, error) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return nil, err
	}
	executions, err := a.store.ListExecutions(context.Background(), ws.ID, 500)
	if err != nil {
		return nil, err
	}
	total := 0
	var executionIDs []string
	for _, exec := range executions {
		if exec.FlowRunID != flowRunID || exec.FlowNodeID != nodeID {
			continue
		}
		result, revertErr := a.RevertExecution(exec.ID)
		if revertErr != nil {
			continue
		}
		executionIDs = append(executionIDs, exec.ID)
		if count, ok := result["revertedPatches"].(int); ok {
			total += count
		}
	}
	return map[string]any{
		"flowRunId": flowRunID, "nodeId": nodeID, "executions": executionIDs, "revertedPatches": total,
	}, nil
}

func (a *App) RevertQuest(questID string) (map[string]any, error) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return nil, err
	}
	executions, err := a.store.ListExecutions(context.Background(), ws.ID, 500)
	if err != nil {
		return nil, err
	}
	total := 0
	var executionIDs []string
	for _, exec := range executions {
		if exec.QuestID != questID {
			continue
		}
		result, revertErr := a.RevertExecution(exec.ID)
		if revertErr != nil {
			continue
		}
		executionIDs = append(executionIDs, exec.ID)
		if count, ok := result["revertedPatches"].(int); ok {
			total += count
		}
	}
	quests, err := a.store.ListQuests(context.Background(), ws.ID)
	if err == nil {
		for _, quest := range quests {
			if quest.ID != questID {
				continue
			}
			quest.Status = domain.QuestCancelled
			now := time.Now().UTC()
			quest.FinishedAt = &now
			quest.UpdatedAt = now
			if err := a.store.SaveQuest(context.Background(), quest); err != nil {
				slog.Warn("quest cancellation not persisted", "quest_id", quest.ID, "error", err)
			}
		}
	}
	return map[string]any{"questId": questID, "executions": executionIDs, "revertedPatches": total}, nil
}

// questDescription — что станет описанием квеста и уйдёт в контекст агента.
//
// Задача словами человека, если она есть. Предложения, созданные до появления
// поля task, её не несут — для них остаётся прежнее поведение, иначе описание
// стало бы пустым у всего, что уже лежит в очереди решений.
func questDescription(proposal domain.QuestProposal) string {
	if task := strings.TrimSpace(proposal.Task); task != "" {
		return task
	}
	return proposal.Rationale
}
