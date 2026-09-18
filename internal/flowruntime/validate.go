package flowruntime

import (
	"fmt"
	"strings"

	"local-agent-workbench/internal/domain"
)

// ValidateGraph rejects graphs whose persisted state machine would be
// ambiguous or impossible to execute safely.
func ValidateGraph(flow domain.FlowGraph) error {
	if len(flow.Nodes) == 0 {
		return fmt.Errorf("flow has no nodes")
	}
	nodes := make(map[string]domain.FlowNode, len(flow.Nodes))
	inputIDs := make([]string, 0, 1)
	for _, node := range flow.Nodes {
		node.ID = strings.TrimSpace(node.ID)
		if node.ID == "" {
			return fmt.Errorf("flow node id is required")
		}
		if _, exists := nodes[node.ID]; exists {
			return fmt.Errorf("duplicate flow node id %q", node.ID)
		}
		switch node.Kind {
		case domain.FlowNodeInput:
			inputIDs = append(inputIDs, node.ID)
		case domain.FlowNodeAgent:
			if strings.TrimSpace(node.AgentID) == "" {
				return fmt.Errorf("agent node %q requires agentId", node.ID)
			}
		case domain.FlowNodeTool:
			if strings.TrimSpace(node.AgentID) == "" || strings.TrimSpace(node.ToolName) == "" {
				return fmt.Errorf("tool node %q requires agentId and toolName", node.ID)
			}
		case domain.FlowNodeLoop:
			if _, err := boundedLoopLimit(node.Config); err != nil {
				return fmt.Errorf("loop node %q: %w", node.ID, err)
			}
		case domain.FlowNodeOutput, domain.FlowNodeCondition, domain.FlowNodeParallel,
			domain.FlowNodeJoin, domain.FlowNodeVerifier, domain.FlowNodeApproval:
		default:
			return fmt.Errorf("node %q has unsupported kind %q", node.ID, node.Kind)
		}
		policy := node.FailurePolicy
		mode := strings.ToLower(strings.TrimSpace(policy.Mode))
		if node.Kind != domain.FlowNodeAgent && mode != "" && mode != "stop" {
			return fmt.Errorf("node %q failure recovery is supported only for agent nodes", node.ID)
		}
		switch mode {
		case "", "stop":
			if policy.MaxRetries != 0 || strings.TrimSpace(policy.FallbackAgentID) != "" {
				return fmt.Errorf("node %q stop policy cannot define retries or fallback", node.ID)
			}
		case "retry":
			if policy.MaxRetries < 0 || policy.MaxRetries > 2 {
				return fmt.Errorf("node %q retry policy allows at most 2 retries", node.ID)
			}
			if strings.TrimSpace(policy.FallbackAgentID) != "" {
				return fmt.Errorf("node %q retry policy cannot define fallbackAgentId", node.ID)
			}
		case "fallback_agent":
			if strings.TrimSpace(policy.FallbackAgentID) == "" {
				return fmt.Errorf("node %q fallback policy requires fallbackAgentId", node.ID)
			}
			if policy.MaxRetries != 0 {
				return fmt.Errorf("node %q fallback policy cannot define maxRetries", node.ID)
			}
		default:
			return fmt.Errorf("node %q has unsupported failure policy %q", node.ID, policy.Mode)
		}
		nodes[node.ID] = node
	}
	if len(inputIDs) != 1 {
		return fmt.Errorf("flow requires exactly one input node, got %d", len(inputIDs))
	}
	outgoing := map[string][]domain.FlowEdge{}
	incoming := map[string]int{}
	edgeIDs := map[string]bool{}
	for _, edge := range flow.Edges {
		if edge.ID != "" {
			if edgeIDs[edge.ID] {
				return fmt.Errorf("duplicate flow edge id %q", edge.ID)
			}
			edgeIDs[edge.ID] = true
		}
		if _, ok := nodes[edge.From]; !ok {
			return fmt.Errorf("edge %q references missing source %q", edge.ID, edge.From)
		}
		if _, ok := nodes[edge.To]; !ok {
			return fmt.Errorf("edge %q references missing target %q", edge.ID, edge.To)
		}
		if edge.From == edge.To {
			return fmt.Errorf("edge %q is a self-cycle", edge.ID)
		}
		outgoing[edge.From] = append(outgoing[edge.From], edge)
		incoming[edge.To]++
	}
	if incoming[inputIDs[0]] != 0 {
		return fmt.Errorf("input node cannot have incoming edges")
	}
	for id, node := range nodes {
		if node.Kind != domain.FlowNodeLoop {
			continue
		}
		if incoming[id] != 2 {
			return fmt.Errorf("loop node %q requires exactly one entry edge and one bounded back edge", id)
		}
		continueEdges, doneEdges := 0, 0
		continueTarget, doneTarget := "", ""
		for _, edge := range outgoing[id] {
			switch strings.ToLower(strings.TrimSpace(edge.Condition)) {
			case "continue":
				continueEdges++
				continueTarget = edge.To
			case "done":
				doneEdges++
				doneTarget = edge.To
			default:
				return fmt.Errorf("loop node %q edges must use condition continue or done", id)
			}
		}
		if continueEdges != 1 || doneEdges != 1 || continueTarget == doneTarget {
			return fmt.Errorf("loop node %q requires one continue edge and one distinct done edge", id)
		}
	}
	visiting, visited := map[string]bool{}, map[string]bool{}
	loopBackEdges := map[string]int{}
	var visit func(string) error
	visit = func(id string) error {
		if visiting[id] {
			return fmt.Errorf("flow contains a cycle through node %q", id)
		}
		if visited[id] {
			return nil
		}
		visiting[id] = true
		for _, edge := range outgoing[id] {
			if visiting[edge.To] {
				if nodes[edge.To].Kind != domain.FlowNodeLoop {
					return fmt.Errorf("flow contains an unbounded cycle through node %q", edge.To)
				}
				if strings.TrimSpace(edge.Condition) != "" {
					return fmt.Errorf("loop back edge %q must not have a condition", edge.ID)
				}
				loopBackEdges[edge.To]++
				continue
			}
			if err := visit(edge.To); err != nil {
				return err
			}
		}
		visiting[id] = false
		visited[id] = true
		return nil
	}
	if err := visit(inputIDs[0]); err != nil {
		return err
	}
	if len(visited) != len(nodes) {
		for id := range nodes {
			if !visited[id] {
				return fmt.Errorf("flow node %q is unreachable from input", id)
			}
		}
	}
	for id, node := range nodes {
		if node.Kind == domain.FlowNodeLoop && loopBackEdges[id] != 1 {
			return fmt.Errorf("loop node %q requires exactly one bounded back edge", id)
		}
	}
	return nil
}

func boundedLoopLimit(config map[string]any) (int, error) {
	if config == nil {
		return 0, fmt.Errorf("maxIterations is required")
	}
	raw, ok := config["maxIterations"]
	if !ok {
		return 0, fmt.Errorf("maxIterations is required")
	}
	var value int
	switch typed := raw.(type) {
	case int:
		value = typed
	case int64:
		value = int(typed)
	case float64:
		if typed != float64(int(typed)) {
			return 0, fmt.Errorf("maxIterations must be an integer")
		}
		value = int(typed)
	default:
		return 0, fmt.Errorf("maxIterations must be an integer")
	}
	if value < 1 || value > 20 {
		return 0, fmt.Errorf("maxIterations must be between 1 and 20")
	}
	return value, nil
}
