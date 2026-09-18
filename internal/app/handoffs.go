package app

import (
	"context"
	"errors"
	"sort"
	"strings"

	"local-agent-workbench/internal/domain"
)

// Что агент получил от предшественника.
//
// Эстафета между агентами работала и раньше: следующий стартует из песочницы
// предыдущего и получает структурированную передачу. Но она уезжала в модель
// текстовым блобом и человеку не показывалась. Когда второй агент делал не то,
// оставалось гадать — он не понял задачу или ему не то передали.
//
// Здесь та же передача, собранная из неизменяемого состояния флоу и пригодная
// для чтения человеком.

type Handoff struct {
	FromNodeID string `json:"fromNodeId"`
	FromNode   string `json:"fromNode"`
	FromAgent  string `json:"fromAgent,omitempty"`
	ToNodeID   string `json:"toNodeId"`
	ToNode     string `json:"toNode"`
	ToAgent    string `json:"toAgent,omitempty"`

	Status  string `json:"status"`
	Summary string `json:"summary,omitempty"`
	// ChangedFiles — что предшественник тронул. Пустой список при завершённом
	// узле означает, что он ничего не изменил, и это тоже важный факт.
	ChangedFiles []string `json:"changedFiles,omitempty"`
	ChangeSetIDs []string `json:"changeSetIds,omitempty"`
	// Delivered=false — узел ещё не начал работу, передача только подготовлена.
	Delivered bool `json:"delivered"`
}

type HandoffChain struct {
	FlowRunID string    `json:"flowRunId"`
	Items     []Handoff `json:"items"`
	// Waiting — узлы, которые ждут предшественника. Их отсутствие в цепочке не
	// значит, что что-то сломалось: работа просто ещё не дошла.
	Waiting []string `json:"waiting,omitempty"`
}

func agentNameByID(agents []domain.ProjectAgent, id string) string {
	for _, agent := range agents {
		if agent.ID == id {
			return agent.Name
		}
	}
	return id
}

func stringsFromOutput(output map[string]any, key string) []string {
	raw, ok := output[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
			out = append(out, text)
		}
	}
	return out
}

// Handoffs собирает цепочку передач одного прогона флоу.
func (a *App) Handoffs(ctx context.Context, flowRunID string) (HandoffChain, error) {
	if strings.TrimSpace(flowRunID) == "" {
		return HandoffChain{}, errors.New("flowRunId is required")
	}
	workspaceID := a.currentWorldID()
	flowRun, err := a.store.GetFlowRun(ctx, flowRunID)
	if err != nil {
		return HandoffChain{}, err
	}
	flow, err := a.store.GetFlow(ctx, flowRun.FlowID)
	if err != nil {
		return HandoffChain{}, err
	}
	agents, err := a.store.ListProjectAgents(ctx, workspaceID)
	if err != nil {
		return HandoffChain{}, err
	}
	changeSets, err := a.store.ListChangeSets(ctx, workspaceID)
	if err != nil {
		return HandoffChain{}, err
	}

	nodeByID := make(map[string]domain.FlowNode, len(flow.Nodes))
	for _, node := range flow.Nodes {
		nodeByID[node.ID] = node
	}
	setsByExecution := map[string][]string{}
	for _, set := range changeSets {
		if set.ExecutionID != "" {
			setsByExecution[set.ExecutionID] = append(setsByExecution[set.ExecutionID], set.ID)
		}
	}

	label := func(id string) string {
		if name := strings.TrimSpace(nodeByID[id].Name); name != "" {
			return name
		}
		return id
	}

	result := HandoffChain{FlowRunID: flowRunID, Items: []Handoff{}}
	waiting := map[string]bool{}

	for _, edge := range flow.Edges {
		fromState, hasFrom := flowRun.NodeStates[edge.From]
		if !hasFrom || fromState.Status != "completed" {
			// Получатель ждёт: показать это честнее, чем промолчать.
			if _, ok := flowRun.NodeStates[edge.To]; ok {
				waiting[label(edge.To)] = true
			}
			continue
		}
		toState := flowRun.NodeStates[edge.To]
		executionID, _ := fromState.Output["executionId"].(string)

		item := Handoff{
			FromNodeID:   edge.From,
			FromNode:     label(edge.From),
			FromAgent:    agentNameByID(agents, nodeByID[edge.From].AgentID),
			ToNodeID:     edge.To,
			ToNode:       label(edge.To),
			ToAgent:      agentNameByID(agents, nodeByID[edge.To].AgentID),
			Status:       fromState.Status,
			Summary:      handoffResultSummary(fromState.Output),
			ChangedFiles: stringsFromOutput(fromState.Output, "changedFiles"),
			ChangeSetIDs: setsByExecution[executionID],
			// Передача доставлена, когда получатель уже начал работу: до этого
			// она подготовлена, но агент её ещё не видел.
			Delivered: toState.Status != "" && toState.Status != "ready" && toState.Status != "blocked",
		}
		result.Items = append(result.Items, item)
	}

	sort.SliceStable(result.Items, func(i, j int) bool {
		if result.Items[i].FromNodeID != result.Items[j].FromNodeID {
			return result.Items[i].FromNodeID < result.Items[j].FromNodeID
		}
		return result.Items[i].ToNodeID < result.Items[j].ToNodeID
	})
	for node := range waiting {
		result.Waiting = append(result.Waiting, node)
	}
	sort.Strings(result.Waiting)
	return result, nil
}
