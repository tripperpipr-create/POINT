package flowruntime

// What a node receives and what counts as its result: edge inputs, Condition
// and Verify checks, requireResult evidence and the final flow result. The
// runtime state machine in runtime.go only asks these questions.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"local-agent-workbench/internal/domain"
)

func cloneMap(value any) map[string]any {
	source, ok := value.(map[string]any)
	if !ok {
		return map[string]any{"value": value}
	}
	result := make(map[string]any, len(source))
	for key, item := range source {
		result[key] = item
	}
	return result
}

func collectInputs(edges []domain.FlowEdge, run domain.FlowRun) map[string]any {
	result := make(map[string]any, len(edges))
	for _, edge := range edges {
		state := run.NodeStates[edge.From]
		result[edge.From] = state.Output
	}
	return result
}

func evaluateCondition(config map[string]any, incoming []domain.FlowEdge, run domain.FlowRun) bool {
	inputs := collectInputs(incoming, run)
	var value any = inputs
	if sourceID, ok := config["sourceNodeId"].(string); ok && sourceID != "" {
		value = inputs[sourceID]
	} else if len(incoming) == 1 {
		value = inputs[incoming[0].From]
	}
	if field, ok := config["field"].(string); ok && field != "" {
		value = nestedValue(value, field)
	}
	operator, _ := config["operator"].(string)
	expected := config["value"]
	switch strings.ToLower(strings.TrimSpace(operator)) {
	case "equals", "eq", "==":
		return fmt.Sprint(value) == fmt.Sprint(expected)
	case "not_equals", "neq", "!=":
		return fmt.Sprint(value) != fmt.Sprint(expected)
	case "exists":
		return value != nil
	case "not_exists":
		return value == nil
	}
	if direct, ok := value.(map[string]any); ok {
		for _, key := range []string{"verified", "success", "branch", "completed"} {
			if candidate, exists := direct[key]; exists {
				return truthy(candidate)
			}
		}
	}
	return truthy(value)
}

func nestedValue(value any, field string) any {
	current := value
	for _, part := range strings.Split(field, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = object[part]
	}
	return current
}

func truthy(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		normalized := strings.ToLower(strings.TrimSpace(typed))
		return normalized != "" && normalized != "false" && normalized != "0" && normalized != "failed"
	case float64:
		return typed != 0
	case int:
		return typed != 0
	case nil:
		return false
	default:
		return true
	}
}

func verifyInputs(config map[string]any, incoming []domain.FlowEdge, run domain.FlowRun) (bool, []map[string]any) {
	checks := make([]map[string]any, 0, len(incoming))
	verified := len(incoming) > 0
	requireResult, _ := config["requireResult"].(bool)
	requireMerged, _ := config["requireMergedResult"].(bool)
	criteria, _ := config["criteria"].([]any)
	for _, edge := range incoming {
		state := run.NodeStates[edge.From]
		passed := state.Status == "completed"
		if status, ok := state.Output["status"].(string); ok {
			passed = passed && status == string(domain.RunCompleted)
		}
		resultPresent := true
		if requireResult {
			resultPresent = hasRequiredResult(state.Output)
			passed = passed && resultPresent
		}
		check := map[string]any{"nodeId": edge.From, "passed": passed}
		if requireResult {
			check["resultPresent"] = resultPresent
		}
		if requireMerged {
			mergeOK := false
			if verifiedFlag, _ := state.Output["mergedResultVerified"].(bool); verifiedFlag {
				mergeOK = true
			} else if mergeID, _ := state.Output["mergeChangeSetId"].(string); strings.TrimSpace(mergeID) != "" {
				if wait, _ := state.Output["waitReason"].(string); wait != "sandbox_merge_conflict" {
					mergeOK = true
				}
			} else if lineage, _ := state.Output["sandboxLineage"].(string); lineage != "merged_parallel_join" {
				// Sequential upstream does not need a merge artifact.
				mergeOK = true
			}
			check["mergedResult"] = mergeOK
			passed = passed && mergeOK
		}
		if len(criteria) > 0 {
			criteriaOK := briefCriteriaSatisfied(state.Output, criteria)
			check["briefCriteria"] = criteriaOK
			passed = passed && criteriaOK
		}
		check["passed"] = passed
		checks = append(checks, check)
		verified = verified && passed
	}
	return verified, checks
}

func briefCriteriaSatisfied(output map[string]any, criteria []any) bool {
	if output == nil {
		return false
	}
	status, _ := output["completionStatus"].(string)
	if status == "verified" || status == "needs_review" || status == "accepted_after_revision" {
		return true
	}
	if evidence, ok := output["completionEvidence"].(map[string]any); ok {
		if evidenceStatus, _ := evidence["status"].(string); evidenceStatus == "verified" || evidenceStatus == "needs_review" {
			return true
		}
	}
	// Manual-only contracts may finish with a non-empty result and explicit review flag.
	if review, _ := output["needsReview"].(bool); review && hasRequiredResult(output) {
		return true
	}
	_ = criteria
	return false
}

// hasRequiredResult accepts either a direct agent result or a Join/Loop
// aggregate where every participating branch has a non-empty result. This
// keeps requireResult honest without rejecting parallel plans merely because
// the graph wrapped their outputs in a Join node.
func hasRequiredResult(output map[string]any) bool {
	if output == nil {
		return false
	}
	if result, ok := output["result"]; ok {
		return meaningfulResult(result)
	}
	if results, ok := output["results"]; ok {
		present, valid := resultCollectionEvidence(results, 0)
		return present && valid
	}
	return false
}

func resultCollectionEvidence(value any, depth int) (bool, bool) {
	if depth > 8 || value == nil {
		return false, true
	}
	switch typed := value.(type) {
	case map[string]any:
		present := false
		for _, child := range typed {
			if child == nil {
				continue
			}
			childPresent, childValid := resultEnvelopeEvidence(child, depth+1)
			if !childPresent || !childValid {
				return present, false
			}
			present = true
		}
		return present, true
	case []any:
		present := false
		for _, child := range typed {
			if child == nil {
				continue
			}
			childPresent, childValid := resultEnvelopeEvidence(child, depth+1)
			if !childPresent || !childValid {
				return present, false
			}
			present = true
		}
		return present, true
	default:
		return meaningfulResult(typed), meaningfulResult(typed)
	}
}

func resultEnvelopeEvidence(value any, depth int) (bool, bool) {
	if depth > 8 || value == nil {
		return false, true
	}
	object, ok := value.(map[string]any)
	if !ok {
		valid := meaningfulResult(value)
		return valid, valid
	}
	if result, exists := object["result"]; exists {
		return true, meaningfulResult(result)
	}
	if results, exists := object["results"]; exists {
		return resultCollectionEvidence(results, depth+1)
	}
	if nested, exists := object["output"]; exists {
		return resultEnvelopeEvidence(nested, depth+1)
	}
	return false, false
}

func meaningfulResult(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(typed) != ""
	case []byte:
		return len(bytes.TrimSpace(typed)) > 0
	case map[string]any:
		return len(typed) > 0
	case []any:
		return len(typed) > 0
	default:
		return strings.TrimSpace(fmt.Sprint(typed)) != ""
	}
}

func flowResult(flow domain.FlowGraph, run domain.FlowRun) string {
	for _, node := range flow.Nodes {
		if node.Kind != domain.FlowNodeOutput {
			continue
		}
		if state := run.NodeStates[node.ID]; state.Output != nil {
			if result, ok := state.Output["result"].(string); ok {
				return result
			}
			if data, err := json.Marshal(state.Output["result"]); err == nil {
				return string(data)
			}
		}
	}
	return "flow completed"
}
