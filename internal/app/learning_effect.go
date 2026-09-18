package app

import (
	"local-agent-workbench/internal/domain"
)

func improvementIsApplied(status string) bool {
	switch status {
	case "applied", "applied_unproven", "applied_proven":
		return true
	default:
		return false
	}
}

// learningProofStatus is the only path to applied_proven: canary Effect
// improved, or a later real personal-benchmark shadow compare that Passed.
// Apply of a new skill always stays unproven (caller passes nil canary and a
// deferred/skipped shadow).
func learningProofStatus(shadow *domain.LearningShadowEvaluation, canary *domain.SkillCanaryEvaluation) string {
	if canary != nil && canary.Effect == "improved" {
		return "applied_proven"
	}
	if shadow != nil && shadow.Status == "compared" && shadow.Passed {
		return "applied_proven"
	}
	return "applied_unproven"
}

func canaryEffect(evaluation domain.SkillCanaryEvaluation) string {
	if evaluation.Status == "regressed" {
		return "regressed"
	}
	if evaluation.Status == "pending" || evaluation.CandidateMetrics.Runs < minimumCanaryRuns {
		return "insufficient_sample"
	}
	if evaluation.BaselineMetrics == nil || evaluation.BaselineMetrics.Runs < minimumCanaryRuns {
		if evaluation.Status == "healthy" {
			return "neutral"
		}
		return "insufficient_sample"
	}
	candidate := evaluation.CandidateMetrics
	baseline := *evaluation.BaselineMetrics
	improved := candidate.CompletionRate-baseline.CompletionRate >= 0.05 ||
		candidate.HealthyRate-baseline.HealthyRate >= 0.05 ||
		(candidate.VerificationRequired >= 2 && baseline.VerificationRequired >= 2 && candidate.VerificationRate-baseline.VerificationRate >= 0.05) ||
		(candidate.ToolCalls >= 5 && baseline.ToolCalls >= 5 && baseline.ToolFailureRate-candidate.ToolFailureRate >= 0.05)
	if improved && evaluation.Status == "healthy" {
		return "improved"
	}
	if evaluation.Status == "healthy" {
		return "neutral"
	}
	return "insufficient_sample"
}

func proofStatusFromCanary(item *domain.AgentImprovement) {
	if item == nil || !improvementIsApplied(item.Status) {
		return
	}
	if item.CanaryEvaluation != nil {
		item.Effect = item.CanaryEvaluation.Effect
		if item.CanaryEvaluation.Effect == "regressed" || item.CanaryEvaluation.Status == "regressed" {
			// rollback path owns status
			return
		}
	}
	item.Status = learningProofStatus(item.ShadowEvaluation, item.CanaryEvaluation)
}
