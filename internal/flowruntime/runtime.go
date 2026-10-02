package flowruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/textutil"
)

type Store interface {
	SaveFlowRun(ctx context.Context, run domain.FlowRun) error
	GetFlowRun(ctx context.Context, id string) (domain.FlowRun, error)
	GetFlow(ctx context.Context, id string) (domain.FlowGraph, error)
}

// Runtime is a deterministic persisted graph state machine.
type Runtime struct {
	Store Store
}

func (r Runtime) Start(ctx context.Context, req StartRequest) (domain.FlowRun, error) {
	flow, err := r.Store.GetFlow(ctx, req.FlowID)
	if err != nil {
		return domain.FlowRun{}, err
	}
	if err := ValidateGraph(flow); err != nil {
		return domain.FlowRun{}, fmt.Errorf("invalid flow: %w", err)
	}
	now := time.Now().UTC()
	run := domain.FlowRun{
		ID: domain.NewID("flowrun"), FlowID: flow.ID, WorkspaceID: req.WorkspaceID, QuestID: req.QuestID,
		Status: domain.RunRunning, NodeStates: map[string]domain.FlowNodeState{}, Snapshot: map[string]any{},
		StartedAt: now,
	}
	if req.Input != nil {
		run.Snapshot["input"] = req.Input
	}
	run.Snapshot["graph"] = flow
	if req.SandboxWorkspace != "" {
		run.Snapshot["sandboxWorkspace"] = map[string]any{"storageMode": req.SandboxWorkspace, "fileRulesVersion": req.FileRulesVersion}
	}
	for _, node := range flow.Nodes {
		status := "blocked"
		if node.Kind == domain.FlowNodeInput {
			status = "ready"
		}
		run.NodeStates[node.ID] = domain.FlowNodeState{Status: status}
	}
	if len(flow.Nodes) == 0 {
		run.Status = domain.RunFailed
		run.Error = "flow has no nodes"
		finished := now
		run.FinishedAt = &finished
	}
	if err := r.Store.SaveFlowRun(ctx, run); err != nil {
		return domain.FlowRun{}, err
	}
	return r.Tick(ctx, run.ID)
}

