package diagnostics

import (
	"encoding/json"
	"local-agent-workbench/internal/domain"
)

// A structured task is fail-closed when its final event is missing, stale or
// incomplete. Legacy command heuristics must never promote it to a success.
func structuredTaskVerification(run domain.Run, events []domain.Event) (required, recorded bool) {
	var brief *domain.TaskBrief
	for _, event := range events {
		switch event.Type {
		case domain.EventRunStarted:
			var payload struct {
				Brief *domain.TaskBrief `json:"taskBrief"`
			}
			if json.Unmarshal(event.Data, &payload) == nil && payload.Brief != nil {
				brief = payload.Brief
				required = true
				recorded = false
			}
		case domain.EventToolRequested, domain.EventToolFinished, domain.EventPatchApplied, domain.EventWorkspaceChanged:
			recorded = false
		case domain.EventCompletionChecked:
			if !required {
				continue
			}
			recorded = false
			var payload struct {
				Status   string `json:"status"`
				Evidence *struct {
					Status       string `json:"status"`
					BriefVersion int    `json:"briefVersion"`
					Criteria     []struct {
						ID     string `json:"criterionId"`
						Kind   string `json:"kind"`
						Status string `json:"status"`
					} `json:"criteria"`
				} `json:"evidence"`
			}
			if json.Unmarshal(event.Data, &payload) != nil || payload.Evidence == nil || payload.Status != "accepted_after_revision" {
				continue
			}
			proof := payload.Evidence
			if proof.Status != "verified" || proof.BriefVersion != brief.Version || len(brief.Criteria) == 0 || len(proof.Criteria) != len(brief.Criteria) {
				continue
			}
			satisfied := make(map[string]string, len(proof.Criteria))
			for _, c := range proof.Criteria {
				if c.Status == "satisfied" {
					satisfied[c.ID] = c.Kind
				}
			}
			recorded = true
			for _, c := range brief.Criteria {
				if c.Kind == "manual" || satisfied[c.ID] != c.Kind {
					recorded = false
					break
				}
			}
		}
	}
	return required, recorded && run.Status == domain.RunCompleted
}
