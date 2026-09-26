package app

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/sandbox"
	"local-agent-workbench/internal/security"
)

// sandboxWaitMessageV2 is what the card says while a quest waits for Docker.
const sandboxWaitMessageV2 = "Ждёт Docker Desktop: запустите его — квест продолжится сам"

// sandboxReadyMessageV2 replaces the wait once the daemon answers; the
// extension continues the quest with the key it holds.
const sandboxReadyMessageV2 = "Docker запущен — продолжаем квест"

// sandboxWatchInterval matches the backend's own re-probe spacing: asking the
// daemon more often only repeats the same answer.
const sandboxWatchInterval = 15 * time.Second

// launchMovedByHumanV2 says whether a failed launch found its quest in a state
// someone else chose. Only `preflight` and `running` belong to the launch
// itself: it sets `running` when it creates the Flow, and a refusal after that
// point used to vanish as "the human moved the quest".
func launchMovedByHumanV2(status domain.QuestStatus) bool {
	return status != domain.QuestPreflight && status != domain.QuestRunning
}

// sandboxUnavailableV2 asks the backend again instead of trusting the answer
// from startup: Docker Desktop is often started after Point.
func (a *App) sandboxUnavailableV2(ctx context.Context) string {
	if a.sandboxBackend == nil {
		return ""
	}
	if rechecker, ok := a.sandboxBackend.(sandbox.Rechecker); ok {
		checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		if err := rechecker.Recheck(checkCtx); err != nil && sandbox.IsUnavailable(err) {
			return security.Redact(err.Error())
		}
		return ""
	}
	return a.sandboxBackend.Capabilities().Unavailable
}

// pauseWorkOrderByCoreV2 pauses a quest for a reason of the core's own and
// records where it stood. Continuation returns there: to `preflight` when the
// launch had not built its Flow yet, to `running` when it had. Without the
// record the pause looked like the human's, and continuing a launch that never
// planned returned `running` with nobody executing.
func (a *App) pauseWorkOrderByCoreV2(ctx context.Context, quest domain.Quest, message string) error {
	from := quest.Status
	if _, err := a.setWorkOrderQuestStatusV2(ctx, quest, domain.QuestPaused, message); err != nil {
		return err
	}
	return a.store.RecordWorkOrderQuestPauseV2(ctx, quest.ID, from, message)
}

// waitForSandboxV2 turns "Docker is not running" into a pause the quest leaves
// by itself. It is not a failure of the work: nothing has run yet, and the
// only thing the human can do is start Docker Desktop.
func (a *App) waitForSandboxV2(ctx context.Context, approval domain.WorkOrderApproval, quest domain.Quest, reason string) {
	if quest.Controller == nil {
		quest.Controller = map[string]any{}
	}
	delete(quest.Controller, "resumeAfterRestart")
	quest.Controller["waitingForSandbox"] = true
	if err := a.pauseWorkOrderByCoreV2(ctx, quest, sandboxWaitMessageV2); err != nil {
		slog.Error("work order not paused for Docker", "quest_id", quest.ID, "error", err, "reason", reason)
		return
	}
	slog.Warn("work order waits for Docker", "quest_id", quest.ID, "reason", reason)
	a.publishWorkOrderNoticeV2(ctx, approval, quest, "warning",
		"Квест ждёт Docker Desktop: исполнитель работает в контейнере, а Docker не запущен. Запустите Docker Desktop — квест продолжится сам.")
}

// publishWorkOrderNoticeV2 writes one line into the Master feed. The card
// alone was not enough: a launch refused minutes after "Утвердить" changed a
// status nobody was looking at.
func (a *App) publishWorkOrderNoticeV2(ctx context.Context, approval domain.WorkOrderApproval, quest domain.Quest, level, text string) {
	text = strings.TrimSpace(security.Redact(text))
	if text == "" {
		return
	}
	_, err := a.store.SaveCompanionMessageOnce(ctx, domain.CompanionMessage{
		ID: domain.NewID("master-workorder-notice"), WorkspaceID: approval.WorkOrder.WorkspaceID,
		ConversationID: approval.WorkOrder.ConversationID, Speaker: "master", Role: "assistant",
		Content: text, Level: level, Mode: "quest_notice",
		ProposalID: approval.WorkOrder.ProposalID, CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		slog.Error("work order notice not published", "quest_id", quest.ID, "error", security.Redact(err.Error()))
	}
}

// startSandboxWatchV2 notices Docker Desktop coming back. The backend that
// started without its daemon re-probes only when something asks it, so the
// banner stayed and waiting quests stayed paused until an unrelated action.
func (a *App) startSandboxWatchV2() {
	a.sandboxWatchMu.Lock()
	defer a.sandboxWatchMu.Unlock()
	if a.sandboxWatchCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.sandboxWatchCancel = cancel
	a.sandboxWatchWG.Add(1)
	go func() {
		defer a.sandboxWatchWG.Done()
		ticker := time.NewTicker(sandboxWatchInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				a.resumeQuestsWaitingForSandboxV2(ctx)
			}
		}
	}()
}

func (a *App) stopSandboxWatchV2() {
	a.sandboxWatchMu.Lock()
	cancel := a.sandboxWatchCancel
	a.sandboxWatchCancel = nil
	a.sandboxWatchMu.Unlock()
	if cancel != nil {
		cancel()
	}
	a.sandboxWatchWG.Wait()
}

// resumeQuestsWaitingForSandboxV2 hands quests paused for Docker back to the
// extension once the daemon answers. The model key lives only in the
// extension's SecretStorage, so the core marks them and the extension
// continues them, exactly as after a restart.
func (a *App) resumeQuestsWaitingForSandboxV2(ctx context.Context) {
	if a.sandboxBackend == nil {
		return
	}
	waiting, err := a.store.ListWorkOrderQuestsWaitingForSandboxV2(ctx)
	if err != nil {
		slog.Warn("sandbox wait scan failed", "error", security.Redact(err.Error()))
		return
	}
	if len(waiting) == 0 && a.sandboxBackend.Capabilities().Unavailable == "" {
		return
	}
	if reason := a.sandboxUnavailableV2(ctx); reason != "" {
		return
	}
	for _, item := range waiting {
		quest, questErr := a.workOrderQuestV2(ctx, item.WorkspaceID, item.QuestID)
		if questErr != nil || quest.Status != domain.QuestPaused {
			continue
		}
		if waitingFlag, _ := quest.Controller["waitingForSandbox"].(bool); !waitingFlag {
			continue
		}
		delete(quest.Controller, "waitingForSandbox")
		quest.Controller["resumeAfterRestart"] = true
		if _, saveErr := a.setWorkOrderQuestStatusV2(ctx, quest, domain.QuestPaused, sandboxReadyMessageV2); saveErr != nil {
			slog.Warn("quest waiting for Docker not released", "quest_id", quest.ID, "error", saveErr)
			continue
		}
		slog.Info("Docker is back; quest handed to the extension to continue", "quest_id", quest.ID)
	}
}