func (r Runtime) Tick(ctx context.Context, flowRunID string) (domain.FlowRun, error) {
	run, err := r.Store.GetFlowRun(ctx, flowRunID)
	if err != nil {
		return domain.FlowRun{}, err
	}
	if run.Status != domain.RunRunning && run.Status != domain.RunWaiting {
		return run, nil
	}
	// Resume ticking after waiting if new ready nodes appeared (e.g. agent completed).
	if run.Status == domain.RunWaiting {
		run.Status = domain.RunRunning
	}
	flow, ok, err := FlowFromSnapshot(run)
	if err != nil {
		return domain.FlowRun{}, err
	}
	if !ok {
		// Backward-compatible migration for runs created before graph snapshots.
		flow, err = r.Store.GetFlow(ctx, run.FlowID)
		if err != nil {
			return domain.FlowRun{}, err
		}
		if run.Snapshot == nil {
			run.Snapshot = map[string]any{}
		}
		run.Snapshot["graph"] = flow
	}
	nodes := map[string]domain.FlowNode{}
	for _, node := range flow.Nodes {
		nodes[node.ID] = node
	}
	incoming := map[string][]domain.FlowEdge{}
	outgoing := map[string][]domain.FlowEdge{}
	for _, edge := range flow.Edges {
		incoming[edge.To] = append(incoming[edge.To], edge)
		outgoing[edge.From] = append(outgoing[edge.From], edge)
	}
	var resetLoopBody func(string, string, map[string]bool)
	resetLoopBody = func(nodeID, loopID string, visited map[string]bool) {
		if nodeID == loopID || visited[nodeID] {
			return
		}
		visited[nodeID] = true
		previous := run.NodeStates[nodeID]
		run.NodeStates[nodeID] = domain.FlowNodeState{Status: "blocked", Attempts: previous.Attempts}
		for _, edge := range outgoing[nodeID] {
			resetLoopBody(edge.To, loopID, visited)
		}
	}
	var skipInactiveBranch func(string, map[string]bool)
	skipInactiveBranch = func(nodeID string, visited map[string]bool) {
		if visited[nodeID] {
			return
		}
		visited[nodeID] = true
		target := run.NodeStates[nodeID]
		if target.Status != "blocked" && target.Status != "waiting_join" {
			return
		}
		for _, edge := range incoming[nodeID] {
			source := run.NodeStates[edge.From]
			if source.Status != "completed" && source.Status != "skipped" {
				return
			}
			if source.Status == "completed" && r.canActivate(edge, run) {
				return
			}
		}
		now := time.Now().UTC()
		target.Status = "skipped"
		target.FinishedAt = &now
		run.NodeStates[nodeID] = target
		for _, edge := range outgoing[nodeID] {
			if nodes[edge.To].Kind == domain.FlowNodeJoin {
				join := run.NodeStates[edge.To]
				if r.joinReady(nodes[edge.To], incoming[edge.To], run) {
					join.Status = "ready"
				} else {
					join.Status = "waiting_join"
				}
				run.NodeStates[edge.To] = join
				continue
			}
			skipInactiveBranch(edge.To, visited)
		}
	}

	// Propagate edges from already-completed nodes (e.g. after CompleteAgentNode).
	for _, node := range flow.Nodes {
		state := run.NodeStates[node.ID]
		if state.Status != "completed" && state.Status != "skipped" {
			continue
		}
		if state.Status == "skipped" {
			for _, edge := range outgoing[node.ID] {
				skipInactiveBranch(edge.To, map[string]bool{})
			}
			continue
		}
		for _, edge := range outgoing[node.ID] {
			if !r.canActivate(edge, run) {
				if node.Kind == domain.FlowNodeCondition || node.Kind == domain.FlowNodeVerifier {
					skipInactiveBranch(edge.To, map[string]bool{})
				}
				continue
			}
			target := run.NodeStates[edge.To]
			if nodes[edge.To].Kind == domain.FlowNodeLoop && target.Status == "completed" && loopIncomingUnseen(target, node.ID, state.Attempts) {
				target.Status = "ready"
				target.FinishedAt = nil
				run.NodeStates[edge.To] = target
				continue
			}
			if target.Status == "blocked" || target.Status == "waiting_join" {
				if nodes[edge.To].Kind == domain.FlowNodeJoin {
					if r.joinReady(nodes[edge.To], incoming[edge.To], run) {
						target.Status = "ready"
					} else {
						target.Status = "waiting_join"
					}
				} else {
					target.Status = "ready"
				}
				run.NodeStates[edge.To] = target
			}
		}
	}

	progress := true
	for progress {
		progress = false
		anyWaiting := false
		for _, node := range flow.Nodes {
			state := run.NodeStates[node.ID]
			if state.Status != "ready" {
				continue
			}
			updated, waiting, err := r.executeNode(node, state, run, incoming[node.ID])
			if err != nil {
				run.Status = domain.RunFailed
				run.Error = err.Error()
				now := time.Now().UTC()
				run.FinishedAt = &now
				run.DurationMs = now.Sub(run.StartedAt).Milliseconds()
				_ = r.Store.SaveFlowRun(ctx, run)
				return run, nil
			}
			run.NodeStates[node.ID] = updated
			if waiting {
				anyWaiting = true
				continue
			}
			progress = true
			if updated.Status == "failed" {
				continue
			}
			for _, edge := range outgoing[node.ID] {
				if !r.canActivate(edge, run) {
					if node.Kind == domain.FlowNodeCondition || node.Kind == domain.FlowNodeVerifier {
						skipInactiveBranch(edge.To, map[string]bool{})
					}
					continue
				}
				if node.Kind == domain.FlowNodeLoop && strings.EqualFold(strings.TrimSpace(edge.Condition), "continue") {
					targetState := run.NodeStates[edge.To]
					if targetState.Status == "completed" || targetState.Status == "skipped" || targetState.Status == "failed" {
						resetLoopBody(edge.To, node.ID, map[string]bool{})
					}
				}
				target := run.NodeStates[edge.To]
				if target.Status == "blocked" || target.Status == "waiting_join" {
					if nodes[edge.To].Kind == domain.FlowNodeJoin {
						if r.joinReady(nodes[edge.To], incoming[edge.To], run) {
							target.Status = "ready"
						} else {
							target.Status = "waiting_join"
						}
					} else {
						target.Status = "ready"
					}
					run.NodeStates[edge.To] = target
				}
			}
		}
		if anyWaiting && !progress {
			run.Status = domain.RunWaiting
			break
		}
	}

	if run.Status == domain.RunWaiting {
		if err := r.Store.SaveFlowRun(ctx, run); err != nil {
			return domain.FlowRun{}, err
		}
		return run, nil
	}

	waitingNodes := false
	allDone := true
	hasFailed := false
	failReason := ""
	for _, node := range flow.Nodes {
		state := run.NodeStates[node.ID]
		if state.Status == "failed" {
			hasFailed = true
			if failReason == "" {
				failReason = strings.TrimSpace(state.Error)
				if failReason == "" {
					failReason = "node " + node.ID + " failed"
				}
				if node.Name != "" {
					failReason = node.Name + ": " + failReason
				}
			}
		}
		if state.Status == "waiting_agent" || state.Status == "waiting_approval" {
			waitingNodes = true
		}
		if state.Status != "completed" && state.Status != "skipped" {
			allDone = false
		}
	}
	now := time.Now().UTC()
	if hasFailed {
		// Parallel / downstream siblings must not keep ownership after a peer failure.
		skipUnfinishedNodes(&run, flow, "пропущено: другой узел Flow завершился с ошибкой")
		run.Status = domain.RunFailed
		if strings.TrimSpace(run.Error) == "" {
			run.Error = failReason
		}
		run.FinishedAt = &now
		run.DurationMs = now.Sub(run.StartedAt).Milliseconds()
	} else if waitingNodes {
		run.Status = domain.RunWaiting
	} else if allDone {
		run.Status = domain.RunCompleted
		run.Result = flowResult(flow, run)
		run.FinishedAt = &now
		run.DurationMs = now.Sub(run.StartedAt).Milliseconds()
	}
	if err := r.Store.SaveFlowRun(ctx, run); err != nil {
		return domain.FlowRun{}, err
	}
	return run, nil
}

