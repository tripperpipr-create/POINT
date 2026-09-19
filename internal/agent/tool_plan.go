// План вызовов инструментов: отпечаток, повторы, подсказки на исправление.
//
// Модель умеет повторять один и тот же вызов и объявлять работу законченной,
// не сделав её. Отсюда отпечаток плана и отдельные тексты обратной связи.
package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/observability"
	"local-agent-workbench/internal/providers"
	workbenchtools "local-agent-workbench/internal/tools"
)

func toolExecutionKey(call providers.ToolCall, workspaceRevision int) string {
	fingerprint := toolPlanFingerprint([]providers.ToolCall{call})
	if call.Name == "propose_patch" {
		return "global:" + fingerprint
	}
	return fmt.Sprintf("revision:%d:%s", workspaceRevision, fingerprint)
}

func toolPlanFingerprint(calls []providers.ToolCall) string {
	type semanticCall struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	plan := make([]semanticCall, 0, len(calls))
	for _, call := range calls {
		arguments := append(json.RawMessage(nil), call.Arguments...)
		var value any
		if json.Unmarshal(arguments, &value) == nil {
			if normalized, err := json.Marshal(value); err == nil {
				arguments = normalized
			}
		}
		plan = append(plan, semanticCall{Name: call.Name, Arguments: arguments})
	}
	encoded, _ := json.Marshal(plan)
	return fmt.Sprintf("%x", sha256.Sum256(encoded))
}

func toolCallNames(calls []providers.ToolCall) []string {
	result := make([]string, 0, len(calls))
	for _, call := range calls {
		result = append(result, call.Name)
	}
	return result
}

func planHasUncompletedCalls(calls []providers.ToolCall, completed map[string]struct{}, workspaceRevision int) bool {
	for _, call := range calls {
		if _, done := completed[toolExecutionKey(call, workspaceRevision)]; !done {
			return true
		}
	}
	return false
}

func toolPlanRecoveryFeedback(calls []providers.ToolCall, episode, maxEpisodes int) string {
	type evidence struct {
		Code                string   `json:"code"`
		Tools               []string `json:"tools"`
		RecoveryEpisode     int      `json:"recoveryEpisode"`
		MaxRecoveryEpisodes int      `json:"maxRecoveryEpisodes"`
	}
	encoded, _ := json.Marshal(evidence{
		Code: "tool_plan_recovery", Tools: toolCallNames(calls),
		RecoveryEpisode: episode, MaxRecoveryEpisodes: maxEpisodes,
	})
	return "<point_tool_plan_gate>\nPoint interrupted a repeated identical tool plan that could not make progress.\nEvidence: " + string(encoded) + "\nDo not repeat the same successful tool call. Inspect a different path, refine arguments, use another allowed tool, or produce a final answer that states the blocker.\n</point_tool_plan_gate>"
}

func reasoningBudgetRecoveryFeedback(episode, maxEpisodes int) string {
	type evidence struct {
		Code                string `json:"code"`
		RecoveryEpisode     int    `json:"recoveryEpisode"`
		MaxRecoveryEpisodes int    `json:"maxRecoveryEpisodes"`
	}
	encoded, _ := json.Marshal(evidence{
		Code: "reasoning_budget_recovery", RecoveryEpisode: episode, MaxRecoveryEpisodes: maxEpisodes,
	})
	return "<point_reasoning_budget_gate>\nYour previous turn spent the entire output budget on reasoning with no tool call.\nEvidence: " + string(encoded) + "\nCall a tool immediately (propose_patch or read_file). Keep reasoning minimal; do not re-read Makefile/Dockerfile or explore vendor outside the assignment package.\n</point_reasoning_budget_gate>"
}

