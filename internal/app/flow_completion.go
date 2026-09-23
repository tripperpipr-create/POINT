// Завершение потока: запуск отложенного, приём доказательств, финал квеста.
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/flowruntime"
	"local-agent-workbench/internal/security"
)

func (a *App) LaunchPendingExecution(executionID, apiKey string) (domain.Run, error) {
	exec, err := a.findExecution(executionID)
	if err != nil {
		return domain.Run{}, err
	}
	if exec.Status != domain.RunPending && exec.Status != domain.RunInterrupted {
		return domain.Run{}, fmt.Errorf("execution %s is not pending", executionID)
	}
	if exec.FlowRunID != "" {
		a.rememberFlowOrchestratorKey(exec.FlowRunID, apiKey)
	}
	var contextItems []domain.RunContextInput
	checkKind := ""
	if exec.FlowRunID != "" && exec.FlowNodeID != "" {
		if flowRun, runErr := a.store.GetFlowRun(context.Background(), exec.FlowRunID); runErr == nil {
			checkKind = flowNodeCompletionCheckKind(flowRun, exec.FlowNodeID)
			if flow, ok, flowErr := flowruntime.FlowFromSnapshot(flowRun); flowErr == nil && ok {
				quest := domain.Quest{ID: flowRun.QuestID, Title: flow.Name}
				if flowRun.QuestID != "" {
					if quests, listErr := a.store.ListQuests(context.Background(), exec.WorkspaceID); listErr == nil {
						for _, item := range quests {
							if item.ID == flowRun.QuestID {
								quest = item
								break
							}
						}
					}
				}
				changeSets, _ := a.store.ListChangeSets(context.Background(), flowRun.WorkspaceID)
				contextItems = flowNodeContext(quest, flow, flowRun, exec.FlowNodeID, changeSets)
			}
		}
	}
	return a.StartRun(StartRunRequest{
		ProfileID: exec.ProjectAgentID, Task: exec.Task, APIKey: apiKey,
		QuestID: exec.QuestID, FlowRunID: exec.FlowRunID, FlowNodeID: exec.FlowNodeID, ExecutionID: exec.ID,
		CompletionCheckKind: checkKind,
		ContextItems:        contextItems,
	})
}

func flowNodeCompletionCheckKind(flowRun domain.FlowRun, nodeID string) string {
	if nodeID == "" || flowRun.NodeStates == nil {
		return ""
	}
	state := flowRun.NodeStates[nodeID]
	if lineage, _ := state.Output["sandboxLineage"].(string); lineage == "merged_parallel_join" {
		return "merged-result"
	}
	return ""
}

func (a *App) ResumeFlowApproval(flowRunID, nodeID string, approved bool) (domain.FlowRun, error) {
	runtime := flowruntime.Runtime{Store: a.store}
	flowRun, err := runtime.ResumeAfterApproval(context.Background(), flowRunID, nodeID, approved)
	if err != nil {
		return domain.FlowRun{}, err
	}
	if flowRun.Status == domain.RunFailed || flowRun.Status == domain.RunCancelled {
		a.cancelSiblingFlowExecutions(flowRun.ID, "")
		a.closeUnfinishedFlowChildQuests(flowRun.ID, false)
		if flowRun.QuestID != "" {
			a.finalizeQuestAfterFlow(flowRun.QuestID, false)
		}
		return flowRun, nil
	}
	if flowRun.Status == domain.RunCompleted && flowRun.QuestID != "" {
		a.closeUnfinishedFlowChildQuests(flowRun.ID, true)
		a.finalizeQuestAfterFlow(flowRun.QuestID, true)
		return flowRun, nil
	}
	_ = a.scheduleFlowAgentExecutionsFromRun(flowRun)
	return flowRun, nil
}

func (a *App) attachCompletionEvidence(ctx context.Context, runID string, output map[string]any) {
	if strings.TrimSpace(runID) == "" || output == nil {
		return
	}
	events, err := a.store.ListByRun(ctx, runID)
	if err != nil {
		return
	}
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type != domain.EventCompletionChecked {
			continue
		}
		var payload struct {
			Status   string          `json:"status"`
			Evidence json.RawMessage `json:"evidence"`
		}
		if json.Unmarshal(events[i].Data, &payload) != nil {
			return
		}
		output["completionStatus"] = payload.Status
		if len(payload.Evidence) > 0 {
			var evidence map[string]any
			if json.Unmarshal(payload.Evidence, &evidence) == nil {
				output["completionEvidence"] = evidence
				if status, _ := evidence["status"].(string); status == "needs_review" {
					output["needsReview"] = true
				}
			}
		}
		return
	}
}

