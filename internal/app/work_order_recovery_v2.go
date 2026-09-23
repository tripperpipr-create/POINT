package app

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/security"
)

// reconcileBrokenWorkOrderFinalizationsV2 repairs the one historic state the
// old Flow cleanup could create. It intentionally bypasses delivery and
// completion runners: the workspace already contains the applied Change Sets.
func (a *App) reconcileBrokenWorkOrderFinalizationsV2(ctx context.Context) {
	// A deterministic node may have completed before the launch path persisted
	// its milestone link.  Older cores then wrote the root back to running even
	// though the Flow was failed.  Replay only the evidence finalizer; a failed
	// Flow cannot enter the delivery branch, so no commands or changes are
	// applied again.
	interrupted, interruptedErr := a.store.ListTerminalFlowPendingWorkOrdersV2(ctx)
	if interruptedErr != nil {
		slog.Error("terminal flow work order recovery scan failed", "error", security.Redact(interruptedErr.Error()))
	} else {
		for _, approval := range interrupted {
			a.finalizeQuestAfterFlow(approval.QuestID, false)
			slog.Info("reconciled terminal flow work order", "work_order_id", approval.WorkOrder.ID, "quest_id", approval.QuestID, "flow_succeeded", false)
		}
	}

	candidates, err := a.store.ListBrokenWorkOrderFinalizationsV2(ctx)
	if err != nil {
		slog.Error("work order finalization recovery scan failed", "error", security.Redact(err.Error()))
		return
	}
	for _, approval := range candidates {
		prepared, prepareErr := a.store.PrepareBrokenWorkOrderFinalizationV2(ctx, approval)
		if prepareErr != nil {
			slog.Error("work order finalization recovery prepare failed", "quest_id", approval.QuestID, "error", security.Redact(prepareErr.Error()))
			continue
		}
		if !prepared {
			continue
		}
		quest, questErr := a.workOrderQuestV2(ctx, approval.WorkOrder.WorkspaceID, approval.QuestID)
		if questErr != nil {
			slog.Error("work order finalization recovery could not load quest", "quest_id", approval.QuestID, "error", security.Redact(questErr.Error()))
			continue
		}
		bundle := a.buildWorkOrderEvidenceV2(ctx, approval, quest, true)
		bundle.DeliveryVerified = true
		bundle.DeliveryTarget = approval.WorkOrder.Workspace.Path
		bundle.WorkspaceRevision = workOrderRevisionV2(approval.WorkOrder, quest, bundle)
		bundle.KnownLimitations = append(bundle.KnownLimitations, "Итог восстановлен из сохранённых запусков и уже применённых Change Sets; проверки и агенты повторно не запускались")
		bundle.DeliveryReceipt = &domain.DeliveryReceipt{
			ID: "delivery-recovered-" + quest.ID, QuestID: quest.ID,
			WorkOrderDigest: bundle.BriefDigest, Target: bundle.DeliveryTarget,
			WorkspaceRevision: bundle.WorkspaceRevision, DeliveredAt: time.Now().UTC(),
		}
		if approval.WorkOrder.ProposalID == "" && strings.HasPrefix(approval.WorkOrder.ID, "workorder-") {
			approval.WorkOrder.ProposalID = strings.TrimPrefix(approval.WorkOrder.ID, "workorder-")
		}
		status, finalizeErr := a.store.FinalizeWorkOrderQuestV2(ctx, quest.ID, bundle)
		if finalizeErr != nil {
			a.blockWorkOrderFinalizationV2(ctx, quest, "Не удалось восстановить итог WorkOrder: "+security.Redact(finalizeErr.Error()), finalizeErr)
			continue
		}
		if stored, evidenceErr := a.store.GetEvidenceBundle(ctx, quest.ID); evidenceErr == nil {
			bundle = stored
		}
		a.publishWorkOrderOutcomeV2(ctx, approval, quest, status, bundle)
		slog.Info("reconciled historic work order finalization", "work_order_id", approval.WorkOrder.ID, "quest_id", quest.ID, "status", status, "assurance", bundle.Assurance)
	}
}
