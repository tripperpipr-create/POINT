package domain

import "time"

// CompatibilityFeature identifies a legacy surface whose real use must be
// measured before the surface can be retired. Values are stable telemetry
// keys; changing a key would split one release history into two.
type CompatibilityFeature string

const (
	CompatibilityProfileSave          CompatibilityFeature = "legacy_profile_save"
	CompatibilityProfileDelete        CompatibilityFeature = "legacy_profile_delete"
	CompatibilityProfileRunFallback   CompatibilityFeature = "legacy_profile_run_fallback"
	CompatibilityWorkflowSave         CompatibilityFeature = "legacy_workflow_save"
	CompatibilityWorkflowDelete       CompatibilityFeature = "legacy_workflow_delete"
	CompatibilityWorkflowRun          CompatibilityFeature = "legacy_workflow_run"
	CompatibilityCustomCommandSave    CompatibilityFeature = "legacy_custom_command_save"
	CompatibilityCustomCommandExecute CompatibilityFeature = "legacy_custom_command_execute"
	CompatibilityRunSnapshotRead      CompatibilityFeature = "legacy_run_snapshot_read"
)

// CompatibilityUsage is an aggregate, privacy-preserving usage fact. It never
// stores prompts, paths, identifiers of agents/tools, arguments, or secrets.
// WorkspaceID is a random local scoping key used only for isolation in storage;
// the public Statistics view removes it and merges matching rows. A separate
// row per application and legacy format version makes the evidence suitable for
// release-window removal decisions without a synthetic score.
type CompatibilityUsage struct {
	Feature            CompatibilityFeature `json:"feature"`
	WorkspaceID        string               `json:"workspaceId,omitempty"`
	ApplicationVersion string               `json:"applicationVersion"`
	LegacyVersion      string               `json:"legacyVersion"`
	Count              int64                `json:"count"`
	FirstSeen          time.Time            `json:"firstSeen"`
	LastSeen           time.Time            `json:"lastSeen"`
}
