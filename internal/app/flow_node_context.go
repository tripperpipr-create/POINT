// Что узел потока видит и что передаёт дальше.
//
// Контекст узла, хендоффы от завершённых соседей, детерминированные
// инструменты и отмена соседей при сбое — всё про связь узлов между собой.
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"local-agent-workbench/internal/agent"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/flowruntime"
	"local-agent-workbench/internal/policy"
	"local-agent-workbench/internal/sandbox"
	"local-agent-workbench/internal/security"
	"local-agent-workbench/internal/workspace"
)

func (a *App) executeDeterministicFlowTool(quest domain.Quest, flow domain.FlowGraph, flowRun domain.FlowRun, node domain.FlowNode, projectAgent domain.ProjectAgent) (domain.FlowRun, error) {
	profile := domain.ProfileFromProjectAgent(projectAgent)
	if err := a.enrichProjectAgentForRun(flowRun.WorkspaceID, projectAgent, &profile, nil); err != nil {
		return domain.FlowRun{}, err
	}
	decision := policy.Engine{TrustedCustomTool: a.trustedCustomTool}.Evaluate(profile, node.ToolName)
	if decision.Risk != domain.ToolRiskLow || decision.RequiresApproval || decision.Policy != domain.ToolPolicyAllow {
		return domain.FlowRun{}, fmt.Errorf("tool node %q cannot execute %q deterministically: only explicitly allowed low-risk tools are supported", node.ID, node.ToolName)
	}
	upstreamExecutions := completedUpstreamExecutionIDs(flow, flowRun, node.ID)
	parentExecutionID := ""
	if len(upstreamExecutions) == 1 {
		parentExecutionID = upstreamExecutions[0]
	}
	state := flowRun.NodeStates[node.ID]
	if waitReason, _ := state.Output["waitReason"].(string); waitReason == "sandbox_merge_conflict" {
		return flowRun, nil
	}
	var exec domain.ExecutionInstance
	var err error
	var mergeSet *domain.ChangeSet
	if len(upstreamExecutions) > 1 {
		var merged sandbox.MergeResult
		exec, merged, mergeSet, err = a.startMergedSandboxedExecution(
			projectAgent.ID, "Tool: "+node.ToolName, quest.ID, flowRun.ID, node.ID,
			upstreamExecutions, mergeResolutionsFromOutput(state.Output),
		)
		if err == nil && len(merged.Conflicts) > 0 {
			state.Output["needsSchedule"] = true
			state.Output["waitReason"] = "sandbox_merge_conflict"
			state.Output["sandboxLineage"] = "merge_conflict"
			state.Output["upstreamExecutionIds"] = append([]string(nil), upstreamExecutions...)
			state.Output["mergeConflicts"] = merged.Conflicts
			state.Output["mergeConflictCount"] = len(merged.Conflicts)
			flowRun.NodeStates[node.ID] = state
			a.syncIntakeStatus(context.Background(), quest.ID, domain.IntakeIntegrating, "merge conflict requires integration agent")
			if saveErr := a.store.SaveFlowRun(context.Background(), flowRun); saveErr != nil {
				return domain.FlowRun{}, saveErr
			}
			return flowRun, nil
		}
	} else {
		rootSeedPath := ""
		if parentExecutionID == "" {
			rootSeedPath = a.rootFlowSeedPath(flowRun)
		}
		exec, err = a.startSandboxedExecutionWithSeed(projectAgent.ID, "Tool: "+node.ToolName, quest.ID, parentExecutionID, rootSeedPath)
	}
	if err != nil {
		return domain.FlowRun{}, err
	}
	exec.FlowRunID = flowRun.ID
	exec.FlowNodeID = node.ID
	exec.Status = domain.RunRunning
	if err = a.store.SaveExecution(context.Background(), exec); err != nil {
		return domain.FlowRun{}, err
	}
	sandboxRecord, err := a.store.GetSandbox(context.Background(), exec.SandboxID)
	if err != nil {
		return domain.FlowRun{}, err
	}
	sandboxFS, err := workspace.Open(sandboxRecord.Path)
	if err != nil {
		return domain.FlowRun{}, err
	}
	customTools, err := a.store.ListCustomTools(context.Background())
	if err != nil {
		return domain.FlowRun{}, err
	}
	registry, _ := agent.BuildToolRegistryWithSources(sandboxFS, customTools, serverProfileBridge{app: a}, a.dbToolAccess(), profile)
	tool, ok := registry.Get(node.ToolName)
	if !ok {
		return domain.FlowRun{}, fmt.Errorf("tool node %q references unavailable tool %q", node.ID, node.ToolName)
	}
	arguments := node.Config["arguments"]
	if arguments == nil {
		arguments = map[string]any{}
	}
	raw, err := json.Marshal(arguments)
	if err != nil {
		return domain.FlowRun{}, fmt.Errorf("encode tool node %q arguments: %w", node.ID, err)
	}
	timeout := time.Duration(projectAgent.MaxDurationSeconds) * time.Second
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	result := tool.Execute(ctx, raw)
	cancel()
	encoded, _ := json.Marshal(result)
	now := time.Now().UTC()
	exec.FinishedAt = &now
	exec.DurationMs = now.Sub(exec.StartedAt).Milliseconds()
	exec.Result = string(encoded)
	exec.Status = domain.RunCompleted
	if !result.OK {
		exec.Status = domain.RunFailed
		if result.Error != nil {
			exec.Error = result.Error.Message
		}
	}
	if err = a.store.SaveExecution(context.Background(), exec); err != nil {
		return domain.FlowRun{}, err
	}
	runtime := flowruntime.Runtime{Store: a.store}
	output := map[string]any{"toolName": node.ToolName, "result": result, "executionId": exec.ID}
	if parentExecutionID != "" {
		output["sandboxLineage"] = "inherited"
		output["seedExecutionId"] = parentExecutionID
	} else if len(upstreamExecutions) > 1 {
		output["sandboxLineage"] = "merged_parallel_join"
		output["upstreamExecutionIds"] = append([]string(nil), upstreamExecutions...)
		if mergeSet != nil {
			output["mergeChangeSetId"] = mergeSet.ID
			output["mergedChangeSetIds"] = append([]string(nil), mergeSet.Supersedes...)
			output["mergedResultVerified"] = true
		}
	} else {
		output["sandboxLineage"] = "fresh"
	}
	return runtime.CompleteAgentNode(context.Background(), flowRun.ID, node.ID, result.OK, output)
}

