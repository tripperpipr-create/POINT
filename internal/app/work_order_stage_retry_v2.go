// Повтор этапа с места сбоя: провал этапа Flow ждёт решения человека.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/environment"
	"local-agent-workbench/internal/flowruntime"
	"local-agent-workbench/internal/security"
)

// stageFailureKey — отметка в контроллере квеста: этап Flow провалился, и
// квест ждёт решения человека вместо вердикта. stageFailureSettledKey — человек
// решил завершить квест: следующее завершение Flow выносит вердикт.
const (
	stageFailureKey        = "stageFailure"
	stageFailureSettledKey = "stageFailureSettled"
)

var errNoStageFailureV2 = errors.New("квест не ждёт решения по проваленному этапу")

// holdWorkOrderQuestForStageDecisionV2 не даёт проваленному этапу сразу стать
// вердиктом. Раньше любой сбой этапа — даже сбой самого Point после успешной
// команды — закрывал квест `blocked`: пакет доказательств у квеста один и
// неизменяем, и повторить работу можно было только новой версией наряда, с
// нуля. Теперь квест ждёт человека: «Повторить этап» продолжает Flow с
// проваленного этапа, сохраняя пройденные, «Завершить квест» выносит вердикт.
func (a *App) holdWorkOrderQuestForStageDecisionV2(ctx context.Context, quest domain.Quest, approval domain.WorkOrderApproval) bool {
	if strings.TrimSpace(quest.FlowRunID) == "" || stageFailureSettled(quest) {
		return false
	}
	switch quest.Status {
	case domain.QuestPreflight, domain.QuestRunning, domain.QuestVerifying:
	default:
		return false
	}
	run, err := a.store.GetFlowRun(ctx, quest.FlowRunID)
	if err != nil {
		return false
	}
	failure, failed := terminalFailedWorkOrderFlowV2(run)
	if !failed {
		return false
	}
	nodeID, nodeName := failedFlowNodeV2(run)
	if quest.Controller == nil {
		quest.Controller = map[string]any{}
	}
	diagnosis := a.stageFailureDiagnosisV2(ctx, run, nodeID, failure)
	autoRetry := stageAutoRetryInfo(quest, nodeID, diagnosis)
	quest.Controller[stageFailureKey] = map[string]any{
		"nodeId": nodeID, "nodeName": nodeName, "flowRunId": run.ID,
		"error": truncateRunes(security.Redact(failure), 1000), "at": time.Now().UTC().Format(time.RFC3339Nano),
		"diagnosis": diagnosis, "autoRetry": autoRetry, "runtimeChoices": environment.RetryRuntimeChoices(),
	}
	// Предложение прежнего провала к этому не относится.
	delete(quest.Controller, stageRetryProposalKey)
	message := truncateRunes(diagnosis.Summary(nodeName), 600) + ". Повторите этап — пройденные этапы сохранятся — или завершите квест."
	if allowed, _ := autoRetry["allowed"].(bool); allowed {
		message = truncateRunes(diagnosis.Summary(nodeName), 600) + fmt.Sprintf(". Сбой среды: Point повторит этап сам через %d с.", stageAutoRetryDelaySecs)
	}
	if _, err = a.setWorkOrderQuestStatusV2(ctx, quest, domain.QuestAwaitingUser, message); err != nil {
		slog.Warn("stage failure hold not recorded; quest goes to verdict", "quest_id", quest.ID, "error", security.Redact(err.Error()))
		return false
	}
	a.publishWorkOrderNoticeV2(ctx, approval, quest, "warning", message)
	slog.Info("work order quest holds for a stage decision", "quest_id", quest.ID, "node_id", nodeID)
	return true
}

// stageRetryStatusMessage — что человек видит, пока этап повторяется: кто
// повторил и с какими правками.
func stageRetryStatusMessage(nodeName string, plan stageRetryPlan) string {
	who := "Повтор этапа"
	if plan.Source == StageRetrySourceAuto {
		who = "Point повторяет этап"
		if plan.Proposal != "" {
			who = "Мастер повторяет этап"
		}
	}
	message := fmt.Sprintf("%s «%s»", who, nodeName)
	changes := []string{}
	if plan.Runtime != "" {
		if choice, ok := environment.RetryRuntimeChoiceByID(plan.Runtime); ok {
			changes = append(changes, "в среде "+choice.Label)
		}
	}
	if plan.Instruction != "" {
		changes = append(changes, "с указанием исполнителю")
	}
	for _, change := range plan.Criteria {
		changes = append(changes, "с изменённой проверкой "+change.CriterionID)
	}
	if len(changes) > 0 {
		message += " " + strings.Join(changes, ", ")
	}
	return message
}

