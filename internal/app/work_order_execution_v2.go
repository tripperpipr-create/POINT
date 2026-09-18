package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/security"
)

// workOrderLaunchBudget ограничивает фоновый запуск утверждённого наряда.
// Планировщик milestone обращается к модели, и на локальном endpoint это
// минуты, а не секунды.
const workOrderLaunchBudget = 30 * time.Minute

const workOrderLaunchStartedMessage = "Готовим план выполнения"

// workOrderPlannerFallbackNote предупреждает, что Flow собран движком Point, а
// не моделью: план шаблонный, и человек читает его с этой поправкой.
const workOrderPlannerFallbackNote = "План собран движком Point: модель не ответила"

// resumeApprovedWorkOrderV2 crosses the durable approval boundary into the
// existing execution engine. Approval remains successful even when preflight
// fails; the quest is then blocked with a redacted reason instead of returning
// an HTTP error that could tempt a client to create a second approval.
//
// Сам запуск живёт дольше запроса. Пока он шёл внутри HTTP-вызова, вебвью
// обрывал его своим сроком в 20 с, отменённый контекст уносил с собой и запись
// отказа — квест навсегда оставался в `preflight` без причины и без кнопки,
// потому что `preflight` не возобновляют. Теперь запрос только начинает работу,
// а исход — `running` или `blocked` с причиной — пишет фоновая задача.
func (a *App) resumeApprovedWorkOrderV2(ctx context.Context, approval domain.WorkOrderApproval, apiKey string) (domain.WorkOrderApproval, error) {
	quest, err := a.workOrderQuestV2(ctx, approval.WorkOrder.WorkspaceID, approval.QuestID)
	if err != nil {
		return approval, err
	}
	approval.Status = string(quest.Status)
	approval.FlowID, approval.FlowRunID = quest.FlowID, quest.FlowRunID
	if quest.Controller != nil {
		if message, _ := quest.Controller["statusMessage"].(string); message != "" {
			approval.Message = message
		}
	}
	if quest.Status != domain.QuestPreflight || quest.FlowID != "" || quest.FlowRunID != "" {
		approval.WorkOrder.Runtime = workOrderRuntimeV2(quest, approval.Message)
		approval.WorkOrder.Runtime.AgentIDs = approval.AgentIDs
		return approval, nil
	}

	quest, approval.Message = a.startWorkOrderLaunchV2(ctx, approval, quest, apiKey)
	approval.Status = string(quest.Status)
	approval.FlowID, approval.FlowRunID = quest.FlowID, quest.FlowRunID
	approval.WorkOrder.Runtime = workOrderRuntimeV2(quest, approval.Message)
	approval.WorkOrder.Runtime.AgentIDs = approval.AgentIDs
	return approval, nil
}

// startWorkOrderLaunchV2 отдаёт запуск фоновой задаче и возвращает то, что видно
// человеку прямо сейчас. Повторное нажатие не плодит вторую попытку: запуск
// один на квест.
func (a *App) startWorkOrderLaunchV2(ctx context.Context, approval domain.WorkOrderApproval, quest domain.Quest, apiKey string) (domain.Quest, string) {
	a.workOrderLaunchMu.Lock()
	if a.workOrderLaunchStopping {
		a.workOrderLaunchMu.Unlock()
		return quest, "Ядро останавливается; запуск продолжится после перезапуска"
	}
	if _, running := a.workOrderLaunchCancels[quest.ID]; running {
		a.workOrderLaunchMu.Unlock()
		return quest, workOrderLaunchStartedMessage
	}
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), workOrderLaunchBudget)
	if a.workOrderLaunchCancels == nil {
		a.workOrderLaunchCancels = map[string]context.CancelFunc{}
	}
	a.workOrderLaunchCancels[quest.ID] = cancel
	a.workOrderLaunchMu.Unlock()

	// Сообщение пишется до старта: человек должен видеть, что происходит, даже
	// если первый запрос к модели займёт минуты.
	if started, err := a.setWorkOrderQuestStatusV2(runCtx, quest, domain.QuestPreflight, workOrderLaunchStartedMessage); err == nil {
		quest = started
	}
	a.workOrderLaunchWG.Add(1)
	go func(quest domain.Quest) {
		defer a.workOrderLaunchWG.Done()
		defer cancel()
		defer func() {
			a.workOrderLaunchMu.Lock()
			delete(a.workOrderLaunchCancels, quest.ID)
			a.workOrderLaunchMu.Unlock()
		}()
		a.runWorkOrderLaunchV2(runCtx, approval, quest, apiKey)
	}(quest)
	return quest, workOrderLaunchStartedMessage
}

