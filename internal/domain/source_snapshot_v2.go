package domain

import (
	"errors"
	"strings"
	"time"
)

// SourceSnapshot is the immutable, content-addressed representation used by a
// WorkOrder. StoragePath is deliberately excluded from JSON: it is an internal
// local pointer and must never be sent to a model or remote client.
type SourceSnapshot struct {
	ID             string    `json:"id"`
	WorkspaceID    string    `json:"workspaceId,omitempty"`
	Kind           string    `json:"kind"` // text | image | workspace_file | local_file | url | git
	Label          string    `json:"label"`
	Locator        string    `json:"locator,omitempty"`
	CanonicalURL   string    `json:"canonicalUrl,omitempty"`
	Format         string    `json:"format,omitempty"`
	MediaType      string    `json:"mediaType,omitempty"`
	ExtractedText  string    `json:"extractedText,omitempty"`
	Digest         string    `json:"digest"`
	SourceBytes    int64     `json:"sourceBytes"`
	ExtractedBytes int64     `json:"extractedBytes"`
	Warnings       []string  `json:"warnings"`
	CreatedAt      time.Time `json:"createdAt"`
	StoragePath    string    `json:"-"`
}

// SourceSnapshotDiff describes a refresh without mutating either snapshot.
// Added/removed lines are bounded, deterministic requirement hints for the
// Master; approving their product meaning still requires a WorkOrder revision.
type SourceSnapshotDiff struct {
	Previous     SourceSnapshotRef `json:"previous"`
	Current      SourceSnapshotRef `json:"current"`
	Changed      bool              `json:"changed"`
	AddedLines   []string          `json:"addedLines,omitempty"`
	RemovedLines []string          `json:"removedLines,omitempty"`
}

func ValidateSourceSnapshot(snapshot SourceSnapshot) error {
	if strings.TrimSpace(snapshot.ID) == "" || strings.TrimSpace(snapshot.Digest) == "" {
		return errors.New("source snapshot id and digest are required")
	}
	switch snapshot.Kind {
	case "text", "image", "workspace_file", "local_file", "url", "git":
	default:
		return errors.New("unsupported source snapshot kind")
	}
	if snapshot.CreatedAt.IsZero() {
		return errors.New("source snapshot creation time is required")
	}
	return nil
}
