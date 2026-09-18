package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/security"
)

type WorkOrderQuestControlRequest struct {
	Message string `json:"message,omitempty"`
	// APIKey is transient resume material from desktop SecretStorage. It is
	// never written to the quest control journal.
	APIKey string `json:"apiKey,omitempty"`
}

type WorkOrderQuestControlResult struct {
	QuestID      string             `json:"questId"`
	Status       domain.QuestStatus `json:"status"`
	Action       string             `json:"action"`
	FlowRunID    string             `json:"flowRunId,omitempty"`
	AffectedRuns int                `json:"affectedRuns"`
}

func (a *App) ControlWorkOrderQuestV2(ctx context.Context, questID, action string, request WorkOrderQuestControlRequest) (WorkOrderQuestControlResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	action = strings.ToLower(strings.TrimSpace(action))
	if len(request.APIKey) > 64*1024 {
		return WorkOrderQuestControlResult{}, errors.New("API credential exceeds 64 KiB")
	}
	if action == "message" {
		request.Message = strings.TrimSpace(request.Message)
		if request.Message == "" || len(request.Message) > 32768 {
			return WorkOrderQuestControlResult{}, errors.New("quest message must contain 1 to 32768 bytes")
		}
	}
	quest, err := a.WorkOrderQuestV2(ctx, questID)
	if err != nil {
		return WorkOrderQuestControlResult{}, err
	}
	result := WorkOrderQuestControlResult{QuestID: questID, Status: quest.Status, Action: action, FlowRunID: quest.FlowRunID}
	if err = validateWorkOrderRuntimeControl(quest.Status, action); err != nil {
		return result, err
	}
	if action == "cancel" || action == "pause" {
		// Фоновый запуск ждёт планировщика минутами. Решение человека старше:
		// иначе отменённый квест ещё полчаса держал бы за собой запуск.
		a.cancelWorkOrderLaunchV2(questID)
	}
	if quest.FlowRunID != "" {
		runtimeRequest := request
		// A pasted credential must not become a model amendment. The immutable
		// journal independently applies the same redaction before persistence.
		runtimeRequest.Message = security.Redact(runtimeRequest.Message)
		result.AffectedRuns, err = a.controlWorkOrderFlowRuntimeV2(ctx, quest, action, runtimeRequest)
		if err != nil {
			return result, err
		}
	}
	result.Status, err = a.store.ControlWorkOrderQuestV2(ctx, questID, action, request.Message)
	if err != nil || action != "resume" {
		return result, err
	}
	if result.Status != domain.QuestPreflight {
		// Возобновление из паузы возвращает прежний статус — обычно `running`.
		// Планировщик к этой секунде уже отработал и мог оставить узел ждать
		// ключ, но его собственный пересчёт прошёл раньше этой записи и увидел
		// ещё `paused`, которую трогать нельзя. Пересчитываем здесь, по факту
		// нового статуса, иначе карточка снова покажет «выполняется» у работы,
		// которая стоит, и «Повторить запуск» будет отвергнут как переход из
		// `running`.
		if quest.FlowRunID != "" {
			a.syncWorkOrderQuestStallV2(quest.FlowRunID)
			if latest, latestErr := a.WorkOrderQuestV2(ctx, questID); latestErr == nil {
				result.Status = latest.Status
			}
		}
		return result, nil
	}
	// Возобновление из блокировки вернуло наряд к проверке окружения — саму
	// проверку надо провести, иначе квест останется в `preflight` навсегда.
	// Неудача не теряется: launch сам переведёт квест обратно в `blocked` с
	// причиной, и человек прочитает её на той же карточке.
	approval, approvalErr := a.store.WorkOrderApprovalByQuestV2(ctx, questID)
	if approvalErr != nil {
		return result, nil
	}
	resumed, resumeErr := a.resumeApprovedWorkOrderV2(ctx, approval, request.APIKey)
	if resumeErr != nil {
		return result, nil
	}
	// У квеста с живым Flow resumeApprovedWorkOrderV2 ничего не запускает: он
	// видит готовые FlowID/FlowRunID и выходит сразу. Квест оставался в
	// `preflight`, из которого нет ни одного разрешённого действия, — тупик
	// хуже прежнего. Flow уже перепланирован выше, и здесь довершается только
	// честный статус: ждёт человека, заблокирован или пошёл.
	if domain.QuestStatus(resumed.Status) == domain.QuestPreflight && quest.FlowRunID != "" {
		a.syncWorkOrderQuestStallV2(quest.FlowRunID)
		if latest, latestErr := a.WorkOrderQuestV2(ctx, questID); latestErr == nil {
			resumed.Status = string(latest.Status)
		}
	}
	result.Status = domain.QuestStatus(resumed.Status)
	if resumed.FlowRunID != "" {
		result.FlowRunID = resumed.FlowRunID
	}
	return result, nil
}

func validateWorkOrderRuntimeControl(status domain.QuestStatus, action string) error {
	switch action {
	case "message":
		return nil
	case "pause":
		if domain.CanTransitionWorkOrderQuest(status, domain.QuestPaused) {
			return nil
		}
	case "cancel":
		if domain.CanTransitionWorkOrderQuest(status, domain.QuestCancelled) {
			return nil
		}
	case "resume":
		// Пауза — решение человека, блокировка — состояние среды. Второе тоже
		// возобновляемо: человек чинит причину (поднимает Docker, отдаёт ключ) и
		// просит попробовать снова. Домен этот переход разрешает, и без него у
		// заблокированного наряда остаётся единственный выход — отмена.
		// Ожидание человека возобновляемо по той же причине: работа стоит на
		// нём, он её разблокировал и просит продолжить. Домен этот переход
		// разрешает; без него у квеста, ждущего ключ, не было ни одной кнопки.
		if status == domain.QuestPaused || status == domain.QuestBlocked || status == domain.QuestAwaitingUser {
			return nil
		}
	default:
		return errors.New("unsupported quest control action")
	}
	return fmt.Errorf("quest status %s does not allow %s", status, action)
}

