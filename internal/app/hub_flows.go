// Схемы потока и их прогоны.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/flowruntime"
	"local-agent-workbench/internal/policy"
)

// DeleteFlow убирает схему проекта.
//
// Без этого действия любой отработавший квест держал своих исполнителей вечно:
// Мастер создаёт схему под квест, узлы схемы называют агента по идентификатору,
// а удаление агента отказом отправляет «заменить его в схеме». Заменять было
// негде — схему нельзя было ни удалить, ни закрыть, — и персонаж оставался в
// ростере навсегда вместе со схемой, которой больше никто не пользуется.
//
// Отказы здесь те же, что у правки графа: живой прогон и незакрытый квест. Оба
// названы вместе с местом, где их снимают.
func (a *App) DeleteFlow(flowID string) error {
	flowID = strings.TrimSpace(flowID)
	if flowID == "" {
		return errors.New("не указана схема")
	}
	ws, err := a.requireWorkspace()
	if err != nil {
		return err
	}
	ctx := context.Background()
	flow, err := a.store.GetFlow(ctx, flowID)
	if err != nil {
		return err
	}
	if flow.WorkspaceID != ws.ID {
		return errors.New("схема принадлежит другому проекту")
	}
	if err = flowHasActiveRun(a.store, flowID); err != nil {
		return err
	}
	quests, err := a.store.ListQuests(ctx, ws.ID)
	if err != nil {
		return err
	}
	for _, quest := range quests {
		if quest.FlowID != flowID {
			continue
		}
		switch quest.Status {
		case domain.QuestCompleted, domain.QuestNeedsReview, domain.QuestBlocked, domain.QuestFailed, domain.QuestCancelled:
			continue
		}
		return fmt.Errorf("схему ведёт квест %q — закройте или удалите квест", quest.Title)
	}
	return a.store.DeleteFlow(ctx, ws.ID, flowID)
}

func (a *App) SaveFlow(flow domain.FlowGraph) (domain.FlowGraph, error) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return domain.FlowGraph{}, err
	}
	if flow.WorkspaceID != "" && flow.WorkspaceID != ws.ID {
		return domain.FlowGraph{}, errors.New("flow workspace does not match the open project")
	}
	flow.WorkspaceID = ws.ID
	if flow.ID != "" {
		if err := flowHasActiveRun(a.store, flow.ID); err != nil {
			return domain.FlowGraph{}, err
		}
	}
	now := time.Now().UTC()
	if err := flowruntime.ValidateGraph(flow); err != nil {
		return domain.FlowGraph{}, err
	}
	agents, err := a.store.ListProjectAgents(context.Background(), flow.WorkspaceID)
	if err != nil {
		return domain.FlowGraph{}, err
	}
	agentByID := make(map[string]domain.ProjectAgent, len(agents))
	for _, agent := range agents {
		agentByID[agent.ID] = agent
	}
	for _, node := range flow.Nodes {
		if node.Kind != domain.FlowNodeAgent && node.Kind != domain.FlowNodeTool {
			continue
		}
		agent, ok := agentByID[node.AgentID]
		if !ok {
			return domain.FlowGraph{}, fmt.Errorf("flow node %q references unknown project agent %q", node.ID, node.AgentID)
		}
		if node.Kind == domain.FlowNodeTool && !policy.ProfileGrants(domain.ProfileFromProjectAgent(agent)).Allows(node.ToolName) {
			return domain.FlowGraph{}, fmt.Errorf("tool node %q uses tool %q not allowed by agent %q", node.ID, node.ToolName, agent.Name)
		}
		if node.Kind == domain.FlowNodeTool {
			decision := policy.Engine{TrustedCustomTool: a.trustedCustomTool}.Evaluate(domain.ProfileFromProjectAgent(agent), node.ToolName)
			if decision.Risk != domain.ToolRiskLow || decision.RequiresApproval || decision.Policy != domain.ToolPolicyAllow {
				return domain.FlowGraph{}, fmt.Errorf("tool node %q requires an explicitly allowed low-risk tool; %q needs an agent or approval workflow", node.ID, node.ToolName)
			}
		}
		if fallbackID := strings.TrimSpace(node.FailurePolicy.FallbackAgentID); fallbackID != "" {
			fallback, exists := agentByID[fallbackID]
			if !exists {
				return domain.FlowGraph{}, fmt.Errorf("flow node %q references unknown fallback agent %q", node.ID, fallbackID)
			}
			if fallback.ID == agent.ID {
				return domain.FlowGraph{}, fmt.Errorf("flow node %q fallback agent must differ from its primary agent", node.ID)
			}
		}
	}
	if flow.ID == "" {
		flow.ID = domain.NewID("flow")
		flow.CreatedAt = now
	}
	if flow.CreatedAt.IsZero() {
		flow.CreatedAt = now
	}
	flow.UpdatedAt = now
	if err := a.store.SaveFlow(context.Background(), flow); err != nil {
		return domain.FlowGraph{}, err
	}
	return flow, nil
}

func (a *App) StartFlowRun(flowID, questID string, input map[string]any) (domain.FlowRun, error) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return domain.FlowRun{}, err
	}
	flow, err := a.store.GetFlow(context.Background(), flowID)
	if err != nil {
		return domain.FlowRun{}, err
	}
	if flow.WorkspaceID != ws.ID {
		return domain.FlowRun{}, errors.New("flow belongs to another workspace")
	}
	if err = a.requireProjectAgentsReady(context.Background(), ws.ID, flowProjectAgentIDs(flow)); err != nil {
		return domain.FlowRun{}, err
	}
	runtime := flowruntime.Runtime{Store: a.store}
	run, err := runtime.Start(context.Background(), flowruntime.StartRequest{
		FlowID: flowID, WorkspaceID: ws.ID, QuestID: questID, Input: input,
	})
	if err != nil {
		return domain.FlowRun{}, err
	}
	quest := domain.Quest{ID: questID, WorkspaceID: ws.ID, Title: flow.Name, FlowID: flow.ID, Importance: domain.QuestNormal}
	if questID != "" {
		if quests, listErr := a.store.ListQuests(context.Background(), ws.ID); listErr == nil {
			for _, item := range quests {
				if item.ID == questID {
					quest = item
					break
				}
			}
		}
	}
	if _, err = a.ensureFlowNodeQuests(quest, flow, run); err != nil {
		return run, err
	}
	if err = a.scheduleFlowAgentExecutionsFromRun(run); err != nil {
		slog.Error("flow run schedule failed", "flow_run_id", run.ID, "flow_id", flowID, "error", err)
		return run, err
	}
	slog.Info("flow run started", "flow_run_id", run.ID, "flow_id", flowID, "quest_id", questID, "workspace_id", ws.ID)
	return a.store.GetFlowRun(context.Background(), run.ID)
}

func (a *App) TickFlowRun(flowRunID string) (domain.FlowRun, error) {
	runtime := flowruntime.Runtime{Store: a.store}
	return runtime.Tick(context.Background(), flowRunID)
}

func (a *App) CompileWorkflowToFlow(workflowID string) (domain.FlowGraph, error) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return domain.FlowGraph{}, err
	}
	workflows, err := a.store.ListWorkflows(context.Background())
	if err != nil {
		return domain.FlowGraph{}, err
	}
	for _, workflow := range workflows {
		if workflow.ID == workflowID {
			flow := flowruntime.CompileLinearWorkflow(ws.ID, workflow)
			return a.SaveFlow(flow)
		}
	}
	return domain.FlowGraph{}, fmt.Errorf("workflow %s not found", workflowID)
}