func (r Runtime) ResumeAfterApproval(ctx context.Context, flowRunID, nodeID string, approved bool) (domain.FlowRun, error) {
	run, err := r.Store.GetFlowRun(ctx, flowRunID)
	if err != nil {
		return domain.FlowRun{}, err
	}
	if isTerminalFlowStatus(run.Status) {
		return run, fmt.Errorf("flow run %s is already %s", flowRunID, run.Status)
	}
	state := run.NodeStates[nodeID]
	if state.Status != "waiting_approval" {
		return run, fmt.Errorf("node %s is not waiting for approval (status=%s)", nodeID, state.Status)
	}
	if approved {
		state.Status = "completed"
		state.Output = map[string]any{"approved": true}
	} else {
		state.Status = "failed"
		state.Error = "approval denied"
	}
	now := time.Now().UTC()
	state.FinishedAt = &now
	run.NodeStates[nodeID] = state
	run.Status = domain.RunRunning
	if err := r.Store.SaveFlowRun(ctx, run); err != nil {
		return domain.FlowRun{}, err
	}
	return r.Tick(ctx, flowRunID)
}

// CompleteAgentNode marks a waiting agent/tool node finished and continues the graph.
// Late completions against an already-terminal FlowRun are recorded without reopening the run.
func (r Runtime) CompleteAgentNode(ctx context.Context, flowRunID, nodeID string, success bool, output map[string]any) (domain.FlowRun, error) {
	run, err := r.Store.GetFlowRun(ctx, flowRunID)
	if err != nil {
		return domain.FlowRun{}, err
	}
	state := run.NodeStates[nodeID]
	if state.Status != "waiting_agent" && state.Status != "waiting_approval" {
		return run, nil
	}
	now := time.Now().UTC()
	if success {
		state.Status = "completed"
		if output == nil {
			output = map[string]any{}
		}
		output["completed"] = true
		state.Output = output
	} else {
		state.Status = "failed"
		state.Error = agentNodeFailureMessage(output)
		state.Output = output
	}
	state.FinishedAt = &now
	run.NodeStates[nodeID] = state

	// Never resurrect a failed/cancelled/completed flow when a parallel sibling finishes late.
	if isTerminalFlowStatus(run.Status) {
		if success {
			if state.Output == nil {
				state.Output = map[string]any{}
			}
			state.Output["lateCompletion"] = true
			state.Output["ignoredForGraph"] = true
			run.NodeStates[nodeID] = state
		}
		if err := r.Store.SaveFlowRun(ctx, run); err != nil {
			return domain.FlowRun{}, err
		}
		return run, nil
	}

	run.Status = domain.RunRunning
	if err := r.Store.SaveFlowRun(ctx, run); err != nil {
		return domain.FlowRun{}, err
	}
	return r.Tick(ctx, flowRunID)
}

