package forge

import "context"

type Connection struct {
	ID        string `json:"id"`
	Provider  string `json:"provider"`
	Name      string `json:"name"`
	URL       string `json:"url"`
	CAPath    string `json:"caPath,omitempty"`
	SecretRef string `json:"secretRef"`
	Enabled   bool   `json:"enabled"`
}
type Binding struct {
	WorkspaceID  string `json:"workspaceId"`
	RepoRoot     string `json:"repoRoot"`
	Remote       string `json:"remote"`
	ConnectionID string `json:"connectionId,omitempty"`
	Project      string `json:"project,omitempty"`
	Mode         string `json:"mode"` // auto, manual, off
}
type Capability struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}
type Position struct {
	OldPath  string `json:"oldPath"`
	NewPath  string `json:"newPath"`
	OldLine  int    `json:"oldLine,omitempty"`
	NewLine  int    `json:"newLine,omitempty"`
	BaseSHA  string `json:"baseSha"`
	HeadSHA  string `json:"headSha"`
	StartSHA string `json:"startSha"`
}
type Request struct {
	ConnectionID       string    `json:"connectionId"`
	Project            string    `json:"project,omitempty"`
	Action             string    `json:"action"`
	Scope              string    `json:"scope,omitempty"`
	IID                int       `json:"iid,omitempty"`
	Page               int       `json:"page,omitempty"`
	Path               string    `json:"path,omitempty"`
	Ref                string    `json:"ref,omitempty"`
	ExpectedSHA        string    `json:"expectedSha,omitempty"`
	Title              string    `json:"title,omitempty"`
	Description        string    `json:"description,omitempty"`
	SourceBranch       string    `json:"sourceBranch,omitempty"`
	TargetBranch       string    `json:"targetBranch,omitempty"`
	Draft              bool      `json:"draft,omitempty"`
	ReviewerIDs        []int     `json:"reviewerIds,omitempty"`
	Body               string    `json:"body,omitempty"`
	DiscussionID       string    `json:"discussionId,omitempty"`
	Resolved           bool      `json:"resolved,omitempty"`
	Position           *Position `json:"position,omitempty"`
	PipelineID         int       `json:"pipelineId,omitempty"`
	JobID              int       `json:"jobId,omitempty"`
	Confirmed          bool      `json:"confirmed,omitempty"`
	RemoveSourceBranch bool      `json:"removeSourceBranch,omitempty"`
	Squash             bool      `json:"squash,omitempty"`
}
type Response struct {
	Data         any                   `json:"data,omitempty"`
	NextPage     int                   `json:"nextPage,omitempty"`
	Capabilities map[string]Capability `json:"capabilities,omitempty"`
	Uncertain    bool                  `json:"uncertain,omitempty"`
}

// Provider owns protocol details; the workbench only consumes normalized models.
type Provider interface {
	ID() string
	ReviewTerm() string
	Execute(context.Context, Request) (Response, error)
}
type Error struct {
	Reason    string `json:"reason"`
	Problem   string `json:"problem"`
	Uncertain bool   `json:"uncertain,omitempty"`
}

func (e *Error) Error() string { return e.Problem }
func IsWrite(action string) bool {
	switch action {
	case "create", "update", "comment", "reply", "resolve", "approve", "unapprove", "merge", "retryJob", "retryPipeline":
		return true
	}
	return false
}
