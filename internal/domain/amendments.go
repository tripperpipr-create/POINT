package domain

// ContextAmendAction adjusts live run context at safe checkpoints.
type ContextAmendAction string

const (
	ContextAmendAdd    ContextAmendAction = "add"
	ContextAmendPin    ContextAmendAction = "pin"
	ContextAmendUnpin  ContextAmendAction = "unpin"
	ContextAmendRemove ContextAmendAction = "remove"
)

// ContextAmendment is one add/pin/unpin/remove request queued for the next turn.
// Added items are backend-resolved immutable snapshots; the webview never gets
// to inject raw model context through this structure.
type ContextAmendment struct {
	Action ContextAmendAction `json:"action"`
	ItemID string             `json:"itemId,omitempty"`
	Item   *RunContextItem    `json:"item,omitempty"`
}

type RunMessageAmendment struct {
	Content        string `json:"content"`
	LearningIntent string `json:"learningIntent,omitempty"` // correction only after explicit user choice
}

// ExecutionAmendments holds mid-run controls applied before the next model turn.
type ExecutionAmendments struct {
	PendingMessages []RunMessageAmendment `json:"pendingMessages,omitempty"`
	ForbiddenPaths  []string              `json:"forbiddenPaths,omitempty"`
	ContextAmends   []ContextAmendment    `json:"contextAmendments,omitempty"`
}

// ConflictResolution records user intent when resolving change-set conflicts.
type ConflictResolution struct {
	Strategy string  `json:"strategy"` // keep_ours | keep_theirs | manual
	Path     string  `json:"path"`
	Content  *string `json:"content,omitempty"`
}
