package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
)

func nextWorkOrderMilestoneV2(order domain.WorkOrder, runtimes []domain.MilestoneRuntime) (domain.MilestonePlan, domain.MilestoneRuntime, bool) {
	byID := make(map[string]domain.MilestoneRuntime, len(runtimes))
	for _, runtime := range runtimes {
		byID[runtime.MilestoneID] = runtime
	}
	for _, milestone := range order.Milestones {
		runtime, exists := byID[milestone.ID]
		if !exists || runtime.Status != domain.QuestDraft {
			continue
		}
		ready := true
		for _, dependency := range milestone.DependsOn {
			if byID[dependency].Status != domain.QuestCompleted {
				ready = false
				break
			}
		}
		if ready {
			return milestone, runtime, true
		}
	}
	return domain.MilestonePlan{}, domain.MilestoneRuntime{}, false
}

func taskBriefForMilestoneV2(order domain.WorkOrder, milestone domain.MilestonePlan) (domain.TaskBrief, error) {
	brief, err := taskBriefFromWorkOrderV2(order)
	if err != nil {
		return domain.TaskBrief{}, err
	}
	brief.State = "ready"
	brief.ApprovedVersion, brief.ApprovedDigest = 0, ""
	brief.Goal = milestone.Goal
	if len(milestone.Scope) > 0 {
		brief.Scope = append([]string(nil), milestone.Scope...)
	}
	wanted := make(map[string]bool, len(milestone.CriterionIDs))
	for _, id := range milestone.CriterionIDs {
		wanted[id] = true
	}
	criteria := make([]domain.AcceptanceCriterion, 0, len(wanted))
	for _, criterion := range order.Criteria {
		if wanted[criterion.ID] {
			criteria = append(criteria, criterion)
		}
	}
	if len(criteria) == 0 {
		return domain.TaskBrief{}, errors.New("milestone has no acceptance criteria")
	}
	brief.Criteria = criteria
	if milestone.Budget.Tokens > 0 {
		brief.Budget.Tokens = milestone.Budget.Tokens
	}
	if milestone.Budget.CostCents > 0 {
		brief.Budget.CostCents = milestone.Budget.CostCents
	}
	if milestone.Budget.ActiveSeconds > 0 {
		brief.Budget.ActiveSeconds = milestone.Budget.ActiveSeconds
	}
	if milestone.Budget.MaxParallel > 0 {
		brief.Budget.MaxParallel = milestone.Budget.MaxParallel
	}
	if milestone.Budget.MaxAttempts > 0 {
		brief.Budget.MaxAttempts = milestone.Budget.MaxAttempts
	}
	return domain.ApproveTaskBrief(brief)
}

func (a *App) markWorkOrderMilestoneV2(ctx context.Context, approval domain.WorkOrderApproval, runtime domain.MilestoneRuntime, status domain.QuestStatus, flowID, flowRunID string) error {
	now := time.Now().UTC()
	runtime.Status, runtime.FlowID, runtime.FlowRunID, runtime.UpdatedAt = status, flowID, flowRunID, now
	if status == domain.QuestRunning {
		runtime.Attempt++
		runtime.StartedAt = &now
		runtime.FinishedAt = nil
	} else if status == domain.QuestCompleted || status == domain.QuestNeedsReview || status == domain.QuestBlocked || status == domain.QuestCancelled {
		runtime.FinishedAt = &now
	}
	return a.store.SaveMilestoneRuntimeV2(ctx, approval.QuestID, approval.WorkOrder.ID, approval.WorkOrder.Version, runtime)
}

// advanceWorkOrderMilestoneV2 records the finished Flow and starts only the
// next dependency-ready milestone. Future milestones remain goals until this
// point, so their detailed Flow can be replanned without changing scope.
func (a *App) advanceWorkOrderMilestoneV2(approval domain.WorkOrderApproval, success bool) (bool, error) {
	ctx := context.Background()
	quest, err := a.workOrderQuestV2(ctx, approval.WorkOrder.WorkspaceID, approval.QuestID)
	if err != nil {
		return false, err
	}
	if quest.FlowRunID == "" {
		return false, nil
	}
	runtimes, err := a.store.ListMilestoneRuntimesV2(ctx, quest.ID, approval.WorkOrder.Version)
	if err != nil {
		return false, err
	}
	currentIndex := -1
	for index := range runtimes {
		if runtimes[index].FlowRunID == quest.FlowRunID {
			currentIndex = index
			break
		}
	}
	// A deterministic first node can finish before launchApprovedWorkOrderV2
	// returns and records the Flow identity on its milestone runtime.  The root
	// quest already carries the approved current milestone, so use that durable
	// link to close the race instead of leaving the WorkOrder in preflight.
	if currentIndex < 0 && quest.Controller != nil {
		currentMilestoneID, _ := quest.Controller["currentMilestoneId"].(string)
		for index := range runtimes {
			if currentMilestoneID != "" && runtimes[index].MilestoneID == currentMilestoneID {
				currentIndex = index
				break
			}
		}
	}
	if currentIndex < 0 {
		return false, errors.New("current work order milestone runtime was not found")
	}
	current := runtimes[currentIndex]
	if current.FlowID == "" {
		current.FlowID = quest.FlowID
	}
	if current.FlowRunID == "" {
		current.FlowRunID = quest.FlowRunID
	}
	if success {
		if err = a.markWorkOrderMilestoneV2(ctx, approval, current, domain.QuestCompleted, current.FlowID, current.FlowRunID); err != nil {
			return false, err
		}
		runtimes[currentIndex].Status = domain.QuestCompleted
	} else {
		if err = a.markWorkOrderMilestoneV2(ctx, approval, current, domain.QuestBlocked, current.FlowID, current.FlowRunID); err != nil {
			return false, err
		}
		return false, nil
	}
	if _, _, exists := nextWorkOrderMilestoneV2(approval.WorkOrder, runtimes); !exists {
		return false, nil
	}
	apiKey := a.flowOrchestratorKey(current.FlowRunID)
	a.clearFlowOrchestratorKey(current.FlowRunID)
	quest.FlowID, quest.FlowRunID = "", ""
	quest.ControllerState = "milestone_transition"
	if quest.Controller == nil {
		quest.Controller = map[string]any{}
	}
	quest.Controller["statusMessage"] = "Предыдущий milestone проверен; строится Flow следующего milestone"
	quest.UpdatedAt = time.Now().UTC()
	if err = a.store.SaveQuest(ctx, quest); err != nil {
		return false, err
	}
	result, launchErr := a.launchApprovedWorkOrderV2(ctx, approval, quest, apiKey)
	if launchErr != nil {
		message := "Следующий milestone не запущен: " + strings.TrimSpace(launchErr.Error())
		_, _ = a.setWorkOrderQuestStatusV2(ctx, quest, domain.QuestBlocked, message)
		return true, nil
	}
	if result.FlowRun != nil && flowRunNeedsUserV2(*result.FlowRun) {
		latest, loadErr := a.workOrderQuestV2(ctx, approval.WorkOrder.WorkspaceID, approval.QuestID)
		if loadErr == nil {
			_, _ = a.setWorkOrderQuestStatusV2(ctx, latest, domain.QuestAwaitingUser, "Следующему milestone требуется credential или решение пользователя")
		}
	}
	return true, nil
}
