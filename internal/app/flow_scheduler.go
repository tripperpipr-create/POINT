// Планировщик узлов потока: кто из агентов может начать прямо сейчас.
//
// Узел ждёт по нескольким причинам сразу — не готов апстрим, занят корень
// записи, упёрлись в предел параллельности, нет ключа. Причина сохраняется в
// прогоне: интерфейс показывает её человеку, а не «ничего не происходит».
package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/flowruntime"
	"local-agent-workbench/internal/orchestrator"
	"local-agent-workbench/internal/policy"
	"local-agent-workbench/internal/sandbox"
)

func (a *App) scheduleFlowAgentExecutions(quest domain.Quest, flow domain.FlowGraph, flowRun domain.FlowRun, apiKey string) error {
	a.rememberFlowOrchestratorKey(flowRun.ID, apiKey)
	return a.scheduleWaitingAgentNodes(quest, flow, flowRun, apiKey)
}

// ResumeActiveFlowRuns restores persisted orchestration after the local core restarts.
func (a *App) ResumeActiveFlowRuns(workspaceID string) error {
	ctx := context.Background()
	runs, err := a.store.ListFlowRuns(ctx, workspaceID, 500)
	if err != nil {
		return err
	}
	executions, err := a.store.ListExecutions(ctx, workspaceID, 500)
	if err != nil {
		return err
	}
	executionByID := make(map[string]domain.ExecutionInstance, len(executions))
	for _, execution := range executions {
		executionByID[execution.ID] = execution
	}
	runtime := flowruntime.Runtime{Store: a.store}
	for _, flowRun := range runs {
		if flowRun.Status != domain.RunRunning && flowRun.Status != domain.RunWaiting {
			continue
		}
		current := flowRun
		for nodeID, state := range current.NodeStates {
			if state.Status != "waiting_agent" {
				continue
			}
			executionID, _ := state.Output["executionId"].(string)
			execution := executionByID[executionID]
			if execution.ID == "" {
				continue
			}
			if execution.Status == domain.RunRunning && !a.engine.IsActiveRun(execution.RunID) {
				execution.Status = domain.RunInterrupted
				execution.Error = "local core restarted; execution is ready to resume"
				if saveErr := a.store.SaveExecution(ctx, execution); saveErr != nil {
					return saveErr
				}
				executionByID[execution.ID] = execution
			}
			switch execution.Status {
			case domain.RunCompleted, domain.RunFailed, domain.RunCancelled:
				if execution.Status == domain.RunFailed {
					if recoveredRun, recovered, recoveryErr := a.recoverFlowNodeFailure(current.ID, nodeID, execution); recoveryErr != nil {
						return recoveryErr
					} else if recovered {
						current = recoveredRun
						continue
					}
				}
				childStatus := domain.QuestFailed
				if execution.Status == domain.RunCompleted {
					childStatus = domain.QuestCompleted
				} else if execution.Status == domain.RunCancelled {
					childStatus = domain.QuestCancelled
				}
				a.setFlowChildQuestStatus(current.ID, nodeID, childStatus)
				output := map[string]any{
					"runId": execution.RunID, "result": execution.Result, "status": execution.Status,
				}
				a.attachCompletionEvidence(ctx, execution.RunID, output)
				current, err = runtime.CompleteAgentNode(ctx, current.ID, nodeID, execution.Status == domain.RunCompleted, output)
				if err != nil {
					return err
				}
			}
		}
		current, err = runtime.Tick(ctx, current.ID)
		if err != nil {
			return err
		}
		if current.Status == domain.RunRunning || current.Status == domain.RunWaiting {
			if err = a.scheduleFlowAgentExecutionsFromRun(current); err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *App) scheduleFlowAgentExecutionsFromRun(flowRun domain.FlowRun) error {
	if flowRun.Status == domain.RunFailed || flowRun.Status == domain.RunCancelled || flowRun.Status == domain.RunCompleted {
		a.clearFlowOrchestratorKey(flowRun.ID)
		return nil
	}
	flow, ok, err := flowruntime.FlowFromSnapshot(flowRun)
	if err != nil {
		return err
	}
	if !ok {
		flow, err = a.store.GetFlow(context.Background(), flowRun.FlowID)
		if err != nil {
			return err
		}
	}
	quest := domain.Quest{ID: flowRun.QuestID, Title: flow.Name}
	if flowRun.QuestID != "" {
		quests, qErr := a.store.ListQuests(context.Background(), flowRun.WorkspaceID)
		if qErr == nil {
			for _, item := range quests {
				if item.ID == flowRun.QuestID {
					quest = item
					break
				}
			}
		}
	}
	return a.scheduleFlowAgentExecutions(quest, flow, flowRun, a.flowOrchestratorKey(flowRun.ID))
}

// rootFlowSeedPath returns the immutable starting snapshot of the earliest
// root execution in this FlowRun. Every root branch is forked from that same
// snapshot, even when parallelism limits schedule the branches at different
// times or the user edits the live workspace between them.
func (a *App) rootFlowSeedPath(flowRun domain.FlowRun) string {
	executions, err := a.store.ListExecutions(context.Background(), flowRun.WorkspaceID, 500)
	if err != nil {
		return ""
	}
	var root *domain.ExecutionInstance
	var rootRecord domain.SandboxRecord
	for index := range executions {
		execution := &executions[index]
		if execution.FlowRunID != flowRun.ID {
			continue
		}
		record, loadErr := a.store.GetSandbox(context.Background(), execution.SandboxID)
		if loadErr != nil || len(sandboxParentExecutionIDs(record)) > 0 {
			continue
		}
		if root == nil || execution.StartedAt.Before(root.StartedAt) || (execution.StartedAt.Equal(root.StartedAt) && execution.ID < root.ID) {
			root = execution
			rootRecord = record
		}
	}
	if root == nil {
		return ""
	}
	path := strings.TrimSpace(rootRecord.BaselinePath)
	if info, statErr := os.Stat(path); statErr == nil && info.IsDir() {
		return path
	}
	return ""
}

// Причина ожидания узла живёт только в записи прогона: интерфейс читает
// waitReason оттуда и по нему объясняет человеку, почему агент ещё не начал.
// Отказ записи означает узел, «висящий без причины», — молчать здесь нельзя.
func (a *App) saveFlowRunWaitReason(flowRun domain.FlowRun, nodeID, reason string) {
	if err := a.store.SaveFlowRun(context.Background(), flowRun); err != nil {
		slog.Warn("flow node wait reason not persisted", "flow_run_id", flowRun.ID, "flow_node_id", nodeID, "wait_reason", reason, "error", err)
	}
}

func (a *App) scheduleWaitingAgentNodes(quest domain.Quest, flow domain.FlowGraph, flowRun domain.FlowRun, apiKey string) error {
	if flowRun.Status == domain.RunFailed || flowRun.Status == domain.RunCancelled || flowRun.Status == domain.RunCompleted {
		return nil
	}
	maxConcurrent := 4
	if cfg, ok := a.loadOrchestratorConfig(flowRun.WorkspaceID); ok {
		maxConcurrent = orchestrator.MaxConcurrentAgents(cfg)
	}
	if quest.Brief != nil && quest.Brief.Budget.MaxParallel < maxConcurrent {
		maxConcurrent = quest.Brief.Budget.MaxParallel
	}
	changeSets, _ := a.store.ListChangeSets(context.Background(), flowRun.WorkspaceID)
	childQuests, err := a.ensureFlowNodeQuests(quest, flow, flowRun)
	if err != nil {
		return err
	}
	runningOrStarting := 0
	for _, node := range flow.Nodes {
		if node.Kind != domain.FlowNodeAgent && node.Kind != domain.FlowNodeTool {
			continue
		}
		state := flowRun.NodeStates[node.ID]
		if state.Status != "waiting_agent" {
			continue
		}
		if executionID, _ := state.Output["executionId"].(string); executionID != "" {
			if exec, err := a.findExecution(executionID); err == nil && exec.Status == domain.RunRunning {
				runningOrStarting++
			}
		}
	}

	seen := map[string]bool{}
	for _, node := range flow.Nodes {
		if node.Kind != domain.FlowNodeAgent && node.Kind != domain.FlowNodeTool {
			continue
		}
		state := flowRun.NodeStates[node.ID]
		if state.Status != "waiting_agent" {
			continue
		}
		effectiveAgentID := node.AgentID
		if candidate, _ := state.Output["effectiveAgentId"].(string); strings.TrimSpace(candidate) != "" {
			effectiveAgentID = strings.TrimSpace(candidate)
		}
		if effectiveAgentID == "" {
			runtime := flowruntime.Runtime{Store: a.store}
			_, _ = runtime.CompleteAgentNode(context.Background(), flowRun.ID, node.ID, false, map[string]any{
				"error": "agentId is required for agent and tool nodes",
			})
			continue
		}
		needsSchedule, _ := state.Output["needsSchedule"].(bool)
		key := node.ID + ":" + effectiveAgentID
		if seen[key] {
			continue
		}
		seen[key] = true
		task := quest.Title
		if node.Name != "" {
			task = node.Name + ": " + quest.Title
		}
		if instruction, ok := node.Config["instruction"].(string); ok && strings.TrimSpace(instruction) != "" {
			task = strings.TrimSpace(instruction)
		} else if inst := orchestrator.DefaultStageInstruction(domain.FlowNodeStageRole(node)); inst != "" {
			task = inst
		}
		if role := domain.FlowNodeStageRole(node); (role == domain.StageRoleImplement || role == domain.StageRoleIntegrate) && quest.Title != "" && !strings.Contains(task, quest.Title) {
			task += "\n\nQuest goal: " + quest.Title
		}
		contract, contractErr := workContractFromNode(node)
		if contractErr != nil {
			return fmt.Errorf("flow node %s work contract: %w", node.ID, contractErr)
		}
		task += workContractInstructions(contract)
		projectAgent, agentErr := a.store.GetProjectAgent(context.Background(), effectiveAgentID)
		if agentErr != nil {
			return agentErr
		}
		if node.Kind == domain.FlowNodeTool {
			if strings.TrimSpace(node.ToolName) == "" || !policy.ProfileGrants(domain.ProfileFromProjectAgent(projectAgent)).Allows(node.ToolName) {
				runtime := flowruntime.Runtime{Store: a.store}
				_, _ = runtime.CompleteAgentNode(context.Background(), flowRun.ID, node.ID, false, map[string]any{
					"error": "tool node requires an allowed tool", "toolName": node.ToolName,
				})
				continue
			}
			updated, executeErr := a.executeDeterministicFlowTool(quest, flow, flowRun, node, projectAgent)
			if executeErr != nil {
				return executeErr
			}
			return a.scheduleFlowAgentExecutionsFromRun(updated)
		}
		childQuest := childQuests[node.ID]
		executionQuestID := quest.ID
		if childQuest.ID != "" {
			executionQuestID = childQuest.ID
			a.setFlowChildQuestStatus(flowRun.ID, node.ID, domain.QuestActive)
		}
		var exec domain.ExecutionInstance
		executionID, _ := state.Output["executionId"].(string)
		if executionID != "" {
			exec, _ = a.findExecution(executionID)
			if exec.Status == domain.RunRunning {
				continue
			}
			if exec.ID != "" && exec.Status != domain.RunPending && exec.Status != domain.RunInterrupted {
				continue
			}
		}
		if !reviewReadsIntegratedRevision(quest, node) {
			if state.Output == nil {
				state.Output = map[string]any{}
			}
			state.Output["waitReason"] = "await_integrated_revision"
			flowRun.NodeStates[node.ID] = state
			a.saveFlowRunWaitReason(flowRun, node.ID, "await_integrated_revision")
			continue
		}
		if domain.FlowNodeWriteFiles(node) && writerRootBusy(flow, flowRun, node) {
			if state.Output == nil {
				state.Output = map[string]any{}
			}
			state.Output["waitReason"] = "serial_writer_root"
			flowRun.NodeStates[node.ID] = state
			a.saveFlowRunWaitReason(flowRun, node.ID, "serial_writer_root")
			continue
		}
		if runningOrStarting >= maxConcurrent {
			if state.Output == nil {
				state.Output = map[string]any{}
			}
			state.Output["waitReason"] = "parallelism_limit"
			state.Output["maxConcurrent"] = maxConcurrent
			flowRun.NodeStates[node.ID] = state
			a.saveFlowRunWaitReason(flowRun, node.ID, "parallelism_limit")
			continue
		}
		if exec.ID == "" {
			if !needsSchedule && state.Output["executionId"] != nil {
				continue
			}
			upstreamExecutions := completedUpstreamExecutionIDs(flow, flowRun, node.ID)
			parentExecutionID := ""
			if len(upstreamExecutions) == 1 {
				parentExecutionID = upstreamExecutions[0]
			}
			var created domain.ExecutionInstance
			var createErr error
			var mergeSet *domain.ChangeSet
			if len(upstreamExecutions) > 1 {
				if waitReason, _ := state.Output["waitReason"].(string); waitReason == "sandbox_merge_conflict" {
					continue
				}
				var merged sandbox.MergeResult
				created, merged, mergeSet, createErr = a.startMergedSandboxedExecution(
					effectiveAgentID, task, executionQuestID, flowRun.ID, node.ID, upstreamExecutions, mergeResolutionsFromOutput(state.Output),
				)
				if createErr == nil && len(merged.Conflicts) > 0 {
					state.Output["needsSchedule"] = true
					state.Output["waitReason"] = "sandbox_merge_conflict"
					state.Output["sandboxLineage"] = "merge_conflict"
					state.Output["upstreamExecutionIds"] = append([]string(nil), upstreamExecutions...)
					state.Output["mergeConflicts"] = merged.Conflicts
					state.Output["mergeConflictCount"] = len(merged.Conflicts)
					flowRun.NodeStates[node.ID] = state
					if saveErr := a.store.SaveFlowRun(context.Background(), flowRun); saveErr != nil {
						return saveErr
					}
					a.setProjectControllerState(context.Background(), quest.ID, controllerNeedsUser)
					continue
				}
				a.setProjectControllerState(context.Background(), quest.ID, controllerWaitingMerge)
			} else {
				rootSeedPath := ""
				if parentExecutionID == "" {
					rootSeedPath = a.rootFlowSeedPath(flowRun)
				}
				created, createErr = a.startSandboxedExecutionWithSeed(effectiveAgentID, task, executionQuestID, parentExecutionID, rootSeedPath)
			}
			if createErr != nil {
				return createErr
			}
			exec = created
			exec.FlowRunID = flowRun.ID
			exec.FlowNodeID = node.ID
			if saveErr := a.store.SaveExecution(context.Background(), exec); saveErr != nil {
				return saveErr
			}
			state.Output["needsSchedule"] = false
			state.Output["executionId"] = exec.ID
			state.Output["effectiveAgentId"] = effectiveAgentID
			state.Output["waitReason"] = ""
			state.Output["handoffFrom"] = upstreamHandoffSummary(flow, flowRun, node.ID)
			if parentExecutionID != "" {
				state.Output["sandboxLineage"] = "inherited"
				state.Output["seedExecutionId"] = parentExecutionID
			} else if len(upstreamExecutions) > 1 {
				state.Output["sandboxLineage"] = "merged_parallel_join"
				state.Output["upstreamExecutionIds"] = append([]string(nil), upstreamExecutions...)
				delete(state.Output, "mergeConflicts")
				state.Output["mergeConflictCount"] = 0
				if mergeSet != nil {
					state.Output["mergeChangeSetId"] = mergeSet.ID
					state.Output["mergedChangeSetIds"] = append([]string(nil), mergeSet.Supersedes...)
					state.Output["mergedResultVerified"] = true
				}
			} else {
				state.Output["sandboxLineage"] = "fresh"
			}
			flowRun.NodeStates[node.ID] = state
			if saveErr := a.store.SaveFlowRun(context.Background(), flowRun); saveErr != nil {
				return saveErr
			}
		}

		role := domain.FlowNodeStageRole(node)
		approval, approvalErr := a.store.WorkOrderApprovalByQuestV2(context.Background(), flowRun.QuestID)
		if approvalErr == nil && hostComposeVerificationNodeV2(node, approval.WorkOrder) {
			updated, passErr := a.completeStagePassthrough(flowRun, node, exec,
				"approved Compose criteria deferred to host checks after delivery")
			if passErr != nil {
				return passErr
			}
			return a.continueAfterDeterministicStage(updated)
		}
		if approvalErr == nil && hostManualComposeNodeV2(node, approval.WorkOrder) {
			updated, passErr := a.completeStagePassthrough(flowRun, node, exec,
				"manual Docker acceptance requires the delivered workspace and host daemon")
			if passErr != nil {
				return passErr
			}
			return a.continueAfterDeterministicStage(updated)
		}
		lineage, _ := state.Output["sandboxLineage"].(string)
		stackID := ""
		if role == domain.StageRoleIntegrate {
			if approval, approvalErr := a.store.WorkOrderApprovalByQuestV2(context.Background(), flowRun.QuestID); approvalErr == nil {
				stackID = approval.WorkOrder.Stack.ID
			}
		}
		if stageAllowsLLMBypass(role, lineage, stackID) {
			updated, passErr := a.completeStagePassthrough(flowRun, node, exec,
				"serial inherited tip already integrated; skipped redundant "+role+" LLM stage")
			if passErr != nil {
				return passErr
			}
			return a.continueAfterDeterministicStage(updated)
		}
		if stageUsesDeterministicBootstrap(role) {
			updated, bootErr := a.completeDeterministicBootstrap(flowRun, node, exec, projectAgent)
			if bootErr != nil {
				return bootErr
			}
			return a.continueAfterDeterministicStage(updated)
		}
		if role == domain.StageRoleAccept {
			if handled, updated, acceptErr := a.tryDeterministicAccept(quest, flowRun, node, exec, projectAgent); acceptErr != nil {
				return acceptErr
			} else if handled {
				return a.continueAfterDeterministicStage(updated)
			}
		}

		// Cursor without a local MCP address still uses the interactive IDE
		// adapter. Headless Cursor/Codex/Claude share the ordinary StartRun path.
		if projectAgent.Provider == domain.ProviderCursor && strings.TrimSpace(a.SelfURL()) == "" {
			if state.Output == nil {
				state.Output = map[string]any{}
			}
			state.Output["waitReason"] = "waiting_interactive_cursor"
			delete(state.Output, "startError")
			flowRun.NodeStates[node.ID] = state
			a.saveFlowRunWaitReason(flowRun, node.ID, "waiting_interactive_cursor")
			continue
		}

		// Auto-start local/no-key providers; otherwise leave pending for Launch with API key.
		if providerNeedsAPIKey(projectAgent.Provider, projectAgent.ProviderPreset) && strings.TrimSpace(apiKey) == "" {
			if state.Output == nil {
				state.Output = map[string]any{}
			}
			state.Output["waitReason"] = "waiting_api_key"
			flowRun.NodeStates[node.ID] = state
			a.saveFlowRunWaitReason(flowRun, node.ID, "waiting_api_key")
			slog.Warn("flow agent waiting for API key", "flow_run_id", flowRun.ID, "flow_node_id", node.ID, "execution_id", exec.ID, "provider", projectAgent.Provider)
			continue
		}
		checkKind := ""
		if lineage, _ := state.Output["sandboxLineage"].(string); lineage == "merged_parallel_join" {
			checkKind = "merged-result"
			a.syncIntakeStatus(context.Background(), flowRun.QuestID, domain.IntakeIntegrating, "")
		}
		modelBinding, bindingErr := modelBindingFromNode(node)
		if bindingErr != nil {
			return fmt.Errorf("flow node %s model binding: %w", node.ID, bindingErr)
		}
		// Clear an earlier wait before launching. Doing this after StartRun can
		// overwrite a fast completion with a stale Flow snapshot.
		delete(state.Output, "waitReason")
		delete(state.Output, "startError")
		flowRun.NodeStates[node.ID] = state
		if saveErr := a.store.SaveFlowRun(context.Background(), flowRun); saveErr != nil {
			return saveErr
		}
		// Model-planned work nodes are implementation stages. The parent
		// WorkOrder criteria belong to Accept, not each writer's final answer.
		startRole := workOrderExecutionStageRoleV2(node, approvalErr == nil)
		_, startErr := a.StartRun(StartRunRequest{
			ProfileID: effectiveAgentID, Task: task, APIKey: apiKey,
			QuestID: executionQuestID, FlowRunID: flowRun.ID, FlowNodeID: node.ID, ExecutionID: exec.ID,
			StageRole:           startRole,
			CompletionCheckKind: checkKind,
			ContextItems:        flowNodeContext(quest, flow, flowRun, node.ID, changeSets),
			ModelBinding:        modelBinding,
			WorkContract:        &contract,
		})
		if startErr != nil {
			if state.Output == nil {
				state.Output = map[string]any{}
			}
			state.Output["waitReason"] = "start_failed"
			state.Output["startError"] = startErr.Error()
			flowRun.NodeStates[node.ID] = state
			a.saveFlowRunWaitReason(flowRun, node.ID, "start_failed")
			slog.Warn("flow agent start failed", "flow_run_id", flowRun.ID, "flow_node_id", node.ID, "execution_id", exec.ID, "agent_id", effectiveAgentID, "error", startErr)
			continue
		}
		if checkKind == "merged-result" || node.Kind == domain.FlowNodeVerifier {
			a.syncIntakeStatus(context.Background(), flowRun.QuestID, domain.IntakeVerifying, "")
		}
		runningOrStarting++
	}
	a.syncWorkOrderQuestStallV2(flowRun.ID)
	return nil
}

// syncWorkOrderQuestStallV2 доводит до карточки то, что увидел планировщик.
//
// Планировщик оставляет узел в waiting_agent и идёт дальше: ключа нет, запуск
// не состоялся, песочница разошлась. Статус квеста при этом никто не трогал —
// он пересчитывался только на запуске наряда. После перезапуска ядра и ручного
// «Продолжить» квест оставался `running` у работы, которая стоит: карточка
// писала «Квест выполняется», причины не называла, а кнопки возобновления не
// давала, потому что `running` не возобновляем. Выхода из этого не было вовсе.
//
// Место одно на всех вызывающих: планировщик — единственный, кто оставляет
// узлы ждать, и пересчёт принадлежит ему, а не каждому из четырнадцати мест,
// откуда его зовут.
func (a *App) syncWorkOrderQuestStallV2(flowRunID string) {
	ctx := context.Background()
	flowRun, err := a.store.GetFlowRun(ctx, flowRunID)
	if err != nil || strings.TrimSpace(flowRun.QuestID) == "" {
		return
	}
	status, message, _ := workOrderLaunchOutcomeV2(&flowRun, "")
	if status == domain.QuestRunning {
		return
	}
	// Наряд v2 узнаётся по утверждению: у остальных квестов свой жизненный цикл,
	// и трогать их статус отсюда нельзя.
	approval, approvalErr := a.store.WorkOrderApprovalByQuestV2(ctx, flowRun.QuestID)
	if approvalErr != nil {
		return
	}
	quest, questErr := a.workOrderQuestV2(ctx, approval.WorkOrder.WorkspaceID, flowRun.QuestID)
	if questErr != nil || quest.Status == status {
		return
	}
	// Решение человека старше наблюдения: паузу и отмену пересчёт не отменяет.
	switch quest.Status {
	case domain.QuestPreflight, domain.QuestRunning, domain.QuestVerifying, domain.QuestApplying:
	default:
		return
	}
	if _, saveErr := a.setWorkOrderQuestStatusV2(ctx, quest, status, message); saveErr != nil {
		slog.Warn("work order stall status not persisted", "quest_id", flowRun.QuestID, "status", status, "error", saveErr)
		return
	}
	slog.Info("work order quest stalled", "quest_id", flowRun.QuestID, "flow_run_id", flowRun.ID, "status", status)
}

func providerNeedsAPIKey(kind domain.ProviderKind, presetID string) bool {
	for _, preset := range domain.BuiltInProviderCatalog() {
		if preset.ID == presetID {
			return preset.RequiresAPIKey
		}
	}
	// Локальной Ollama ключ не нужен; остальным — нужен.
	return kind != domain.ProviderOllama
}
