package flowruntime

// Loop bookkeeping: which incoming attempts a Loop node has already seen and
// the bounded result it reports once it stops iterating.

import (
	"encoding/json"
	"fmt"
	"strings"

	"local-agent-workbench/internal/domain"
)

func loopIncomingUnseen(state domain.FlowNodeState, sourceID string, sourceAttempts int) bool {
	return sourceAttempts > loopSeenAttempts(state.Output)[sourceID]
}

func loopSeenAttempts(output map[string]any) map[string]int {
	result := map[string]int{}
	if output == nil {
		return result
	}
	raw, ok := output["seenIncoming"].(map[string]any)
	if ok {
		for key, value := range raw {
			result[key] = numericInt(value)
		}
		return result
	}
	if typed, ok := output["seenIncoming"].(map[string]int); ok {
		for key, value := range typed {
			result[key] = value
		}
	}
	return result
}

func loopResults(output map[string]any) []any {
	if output == nil {
		return []any{}
	}
	if values, ok := output["results"].([]any); ok {
		return append([]any(nil), values...)
	}
	return []any{}
}

func boundedLoopResult(nodeID string, state domain.FlowNodeState) map[string]any {
	value := any(state.Output)
	if encoded, err := json.Marshal(value); err != nil || len(encoded) > 16*1024 {
		summary := strings.TrimSpace(fmt.Sprint(state.Output["result"]))
		if len([]rune(summary)) > 4000 {
			summary = string([]rune(summary)[:4000])
		}
		value = map[string]any{"summary": summary, "truncated": true}
	}
	return map[string]any{"nodeId": nodeID, "attempt": state.Attempts, "output": value}
}

func numericInt(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
}