func completedUpstreamAgentNodeIDs(flow domain.FlowGraph, flowRun domain.FlowRun, nodeID string) []string {
	nodeByID := make(map[string]domain.FlowNode, len(flow.Nodes))
	incoming := make(map[string][]string, len(flow.Nodes))
	for _, node := range flow.Nodes {
		nodeByID[node.ID] = node
	}
	for _, edge := range flow.Edges {
		incoming[edge.To] = append(incoming[edge.To], edge.From)
	}
	visited := map[string]bool{}
	found := map[string]bool{}
	result := make([]string, 0, 4)
	var walk func(string)
	walk = func(currentID string) {
		if visited[currentID] {
			return
		}
		visited[currentID] = true
		state, ok := flowRun.NodeStates[currentID]
		if !ok || state.Status != "completed" {
			return
		}
		node := nodeByID[currentID]
		if node.Kind == domain.FlowNodeAgent || node.Kind == domain.FlowNodeTool || (node.Kind == "" && strings.TrimSpace(node.AgentID) != "") {
			if executionID, _ := state.Output["executionId"].(string); strings.TrimSpace(executionID) != "" && !found[currentID] {
				found[currentID] = true
				result = append(result, currentID)
			}
			return
		}
		for _, parentID := range incoming[currentID] {
			walk(parentID)
		}
	}
	for _, parentID := range incoming[nodeID] {
		walk(parentID)
	}
	return result
}