func emptyResponseRecoveryFeedback(episode, maxEpisodes int) string {
	type evidence struct {
		Code                string `json:"code"`
		RecoveryEpisode     int    `json:"recoveryEpisode"`
		MaxRecoveryEpisodes int    `json:"maxRecoveryEpisodes"`
	}
	encoded, _ := json.Marshal(evidence{
		Code: "empty_response_recovery", RecoveryEpisode: episode, MaxRecoveryEpisodes: maxEpisodes,
	})
	return "<point_empty_response_gate>\nYour previous turn returned nothing: no answer and no tool call.\nEvidence: " + string(encoded) + "\nName the next concrete step now. Either call one tool with complete arguments, or write the final answer stating what blocks you. Do not reason silently.\n</point_empty_response_gate>"
}

func toolPlanNudgeFeedback(calls []providers.ToolCall, identicalPlans int, canRetry bool) string {
	type evidence struct {
		Code           string   `json:"code"`
		Tools          []string `json:"tools"`
		IdenticalPlans int      `json:"identicalPlans"`
		CanRetry       bool     `json:"canRetry"`
	}
	encoded, _ := json.Marshal(evidence{
		Code: "duplicate_tool_plan", Tools: toolCallNames(calls), IdenticalPlans: identicalPlans, CanRetry: canRetry,
	})
	guidance := "Change at least one tool or argument. Reusing an identical successful call will be rejected."
	if canRetry {
		guidance = "The previous attempt did not succeed. Change arguments, inspect missing evidence, or choose a different allowed tool instead of repeating the same failing plan."
	}
	return "<point_tool_plan_gate>\nPoint detected a repeated tool plan.\nEvidence: " + string(encoded) + "\n" + guidance + "\n</point_tool_plan_gate>"
}

func (e *Engine) rejectDuplicateTool(ctx context.Context, active *activeRun, call providers.ToolCall) domain.ToolResult {
	run := e.snapshot(active)
	result := workbenchtools.FailWithHint(
		"duplicate_tool_call",
		"the identical successful tool plan already ran in the previous step; use its existing result or choose a different action",
		"reuse the earlier tool output, inspect a different path/query, or continue with propose_patch / verification using the evidence you already have",
	)
	e.publishOrLog(ctx, run, domain.EventToolRequested, "model", map[string]any{"tool": call.Name, "arguments": call.Arguments})
	e.publishOrLog(ctx, run, domain.EventToolFinished, "agent", map[string]any{"tool": call.Name, "durationMs": 0, "result": result})
	return result
}

func patchInspectionMatches(inspection patchInspectionState, proposal domain.PatchProposal) bool {
	if !inspection.Known {
		return true
	}
	if proposal.OriginalExisted != inspection.OriginalExisted {
		return false
	}
	return !inspection.OriginalExisted || strings.EqualFold(proposal.OriginalHash, inspection.SHA256)
}

func inspectionHint(requirement patchInspectionRequirement) string {
	switch requirement.Code {
	case "inspection_required":
		if requirement.RequiredTool == "list_files" {
			return "call list_files (or read a neighboring file) in this turn, wait for the result, then propose the new file in a later turn"
		}
		return "call " + requirement.RequiredTool + " on the target in this turn, wait for the result, then propose_patch in a later turn"
	case "inspection_scope_required":
		return "search_code for each missing oldText fragment or read_file the complete target, then propose_patch only in a later turn"
	case "inspection_stale":
		return "read_file the complete current file again, then propose_patch in a later turn with anchors taken from that fresh result"
	default:
		return "inspect the target with the required tool in an earlier turn before proposing a patch"
	}
}

func (e *Engine) rejectPatchInspection(ctx context.Context, active *activeRun, toolName string, requirement patchInspectionRequirement, durationMs int64) domain.ToolResult {
	result := workbenchtools.FailWithHint(requirement.Code, requirement.Message, inspectionHint(requirement))
	observability.From(ctx).Warn("agent patch inspection rejected",
		"run_id", e.snapshot(active).ID,
		"tool", toolName,
		"code", requirement.Code,
		"path", requirement.Path,
		"required_tool", requirement.RequiredTool,
	)
	e.publishOrLog(ctx, e.snapshot(active), domain.EventAgentGuardrail, "agent", requirement)
	e.publishOrLog(ctx, e.snapshot(active), domain.EventToolFinished, "agent", map[string]any{"tool": toolName, "durationMs": durationMs, "result": result})
	return result
}
