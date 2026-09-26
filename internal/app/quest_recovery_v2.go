package app

import (
	"context"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/observability"
)

// terminalFailedWorkOrderFlowV2 recognizes a Flow with no node left to run.
// A stale "running" status must not make Resume claim a new attempt began.
func terminalFailedWorkOrderFlowV2(run domain.FlowRun) (string, bool) {
	if len(run.NodeStates) == 0 {
		return "", false
	}
	failure := ""
	hasFailure := false
	for _, state := range run.NodeStates {
		switch state.Status {
		case "completed", "skipped", "failed", "cancelled":
		default:
			return "", false
		}
		if state.Status == "failed" {
			hasFailure = true
			failure = strings.TrimSpace(state.Error)
			if fromOutput, _ := state.Output["error"].(string); strings.TrimSpace(fromOutput) != "" {
				failure = strings.TrimSpace(fromOutput)
			}
		}
	}
	if !hasFailure {
		return "", false
	}
	if failure == "" {
		failure = "этап Flow завершился ошибкой"
	}
	return failure, true
}

// Recover a historic no-op Resume before generic restart recovery pauses it.
func (a *App) reconcileNoopWorkOrderResumesV2(ctx context.Context) {
	quests, err := a.store.ListInterruptedWorkOrderQuestsV2(ctx)
	if err != nil {
		return
	}
	for _, candidate := range quests {
		if candidate.Status != domain.QuestPreflight {
			continue
		}
		quest, questErr := a.workOrderQuestV2(ctx, candidate.WorkspaceID, candidate.QuestID)
		if questErr != nil || quest.FlowRunID == "" {
			continue
		}
		run, runErr := a.store.GetFlowRun(ctx, quest.FlowRunID)
		if runErr != nil {
			continue
		}
		if workOrderFlowFinishedV2(run) {
			a.reconcileFinishedFlowResumeV2(ctx, quest, run)
			continue
		}
		failure, terminal := terminalFailedWorkOrderFlowV2(run)
		if !terminal {
			continue
		}
		now := time.Now().UTC()
		run.Status, run.Error, run.FinishedAt = domain.RunFailed, failure, &now
		if saveErr := a.store.SaveFlowRun(ctx, run); saveErr != nil {
			continue
		}
		_, _ = a.setWorkOrderQuestStatusV2(ctx, quest, domain.QuestBlocked, "Этап завершился ошибкой; изменения не доставлены: "+failure)
	}
}

// questRecoveryMessageV2 is what the user reads in the launch card after the
// core, Docker or Windows itself went away mid-quest.
const questRecoveryMessageV2 = "Ядро перезапустилось; квест остановлен на последнем checkpoint. Продолжить можно из карточки."

// pauseInterruptedWorkOrderQuestsV2 turns quests abandoned by a stopped core
// into an honest pause. A tool call whose outcome nobody observed is never
// retried here: continuation is an explicit decision made from the card, so a
// half-finished dangerous action cannot be repeated by a restart.
func (a *App) pauseInterruptedWorkOrderQuestsV2(ctx context.Context) {
	interrupted, err := a.store.ListInterruptedWorkOrderQuestsV2(ctx)
	if err != nil {
		observability.From(ctx).Error("interrupted quest scan failed", "error", err)
		return
	}
	for _, item := range interrupted {
		quest, questErr := a.workOrderQuestV2(ctx, item.WorkspaceID, item.QuestID)
		if questErr != nil {
			observability.From(ctx).Error("interrupted quest load failed", "quest_id", item.QuestID, "error", questErr)
			continue
		}
		interruptedStatus := quest.Status
		if _, saveErr := a.setWorkOrderQuestStatusV2(ctx, quest, domain.QuestPaused, questRecoveryMessageV2); saveErr != nil {
			observability.From(ctx).Error("interrupted quest pause failed", "quest_id", item.QuestID, "error", saveErr)
			continue
		}
		if recordErr := a.store.RecordWorkOrderQuestPauseV2(ctx, quest.ID, interruptedStatus, questRecoveryMessageV2); recordErr != nil {
			observability.From(ctx).Error("interrupted quest pause not recorded", "quest_id", item.QuestID, "error", recordErr)
		}
	}
}

// reconcileFinishedFlowResumeV2 repairs what the old "Продолжить" left after a
// verdict: the quest stuck in `preflight` and its finished Flow rewritten to
// `running`. The Flow gets its end back, and a quest with a verdict returns to
// it; one without a verdict is left for the pause that follows, from which
// continuing runs the check that never happened.
func (a *App) reconcileFinishedFlowResumeV2(ctx context.Context, quest domain.Quest, run domain.FlowRun) {
	if run.Status != domain.RunCompleted {
		now := time.Now().UTC()
		run.Status, run.Error = domain.RunCompleted, ""
		if run.FinishedAt == nil {
			run.FinishedAt = &now
		}
		if err := a.store.SaveFlowRun(ctx, run); err != nil {
			observability.From(ctx).Error("finished flow status not restored", "flow_run_id", run.ID, "error", err)
		}
	}
	if _, final, err := a.store.WorkOrderVerdictV2(ctx, quest.ID); err != nil || !final {
		return
	}
	if status, err := a.store.FinalizeWorkOrderQuestV2(ctx, quest.ID, domain.EvidenceBundle{}); err != nil {
		observability.From(ctx).Error("quest verdict not restored", "quest_id", quest.ID, "error", err)
	} else {
		observability.From(ctx).Info("quest returned to its verdict after a no-op resume", "quest_id", quest.ID, "status", status)
	}
}