func completedUpstreamExecutionIDs(flow domain.FlowGraph, flowRun domain.FlowRun, nodeID string) []string {
	result := make([]string, 0, 4)
	seen := map[string]bool{}
	for _, upstreamNodeID := range completedUpstreamAgentNodeIDs(flow, flowRun, nodeID) {
		executionID, _ := flowRun.NodeStates[upstreamNodeID].Output["executionId"].(string)
		executionID = strings.TrimSpace(executionID)
		if executionID != "" && !seen[executionID] {
			seen[executionID] = true
			result = append(result, executionID)
		}
	}
	return result
}

func flowNodeContext(quest domain.Quest, flow domain.FlowGraph, flowRun domain.FlowRun, nodeID string, changeSets []domain.ChangeSet) []domain.RunContextInput {
	inputs := make([]domain.RunContextInput, 0, 4)
	currentName := nodeID
	currentAgent := ""
	for _, node := range flow.Nodes {
		if node.ID == nodeID {
			if strings.TrimSpace(node.Name) != "" {
				currentName = node.Name
			}
			currentAgent = node.AgentID
			break
		}
	}
	if quest.ID != "" {
		questContext := map[string]any{
			"title": quest.Title, "description": quest.Description, "objectives": quest.Objectives,
			"constraints": quest.Constraints, "definitionOfDone": quest.DefinitionOfDone,
			"importance": quest.Importance,
			"coordination": map[string]any{
				"flowRunId": flowRun.ID, "currentNodeId": nodeID, "currentNode": currentName,
				"currentAgentId": currentAgent, "role": "active",
			},
		}
		if data, err := json.Marshal(questContext); err == nil {
			inputs = append(inputs, domain.RunContextInput{
				Kind: domain.ContextText, Label: "Quest brief", Content: string(data),
			})
		}
	}
	nodeByID := make(map[string]domain.FlowNode, len(flow.Nodes))
	for _, node := range flow.Nodes {
		nodeByID[node.ID] = node
	}
	for _, upstreamNodeID := range completedUpstreamAgentNodeIDs(flow, flowRun, nodeID) {
		state := flowRun.NodeStates[upstreamNodeID]
		if state.Status != "completed" || state.Output == nil {
			continue
		}
		from := nodeByID[upstreamNodeID]
		fromLabel := strings.TrimSpace(from.Name)
		if fromLabel == "" {
			fromLabel = upstreamNodeID
		}
		executionID, _ := state.Output["executionId"].(string)
		handoff := map[string]any{
			"kind":           "agent_handoff",
			"fromNodeId":     upstreamNodeID,
			"fromNode":       fromLabel,
			"fromAgentId":    from.AgentID,
			"toNodeId":       nodeID,
			"toNode":         currentName,
			"toAgentId":      currentAgent,
			"status":         state.Status,
			"executionId":    executionID,
			"result":         state.Output["result"],
			"runId":          state.Output["runId"],
			"changedFiles":   state.Output["changedFiles"],
			"upstreamStatus": state.Output["status"],
		}
		if reviewed := changeSetHandoff(changeSets, executionID); len(reviewed) > 0 {
			handoff["changeSets"] = reviewed
		}
		if summary := handoffResultSummary(state.Output); summary != "" {
			handoff["summary"] = summary
		}
		data, err := json.Marshal(handoff)
		if err != nil {
			continue
		}
		content := string(data)
		if len(content) > 64*1024 {
			content = content[:64*1024] + "\n[truncated]"
		}
		inputs = append(inputs, domain.RunContextInput{
			Kind: domain.ContextText, Label: "Передача от · " + fromLabel, Content: content,
		})
	}
	return inputs
}

