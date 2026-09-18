package diagnostics

import (
	"sort"
	"strings"
	"time"
)

const SystemSchemaVersion = 1

type SystemStatus string

const (
	SystemReady    SystemStatus = "READY"
	SystemDegraded SystemStatus = "DEGRADED"
	SystemBlocked  SystemStatus = "BLOCKED"
)

type RepairAction struct {
	ID                   string `json:"id"`
	Label                string `json:"label"`
	RequiresConfirmation bool   `json:"requiresConfirmation,omitempty"`
}

type SystemCheck struct {
	Code          string         `json:"code"`
	Status        SystemStatus   `json:"status"`
	Summary       string         `json:"summary"`
	Detail        string         `json:"detail,omitempty"`
	NextAction    string         `json:"nextAction,omitempty"`
	Metrics       map[string]any `json:"metrics,omitempty"`
	RepairActions []RepairAction `json:"repairActions,omitempty"`
}

type SystemReport struct {
	SchemaVersion   int           `json:"schemaVersion"`
	Status          SystemStatus  `json:"status"`
	CheckedAt       time.Time     `json:"checkedAt"`
	Checks          []SystemCheck `json:"checks"`
	BlockingReasons []string      `json:"blockingReasons,omitempty"`
}

// CompileSystemReport derives one lifecycle state from named source checks.
// It deliberately exposes the checks and their raw metrics instead of hiding
// them behind a synthetic score.
func CompileSystemReport(checks []SystemCheck, now time.Time) SystemReport {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	result := SystemReport{
		SchemaVersion: SystemSchemaVersion,
		Status:        SystemReady,
		CheckedAt:     now.UTC(),
		Checks:        append([]SystemCheck(nil), checks...),
	}
	for index := range result.Checks {
		check := &result.Checks[index]
		check.Code = strings.TrimSpace(check.Code)
		check.Summary = strings.TrimSpace(check.Summary)
		check.Detail = strings.TrimSpace(check.Detail)
		check.NextAction = strings.TrimSpace(check.NextAction)
		switch check.Status {
		case SystemBlocked:
			result.Status = SystemBlocked
			reason := check.Summary
			if reason == "" {
				reason = check.Code
			}
			if reason != "" {
				result.BlockingReasons = append(result.BlockingReasons, reason)
			}
		case SystemDegraded:
			if result.Status == SystemReady {
				result.Status = SystemDegraded
			}
		case SystemReady:
		default:
			check.Status = SystemBlocked
			result.Status = SystemBlocked
			reason := check.Summary
			if reason == "" {
				reason = check.Code
			}
			if reason != "" {
				result.BlockingReasons = append(result.BlockingReasons, reason)
			}
		}
	}
	sort.Strings(result.BlockingReasons)
	if result.BlockingReasons == nil {
		result.BlockingReasons = []string{}
	}
	if result.Checks == nil {
		result.Checks = []SystemCheck{}
	}
	return result
}
