package app

import (
	"context"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/observability"
)

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
		if _, saveErr := a.setWorkOrderQuestStatusV2(ctx, quest, domain.QuestPaused, questRecoveryMessageV2); saveErr != nil {
			observability.From(ctx).Error("interrupted quest pause failed", "quest_id", item.QuestID, "error", saveErr)
		}
	}
}
