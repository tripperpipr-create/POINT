package sandbox

import "local-agent-workbench/internal/domain"

// MergeSeed is one completed branch head participating in a deterministic
// Flow join. Paths remain server-side; only bounded conflict metadata is
// exposed to clients.
type MergeSeed struct {
	ExecutionID string
	SandboxID   string
	Path        string
}

type MergeResolution struct {
	Path        string  `json:"path"`
	Strategy    string  `json:"strategy"` // use_parent | manual
	ExecutionID string  `json:"executionId,omitempty"`
	Content     *string `json:"content,omitempty"`
	Delete      bool    `json:"delete,omitempty"`
}

type MergeCandidate struct {
	ExecutionID string `json:"executionId"`
	Kind        string `json:"kind"`
	Hash        string `json:"hash,omitempty"`
	proposed    string
}

type MergeConflict struct {
	Path       string           `json:"path"`
	Candidates []MergeCandidate `json:"candidates"`
}

type MergeRequest struct {
	StorageMode          string
	FileRulesVersion     string
	QuestID              string
	WorkspaceID          string
	ExecutionID          string
	BasePath             string
	Runtime              RuntimeRequirements
	Seeds                []MergeSeed
	Resolutions          []MergeResolution
	BaselineChangeSetIDs []string
}

type MergeResult struct {
	Record    domain.SandboxRecord `json:"record"`
	Conflicts []MergeConflict      `json:"conflicts,omitempty"`
	Paths     []string             `json:"paths,omitempty"`
}