func isTerminalFlowStatus(status domain.RunStatus) bool {
	return status == domain.RunCompleted || status == domain.RunFailed || status == domain.RunCancelled
}

func agentNodeFailureMessage(output map[string]any) string {
	if output != nil {
		for _, key := range []string{"error", "message", "result"} {
			if raw, ok := output[key]; ok {
				switch typed := raw.(type) {
				case string:
					if msg := strings.TrimSpace(typed); msg != "" {
						return truncateHandoff(msg, 512)
					}
				}
			}
		}
		if status, ok := output["status"].(string); ok && strings.TrimSpace(status) != "" && status != string(domain.RunCompleted) {
			return "agent execution " + strings.TrimSpace(status)
		}
	}
	return "agent execution failed"
}

func skipUnfinishedNodes(run *domain.FlowRun, flow domain.FlowGraph, reason string) {
	now := time.Now().UTC()
	for _, node := range flow.Nodes {
		state := run.NodeStates[node.ID]
		switch state.Status {
		case "blocked", "ready", "waiting_agent", "waiting_approval", "waiting_join":
			state.Status = "skipped"
			state.Error = reason
			if state.Output == nil {
				state.Output = map[string]any{}
			}
			state.Output["skippedDueToPeerFailure"] = true
			state.FinishedAt = &now
			run.NodeStates[node.ID] = state
		}
	}
}

// Хендофф ограничен по объёму в байтах, но граница проходит по руне: раньше
// `value[:limit]` разрывал русский текст посередине знака и в JSON уходил
// невалидный UTF-8.
func truncateHandoff(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return textutil.BoundedBytes(value, limit) + "…"
}

