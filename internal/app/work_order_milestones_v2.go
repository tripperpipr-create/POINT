package app

import (
	"context"
	"errors"
	"fmt"
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
	} else if status == domain.QuestCompleted || status == domain.QuestBlocked || status == domain.QuestCancelled {
		runtime.FinishedAt = &now
	}
	return a.store.SaveMilestoneRuntimeV2(ctx, approval.QuestID, approval.WorkOrder.ID, approval.WorkOrder.Version, runtime)
}

// advanceWorkOrderMilestoneV2 records the finished Flow and starts only the
// next dependency-ready milestone. Future milestones remain goals until this
// point, so their detailed Flow can be replanned without changing scope.
func (a *App) advanceWorkOrderMilestoneV2(approval domain.WorkOrderApproval, success bool) bool {
	ctx := context.Background()
	quest, err := a.workOrderQuestV2(ctx, approval.WorkOrder.WorkspaceID, approval.QuestID)
	if err != nil || quest.FlowRunID == "" {
		return false
	}
	runtimes, err := a.store.ListMilestoneRuntimesV2(ctx, quest.ID, approval.WorkOrder.Version)
	if err != nil {
		return false
	}
	currentIndex := -1
	for index := range runtimes {
		if runtimes[index].FlowRunID == quest.FlowRunID {
			currentIndex = index
			break
		}
	}
	if currentIndex < 0 {
		return false
	}
	current := runtimes[currentIndex]
	if success {
		_ = a.markWorkOrderMilestoneV2(ctx, approval, current, domain.QuestCompleted, current.FlowID, current.FlowRunID)
		runtimes[currentIndex].Status = domain.QuestCompleted
	} else {
		_ = a.markWorkOrderMilestoneV2(ctx, approval, current, domain.QuestBlocked, current.FlowID, current.FlowRunID)
		return false
	}
	if _, _, exists := nextWorkOrderMilestoneV2(approval.WorkOrder, runtimes); !exists {
		return false
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
		return true
	}
	result, launchErr := a.launchApprovedWorkOrderV2(ctx, approval, quest, apiKey)
	if launchErr != nil {
		message := "Следующий milestone не запущен: " + strings.TrimSpace(launchErr.Error())
		_, _ = a.setWorkOrderQuestStatusV2(ctx, quest, domain.QuestBlocked, message)
		return true
	}
	if result.FlowRun != nil && flowRunNeedsUserV2(*result.FlowRun) {
		latest, loadErr := a.workOrderQuestV2(ctx, approval.WorkOrder.WorkspaceID, approval.QuestID)
		if loadErr == nil {
			_, _ = a.setWorkOrderQuestStatusV2(ctx, latest, domain.QuestAwaitingUser, "Следующему milestone требуется credential или решение пользователя")
		}
	}
	return true
}

func milestonePlanByIDV2(order domain.WorkOrder, id string) (domain.MilestonePlan, error) {
	for _, milestone := range order.Milestones {
		if milestone.ID == id {
			return milestone, nil
		}
	}
	return domain.MilestonePlan{}, fmt.Errorf("milestone %q is not in the approved WorkOrder", id)
}
