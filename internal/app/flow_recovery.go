package app

import (
	"context"
	"fmt"
	"strings"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/flowruntime"
)

func normalizeFailurePolicy(policy domain.FlowFailurePolicy) domain.FlowFailurePolicy {
	policy.Mode = strings.ToLower(strings.TrimSpace(policy.Mode))
	if policy.Mode == "" {
		policy.Mode = "stop"
	}
	if policy.Mode == "retry" && policy.MaxRetries == 0 {
		policy.MaxRetries = 1
	}
	return policy
}

func retryableFlowFailure(status domain.RunStatus, message string) bool {
	if status == domain.RunCancelled || status == domain.RunInterrupted {
		return false
	}
	lower := strings.ToLower(strings.TrimSpace(message))
	for _, marker := range []string{
		"configuration", "configure", "unknown tool", "unknown agent", "unsupported provider", "readiness", "not ready",
		"unknown_outcome", "tool_journal_integrity", "workspace mutation audit failed", "persist completion evidence",
		"approval denied", "approval was denied", "verification failed", "budget", "pricing profile", "permission denied",
	} {
		if strings.Contains(lower, marker) {
			return false
		}
	}
	return true
}

// recoverFlowNodeFailure keeps the node waiting and records a fresh attempt.
// The scheduler then creates a new Execution from the same immutable upstream
// parent; the failed Execution and its Change Set remain audit evidence.
func (a *App) recoverFlowNodeFailure(flowRunID, nodeID string, execution domain.ExecutionInstance) (domain.FlowRun, bool, error) {
	run, err := a.store.GetFlowRun(context.Background(), flowRunID)
	if err != nil {
		return domain.FlowRun{}, false, err
	}
	flow, ok, err := flowruntime.FlowFromSnapshot(run)
	if err != nil {
		return domain.FlowRun{}, false, err
	}
	if !ok {
		flow, err = a.store.GetFlow(context.Background(), run.FlowID)
		if err != nil {
			return domain.FlowRun{}, false, err
		}
	}
	var node *domain.FlowNode
	for index := range flow.Nodes {
		if flow.Nodes[index].ID == nodeID {
			node = &flow.Nodes[index]
			break
		}
	}
	if node == nil || !retryableFlowFailure(execution.Status, execution.Error) {
		return run, false, nil
	}
	policy := normalizeFailurePolicy(node.FailurePolicy)
	state := run.NodeStates[nodeID]
	if state.Output == nil {
		state.Output = map[string]any{}
	}
	brief, briefErr := a.taskBriefForQuest(context.Background(), execution.WorkspaceID, execution.QuestID)
	if briefErr != nil {
		return run, false, briefErr
	}
	if brief != nil {
		lastError, _ := state.Output["lastError"].(string)
		if brief.Mode == domain.TaskModePrecise || state.Attempts >= brief.Budget.MaxAttempts || (lastError != "" && lastError == execution.Error) {
			return run, false, nil
		}
	}
	recoverable := false
	switch policy.Mode {
	case "retry":
		recoverable = state.Attempts <= policy.MaxRetries
	case "fallback_agent":
		_, alreadyUsed := state.Output["fallbackUsed"]
		if !alreadyUsed && strings.TrimSpace(policy.FallbackAgentID) != "" {
			fallback, loadErr := a.store.GetProjectAgent(context.Background(), policy.FallbackAgentID)
			if loadErr != nil {
				return run, false, loadErr
			}
			readiness := a.projectAgentReadiness(context.Background(), fallback)
			if readiness.State == "BLOCKED" {
				return run, false, fmt.Errorf("fallback agent %q is not ready: %s", fallback.Name, strings.Join(readiness.Blocking, "; "))
			}
			state.Output["effectiveAgentId"] = fallback.ID
			state.Output["fallbackUsed"] = true
			recoverable = true
		}
	}
	if !recoverable {
		return run, false, nil
	}
	attemptIDs := stringListFromAny(state.Output["attemptExecutionIds"])
	attemptIDs = append(attemptIDs, execution.ID)
	state.Output["attemptExecutionIds"] = attemptIDs
	state.Output["previousExecutionId"] = execution.ID
	state.Output["lastError"] = execution.Error
	state.Output["needsSchedule"] = true
	state.Output["waitReason"] = "retry_scheduled"
	delete(state.Output, "executionId")
	state.Status = "waiting_agent"
	state.Error = ""
	state.FinishedAt = nil
	state.Attempts++
	run.NodeStates[nodeID] = state
	run.Status = domain.RunWaiting
	run.Error = ""
	run.FinishedAt = nil
	if err = a.store.SaveFlowRun(context.Background(), run); err != nil {
		return domain.FlowRun{}, false, err
	}
	a.setFlowChildQuestStatus(flowRunID, nodeID, domain.QuestActive)
	return run, true, nil
}

func stringListFromAny(value any) []string {
	result := []string{}
	switch typed := value.(type) {
	case []string:
		return append(result, typed...)
	case []any:
		for _, item := range typed {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				result = append(result, text)
			}
		}
	}
	return result
}

func (a *App) flowAttemptOutput(flowRunID, nodeID, executionID string, output map[string]any) map[string]any {
	if output == nil {
		output = map[string]any{}
	}
	if run, err := a.store.GetFlowRun(context.Background(), flowRunID); err == nil {
		state := run.NodeStates[nodeID]
		attemptIDs := stringListFromAny(state.Output["attemptExecutionIds"])
		if executionID != "" {
			attemptIDs = append(attemptIDs, executionID)
		}
		output["attemptExecutionIds"] = attemptIDs
		output["attemptCount"] = state.Attempts
		if effective, _ := state.Output["effectiveAgentId"].(string); effective != "" {
			output["effectiveAgentId"] = effective
		}
		if fallback, ok := state.Output["fallbackUsed"]; ok {
			output["fallbackUsed"] = fallback
		}
	}
	return output
}
