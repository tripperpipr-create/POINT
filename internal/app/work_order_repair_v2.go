package app

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/security"
)

// workOrderRepairFeedbackLimit keeps the report of a failed attempt within
// what the planner and the executor's inbox accept.
const workOrderRepairFeedbackLimit = 3800

// workOrderAttemptV2 is the number of the attempt the quest is on, from 1.
// The milestone runtime counter is not used: it grows only on one of the
// launch paths and stayed 0 in the live run.
func workOrderAttemptV2(quest domain.Quest) int {
	switch value := quest.Controller["repairAttempt"].(type) {
	case float64:
		if value >= 1 {
			return int(value)
		}
	case int:
		if value >= 1 {
			return value
		}
	}
	return 1
}

func workOrderMaxAttemptsV2(order domain.WorkOrder) int {
	if order.Budget.MaxAttempts > 0 {
		return order.Budget.MaxAttempts
	}
	return 3
}

// startWorkOrderRepairAttemptV2 decides whether a failed host check ends the
// quest or starts another attempt. The live run of 26.09 ended as
// «Заблокировано… повторите запуск» with the approved budget allowing three
// attempts, and the verdict made the quest impossible to continue. Another
// attempt happens before any verdict: evidence and the gate stay one per
// quest, and the executor gets what the host observed.
//
// Only failures the code can change qualify, and only when every failed
// host check is such a failure: a port held by another stack would fail the
// next attempt the same way.
func (a *App) startWorkOrderRepairAttemptV2(ctx context.Context, approval domain.WorkOrderApproval, quest domain.Quest, bundle domain.EvidenceBundle, hostChecks []domain.VerificationCheck) bool {
	repairable := repairableHostFailuresV2(hostChecks)
	if len(repairable) == 0 {
		return false
	}
	for _, check := range hostChecks {
		if !check.Satisfied && (check.ExitCode == nil || hostCheckActionV2(check.Summary) != "") {
			return false
		}
	}
	order := approval.WorkOrder
	attempt, maxAttempts := workOrderAttemptV2(quest), workOrderMaxAttemptsV2(order)
	if attempt >= maxAttempts {
		return false
	}
	if quest.Status != domain.QuestVerifying && quest.Status != domain.QuestApplying {
		return false
	}
	if err := a.resetWorkOrderMilestoneForRepairV2(ctx, approval, quest); err != nil {
		slog.Warn("repair attempt not started: milestone not reset", "quest_id", quest.ID, "error", err)
		return false
	}
	failedNames := failedHostCriteriaNamesV2(repairable, order)
	feedback := workOrderRepairFeedbackV2(order, repairable, bundle.HostDiagnostics, attempt, maxAttempts)
	previousFlowRun := quest.FlowRunID
	next := attempt + 1
	if quest.Controller == nil {
		quest.Controller = map[string]any{}
	}
	history, _ := quest.Controller["repairHistory"].([]any)
	quest.Controller["repairHistory"] = append(history, map[string]any{
		"attempt": attempt, "failed": failedNames, "flowRunId": previousFlowRun, "at": time.Now().UTC().Format(time.RFC3339),
	})
	quest.Controller["repairAttempt"] = next
	quest.Controller["repairFeedback"] = feedback
	quest.Controller["resumeAfterRestart"] = true
	quest.Controller["launchPhase"] = "repair"
	quest.FlowID, quest.FlowRunID = "", ""
	quest.UpdatedAt = time.Now().UTC()
	if err := a.store.SaveQuest(ctx, quest); err != nil {
		slog.Warn("repair attempt not started: quest not saved", "quest_id", quest.ID, "error", err)
		return false
	}
	message := fmt.Sprintf("Проверка на хосте не прошла (%s). Point исправляет — попытка %d из %d", strings.Join(failedNames, "; "), next, maxAttempts)
	if _, err := a.setWorkOrderQuestStatusV2(ctx, quest, domain.QuestPaused, message); err != nil {
		slog.Warn("repair attempt not started: quest not paused", "quest_id", quest.ID, "error", err)
		return false
	}
	// Продолжение возвращает квест к проверке окружения и новому плану, а не к
	// проверке, которая только что провалилась.
	if err := a.store.RecordWorkOrderQuestPauseV2(ctx, quest.ID, domain.QuestPreflight, message); err != nil {
		slog.Warn("repair pause not recorded", "quest_id", quest.ID, "error", err)
	}
	a.clearFlowOrchestratorKey(previousFlowRun)
	notice := []string{fmt.Sprintf("Проверка на хосте не прошла: %s.", strings.Join(failedNames, "; "))}
	if highlights := hostDiagnosticHighlightsV2(bundle.HostDiagnostics, 3); len(highlights) > 0 {
		notice = append(notice, "Журнал контейнеров: "+strings.Join(highlights, " · "))
	}
	notice = append(notice, fmt.Sprintf("Point исправляет сам: попытка %d из %d, исполнитель получит этот отчёт.", next, maxAttempts))
	a.publishWorkOrderNoticeV2(ctx, approval, quest, "warning", strings.Join(notice, "\n"))
	slog.Info("work order repair attempt scheduled", "quest_id", quest.ID, "attempt", next, "max_attempts", maxAttempts, "failed", strings.Join(failedNames, "; "))
	return true
}