func (r Runtime) executeNode(node domain.FlowNode, state domain.FlowNodeState, run domain.FlowRun, incoming []domain.FlowEdge) (domain.FlowNodeState, bool, error) {
	now := time.Now().UTC()
	state.Attempts++
	if state.StartedAt == nil {
		state.StartedAt = &now
	}
	switch node.Kind {
	case domain.FlowNodeInput:
		state.Status = "completed"
		state.Output = cloneMap(run.Snapshot["input"])
	case domain.FlowNodeOutput:
		state.Status = "completed"
		state.Output = map[string]any{"result": collectInputs(incoming, run)}
	case domain.FlowNodeCondition:
		state.Status = "completed"
		state.Output = map[string]any{
			"branch": evaluateCondition(node.Config, incoming, run),
			"inputs": collectInputs(incoming, run),
		}
	case domain.FlowNodeParallel:
		state.Status = "completed"
		state.Output = map[string]any{"forked": true}
	case domain.FlowNodeJoin:
		state.Status = "completed"
		state.Output = map[string]any{"joined": true, "results": collectInputs(incoming, run)}
	case domain.FlowNodeLoop:
		maxIterations, err := boundedLoopLimit(node.Config)
		if err != nil {
			return state, false, err
		}
		seen := loopSeenAttempts(state.Output)
		results := loopResults(state.Output)
		hadPreviousInput := len(seen) > 0
		for _, edge := range incoming {
			source := run.NodeStates[edge.From]
			if source.Status != "completed" || source.Attempts <= seen[edge.From] {
				continue
			}
			if hadPreviousInput {
				results = append(results, boundedLoopResult(edge.From, source))
			}
			seen[edge.From] = source.Attempts
		}
		branch := "continue"
		iteration := state.Attempts
		if state.Attempts > maxIterations {
			branch = "done"
			iteration = maxIterations
		}
		state.Status = "completed"
		state.Output = map[string]any{
			"branch": branch, "iteration": iteration, "completedIterations": len(results),
			"maxIterations": maxIterations, "results": results, "seenIncoming": seen,
		}
	case domain.FlowNodeVerifier:
		verified, checks := verifyInputs(node.Config, incoming, run)
		state.Output = map[string]any{"verified": verified, "checks": checks}
		if verified {
			state.Status = "completed"
		} else {
			state.Status = "failed"
			state.Error = "verification failed"
		}
	case domain.FlowNodeApproval:
		state.Status = "waiting_approval"
		return state, true, nil
	case domain.FlowNodeAgent, domain.FlowNodeTool:
		// Orchestration schedules real work; node stays waiting until CompleteAgentNode.
		state.Status = "waiting_agent"
		state.Output = map[string]any{
			"needsSchedule": true,
			"agentId":       node.AgentID,
			"toolName":      node.ToolName,
		}
		return state, true, nil
	default:
		return state, false, fmt.Errorf("unknown node kind %q", node.Kind)
	}
	finished := time.Now().UTC()
	state.FinishedAt = &finished
	return state, false, nil
}

func (r Runtime) canActivate(edge domain.FlowEdge, run domain.FlowRun) bool {
	if edge.Condition == "" {
		return true
	}
	condition := strings.ToLower(strings.TrimSpace(edge.Condition))
	from := run.NodeStates[edge.From]
	if from.Output == nil {
		return condition == "false" || condition == "failure" || condition == "fail"
	}
	if branch, ok := from.Output["branch"].(bool); ok {
		if condition == "true" || condition == "pass" || condition == "success" {
			return branch
		}
		if condition == "false" || condition == "fail" || condition == "failure" {
			return !branch
		}
	}
	if branch, ok := from.Output["branch"].(string); ok {
		return condition == strings.ToLower(strings.TrimSpace(branch))
	}
	if verified, ok := from.Output["verified"].(bool); ok {
		if condition == "true" || condition == "pass" || condition == "success" {
			return verified
		}
		if condition == "false" || condition == "fail" || condition == "failure" {
			return !verified
		}
	}
	return false
}

func (r Runtime) joinReady(_ domain.FlowNode, edges []domain.FlowEdge, run domain.FlowRun) bool {
	for _, edge := range edges {
		state := run.NodeStates[edge.From]
		if state.Status != "completed" && state.Status != "skipped" {
			return false
		}
	}
	return true
}

// FlowFromSnapshot returns the immutable graph captured when the run started.
func FlowFromSnapshot(run domain.FlowRun) (domain.FlowGraph, bool, error) {
	if run.Snapshot == nil || run.Snapshot["graph"] == nil {
		return domain.FlowGraph{}, false, nil
	}
	if flow, ok := run.Snapshot["graph"].(domain.FlowGraph); ok {
		return flow, true, nil
	}
	data, err := json.Marshal(run.Snapshot["graph"])
	if err != nil {
		return domain.FlowGraph{}, false, fmt.Errorf("encode flow snapshot: %w", err)
	}
	var flow domain.FlowGraph
	if err = json.Unmarshal(data, &flow); err != nil {
		return domain.FlowGraph{}, false, fmt.Errorf("decode flow snapshot: %w", err)
	}
	if flow.ID == "" {
		return domain.FlowGraph{}, false, nil
	}
	return flow, true, nil
}