// runWorkOrderLaunchV2 доводит запуск до состояния, которое можно прочитать на
// карточке. Исход пишется контекстом без отмены: срок запуска не должен стереть
// причину отказа.
func (a *App) runWorkOrderLaunchV2(ctx context.Context, approval domain.WorkOrderApproval, quest domain.Quest, apiKey string) {
	result, launchErr := a.launchApprovedWorkOrderV2(ctx, approval, quest, apiKey)
	writeCtx := context.WithoutCancel(ctx)
	latest, err := a.workOrderQuestV2(writeCtx, approval.WorkOrder.WorkspaceID, approval.QuestID)
	if err != nil {
		slog.Error("work order launch state unreadable", "quest_id", approval.QuestID, "error", err)
		return
	}
	if launchErr != nil {
		message := security.Redact(launchErr.Error())
		if message == "" {
			message = "preflight failed"
		}
		if latest.Status != domain.QuestPreflight {
			// Пока шёл запуск, человек успел отменить или поставить на паузу.
			// Его решение старше нашего отказа.
			slog.Warn("work order launch failed after the human moved the quest", "quest_id", approval.QuestID, "status", latest.Status, "error", launchErr)
			return
		}
		if _, saveErr := a.setWorkOrderQuestStatusV2(writeCtx, latest, domain.QuestBlocked, message); saveErr != nil {
			slog.Error("work order blocked state not persisted", "quest_id", approval.QuestID, "error", saveErr, "launch_error", launchErr)
		}
		return
	}
	var run *domain.FlowRun
	if result.FlowRun != nil {
		if loaded, loadErr := a.store.GetFlowRun(writeCtx, result.FlowRun.ID); loadErr == nil {
			run = &loaded
		}
	}
	status, message, note := workOrderLaunchOutcomeV2(run, result.PlannerFallback)
	if note != "" {
		if latest.Controller == nil {
			latest.Controller = map[string]any{}
		}
		latest.Controller["plannerNote"] = note
	}
	if _, err = a.setWorkOrderQuestStatusV2(writeCtx, latest, status, message); err != nil {
		slog.Error("work order running state not persisted", "quest_id", approval.QuestID, "error", err)
	}
}

