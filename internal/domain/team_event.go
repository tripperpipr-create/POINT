package domain

import "time"

type TeamEvent struct {
	ID          string     `json:"id"`
	WorkspaceID string     `json:"workspaceId"`
	QuestID     string     `json:"questId"`
	FlowRunID   string     `json:"flowRunId"`
	FlowNodeID  string     `json:"flowNodeId"`
	FromAgentID string     `json:"fromAgentId"`
	ToAgentID   string     `json:"toAgentId,omitempty"`
	Kind        string     `json:"kind"` // question|answer|blocker|contract|decision|review_request|finding|status
	Message     string     `json:"message"`
	ArtifactID  string     `json:"artifactId,omitempty"`
	DeliveredAt *time.Time `json:"deliveredAt,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
}
