package agent

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"local-agent-workbench/internal/domain"
)

// CompletionEvidence separates fulfilled machine checks from user review and
// unresolved observations. A finished run is not automatically a verified task.
type CompletionEvidence struct {
	BriefVersion      int                 `json:"briefVersion,omitempty"`
	WorkspaceRevision int                 `json:"workspaceRevision"`
	Status            string              `json:"status"` // verified | needs_review | blocked
	Criteria          []CriterionEvidence `json:"criteria"`
	Diagnostics       []CheckEvidence     `json:"diagnostics"`
}

type CriterionEvidence struct {
	CriterionID      string         `json:"criterionId"`
	Text             string         `json:"text"`
	Kind             string         `json:"kind"`
	Status           string         `json:"status"` // satisfied | pending | failed | stale | needs_review | unavailable
	ExpectedExitCode *int           `json:"expectedExitCode,omitempty"`
	Check            *CheckEvidence `json:"check,omitempty"`
}

type CheckEvidence struct {
	Identity          string          `json:"identity"`
	Tool              string          `json:"tool"`
	Arguments         json.RawMessage `json:"arguments"`
	WorkspaceRevision int             `json:"workspaceRevision"`
	ExitCode          *int            `json:"exitCode,omitempty"`
	TimedOut          bool            `json:"timedOut"`
	Detail            string          `json:"detail"`
	Status            string          `json:"status"` // passed | stale | unresolved | regression
}

func (t *completionTracker) isDeclaredCheck(identity string) bool {
	if t.brief == nil {
		return false
	}
	for _, criterion := range t.brief.Criteria {
		if criterion.Kind != "manual" && verificationIdentity(criterion.Tool, criterion.Arguments) == identity {
			return true
		}
	}
	return false
}

func (t *completionTracker) sortedCheckIdentities() []string {
	identities := make([]string, 0, len(t.checks))
	for identity := range t.checks {
		identities = append(identities, identity)
	}
	sort.Strings(identities)
	return identities
}

func (t *completionTracker) criterionEvidence(criterion domain.AcceptanceCriterion, revision int) CriterionEvidence {
	result := CriterionEvidence{CriterionID: criterion.ID, Text: criterion.Text, Kind: criterion.Kind, Status: "pending"}
	if criterion.Kind == "manual" {
		result.Status = "needs_review"
		return result
	}
	if criterion.Kind != "verification" && criterion.Kind != "reproduction" {
		result.Status = "unavailable"
		return result
	}
	if _, enabled := t.verificationTools[criterion.Tool]; !enabled {
		result.Status = "unavailable"
		return result
	}
	expected := 0
	if criterion.ExpectedExitCode != nil {
		expected = *criterion.ExpectedExitCode
	}
	result.ExpectedExitCode = &expected
	// Defend the execution gate even if a caller bypasses brief validation.
	if expected < 0 || expected > 255 || (criterion.Kind == "verification" && expected != 0) || (criterion.Kind == "reproduction" && criterion.ExpectedExitCode == nil) {
		result.Status = "unavailable"
		return result
	}
	identity := verificationIdentity(criterion.Tool, criterion.Arguments)
	check, ok := t.checks[identity]
	if !ok {
		return result
	}
	evidence := describeCheck(identity, check, revision)
	result.Check = &evidence
	switch {
	case check.Revision != revision:
		result.Status = "stale"
	case !check.ResultOK || !check.Structured || check.TimedOut || check.ExitCode == nil || *check.ExitCode != expected:
		result.Status = "failed"
	default:
		result.Status = "satisfied"
		// A nonzero outcome explicitly expected by a reproduction is success
		// for that criterion, even though it is not a passing verification.
		result.Check.Status = "passed"
	}
	return result
}

func describeCheck(identity string, attempt verificationAttempt, revision int) CheckEvidence {
	status := "passed"
	switch {
	case !attempt.Passed && attempt.EverPassed:
		status = "regression"
	case !attempt.Passed:
		status = "unresolved"
	case attempt.Revision != revision:
		status = "stale"
	}
	return CheckEvidence{
		Identity: identity, Tool: attempt.Tool, Arguments: append(json.RawMessage(nil), attempt.Arguments...),
		WorkspaceRevision: attempt.Revision, ExitCode: attempt.ExitCode, TimedOut: attempt.TimedOut,
		Detail: attempt.Detail, Status: status,
	}
}

