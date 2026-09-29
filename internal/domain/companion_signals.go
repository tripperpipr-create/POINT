package domain

import "time"

// IDEObservation is bounded, workspace-scoped evidence reported by the IDE.
// It lets Companion reason about editor diagnostics and terminal/task outcomes
// without turning the LLM into the runtime or granting it terminal access.
type IDEObservation struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspaceId"`
	Kind        string    `json:"kind"` // diagnostic | terminal | task | debug | run | scm
	Source      string    `json:"source,omitempty"`
	Level       string    `json:"level"` // info | warning | error
	Summary     string    `json:"summary"`
	Detail      string    `json:"detail,omitempty"`
	Path        string    `json:"path,omitempty"`
	Line        int       `json:"line,omitempty"`
	Command     string    `json:"command,omitempty"`
	ExitCode    *int      `json:"exitCode,omitempty"`
	ObservedAt  time.Time `json:"observedAt"`
	FirstSeen   time.Time `json:"firstSeen,omitempty"`
	LastSeen    time.Time `json:"lastSeen,omitempty"`
	Count       int       `json:"count,omitempty"`
	NoveltyHash string    `json:"noveltyHash,omitempty"`
	FocusPath   string    `json:"focusPath,omitempty"`
}

// CompanionIntervention is a live, non-blocking recommendation derived from
// observable project state. Enforcement remains in policy/permission layers.
type CompanionInterventionAction string

const (
	CompanionInterventionOpenRun         CompanionInterventionAction = "open_run"
	CompanionInterventionMessageRun      CompanionInterventionAction = "message_run"
	CompanionInterventionPrompt          CompanionInterventionAction = "companion_prompt"
	CompanionInterventionProbeConnection CompanionInterventionAction = "probe_connection"
)

type CompanionIntervention struct {
	ID            string                      `json:"id"`
	Level         string                      `json:"level"` // suggestion | warning | critical
	Title         string                      `json:"title"`
	Detail        string                      `json:"detail"`
	ActionTab     string                      `json:"actionTab,omitempty"`
	RelatedID     string                      `json:"relatedId,omitempty"`
	RelatedPath   string                      `json:"relatedPath,omitempty"`
	RelatedLine   int                         `json:"relatedLine,omitempty"`
	ActionKind    CompanionInterventionAction `json:"actionKind,omitempty"`
	ActionLabel   string                      `json:"actionLabel,omitempty"`
	ActionMessage string                      `json:"actionMessage,omitempty"`
	OccurrenceKey string                      `json:"occurrenceKey"`
}
