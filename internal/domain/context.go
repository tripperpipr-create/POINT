package domain

type ContextKind string

const (
	ContextText          ContextKind = "text"
	ContextWorkspaceFile ContextKind = "workspace_file"
	ContextDocument      ContextKind = "document"
	ContextTable         ContextKind = "table"
	ContextImage         ContextKind = "image"
)

// RunContextInput is the user-controlled reference accepted when a run starts.
// Workspace files are resolved and read by the backend; callers never provide
// their content directly.
type RunContextInput struct {
	Kind      ContextKind `json:"kind"`
	Label     string      `json:"label,omitempty"`
	Path      string      `json:"path,omitempty"`
	Content   string      `json:"content,omitempty"`
	Category  string      `json:"category,omitempty"`
	AddedBy   string      `json:"addedBy,omitempty"`
	Reason    string      `json:"reason,omitempty"`
	Source    string      `json:"source,omitempty"`
	Relevance float64     `json:"relevance,omitempty"`
	Pinned    bool        `json:"pinned,omitempty"`
}

// RunContextItem is the immutable, bounded snapshot actually shown to the
// model and retained with the run for auditability.
type RunContextItem struct {
	ID            string      `json:"id"`
	Kind          ContextKind `json:"kind"`
	Label         string      `json:"label"`
	Path          string      `json:"path,omitempty"`
	Format        string      `json:"format,omitempty"`
	MediaType     string      `json:"mediaType,omitempty"`
	Content       string      `json:"content"`
	DataBase64    string      `json:"dataBase64,omitempty"`
	Digest        string      `json:"digest,omitempty"`
	Size          int64       `json:"size"`
	SourceSize    int64       `json:"sourceSize,omitempty"`
	ExtractedSize int64       `json:"extractedSize,omitempty"`
	Width         int         `json:"width,omitempty"`
	Height        int         `json:"height,omitempty"`
	Truncated     bool        `json:"truncated,omitempty"`
	// Provenance / inspector fields (Agent Hub MVP).
	Category      string  `json:"category,omitempty"`
	AddedBy       string  `json:"addedBy,omitempty"`
	Reason        string  `json:"reason,omitempty"`
	Source        string  `json:"source,omitempty"`
	Relevance     float64 `json:"relevance,omitempty"`
	TokenEstimate int     `json:"tokenEstimate,omitempty"`
	Pinned        bool    `json:"pinned,omitempty"`
	Amendable     bool    `json:"amendable,omitempty"`
	Pending       bool    `json:"pending,omitempty"`
}

// ContextPreview describes the exact bounded snapshot that will be supplied to
// a model. Image payloads are omitted from public previews but their byte size
// and digest remain visible for auditability.
type ContextPreview struct {
	Items             []RunContextItem `json:"items"`
	TotalSourceBytes  int64            `json:"totalSourceBytes"`
	TotalContextBytes int64            `json:"totalContextBytes"`
	TotalImageBytes   int64            `json:"totalImageBytes"`
	EstimatedTokens   int              `json:"estimatedTokens"`
	Warnings          []string         `json:"warnings"`
	Active            bool             `json:"active,omitempty"`
	PendingItems      int              `json:"pendingItems,omitempty"`
}
