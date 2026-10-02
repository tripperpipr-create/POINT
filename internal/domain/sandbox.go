package domain

import "time"

// SandboxRecord tracks an isolated execution workspace.
type SandboxRecord struct {
	QuestID              string     `json:"questId,omitempty"`
	StorageMode          string     `json:"storageMode,omitempty"`
	WorkspaceVolume      string     `json:"workspaceVolume,omitempty"`
	FileRulesVersion     string     `json:"fileRulesVersion,omitempty"`
	SandboxdDigest       string     `json:"sandboxdDigest,omitempty"`
	ID                   string     `json:"id"`
	WorkspaceID          string     `json:"workspaceId"`
	ExecutionID          string     `json:"executionId"`
	Kind                 string     `json:"kind"` // worktree | copy | merge-copy | live
	Backend              string     `json:"backend"`
	BackendVersion       string     `json:"backendVersion,omitempty"`
	BackendImage         string     `json:"backendImage,omitempty"`
	BackendImageDigest   string     `json:"backendImageDigest,omitempty"`
	Path                 string     `json:"path"`
	BaseCommit           string     `json:"baseCommit,omitempty"`
	ParentSandboxID      string     `json:"parentSandboxId,omitempty"`
	ParentExecutionID    string     `json:"parentExecutionId,omitempty"`
	ParentSandboxIDs     []string   `json:"parentSandboxIds,omitempty"`
	ParentExecutionIDs   []string   `json:"parentExecutionIds,omitempty"`
	BaselinePath         string     `json:"baselinePath,omitempty"`
	BaselineChangeSetIDs []string   `json:"baselineChangeSetIds,omitempty"`
	CreatedAt            time.Time  `json:"createdAt"`
	ClosedAt             *time.Time `json:"closedAt,omitempty"`
}