func stageFailureSettled(quest domain.Quest) bool {
	settled, _ := quest.Controller[stageFailureSettledKey].(bool)
	return settled
}

func stageFailurePending(quest domain.Quest) bool {
	_, pending := quest.Controller[stageFailureKey].(map[string]any)
	return pending && quest.Status == domain.QuestAwaitingUser && !stageFailureSettled(quest)
}

// failedFlowNodeV2 называет проваленный узел так, как его видит человек.
func failedFlowNodeV2(run domain.FlowRun) (string, string) {
	flow, _, _ := flowruntime.FlowFromSnapshot(run)
	names := map[string]string{}
	for _, node := range flow.Nodes {
		names[node.ID] = strings.TrimSpace(node.Name)
	}
	for nodeID, state := range run.NodeStates {
		if state.Status == "failed" {
			if name := names[nodeID]; name != "" {
				return nodeID, name
			}
			return nodeID, nodeID
		}
	}
	return "", "этап"
}

// retryFailedWorkOrderStageV2 продолжает Flow с проваленного этапа. Пройденные
// этапы и их результаты остаются; проваленный узел получает новую попытку с
// отчётом о прежней, пропущенные из-за него узлы снова ждут своих
// предшественников. Прежнее исполнение и его набор правок остаются уликами.
func (a *App) retryFailedWorkOrderStageV2(ctx context.Context, quest domain.Quest, apiKey string, plan stageRetryPlan) (domain.QuestStatus, error) {
	if !stageFailurePending(quest) {
		return quest.Status, errNoStageFailureV2
	}
	run, err := a.store.GetFlowRun(ctx, quest.FlowRunID)
	if err != nil {
		return quest.Status, err
	}
	if _, failed := terminalFailedWorkOrderFlowV2(run); !failed {
		return quest.Status, errNoStageFailureV2
	}
	_, failedNodeID, _ := stageFailureRecord(quest)
	reopen := map[string]domain.QuestStatus{}
	for nodeID, state := range run.NodeStates {
		switch {
		case state.Status == "failed":
			if state.Output == nil {
				state.Output = map[string]any{}
			}
			// Правки повтора — только проваленному этапу, ради которого повтор.
			if nodeID == failedNodeID || failedNodeID == "" {
				delete(state.Output, retryRuntimeOutputKey)
				delete(state.Output, retryInstructionOutputKey)
				if plan.Runtime != "" {
					state.Output[retryRuntimeOutputKey] = plan.Runtime
				}
				if plan.Instruction != "" {
					state.Output[retryInstructionOutputKey] = plan.Instruction
				}
			}
			executionID, _ := state.Output["executionId"].(string)
			attemptIDs := stringListFromAny(state.Output["attemptExecutionIds"])
			if executionID != "" && !slices.Contains(attemptIDs, executionID) {
				attemptIDs = append(attemptIDs, executionID)
			}
			if executionID != "" {
				if execution, getErr := a.store.GetExecution(ctx, executionID); getErr == nil {
					state.Output["previousAttempt"] = a.previousAttemptSummary(ctx, execution, len(attemptIDs))
					state.Output["previousExecutionId"] = execution.ID
				}
			}
			state.Output["attemptExecutionIds"] = attemptIDs
			state.Output["lastError"] = state.Error
			state.Output["needsSchedule"] = true
			state.Output["waitReason"] = "retry_by_user"
			delete(state.Output, "executionId")
			delete(state.Output, "error")
			state.Status = "waiting_agent"
			state.Error = ""
			state.FinishedAt = nil
			state.Attempts++
			run.NodeStates[nodeID] = state
			reopen[nodeID] = domain.QuestActive
		case state.Status == "skipped" && skippedByPeerFailure(state):
			run.NodeStates[nodeID] = domain.FlowNodeState{Status: "blocked", Attempts: state.Attempts}
			reopen[nodeID] = domain.QuestDraft
		}
	}
	run.Status = domain.RunWaiting
	run.Error = ""
	run.FinishedAt = nil
	// Сначала квест: переход проверяет, что он всё ещё ждёт этого решения, и
	// берёт аренду записи. Flow без живого квеста не оживляется. Журнал
	// называет источник и правки: повтор без человека виден так же, как нажатие.
	status, err := a.store.ControlWorkOrderQuestV2(ctx, quest.ID, "retry", plan.journalNote())
	if err != nil {
		return quest.Status, err
	}
	// Поправки критериев — после того, как квест принял повтор: отвергнутый
	// повтор не должен оставлять изменённых проверок.
	now := time.Now().UTC()
	for index, change := range plan.Criteria {
		if saveErr := a.store.SaveCriterionAmendmentV2(ctx, domain.CriterionAmendment{
			QuestID: quest.ID, CriterionID: change.CriterionID, PreviousCommand: change.PreviousCommand,
			Command: change.Command, Reason: change.Reason, ProposedBy: "master",
			CreatedAt: now.Add(time.Duration(index) * time.Microsecond),
		}); saveErr != nil {
			return status, fmt.Errorf("поправка проверки %s не записана: %w", change.CriterionID, saveErr)
		}
	}
	if err = a.store.SaveFlowRun(ctx, run); err != nil {
		return status, err
	}
	a.reopenFlowStageQuestsV2(ctx, run, reopen)
	if latest, loadErr := a.workOrderQuestV2(ctx, quest.WorkspaceID, quest.ID); loadErr == nil {
		failure, _ := latest.Controller[stageFailureKey].(map[string]any)
		name, _ := failure["nodeName"].(string)
		delete(latest.Controller, stageFailureKey)
		delete(latest.Controller, stageRetryProposalKey)
		if plan.Source == StageRetrySourceAuto {
			countStageAutoRetry(&latest, failedNodeID)
		}
		latest.Controller["statusMessage"] = stageRetryStatusMessage(name, plan)
		latest.UpdatedAt = time.Now().UTC()
		_ = a.saveLoadedQuest(ctx, latest, latest.Status, "stage_retry")
	}
	runtime := flowruntime.Runtime{Store: a.store}
	ticked, err := runtime.Tick(ctx, run.ID)
	if err != nil {
		return status, err
	}
	a.rememberFlowOrchestratorKey(ticked.ID, apiKey)
	if err = a.scheduleFlowAgentExecutionsFromRun(ticked); err != nil {
		return status, err
	}
	slog.Info("work order stage retried by user", "quest_id", quest.ID, "flow_run_id", run.ID)
	return status, nil
}