func changeSetHandoff(changeSets []domain.ChangeSet, executionID string) []map[string]any {
	if strings.TrimSpace(executionID) == "" {
		return nil
	}
	const maxTotalDiffRunes = 48 * 1024
	const maxItemDiffRunes = 12 * 1024
	remaining := maxTotalDiffRunes
	result := make([]map[string]any, 0, 2)
	for _, set := range changeSets {
		if set.ExecutionID != executionID || len(result) >= 4 {
			continue
		}
		items := make([]map[string]any, 0, min(len(set.Items), 24))
		for _, item := range set.Items {
			if len(items) >= 24 || remaining <= 0 {
				break
			}
			diff := strings.TrimSpace(security.Redact(item.Diff))
			runes := []rune(diff)
			limit := min(maxItemDiffRunes, remaining)
			truncated := len(runes) > limit
			if truncated {
				runes = runes[:limit]
			}
			remaining -= len(runes)
			entry := map[string]any{"path": item.Path, "kind": item.Kind, "diff": string(runes)}
			if truncated {
				entry["truncated"] = true
			}
			items = append(items, entry)
		}
		result = append(result, map[string]any{
			"id": set.ID, "status": set.Status, "title": set.Title, "dependsOn": append([]string(nil), set.DependsOn...), "items": items,
			"truncated": len(items) < len(set.Items) || remaining <= 0,
		})
	}
	return result
}

func handoffResultSummary(output map[string]any) string {
	if output == nil {
		return ""
	}
	if raw, ok := output["result"]; ok {
		switch typed := raw.(type) {
		case string:
			msg := strings.TrimSpace(typed)
			if msg == "" {
				return ""
			}
			runes := []rune(msg)
			if len(runes) > 400 {
				return string(runes[:400]) + "…"
			}
			return msg
		}
	}
	return ""
}

func upstreamHandoffSummary(flow domain.FlowGraph, flowRun domain.FlowRun, nodeID string) []map[string]any {
	nodeByID := make(map[string]domain.FlowNode, len(flow.Nodes))
	for _, node := range flow.Nodes {
		nodeByID[node.ID] = node
	}
	out := make([]map[string]any, 0, 2)
	for _, edge := range flow.Edges {
		if edge.To != nodeID {
			continue
		}
		state := flowRun.NodeStates[edge.From]
		if state.Status != "completed" {
			continue
		}
		from := nodeByID[edge.From]
		label := strings.TrimSpace(from.Name)
		if label == "" {
			label = edge.From
		}
		out = append(out, map[string]any{
			"fromNodeId": edge.From, "fromNode": label, "fromAgentId": from.AgentID,
			"status": state.Status, "summary": handoffResultSummary(state.Output),
		})
	}
	return out
}

func (a *App) cancelSiblingFlowExecutions(flowRunID, exceptExecutionID string) {
	if flowRunID == "" {
		return
	}
	ws, err := a.requireWorkspace()
	if err != nil {
		return
	}
	executions, err := a.store.ListExecutions(context.Background(), ws.ID, 500)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	for _, exec := range executions {
		if exec.FlowRunID != flowRunID || exec.ID == exceptExecutionID {
			continue
		}
		switch exec.Status {
		case domain.RunRunning:
			if exec.RunID != "" {
				_ = a.engine.Cancel(exec.RunID)
			} else {
				// Interactive Cursor executions have no headless RunID. Persist
				// cancellation so a late SDK completion is idempotently ignored.
				exec.Status = domain.RunCancelled
				exec.Error = "отменено: другой агент Flow завершился с ошибкой"
				exec.FinishedAt = &now
				exec.DurationMs = now.Sub(exec.StartedAt).Milliseconds()
				a.saveCascadeCancellation(exec)
			}
		case domain.RunPending, domain.RunInterrupted, domain.RunWaiting:
			exec.Status = domain.RunCancelled
			exec.Error = "отменено: другой агент Flow завершился с ошибкой"
			exec.FinishedAt = &now
			exec.DurationMs = now.Sub(exec.StartedAt).Milliseconds()
			a.saveCascadeCancellation(exec)
		}
	}
}

// Отмена интерактивного исполнения хранится только в записи: по ней позднее
// завершение SDK распознаётся как уже отменённое. Незаписанная отмена
// возвращает исполнение к жизни задним числом.
func (a *App) saveCascadeCancellation(exec domain.ExecutionInstance) {
	if err := a.store.SaveExecution(context.Background(), exec); err != nil {
		slog.Warn("cascade cancellation not persisted", "execution_id", exec.ID, "quest_id", exec.QuestID, "error", err)
	}
}
