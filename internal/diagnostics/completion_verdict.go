package diagnostics

import "encoding/json"

type completionVerdict struct {
	Status              string
	PendingCriterionIDs []string
}

// Reading old events never changes their bytes. Stronger nested evidence
// overrides optimistic legacy statuses consistently with the Hub renderer.
func readCompletionVerdict(raw json.RawMessage) completionVerdict {
	var payload struct {
		Status              string   `json:"status"`
		AcceptancePassed    *bool    `json:"acceptancePassed"`
		PendingCriterionIDs []string `json:"pendingCriterionIds"`
		Evidence            *struct {
			Status   string `json:"status"`
			Criteria []struct {
				ID     string `json:"criterionId"`
				Status string `json:"status"`
			} `json:"criteria"`
		} `json:"evidence"`
		Verification struct {
			Ran                 bool     `json:"ran"`
			Passed              *bool    `json:"passed"`
			NeedsReview         bool     `json:"needsReview"`
			PendingCriterionIDs []string `json:"pendingCriterionIds"`
		} `json:"verification"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return completionVerdict{}
	}
	result := completionVerdict{Status: payload.Status, PendingCriterionIDs: payload.PendingCriterionIDs}
	if len(result.PendingCriterionIDs) == 0 {
		result.PendingCriterionIDs = payload.Verification.PendingCriterionIDs
	}
	if payload.Status == "preparation_failed" {
		return result
	}
	if payload.Verification.Ran && payload.Verification.Passed != nil && !*payload.Verification.Passed {
		result.Status = "rejected"
		return result
	}
	if payload.Evidence != nil {
		if payload.Evidence.Status == "blocked" {
			result.Status = "rejected"
			return result
		}
		review := payload.Evidence.Status == "needs_review"
		var pending []string
		for _, criterion := range payload.Evidence.Criteria {
			if criterion.Status == "failed" || criterion.Status == "stale" {
				result.Status = "rejected"
				return result
			}
			if criterion.Status == "needs_review" || criterion.Status == "unavailable" {
				review = true
				pending = append(pending, criterion.ID)
			}
		}
		if review {
			result.Status = "needs_review"
			if len(pending) > 0 {
				result.PendingCriterionIDs = pending
			}
			return result
		}
	}
	if payload.Verification.NeedsReview {
		result.Status = "needs_review"
	} else if payload.AcceptancePassed != nil && !*payload.AcceptancePassed && (payload.Status == "accepted" || payload.Status == "accepted_after_revision") {
		result.Status = "implementation_ready"
	}
	return result
}