func (t *completionTracker) Evidence(revision int) CompletionEvidence {
	result := CompletionEvidence{
		WorkspaceRevision: revision, Status: "verified",
		Criteria: []CriterionEvidence{}, Diagnostics: []CheckEvidence{},
	}
	if t.brief != nil {
		result.BriefVersion = t.brief.Version
		if len(t.brief.Criteria) == 0 {
			result.Status = "needs_review"
		}
		for _, criterion := range t.brief.Criteria {
			evidence := t.criterionEvidence(criterion, revision)
			result.Criteria = append(result.Criteria, evidence)
			if evidence.Status == "needs_review" && result.Status == "verified" {
				result.Status = "needs_review"
			} else if evidence.Status != "satisfied" && evidence.Status != "needs_review" {
				result.Status = "blocked"
			}
		}
	}
	for _, identity := range t.sortedCheckIdentities() {
		if t.isDeclaredCheck(identity) {
			continue
		}
		evidence := describeCheck(identity, t.checks[identity], revision)
		result.Diagnostics = append(result.Diagnostics, evidence)
		if evidence.Status == "regression" {
			result.Status = "blocked"
		} else if evidence.Status == "unresolved" && result.Status == "verified" {
			result.Status = "needs_review"
		}
	}
	return result
}

func (t *completionTracker) contractEvidence(revision int) *CompletionEvidence {
	if t.brief == nil {
		return nil
	}
	evidence := t.Evidence(revision)
	return &evidence
}

func (t *completionTracker) missingCriteria(revision int, changedFiles []string) []completionRequirement {
	var missing []completionRequirement
	for _, criterion := range t.brief.Criteria {
		evidence := t.criterionEvidence(criterion, revision)
		switch evidence.Status {
		case "satisfied", "needs_review":
			continue
		case "unavailable":
			missing = append(missing, completionRequirement{"verification_tool_unavailable", fmt.Sprintf("criterion %s has no valid enabled verification tool or expected outcome", criterion.ID)})
		case "pending":
			missing = append(missing, completionRequirement{"verification_required", fmt.Sprintf("criterion %s requires its declared tool and arguments on revision %d", criterion.ID, revision)})
		default:
			detail := ""
			if evidence.Check != nil {
				detail = evidence.Check.Detail
			}
			missing = append(missing, completionRequirement{"verification_failed", fmt.Sprintf("criterion %s is %s on revision %d: %s", criterion.ID, evidence.Status, revision, detail)})
		}
	}
	for _, identity := range t.sortedCheckIdentities() {
		check := t.checks[identity]
		if !t.isDeclaredCheck(identity) && check.EverPassed && !check.Passed {
			missing = append(missing, completionRequirement{"verification_regression", "a previously passing verification failed; another check or a diagnostic reason cannot hide it: " + identity + ": " + check.Detail})
		}
	}
	if len(changedFiles) > 0 && !t.brief.Permissions.WriteFiles {
		missing = append(missing, completionRequirement{"task_scope_violation", "this task does not permit file changes; stop and report the scope violation"})
	}
	return missing
}

func (t *completionTracker) ContractInstructions() string {
	if t.brief == nil {
		return ""
	}
	encoded, _ := json.Marshal(t.brief)
	return strings.Join([]string{
		"<point_task_completion_contract>",
		"Use the approved task brief below as the fixed scope, delivery format and acceptance criteria. Do not invent extra required work or weaken a criterion.",
		"sourceRequest preserves the original user contract. Later approved decisions and criteria take precedence where they explicitly revise that request.",
		string(encoded),
		"For machine criteria execute the declared tool and arguments; an expected-failure reproduction succeeds only at its specified exit code. Manual criteria remain for user review.",
		"Diagnostics are observations, not automatic scope additions. Report unresolved diagnostics honestly; a previously passing check that now fails must not be hidden or relabeled.",
		"When the requested result is ready and required checks are satisfied, finish. List optional ideas separately without implementing them. If scope must change, report a blocker and preserve progress.",
		"</point_task_completion_contract>",
	}, "\n")
}

// TaskContractInstructions is shared by execution and its input-budget preflight.
func TaskContractInstructions(brief *domain.TaskBrief) string {
	return (&completionTracker{brief: brief}).ContractInstructions()
}
