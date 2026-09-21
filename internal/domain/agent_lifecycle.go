package domain

import "time"

const (
	ProjectAgentDraft             = "draft"
	ProjectAgentActive            = "active"
	ProjectAgentEvaluationPending = "evaluation_pending"
)

// AgentSelectionBinding persists the selector result independently from the
// Master's prose so a conversation can resume with the exact same party.
type AgentSelectionBinding struct {
	ConversationID  string    `json:"conversationId"`
	WorkOrderID     string    `json:"workOrderId"`
	WorkspaceID     string    `json:"workspaceId"`
	AgentID         string    `json:"agentId"`
	SelectionDigest string    `json:"selectionDigest"`
	Revision        int       `json:"revision"`
	CreatedAt       time.Time `json:"createdAt"`
}

type AgentLifecycleEvent struct {
	ID          string         `json:"id"`
	WorkspaceID string         `json:"workspaceId"`
	AgentID     string         `json:"agentId"`
	WorkOrderID string         `json:"workOrderId,omitempty"`
	Kind        string         `json:"kind"`
	Detail      map[string]any `json:"detail,omitempty"`
	CreatedAt   time.Time      `json:"createdAt"`
}

// ProjectAgentFromBlueprint creates an active workspace-scoped agent from a
// reusable blueprint.
func ProjectAgentFromBlueprint(workspaceID string, blueprint AgentBlueprint) ProjectAgent {
	now := time.Now().UTC()
	return ProjectAgent{
		ID: NewID("projectagent"), WorkspaceID: workspaceID, BlueprintID: blueprint.ID, Status: ProjectAgentActive,
		Name: blueprint.Name, RoleDescription: blueprint.RoleDescription, Personality: blueprint.Personality,
		Mission: blueprint.Mission, SystemPrompt: blueprint.SystemPrompt,
		Goals: append([]string(nil), blueprint.Goals...), Rules: append([]string(nil), blueprint.Rules...),
		Constraints: append([]string(nil), blueprint.Constraints...), SkillIDs: append([]string(nil), blueprint.SkillIDs...),
		AllowedTools: append([]string(nil), blueprint.AllowedTools...), ToolPolicies: cloneStringMap(blueprint.ToolPolicies),
		Provider: blueprint.Provider, ProviderPreset: blueprint.ProviderPreset, ConnectionID: blueprint.ConnectionID, BaseURL: blueprint.BaseURL,
		PrimaryModel: blueprint.PrimaryModel, FallbackModels: append([]string(nil), blueprint.FallbackModels...),
		Temperature: blueprint.Temperature, MaxOutputTokens: blueprint.MaxOutputTokens,
		ContextWindowTokens: blueprint.ContextWindowTokens, ReasoningEffort: blueprint.ReasoningEffort,
		MaxSteps: blueprint.MaxSteps, MaxDurationSeconds: blueprint.MaxDurationSeconds, ApprovalMode: blueprint.ApprovalMode,
		Experience: 0, Level: 1, CreatedAt: now, UpdatedAt: now,
	}
}