func annotateFlowVerifier(flow *domain.FlowGraph, brief *domain.TaskBrief) {
	if flow == nil || brief == nil {
		return
	}
	criteria := make([]any, 0, len(brief.Criteria))
	for _, c := range brief.Criteria {
		criteria = append(criteria, map[string]any{"id": c.ID, "kind": c.Kind, "text": c.Text})
	}
	for i := range flow.Nodes {
		if flow.Nodes[i].Kind != domain.FlowNodeVerifier {
			continue
		}
		if flow.Nodes[i].Config == nil {
			flow.Nodes[i].Config = map[string]any{}
		}
		flow.Nodes[i].Config["requireResult"] = true
		if len(criteria) > 0 {
			flow.Nodes[i].Config["criteria"] = criteria
		}
		if brief.Permissions.WriteFiles && brief.Budget.MaxParallel > 1 {
			flow.Nodes[i].Config["requireMergedResult"] = true
		}
	}
}

func (a *App) finalizeQuestAfterFlow(questID string, success bool) {
	ctx := context.Background()
	quest, questErr := a.questForFinalization(ctx, questID)
	if questErr != nil {
		slog.Warn("quest finalization could not load quest", "quest_id", questID, "error", security.Redact(questErr.Error()))
		return
	}
	if isWorkOrderQuestV2(quest) {
		approval, approvalErr := a.store.WorkOrderApprovalByQuestV2(ctx, questID)
		if approvalErr != nil {
			a.blockWorkOrderFinalizationV2(ctx, quest, "Не удалось подтвердить утверждённый WorkOrder: "+security.Redact(approvalErr.Error()), approvalErr)
			return
		}
		advanced, milestoneErr := a.advanceWorkOrderMilestoneV2(approval, success)
		if milestoneErr != nil {
			a.blockWorkOrderFinalizationV2(ctx, quest, "Не удалось завершить milestone WorkOrder: "+security.Redact(milestoneErr.Error()), milestoneErr)
			return
		}
		if advanced {
			return
		}
		a.finalizeWorkOrderQuestAfterFlowV2(approval, success)
		return
	}
	ws, err := a.requireWorkspace()
	if err != nil {
		return
	}
	quests, err := a.store.ListQuests(context.Background(), ws.ID)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	for _, quest := range quests {
		if quest.ID != questID {
			continue
		}
		quest.Status = domain.QuestFailed
		content := fmt.Sprintf("Quest «%s» finished with status %s. Objectives: %s", quest.Title, quest.Status, strings.Join(quest.Objectives, "; "))
		verified := success
		if quest.Brief != nil {
			verified = false
			content = "Execution ended for «" + quest.Title + "»; task criteria have not been verified."
			if outcome, outcomeErr := a.QuestOutcome(context.Background(), quest.ID); outcomeErr == nil {
				verified = success && outcome.Verified
				content = "Task «" + quest.Title + "»: " + outcome.Honest
				if success && !verified {
					if taskBriefHasManualCriteria(quest.Brief) {
						quest.Status = domain.QuestNeedsReview
					} else {
						quest.Status = domain.QuestBlocked
					}
				}
			}
		} else if success {
			quest.Status = domain.QuestCompleted
		}
		if quest.Kind == "project" {
			verified = a.projectControllerFinished(context.Background(), &quest, success)
		} else if verified {
			quest.Status = domain.QuestCompleted
		}
		if domain.IsTerminalQuestStatus(quest.Status) {
			quest.FinishedAt = &now
		} else {
			quest.FinishedAt = nil
		}
		quest.UpdatedAt = now
		if err := a.store.SaveQuest(context.Background(), quest); err != nil {
			slog.Warn("quest completion not persisted", "quest_id", quest.ID, "status", quest.Status, "error", err)
		}
		if domain.IsTerminalQuestStatus(quest.Status) {
			a.queueQuestSubagentEvaluations(quest.ID)
		}
		if _, err := a.SaveMemory(domain.MemoryRecord{
			WorkspaceID: ws.ID, Kind: domain.MemoryQuest, OwnerID: quest.ID,
			Content: content, Source: "quest-complete", Confidence: 0.8, Pinned: false,
		}); err != nil {
			slog.Warn("quest memory not saved", "quest_id", quest.ID, "kind", domain.MemoryQuest, "error", err)
		}
		if verified && len(quest.DefinitionOfDone) > 0 {
			_, _ = a.SaveMemory(domain.MemoryRecord{
				WorkspaceID: ws.ID, Kind: domain.MemoryProject,
				Content: "Completed DoD for «" + quest.Title + "»: " + strings.Join(quest.DefinitionOfDone, "; "),
				Source:  "quest-dod", Confidence: 0.7, Pinned: true,
			})
		}
		a.finalizeIntakeAfterQuest(quest.ID, verified)
		return
	}
}

