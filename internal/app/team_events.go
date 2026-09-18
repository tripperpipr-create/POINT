package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
)

var teamEventKinds = map[string]bool{
	"question": true, "answer": true, "blocker": true, "contract": true,
	"decision": true, "review_request": true, "finding": true, "status": true,
}

func (a *App) PublishTeamEvent(ctx context.Context, event domain.TeamEvent) (domain.TeamEvent, error) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return domain.TeamEvent{}, err
	}
	if event.WorkspaceID != "" && event.WorkspaceID != ws.ID {
		return domain.TeamEvent{}, errors.New("team event belongs to another workspace")
	}
	event.WorkspaceID = ws.ID
	event.Kind = strings.ToLower(strings.TrimSpace(event.Kind))
	event.Message = strings.TrimSpace(event.Message)
	if !teamEventKinds[event.Kind] || event.Message == "" || len([]rune(event.Message)) > 4000 {
		return domain.TeamEvent{}, errors.New("invalid team event")
	}
	run, err := a.store.GetFlowRun(ctx, event.FlowRunID)
	if err != nil || run.WorkspaceID != ws.ID {
		return domain.TeamEvent{}, errors.New("team event flow is unavailable")
	}
	flow, err := a.store.GetFlow(ctx, run.FlowID)
	if err != nil {
		return domain.TeamEvent{}, err
	}
	senderOK, recipientOK := false, event.ToAgentID == ""
	for _, node := range flow.Nodes {
		if node.ID == event.FlowNodeID && node.AgentID == event.FromAgentID {
			senderOK = true
		}
		if event.ToAgentID != "" && node.AgentID == event.ToAgentID {
			recipientOK = true
		}
	}
	if !senderOK || !recipientOK {
		return domain.TeamEvent{}, errors.New("team event sender or recipient is outside the Flow")
	}
	event.ID = domain.NewID("teamevent")
	event.CreatedAt = time.Now().UTC()
	if err = a.store.SaveTeamEvent(ctx, event); err != nil {
		return domain.TeamEvent{}, err
	}
	if event.Kind == "blocker" && event.QuestID != "" {
		quests, _ := a.store.ListQuests(ctx, ws.ID)
		for _, quest := range quests {
			if quest.ID == event.QuestID && quest.Kind == "project" {
				quest.ControllerState = controllerReplanning
				if quest.Controller == nil {
					quest.Controller = map[string]any{}
				}
				quest.Controller["blockerEventId"] = event.ID
				quest.Controller["blocker"] = event.Message
				quest.Controller["pendingReplan"] = true
				quest.Controller["pendingReplanReason"] = "team blocker: " + event.Message
				if quest.FlowID == "" {
					quest.FlowID = run.FlowID
				}
				quest.UpdatedAt = time.Now().UTC()
				if err = a.store.SaveQuest(ctx, quest); err != nil {
					return event, err
				}
				maxReplans := 0
				if quest.Brief != nil {
					maxReplans = quest.Brief.Budget.MaxReplans
				}
				if maxReplans <= 0 {
					quest.ControllerState = controllerNeedsUser
					quest.Status = domain.QuestPaused
					quest.UpdatedAt = time.Now().UTC()
					_ = a.store.SaveQuest(ctx, quest)
				} else if result, replanErr := a.ReplanQuest(ctx, ReplanQuestRequest{
					QuestID: quest.ID,
					Reason:  "team blocker: " + event.Message,
					Stages: []domain.ReplanStagePatch{{
						AgentID: event.FromAgentID, Name: "Resolve blocker",
						Instruction: "Resolve the team blocker without rewriting completed history: " + event.Message,
					}},
				}); result.Replan.ID == "" {
					quest.ControllerState = controllerNeedsUser
					quest.Status = domain.QuestPaused
					if replanErr != nil {
						quest.Controller["pendingReplanReason"] = replanErr.Error()
					}
					quest.UpdatedAt = time.Now().UTC()
					_ = a.store.SaveQuest(ctx, quest)
				}
				break
			}
		}
	}
	return event, nil
}

func (a *App) TeamInbox(ctx context.Context, flowRunID, agentID string, acknowledge bool) ([]domain.TeamEvent, error) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return nil, err
	}
	events, err := a.store.ListTeamEvents(ctx, ws.ID, flowRunID, agentID, true, 100)
	if err != nil {
		return nil, err
	}
	if acknowledge {
		now := time.Now().UTC()
		for index := range events {
			if events[index].ToAgentID == agentID {
				events[index].DeliveredAt = &now
				if err = a.store.SaveTeamEvent(ctx, events[index]); err != nil {
					return nil, err
				}
			}
		}
	}
	return events, nil
}

func (a *App) ListTeamEvents(flowRunID string) ([]domain.TeamEvent, error) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return nil, err
	}
	return a.store.ListTeamEvents(context.Background(), ws.ID, flowRunID, "", false, 500)
}
