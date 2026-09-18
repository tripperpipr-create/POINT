package app

import (
	"fmt"
	"strings"
	"time"

	"local-agent-workbench/internal/diagnostics"
	"local-agent-workbench/internal/domain"
)

const learningEvalSchemaVersion = 1

type learningEvalCheck struct {
	Code   string `json:"code"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail"`
}

type learningEvaluation struct {
	SchemaVersion int                 `json:"schemaVersion"`
	Passed        bool                `json:"passed"`
	Checks        []learningEvalCheck `json:"checks"`
	EvaluatedAt   time.Time           `json:"evaluatedAt"`
}

// evaluateLearningCandidate is the deterministic regression gate between a
// reviewer response and persistent agent state. It cannot prove semantic
// quality, so each bounded invariant remains visible instead of becoming an
// opaque score.
func evaluateLearningCandidate(review learningReview, previous *domain.SkillDefinition, agent domain.ProjectAgent, trajectory learningTrajectory, report diagnostics.RunDiagnostics, trigger string, now time.Time) learningEvaluation {
	result := learningEvaluation{SchemaVersion: learningEvalSchemaVersion, Passed: true, EvaluatedAt: now, Checks: []learningEvalCheck{}}
	add := func(code string, passed bool, detail string) {
		result.Checks = append(result.Checks, learningEvalCheck{Code: code, Passed: passed, Detail: detail})
		if !passed {
			result.Passed = false
		}
	}
	if trigger == learningTriggerFailure {
		sourceOK := trajectory.ToolCalls >= learningMinFailureToolCalls &&
			(report.Health != diagnostics.HealthHealthy || (report.Verification.Required && !report.Verification.Recorded) ||
				trajectory.RunStatus == domain.RunFailed || trajectory.RunStatus == domain.RunInterrupted)
		add("failure_source", sourceOK, "failure/recovery learning requires a failed, unhealthy, or verification-gap trajectory")
		add("bounded_definition", (strings.TrimSpace(review.Name) != "" && strings.TrimSpace(review.Instructions) != "" && len([]rune(review.Instructions)) <= 4000) || review.Decision == "skip", "Skill name and bounded instructions are present when creating a recovery skill")
		if review.Decision != "skip" {
			add("portable_content", learningSkillPortable(review.Instructions) && !containsStackTraceLanguage(review.Instructions), "recovery instructions contain no secrets, paths, stack traces, or access expansion")
			add("recovery_not_feature", !containsFeatureBuildLanguage(review.Instructions), "failure learning must teach recovery/antipattern, not a feature build")
		} else {
			add("portable_content", review.MemoryDecision != "learn" || (safePortableMemory(review.Memory) && !containsStackTraceLanguage(review.Memory)), "failure memory is portable")
		}
	} else {
		add("verified_source", report.Health == diagnostics.HealthHealthy && (!report.Verification.Required || report.Verification.Recorded), "source Run is healthy and required verification is recorded")
		add("bounded_definition", strings.TrimSpace(review.Name) != "" && strings.TrimSpace(review.Instructions) != "" && len([]rune(review.Instructions)) <= 4000, "Skill name and bounded instructions are present")
		add("portable_content", learningSkillPortable(review.Instructions), "instructions contain no secrets, absolute repository paths, or access-expansion language")
	}
	allowed := true
	for _, tool := range trajectory.Tools {
		if tool == "read_skill" || !containsString(agent.AllowedTools, tool) {
			allowed = false
			break
		}
	}
	add("capability_boundary", len(trajectory.Tools) > 0 && allowed, "workflow uses only tools already granted to the agent")
	requiresVerificationLanguage := report.Verification.Required || (previous != nil && containsVerificationLanguage(previous.Instructions))
	if trigger == learningTriggerFailure {
		requiresVerificationLanguage = report.Verification.Required && review.Decision != "skip"
	}
	add("verification_regression", !requiresVerificationLanguage || containsVerificationLanguage(review.Instructions) || review.Decision == "skip", "a previously required verification contract is preserved")
	add("feedback_consent", trigger != learningTriggerFeedback || len(trajectory.Feedback) > 0, "feedback-triggered learning contains an explicitly consented correction")
	return result
}

func containsStackTraceLanguage(content string) bool {
	lower := strings.ToLower(content)
	for _, token := range []string{"panic:", "stack trace", "traceback", "goroutine ", " at line ", ".go:", ".java:", "c:\\", "/users/", "/home/"} {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

func containsFeatureBuildLanguage(content string) bool {
	lower := strings.ToLower(content)
	hits := 0
	for _, token := range []string{"implement the feature", "ship the feature", "build the product", "реализуйте функцию", "добавьте фичу"} {
		if strings.Contains(lower, token) {
			hits++
		}
	}
	return hits > 0
}

func learningSkillPortable(content string) bool {
	if !safePortableMemory(content) {
		return false
	}
	lower := strings.ToLower(content)
	for _, forbidden := range []string{
		"bypass approval", "skip approval", "grant permission", "disable policy", "unrestricted access",
		"обойти подтверж", "пропустить подтверж", "выдать разреш", "отключить политику", "неограниченный доступ",
	} {
		if strings.Contains(lower, forbidden) {
			return false
		}
	}
	return true
}

func containsVerificationLanguage(content string) bool {
	lower := strings.ToLower(content)
	for _, token := range []string{"verify", "verification", "test", "check", "проверк", "тест", "валид"} {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

func learningEvaluationEvidence(evaluation learningEvaluation) []string {
	items := make([]string, 0, len(evaluation.Checks)+1)
	status := "PASS"
	if !evaluation.Passed {
		status = "FAIL"
	}
	items = append(items, fmt.Sprintf("eval gate v%d: %s", evaluation.SchemaVersion, status))
	for _, check := range evaluation.Checks {
		result := "pass"
		if !check.Passed {
			result = "fail"
		}
		items = append(items, fmt.Sprintf("eval/%s: %s · %s", check.Code, result, check.Detail))
	}
	return items
}

func learningEvaluationFailure(evaluation learningEvaluation) string {
	failed := make([]string, 0)
	for _, check := range evaluation.Checks {
		if !check.Passed {
			failed = append(failed, check.Code)
		}
	}
	return "eval gate rejected candidate: " + strings.Join(failed, ", ")
}