// resetWorkOrderMilestoneForRepairV2 returns the current milestone to the
// state a launch picks up, keeping its history counter.
func (a *App) resetWorkOrderMilestoneForRepairV2(ctx context.Context, approval domain.WorkOrderApproval, quest domain.Quest) error {
	runtimes, err := a.store.ListMilestoneRuntimesV2(ctx, quest.ID, approval.WorkOrder.Version)
	if err != nil {
		return err
	}
	currentID, _ := quest.Controller["currentMilestoneId"].(string)
	index := -1
	for i, runtime := range runtimes {
		if currentID != "" && runtime.MilestoneID == currentID {
			index = i
			break
		}
		if index < 0 && quest.FlowRunID != "" && runtime.FlowRunID == quest.FlowRunID {
			index = i
		}
	}
	if index < 0 && len(runtimes) == 1 {
		index = 0
	}
	if index >= 0 {
		runtime := runtimes[index]
		runtime.Status = domain.QuestDraft
		runtime.FlowID, runtime.FlowRunID = "", ""
		runtime.FinishedAt = nil
		return a.store.SaveMilestoneRuntimeV2(ctx, quest.ID, approval.WorkOrder.ID, approval.WorkOrder.Version, runtime)
	}
	return fmt.Errorf("milestone runtime of quest %s not found", quest.ID)
}

func failedHostCriteriaNamesV2(checks []domain.VerificationCheck, order domain.WorkOrder) []string {
	texts := map[string]string{}
	for _, criterion := range order.Criteria {
		texts[criterion.ID] = strings.TrimSpace(criterion.Text)
	}
	names := make([]string, 0, len(checks))
	for _, check := range checks {
		name := texts[check.ID]
		if name == "" {
			name = check.ID
		}
		names = append(names, name)
	}
	return names
}

// workOrderRepairFeedbackV2 is the report the next attempt starts from: what
// each failed criterion expected, what the host observed, and what the
// containers said.
func workOrderRepairFeedbackV2(order domain.WorkOrder, failed []domain.VerificationCheck, diagnostics string, attempt, maxAttempts int) string {
	texts := map[string]string{}
	for _, criterion := range order.Criteria {
		texts[criterion.ID] = strings.TrimSpace(criterion.Text)
	}
	lines := []string{fmt.Sprintf("Попытка %d из %d: результат перенесён в проект, но проверки на хосте не прошли.", attempt, maxAttempts)}
	for _, check := range failed {
		name := texts[check.ID]
		if name == "" {
			name = check.ID
		}
		lines = append(lines, "- "+name+": "+lastSummaryLineV2(check.Summary))
	}
	if highlights := hostDiagnosticHighlightsV2(diagnostics, 6); len(highlights) > 0 {
		lines = append(lines, "Журнал контейнеров:")
		for _, line := range highlights {
			lines = append(lines, "  "+line)
		}
	}
	lines = append(lines, "Проверка идёт на чистом стенде (свой Compose-проект, тома пересоздаются), поэтому причина — в файлах проекта: Dockerfile, compose, код или его конфигурация.")
	text := security.Redact(strings.Join(lines, "\n"))
	if runes := []rune(text); len(runes) > workOrderRepairFeedbackLimit {
		text = string(runes[:workOrderRepairFeedbackLimit]) + "…"
	}
	return text
}

// postWorkOrderRepairContextV2 hands the report to the executors of the new
// Flow through their team inbox: the stage instructions come from the
// planner, and the inbox is what every executor reads first.
func (a *App) postWorkOrderRepairContextV2(ctx context.Context, quest domain.Quest, flowRun *domain.FlowRun) {
	feedback, _ := quest.Controller["repairFeedback"].(string)
	if strings.TrimSpace(feedback) == "" || flowRun == nil {
		return
	}
	if err := a.store.SaveTeamEvent(ctx, domain.TeamEvent{
		ID: domain.NewID("teamevent"), WorkspaceID: quest.WorkspaceID, QuestID: quest.ID, FlowRunID: flowRun.ID,
		FromAgentID: "point", Kind: "finding", Message: feedback, CreatedAt: time.Now().UTC(),
	}); err != nil {
		slog.Warn("repair report not delivered to the team inbox", "quest_id", quest.ID, "error", err)
	}
}