// cancelWorkOrderLaunchV2 останавливает фоновый запуск конкретного квеста:
// человек нажал «Отменить», и ждать планировщика больше незачем.
func (a *App) cancelWorkOrderLaunchV2(questID string) {
	a.workOrderLaunchMu.Lock()
	cancel := a.workOrderLaunchCancels[questID]
	a.workOrderLaunchMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// waitWorkOrderLaunches ждёт завершения фоновых запусков. Нужен проверкам,
// которые читают исход сразу после утверждения наряда.
func (a *App) waitWorkOrderLaunches() { a.workOrderLaunchWG.Wait() }

func (a *App) stopWorkOrderLaunches() {
	a.workOrderLaunchMu.Lock()
	a.workOrderLaunchStopping = true
	for _, cancel := range a.workOrderLaunchCancels {
		cancel()
	}
	a.workOrderLaunchMu.Unlock()
	a.workOrderLaunchWG.Wait()
}

func workOrderRuntimeV2(quest domain.Quest, message string) *domain.WorkOrderRuntime {
	return &domain.WorkOrderRuntime{QuestID: quest.ID, Status: quest.Status, Message: security.Redact(message), FlowID: quest.FlowID, FlowRunID: quest.FlowRunID, UpdatedAt: quest.UpdatedAt}
}

func (a *App) launchApprovedWorkOrderV2(ctx context.Context, approval domain.WorkOrderApproval, quest domain.Quest, apiKey string) (QuestProposalResult, error) {
	order := approval.WorkOrder
	view, err := a.OpenWorkspace(order.Workspace.Path)
	if err != nil {
		return QuestProposalResult{}, fmt.Errorf("open approved workspace: %w", err)
	}
	if view.Workspace.ID != order.WorkspaceID {
		return QuestProposalResult{}, fmt.Errorf("approved workspace identity changed")
	}
	if len(approval.AgentIDs) == 0 {
		return QuestProposalResult{}, fmt.Errorf("approved roster has no runnable agents")
	}
	runtimes, err := a.store.ListMilestoneRuntimesV2(ctx, quest.ID, order.Version)
	if err != nil {
		return QuestProposalResult{}, fmt.Errorf("load milestone runtime: %w", err)
	}
	milestone, milestoneRuntime, ok := nextWorkOrderMilestoneV2(order, runtimes)
	if !ok {
		return QuestProposalResult{}, fmt.Errorf("approved WorkOrder has no dependency-ready milestone")
	}
	brief, err := taskBriefForMilestoneV2(order, milestone)
	if err != nil {
		return QuestProposalResult{}, err
	}
	quest.Brief = &brief
	quest.Kind = "project"
	quest.ControllerState = controllerExecutingWave
	if quest.Controller == nil {
		quest.Controller = map[string]any{}
	}
	quest.Controller["source"] = "work_order_v2"
	quest.Controller["currentMilestoneId"] = milestone.ID
	quest.Controller["statusMessage"] = "Создаётся Flow milestone: " + milestone.Goal
	initializeQuestController(&quest)
	proposal := domain.QuestProposal{
		ID:                 "work-order-v2-" + order.ID,
		WorkspaceID:        order.WorkspaceID,
		Title:              milestone.Goal,
		Task:               milestone.Goal,
		Objectives:         append([]string(nil), brief.Scope...),
		Constraints:        append(append([]string(nil), order.OutOfScope...), order.Assumptions...),
		DefinitionOfDone:   criterionTextsV2(brief.Criteria),
		Importance:         domain.QuestNormal,
		EstimateTokens:     order.Budget.Tokens,
		TeamAgentIDs:       append([]string(nil), approval.AgentIDs...),
		TeamAgentIDsLocked: true,
		Brief:              &brief,
	}
	if order.Budget.CostCents > 0 {
		cost := order.Budget.CostCents
		proposal.EstimateCents = &cost
	}
	result, err := a.startQuestFromProposalUsingQuest(ctx, view.Workspace, proposal, true, true, apiKey, &quest, false)
	if err != nil {
		return result, err
	}
	if result.FlowRun == nil || result.Flow == nil {
		return result, errors.New("milestone planner did not create a Flow runtime")
	}
	if err = a.markWorkOrderMilestoneV2(ctx, approval, milestoneRuntime, domain.QuestRunning, result.Flow.ID, result.FlowRun.ID); err != nil {
		return result, fmt.Errorf("persist milestone runtime: %w", err)
	}
	return result, nil
}

func taskBriefFromWorkOrderV2(order domain.WorkOrder) (domain.TaskBrief, error) {
	decisions := make([]domain.BriefDecision, 0, len(order.Assumptions))
	for _, assumption := range order.Assumptions {
		decisions = append(decisions, domain.BriefDecision{Topic: "Предположение", Decision: assumption, Source: "work_order_v2"})
	}
	hosts := make([]string, 0, len(order.Network))
	for _, grant := range order.Network {
		host := grant.Host
		if name, _, err := net.SplitHostPort(grant.Host); err == nil {
			host = name
		}
		hosts = append(hosts, host)
	}
	remotes := []string{}
	for _, source := range order.Sources {
		if source.Kind == "git" && strings.TrimSpace(source.Locator) != "" {
			remotes = append(remotes, source.Locator)
		}
	}
	draft := domain.TaskBrief{
		SourceRequest: order.Goal,
		Version:       order.Version,
		State:         "ready",
		Mode:          domain.TaskModeProject,
		Goal:          order.Goal,
		ResultKind:    "workspace_change",
		Scope:         append([]string(nil), order.Scope...),
		OutOfScope:    append([]string(nil), order.OutOfScope...),
		Decisions:     decisions,
		Criteria:      append([]domain.AcceptanceCriterion(nil), order.Criteria...),
		Permissions: domain.TaskPermissions{
			WriteFiles: true, ExecuteCommands: true,
			ProvisionProjectAgents: len(order.Roster.Temporary) > 0,
			NetworkHosts:           hosts, ConfirmedGitRemotes: remotes,
		},
		Budget: domain.TaskBudget{
			Tokens: order.Budget.Tokens, CostCents: order.Budget.CostCents,
			ActiveSeconds: order.Budget.ActiveSeconds, MaxParallel: order.Budget.MaxParallel,
			MaxReplans: order.Budget.MaxReplans, MaxAttempts: order.Budget.MaxAttempts,
			MaxProjectAgents: order.Budget.MaxProjectAgents,
		},
		WorkOrder: &domain.WorkOrderExecutionContract{
			ID: order.ID, Version: order.Version, Digest: domain.WorkOrderDigest(order),
			SourceDigest: domain.WorkOrderSourceDigest(order),
			Sources:      append([]domain.SourceSnapshotRef(nil), order.Sources...),
			Milestones:   append([]domain.MilestonePlan(nil), order.Milestones...),
			Workspace:    order.Workspace, Stack: order.Stack, Routing: order.Routing,
			Network: append([]domain.NetworkGrant(nil), order.Network...),
			Secrets: append([]domain.SecretRequirement(nil), order.Secrets...), Completion: order.Completion, Delivery: order.Delivery,
		},
	}
	approved, err := domain.ApproveTaskBrief(draft)
	if err != nil {
		return domain.TaskBrief{}, fmt.Errorf("compile approved task contract: %w", err)
	}
	return approved, nil
}

func criterionTextsV2(criteria []domain.AcceptanceCriterion) []string {
	result := make([]string, 0, len(criteria))
	for _, criterion := range criteria {
		result = append(result, criterion.Text)
	}
	return result
}

// workOrderLaunchOutcomeV2 решает, что человек увидит после запуска. Порядок
// разбора: провал запуска старше ожидания человека, иначе отказ привязки модели
// прячется за просьбой о ключе, а квест остаётся running без единого признака
// беды. Третье возвращаемое значение — примечание планировщика для карточки.
func workOrderLaunchOutcomeV2(run *domain.FlowRun, plannerFallback string) (domain.QuestStatus, string, string) {
	status, message := domain.QuestRunning, "План создан, выполнение началось"
	if run != nil {
		if startError, failed := flowRunStartFailureV2(*run); failed {
			status = domain.QuestBlocked
			message = "Исполнителя не удалось запустить: " + security.Redact(startError)
		} else if flowRunNeedsUserV2(*run) {
			status, message = domain.QuestAwaitingUser, "Для запуска исполнителя требуется credential из SecretStorage или активная авторизация CLI"
		}
	}
	// План мог оказаться шаблонным: модель не ответила, и движок собрал Flow
	// сам. Человек обязан знать это, иначе он читает чужой план как свой.
	note := ""
	if fallback := strings.TrimSpace(plannerFallback); fallback != "" {
		note = workOrderPlannerFallbackNote + ": " + security.Redact(fallback)
		message = strings.TrimSpace(message + " · " + note)
	}
	return status, message, note
}

// flowRunStartFailureV2 находит узел, который не смог стартовать, и отдаёт
// текст отказа. Без этого квест остаётся running, а причина живёт только в
// nodeStates и не видна ни на карточке, ни в статусе.
func flowRunStartFailureV2(run domain.FlowRun) (string, bool) {
	for _, state := range run.NodeStates {
		if reason, _ := state.Output["waitReason"].(string); reason != "start_failed" {
			continue
		}
		startError, _ := state.Output["startError"].(string)
		if strings.TrimSpace(startError) == "" {
			startError = "запуск исполнителя не состоялся"
		}
		return startError, true
	}
	return "", false
}

func flowRunNeedsUserV2(run domain.FlowRun) bool {
	for _, state := range run.NodeStates {
		if reason, _ := state.Output["waitReason"].(string); reason == "waiting_api_key" || reason == "waiting_interactive_cursor" || reason == "sandbox_merge_conflict" {
			return true
		}
	}
	return false
}

func (a *App) workOrderQuestV2(ctx context.Context, workspaceID, questID string) (domain.Quest, error) {
	quests, err := a.store.ListQuests(ctx, workspaceID)
	if err != nil {
		return domain.Quest{}, err
	}
	for _, quest := range quests {
		if quest.ID == questID {
			return quest, nil
		}
	}
	return domain.Quest{}, fmt.Errorf("approved quest %q was not found", questID)
}

func (a *App) setWorkOrderQuestStatusV2(ctx context.Context, quest domain.Quest, status domain.QuestStatus, message string) (domain.Quest, error) {
	if quest.Status != status && !domain.CanTransitionWorkOrderQuest(quest.Status, status) {
		return quest, fmt.Errorf("invalid work order quest transition %s -> %s", quest.Status, status)
	}
	quest.Status, quest.ControllerState = status, string(status)
	if quest.Controller == nil {
		quest.Controller = map[string]any{}
	}
	quest.Controller["statusMessage"] = security.Redact(message)
	quest.UpdatedAt = time.Now().UTC()
	if domain.IsTerminalQuestStatus(status) {
		finished := quest.UpdatedAt
		quest.FinishedAt = &finished
	} else {
		quest.FinishedAt = nil
	}
	if err := a.store.SaveQuest(ctx, quest); err != nil {
		return quest, err
	}
	return quest, nil
}