// settleFailedWorkOrderStageV2 — человек решил не повторять: квест получает
// вердикт тем же путём, что получил бы сразу после сбоя.
func (a *App) settleFailedWorkOrderStageV2(ctx context.Context, quest domain.Quest) (domain.QuestStatus, error) {
	if !stageFailurePending(quest) {
		return quest.Status, errNoStageFailureV2
	}
	quest.Controller[stageFailureSettledKey] = true
	// Из ожидания квест возвращается в работу, и вердикт выносится ровно тем
	// путём, что и сразу после сбоя: работа → проверка → вердикт шлюза.
	if _, err := a.setWorkOrderQuestStatusV2(ctx, quest, domain.QuestRunning, "Завершаем квест по решению человека"); err != nil {
		return quest.Status, err
	}
	a.finalizeQuestAfterFlow(quest.ID, false)
	latest, err := a.workOrderQuestV2(ctx, quest.WorkspaceID, quest.ID)
	if err != nil {
		return quest.Status, err
	}
	return latest.Status, nil
}

func skippedByPeerFailure(state domain.FlowNodeState) bool {
	skipped, _ := state.Output["skippedDueToPeerFailure"].(bool)
	return skipped
}

// reopenFlowStageQuestsV2 возвращает в работу квесты этапов, которые провал
// закрыл: проваленный — в работу, пропущенные — в ожидание. Обычный путь
// статуса этапа отменённый квест не поднимает — и правильно, отмена человеком
// окончательна; здесь закрытие сделал сам провал, и повтор его снимает.
func (a *App) reopenFlowStageQuestsV2(ctx context.Context, run domain.FlowRun, statuses map[string]domain.QuestStatus) {
	if len(statuses) == 0 {
		return
	}
	quests, err := a.store.ListQuests(ctx, run.WorkspaceID)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	for _, stage := range quests {
		status, ok := statuses[stage.FlowNodeID]
		if !ok || stage.FlowRunID != run.ID || stage.ParentID != run.QuestID {
			continue
		}
		loaded := stage.Status
		stage.Status, stage.UpdatedAt, stage.FinishedAt = status, now, nil
		_ = a.saveLoadedQuest(ctx, stage, loaded, "stage_retry_reopen")
	}
}
