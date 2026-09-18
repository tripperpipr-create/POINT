package domain

import "time"

// EgressAsk is a Master→user decision about network or git outside the approved allowlist.
type EgressAskKind string

const (
	EgressAskNetworkHost  EgressAskKind = "network_host"
	EgressAskGitRemote    EgressAskKind = "git_remote"
	EgressAskSupervision  EgressAskKind = "supervision"
)

type EgressAskStatus string

const (
	EgressAskPending      EgressAskStatus = "pending"
	EgressAskAllowedOnce  EgressAskStatus = "allowed_once"
	EgressAskAllowedQuest EgressAskStatus = "allowed_quest"
	EgressAskDenied       EgressAskStatus = "denied"
)

type EgressAsk struct {
	ID          string          `json:"id"`
	WorkspaceID string          `json:"workspaceId"`
	QuestID     string          `json:"questId,omitempty"`
	RunID       string          `json:"runId,omitempty"`
	Kind        EgressAskKind   `json:"kind"`
	Target      string          `json:"target"`
	Reason      string          `json:"reason"`
	Risk        string          `json:"risk,omitempty"`
	Status      EgressAskStatus `json:"status"`
	CreatedAt   time.Time       `json:"createdAt"`
	ResolvedAt  time.Time       `json:"resolvedAt,omitempty"`
}
