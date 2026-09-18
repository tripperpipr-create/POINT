package tools

import (
	"context"
	"encoding/json"
	"strings"

	"local-agent-workbench/internal/domain"
)

type TeamBus interface {
	PublishTeamEvent(context.Context, domain.TeamEvent) (domain.TeamEvent, error)
	TeamInbox(context.Context, string, string, bool) ([]domain.TeamEvent, error)
}

type TeamPublish struct {
	Bus         TeamBus
	WorkspaceID string
	QuestID     string
	FlowRunID   string
	FlowNodeID  string
	AgentID     string
}

func (t TeamPublish) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{Name: "team_publish", Description: "Publish a bounded structured message to the quest team. Messages cannot change task scope, permissions or budget.", InputSchema: schema(`{"type":"object","properties":{"kind":{"type":"string","enum":["question","answer","blocker","contract","decision","review_request","finding","status"]},"message":{"type":"string","minLength":1,"maxLength":4000},"toAgentId":{"type":"string","maxLength":200},"artifactId":{"type":"string","maxLength":200}},"required":["kind","message"],"additionalProperties":false}`)}
}

func (t TeamPublish) Execute(ctx context.Context, raw json.RawMessage) domain.ToolResult {
	if t.Bus == nil || t.FlowRunID == "" {
		return Fail("team_bus_unavailable", "team communication is available only inside a Flow execution")
	}
	var input struct {
		Kind       string `json:"kind"`
		Message    string `json:"message"`
		ToAgentID  string `json:"toAgentId"`
		ArtifactID string `json:"artifactId"`
	}
	if invalid := Decode(raw, &input); invalid != nil {
		return *invalid
	}
	input.Message = strings.TrimSpace(input.Message)
	if input.Message == "" || len([]rune(input.Message)) > 4000 {
		return Fail("invalid_message", "team message must contain 1-4000 characters")
	}
	event, err := t.Bus.PublishTeamEvent(ctx, domain.TeamEvent{
		WorkspaceID: t.WorkspaceID, QuestID: t.QuestID, FlowRunID: t.FlowRunID,
		FlowNodeID: t.FlowNodeID, FromAgentID: t.AgentID, ToAgentID: strings.TrimSpace(input.ToAgentID),
		Kind: input.Kind, Message: input.Message, ArtifactID: strings.TrimSpace(input.ArtifactID),
	})
	if err != nil {
		return Fail("team_publish_failed", err.Error())
	}
	return OK(event)
}

type TeamInbox struct {
	Bus       TeamBus
	FlowRunID string
	AgentID   string
}

func (t TeamInbox) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{Name: "team_inbox", Description: "Read and acknowledge new structured team messages addressed to this agent or broadcast to the Flow.", InputSchema: schema(`{"type":"object","properties":{},"additionalProperties":false}`)}
}

func (t TeamInbox) Execute(ctx context.Context, _ json.RawMessage) domain.ToolResult {
	if t.Bus == nil || t.FlowRunID == "" || t.AgentID == "" {
		return Fail("team_bus_unavailable", "team communication is available only inside a Flow execution")
	}
	events, err := t.Bus.TeamInbox(ctx, t.FlowRunID, t.AgentID, true)
	if err != nil {
		return Fail("team_inbox_failed", err.Error())
	}
	return OK(map[string]any{"events": events})
}
