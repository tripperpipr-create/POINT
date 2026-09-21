package domain

import "time"

type RoleRequirement struct {
	Role                 string   `json:"role"`
	Responsibility       string   `json:"responsibility"`
	PreparationKind      string   `json:"preparationKind,omitempty"` // create_agent | subagent
	ParentAgentID        string   `json:"parentAgentId,omitempty"`
	RequiredTools        []string `json:"requiredTools,omitempty"`
	RequiredSkills       []string `json:"requiredSkills,omitempty"`
	RequiredCapabilities []string `json:"requiredCapabilities,omitempty"`
	Verification         string   `json:"verification"`
}

type AgentPrepChain struct {
	ID               string          `json:"id"`
	WorkspaceID      string          `json:"workspaceId"`
	ParentQuestID    string          `json:"parentQuestId"`
	PrepQuestID      string          `json:"prepQuestId"`
	DeferredTaskHash string          `json:"deferredTaskHash"`
	Requirement      RoleRequirement `json:"requirement"`
	CandidateAgentID string          `json:"candidateAgentId,omitempty"`
	State            string          `json:"state"` // design | create | verify | ready | failed
	Error            string          `json:"error,omitempty"`
	Attempts         int             `json:"attempts"`
	CreatedAt        time.Time       `json:"createdAt"`
	UpdatedAt        time.Time       `json:"updatedAt"`
}

type SubagentRequest struct {
	WorkspaceID   string   `json:"workspaceId"`
	QuestID       string   `json:"questId"`
	ParentAgentID string   `json:"parentAgentId"`
	Role          string   `json:"role"`
	Mission       string   `json:"mission"`
	RequiredTools []string `json:"requiredTools,omitempty"`
}