// controlWorkOrderFlowRuntimeV2 applies the user's command to the actual Flow
// before the public quest state is changed. A paused Flow cannot schedule a new
// node while its current model run is moving toward a safe checkpoint.
func (a *App) controlWorkOrderFlowRuntimeV2(ctx context.Context, quest domain.Quest, action string, request WorkOrderQuestControlRequest) (int, error) {
	flowRun, err := a.store.GetFlowRun(ctx, quest.FlowRunID)
	if err != nil {
		return 0, err
	}
	executions, err := a.store.ListExecutions(ctx, quest.WorkspaceID, 500)
	if err != nil {
		return 0, err
	}
	related := make([]domain.ExecutionInstance, 0)
	for _, execution := range executions {
		if execution.QuestID == quest.ID || execution.FlowRunID == flowRun.ID {
			related = append(related, execution)
		}
	}
	if action == "pause" {
		for _, execution := range related {
			if execution.Status == domain.RunRunning && execution.RunID == "" {
				return 0, fmt.Errorf("execution %s uses an interactive CLI runtime that cannot pause; cancel the quest or wait for its checkpoint", execution.ID)
			}
		}
	}

	now := time.Now().UTC()
	switch action {
	case "pause":
		flowRun.Status = domain.RunPaused
		flowRun.FinishedAt = nil
	case "resume":
		flowRun.Status = domain.RunRunning
		flowRun.FinishedAt = nil
	case "cancel":
		flowRun.Status = domain.RunCancelled
		flowRun.Error = "cancelled by user through Master"
		flowRun.FinishedAt = &now
		if !flowRun.StartedAt.IsZero() {
			flowRun.DurationMs = now.Sub(flowRun.StartedAt).Milliseconds()
		}
	}
	if action != "message" {
		if err = a.store.SaveFlowRun(ctx, flowRun); err != nil {
			return 0, err
		}
	}

	affected := 0
	for _, execution := range related {
		switch action {
		case "message":
			if execution.RunID != "" && a.engine.IsActiveRun(execution.RunID) {
				if err = a.engine.InjectRunMessage(execution.RunID, request.Message, ""); err != nil {
					return affected, err
				}
				affected++
			}
		case "pause":
			if execution.RunID != "" && a.engine.IsActiveRun(execution.RunID) {
				if err = a.engine.Pause(execution.RunID); err != nil {
					return affected, err
				}
				affected++
			}
		case "resume":
			if execution.RunID == "" {
				continue
			}
			run, runErr := a.store.GetRun(ctx, execution.RunID)
			if runErr != nil {
				continue
			}
			if run.Status != domain.RunPaused && run.Status != domain.RunInterrupted {
				continue
			}
			if _, err = a.resumeStructuredExecutionIfSafe(&execution, request.APIKey); err != nil {
				return affected, err
			}
			execution.Status = domain.RunRunning
			execution.Error = ""
			execution.FinishedAt = nil
			if err = a.store.SaveExecution(ctx, execution); err != nil {
				return affected, err
			}
			affected++
		case "cancel":
			switch execution.Status {
			case domain.RunCompleted, domain.RunFailed, domain.RunCancelled:
				continue
			}
			if execution.RunID != "" && a.engine.IsActiveRun(execution.RunID) {
				if err = a.engine.Cancel(execution.RunID); err != nil {
					return affected, err
				}
			} else if execution.RunID == "" && execution.Status == domain.RunRunning {
				if err = a.StopExternalCLIExecution(execution.ID); err != nil {
					return affected, err
				}
			} else {
				execution.Status = domain.RunCancelled
				execution.Error = "cancelled by user through Master"
				execution.FinishedAt = &now
				if !execution.StartedAt.IsZero() {
					execution.DurationMs = now.Sub(execution.StartedAt).Milliseconds()
				}
				if err = a.store.SaveExecution(ctx, execution); err != nil {
					return affected, err
				}
			}
			affected++
		}
	}
	if action == "resume" {
		// Планировщик берёт ключ из кэша прогона, а не из запроса. Человек подал
		// credential именно этим нажатием — без этой строки узел снова уходил в
		// waiting_api_key, и «Повторить запуск» ничего не менял.
		if strings.TrimSpace(request.APIKey) != "" {
			a.rememberFlowOrchestratorKey(flowRun.ID, request.APIKey)
		}
		if err = a.scheduleFlowAgentExecutionsFromRun(flowRun); err != nil {
			return affected, err
		}
	}
	return affected, nil
}

func (a *App) WorkOrderQuestV2(ctx context.Context, questID string) (domain.Quest, error) {
	approval, err := a.store.WorkOrderApprovalByQuestV2(ctx, questID)
	if err != nil {
		return domain.Quest{}, err
	}
	quests, err := a.store.ListQuests(ctx, approval.WorkOrder.WorkspaceID)
	if err != nil {
		return domain.Quest{}, err
	}
	for _, quest := range quests {
		if quest.ID == questID {
			return quest, nil
		}
	}
	return domain.Quest{}, errors.Join(sql.ErrNoRows, errors.New("work order quest not found"))
}
