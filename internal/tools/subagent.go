package tools

import (
	"context"
	"encoding/json"
	"strings"

	"local-agent-workbench/internal/domain"
)

type SubagentRequester interface {
	RequestTemporarySubagent(context.Context, domain.SubagentRequest) (domain.ProjectAgent, error)
}

type RequestSubagent struct {
	Requester     SubagentRequester
	WorkspaceID   string
	QuestID       string
	ParentAgentID string
}

func (t RequestSubagent) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{Name: "request_subagent", Description: "Request one quest-scoped specialist under this agent. The server enforces the approved provisioning permission, budget and parent tool ceiling.", InputSchema: schema(`{"type":"object","properties":{"role":{"type":"string","minLength":1,"maxLength":120},"mission":{"type":"string","minLength":1,"maxLength":1000},"requiredTools":{"type":"array","maxItems":16,"items":{"type":"string"}}},"required":["role","mission"],"additionalProperties":false}`)}
}

func (t RequestSubagent) Execute(ctx context.Context, raw json.RawMessage) domain.ToolResult {
	if t.Requester == nil || t.WorkspaceID == "" || t.QuestID == "" || t.ParentAgentID == "" {
		return Fail("subagent_unavailable", "subagents are available only to an active parent inside an approved quest")
	}
	var input struct {
		Role          string   `json:"role"`
		Mission       string   `json:"mission"`
		RequiredTools []string `json:"requiredTools"`
	}
	if invalid := Decode(raw, &input); invalid != nil {
		return *invalid
	}
	input.Role, input.Mission = strings.TrimSpace(input.Role), strings.TrimSpace(input.Mission)
	if input.Role == "" || input.Mission == "" {
		return Fail("invalid_input", "role and mission are required")
	}
	agent, err := t.Requester.RequestTemporarySubagent(ctx, domain.SubagentRequest{WorkspaceID: t.WorkspaceID, QuestID: t.QuestID, ParentAgentID: t.ParentAgentID, Role: input.Role, Mission: input.Mission, RequiredTools: input.RequiredTools})
	if err != nil {
		return Fail("subagent_rejected", err.Error())
	}
	return OK(map[string]any{"agentId": agent.ID, "parentAgentId": agent.ParentAgentID, "ownerQuestId": agent.OwnerQuestID, "role": agent.RoleDescription})
}
