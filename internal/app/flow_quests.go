package app

import (
	"context"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
)

// ensureFlowNodeQuests creates exactly one child quest for every executable
// agent node. Tool/control nodes remain implementation details of the Flow.
func (a *App) ensureFlowNodeQuests(parent domain.Quest, flow domain.FlowGraph, flowRun domain.FlowRun) (map[string]domain.Quest, error) {
	quests, err := a.store.ListQuests(context.Background(), flowRun.WorkspaceID)
	if err != nil {
		return nil, err
	}
	result := make(map[string]domain.Quest)
	for _, quest := range quests {
		if quest.FlowRunID == flowRun.ID && quest.FlowNodeID != "" {
			result[quest.FlowNodeID] = quest
		}
	}
	now := time.Now().UTC()
	for _, node := range flow.Nodes {
		if node.Kind != domain.FlowNodeAgent {
			continue
		}
		if _, exists := result[node.ID]; exists {
			continue
		}
		title := strings.TrimSpace(node.Name)
		if title == "" {
			title = "Agent node " + node.ID
		}
		description := "Flow node of " + strings.TrimSpace(parent.Title)
		if instruction, ok := node.Config["instruction"].(string); ok && strings.TrimSpace(instruction) != "" {
			description = strings.TrimSpace(instruction)
		}
		child := domain.Quest{
			ID: domain.NewID("quest"), WorkspaceID: flowRun.WorkspaceID, ParentID: parent.ID,
			Kind:  "milestone",
			Title: title, Description: description, Objectives: []string{title},
			Constraints: append([]string(nil), parent.Constraints...), DefinitionOfDone: append([]string(nil), parent.DefinitionOfDone...),
			Importance: parent.Importance, Status: domain.QuestDraft, TeamID: parent.TeamID, FlowID: flow.ID,
			FlowRunID: flowRun.ID, FlowNodeID: node.ID, AssignedAgentID: node.AgentID,
			CreatedAt: now, UpdatedAt: now,
		}
		if child.Importance == "" {
			child.Importance = domain.QuestNormal
		}
		if ids := stringsFromNodeConfig(node.Config, "criterionIds"); len(ids) > 0 && parent.Brief != nil {
			child.DefinitionOfDone = criterionTexts(parent.Brief.Criteria, ids)
		}
		if err = a.store.SaveQuest(context.Background(), child); err != nil {
			return nil, err
		}
		result[node.ID] = child
	}
	return result, nil
}

func stringsFromNodeConfig(config map[string]any, key string) []string {
	if config == nil {
		return nil
	}
	switch values := config[key].(type) {
	case []string:
		return append([]string(nil), values...)
	case []any:
		result := make([]string, 0, len(values))
		for _, value := range values {
			if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
				result = append(result, strings.TrimSpace(text))
			}
		}
		return result
	default:
		return nil
	}
}

func criterionTexts(criteria []domain.AcceptanceCriterion, ids []string) []string {
	wanted := make(map[string]bool, len(ids))
	for _, id := range ids {
		wanted[id] = true
	}
	var result []string
	for _, criterion := range criteria {
		if wanted[criterion.ID] {
			result = append(result, criterion.Text)
		}
	}
	return result
}

func (a *App) setFlowChildQuestStatus(flowRunID, nodeID string, status domain.QuestStatus) {
	if flowRunID == "" || nodeID == "" {
		return
	}
	workspaceID := ""
	if run, err := a.store.GetFlowRun(context.Background(), flowRunID); err == nil {
		workspaceID = run.WorkspaceID
	}
	if workspaceID == "" {
		return
	}
	quests, err := a.store.ListQuests(context.Background(), workspaceID)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	for _, quest := range quests {
		if quest.FlowRunID != flowRunID || quest.FlowNodeID != nodeID {
			continue
		}
		quest.Status = status
		quest.UpdatedAt = now
		switch status {
		case domain.QuestCompleted, domain.QuestNeedsReview, domain.QuestBlocked, domain.QuestFailed, domain.QuestCancelled:
			quest.FinishedAt = &now
		default:
			quest.FinishedAt = nil
		}
		_ = a.store.SaveQuest(context.Background(), quest)
		return
	}
}

func (a *App) closeUnfinishedFlowChildQuests(flowRunID string, success bool) {
	run, err := a.store.GetFlowRun(context.Background(), flowRunID)
	if err != nil {
		return
	}
	quests, err := a.store.ListQuests(context.Background(), run.WorkspaceID)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	for _, quest := range quests {
		// Only quests materialized for actual Flow nodes are branch children.
		// The root quest shares FlowRunID with them, but has neither ParentID nor
		// FlowNodeID and must be finalized by the evidence gate below.
		if quest.FlowRunID != flowRunID || quest.ParentID != run.QuestID || strings.TrimSpace(quest.FlowNodeID) == "" {
			continue
		}
		switch quest.Status {
		case domain.QuestCompleted, domain.QuestNeedsReview, domain.QuestBlocked, domain.QuestFailed, domain.QuestCancelled:
			continue
		}
		if success {
			quest.Status = domain.QuestCancelled // branch was skipped by a successful Flow
		} else {
			quest.Status = domain.QuestCancelled
		}
		quest.UpdatedAt, quest.FinishedAt = now, &now
		_ = a.store.SaveQuest(context.Background(), quest)
	}
}