func (a *App) questForFinalization(ctx context.Context, questID string) (domain.Quest, error) {
	quest, err := a.store.GetQuest(ctx, questID)
	if err != nil {
		return domain.Quest{}, fmt.Errorf("quest %q was not found: %w", questID, err)
	}
	return quest, nil
}

func isWorkOrderQuestV2(quest domain.Quest) bool {
	if quest.Brief != nil && quest.Brief.WorkOrder != nil {
		return true
	}
	if quest.Controller == nil {
		return false
	}
	source, _ := quest.Controller["source"].(string)
	workOrderID, _ := quest.Controller["workOrderId"].(string)
	return strings.EqualFold(strings.TrimSpace(source), "work_order_v2") || strings.TrimSpace(workOrderID) != ""
}

func (a *App) blockWorkOrderFinalizationV2(ctx context.Context, quest domain.Quest, message string, cause error) {
	redacted := security.Redact(message)
	if _, err := a.setWorkOrderQuestStatusV2(ctx, quest, domain.QuestBlocked, redacted); err != nil {
		slog.Error("work order finalization failure was not persisted", "quest_id", quest.ID, "error", security.Redact(err.Error()))
	}
	if err := a.store.ReleaseWriterLeaseV2(ctx, quest.ID); err != nil {
		slog.Error("work order writer lease was not released", "quest_id", quest.ID, "error", security.Redact(err.Error()))
	}
	workOrderID, _ := quest.Controller["workOrderId"].(string)
	launchMode, _ := quest.Controller["launchMode"].(string)
	slog.Error("work order finalization failed closed",
		"launch_mode", launchMode, "work_order_id", workOrderID, "quest_id", quest.ID,
		"run_id", a.workOrderRunIDV2(ctx, quest), "evidence_gate", "blocked",
		"block_reason", redacted, "error", security.Redact(cause.Error()))
}

func (a *App) workOrderRunIDV2(ctx context.Context, quest domain.Quest) string {
	executions, err := a.store.ListExecutions(ctx, quest.WorkspaceID, 200)
	if err != nil {
		return ""
	}
	for _, execution := range executions {
		if execution.QuestID == quest.ID && strings.TrimSpace(execution.RunID) != "" {
			return execution.RunID
		}
	}
	return ""
}

func taskBriefHasManualCriteria(brief *domain.TaskBrief) bool {
	if brief == nil {
		return false
	}
	for _, criterion := range brief.Criteria {
		if criterion.Kind == "manual" {
			return true
		}
	}
	return false
}

func (a *App) awardProjectAgentOutcome(projectAgentID string, success bool) {
	if projectAgentID == "" {
		return
	}
	agent, err := a.store.GetProjectAgent(context.Background(), projectAgentID)
	if err != nil {
		return
	}
	agent.TasksCompleted++
	if success {
		agent.SuccessCount++
		agent.Experience += 25
	} else {
		agent.Experience += 5
	}
	agent.Level = 1 + agent.Experience/100
	agent.UpdatedAt = time.Now().UTC()
	if err := a.store.SaveProjectAgent(context.Background(), agent); err != nil {
		slog.Warn("agent outcome not persisted", "project_agent_id", agent.ID, "success", success, "error", err)
	}
}
