package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/verification"
)

type CompletionPolicy struct {
	ExplicitVerification           bool     `json:"explicitVerification"`
	FileChangesRequireVerification bool     `json:"fileChangesRequireVerification"`
	VerificationToolAvailable      bool     `json:"verificationToolAvailable"`
	BlockingConfigurationIssue     bool     `json:"blockingConfigurationIssue"`
	CorrectionEpisodes             int      `json:"correctionEpisodes"`
	AcceptedEvidence               []string `json:"acceptedEvidence"`
}

func DescribeCompletionPolicy(profile domain.AgentProfile, task string, customToolSets ...[]domain.CustomTool) CompletionPolicy {
	explicit := verification.TaskRequires(task)
	available := len(verificationToolNames(profile, firstCustomToolSet(customToolSets))) > 0
	canWrite := contains(profile.AllowedTools, "propose_patch")
	return CompletionPolicy{
		ExplicitVerification: explicit, FileChangesRequireVerification: available,
		VerificationToolAvailable: available, BlockingConfigurationIssue: (explicit && !available) || (canWrite && !available),
		CorrectionEpisodes: 2, AcceptedEvidence: []string{"test", "build", "lint", "static_analysis"},
	}
}

type completionRequirement struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type completionTracker struct {
	explicitVerification           bool
	verificationToolAvailable      bool
	commandAttempts                int
	successfulVerificationRevision int
	latestExitCode                 *int
	latestTimedOut                 bool
	latestCommandRecognized        bool
	verificationTools              map[string]struct{}
	suggestedTools                 []string
	checks                         map[string]verificationAttempt
	brief                          *domain.TaskBrief
}

func newCompletionTracker(profile domain.AgentProfile, task string, customTools []domain.CustomTool, briefs ...*domain.TaskBrief) *completionTracker {
	completionPolicy := DescribeCompletionPolicy(profile, task, customTools)
	tracker := &completionTracker{
		explicitVerification:           completionPolicy.ExplicitVerification,
		verificationToolAvailable:      completionPolicy.VerificationToolAvailable,
		successfulVerificationRevision: -1,
		verificationTools:              verificationToolNames(profile, customTools),
		suggestedTools:                 verificationToolDisplayNames(profile, customTools),
		checks:                         map[string]verificationAttempt{},
	}
	if len(briefs) > 0 && briefs[0] != nil {
		encoded, _ := json.Marshal(briefs[0])
		_ = json.Unmarshal(encoded, &tracker.brief)
	}
	return tracker
}

func (t *completionTracker) ObserveTool(name string, arguments json.RawMessage, result domain.ToolResult, workspaceRevision int) {
	if _, ok := t.verificationTools[name]; !ok {
		return
	}
	t.commandAttempts++
	identity := verificationIdentity(name, arguments)
	t.latestCommandRecognized = name != "run_command" || verification.IsCommand(commandFromArguments(arguments)) || t.isDeclaredCheck(identity)
	exitCode, timedOut, structured := commandOutcome(result)
	t.latestTimedOut = timedOut
	if structured {
		t.latestExitCode = &exitCode
	} else {
		t.latestExitCode = nil
	}
	detail := fmt.Sprintf("exit code %d", exitCode)
	if timedOut {
		detail = "timed out"
	} else if !structured {
		detail = "missing structured outcome"
	} else if !result.OK {
		detail = "tool did not complete successfully: " + detail
	}
	passed := result.OK && structured && exitCode == 0 && !timedOut
	if t.latestCommandRecognized {
		previous, exists := t.checks[identity]
		baselineFailed := previous.BaselineFailed
		if !exists {
			baselineFailed = workspaceRevision == 0 && !passed
		}
		t.checks[identity] = verificationAttempt{
			Revision: workspaceRevision, Passed: passed, Detail: detail,
			Tool: name, Arguments: append(json.RawMessage(nil), arguments...),
			ExitCode: t.latestExitCode, TimedOut: timedOut, Structured: structured,
			ResultOK: result.OK, EverPassed: previous.EverPassed || passed,
			BaselineFailed: baselineFailed,
		}
	}
	if t.latestCommandRecognized && passed {
		t.successfulVerificationRevision = workspaceRevision
	}
}
func (t *completionTracker) Missing(workspaceRevision int, changedFiles []string) []completionRequirement {
	if t.brief != nil {
		return t.missingCriteria(workspaceRevision, changedFiles)
	}
	if t.explicitVerification && !t.verificationToolAvailable {
		return []completionRequirement{{Code: "verification_tool_unavailable", Message: "the task explicitly requires a test/build/lint verification, but no verification-capable tool is enabled for this agent"}}
	}
	required := t.explicitVerification || (workspaceRevision > 0 && len(changedFiles) > 0 && t.verificationToolAvailable)
	if !required {
		return nil
	}
	for _, identity := range t.sortedCheckIdentities() {
		check := t.checks[identity]
		if check.BaselineFailed && !check.EverPassed {
			continue
		}
		if !check.Passed || check.Revision != workspaceRevision {
			return []completionRequirement{{Code: "verification_failed", Message: "an active verification failed or is stale; rerun the same check on the current file revision: " + check.Detail}}
		}
	}
	if t.successfulVerificationRevision == workspaceRevision {
		return nil
	}
	message := "a successful verification-tool result is required"
	if workspaceRevision > 0 {
		message += " after the latest accepted file change"
	}
	if t.commandAttempts > 0 {
		if !t.latestCommandRecognized {
			message += "; the latest command is not a recognized test, build, lint, or static-analysis command"
		} else if t.latestTimedOut {
			message += "; the latest command timed out"
		} else if t.latestExitCode != nil {
			message += fmt.Sprintf("; the latest command exited with code %d", *t.latestExitCode)
		} else {
			message += "; no successful structured command outcome was recorded"
		}
	}
	code := "verification_required"
	if t.commandAttempts > 0 {
		code = "verification_failed"
	}
	return []completionRequirement{{Code: code, Message: message}}
}

func firstCustomToolSet(sets [][]domain.CustomTool) []domain.CustomTool {
	if len(sets) == 0 {
		return nil
	}
	return sets[0]
}

func verificationToolNames(profile domain.AgentProfile, customTools []domain.CustomTool) map[string]struct{} {
	allowed := make(map[string]struct{}, len(profile.AllowedTools))
	for _, name := range profile.AllowedTools {
		allowed[name] = struct{}{}
	}
	result := make(map[string]struct{})
	if _, ok := allowed["run_command"]; ok {
		result["run_command"] = struct{}{}
	}
	for _, tool := range customTools {
		if tool.ProvidesVerification {
			if _, ok := allowed[tool.ID]; ok {
				result[tool.ID] = struct{}{}
			}
		}
	}
	return result
}

func (t *completionTracker) Feedback(requirements []completionRequirement, workspaceRevision int, changedFiles []string) string {
	type evidence struct {
		WorkspaceRevision              int                     `json:"workspaceRevision"`
		ChangedFiles                   []string                `json:"changedFiles"`
		CommandAttempts                int                     `json:"commandAttempts"`
		SuccessfulVerificationRevision int                     `json:"successfulVerificationRevision"`
		LatestCommandRecognized        bool                    `json:"latestCommandRecognized"`
		SuggestedTools                 []string                `json:"suggestedTools,omitempty"`
		Requirements                   []completionRequirement `json:"requirements"`
		Contract                       *CompletionEvidence     `json:"contract,omitempty"`
	}
	encoded, _ := json.Marshal(evidence{
		WorkspaceRevision: workspaceRevision, ChangedFiles: append([]string(nil), changedFiles...), CommandAttempts: t.commandAttempts,
		SuccessfulVerificationRevision: t.successfulVerificationRevision, LatestCommandRecognized: t.latestCommandRecognized,
		SuggestedTools: append([]string(nil), t.suggestedTools...), Requirements: requirements,
		Contract: t.contractEvidence(workspaceRevision),
	})
	guidance := "Use an allowed tool — prefer a verification-capable tool (providesVerification, or a recognized test/build/lint/static-analysis run_command). Prefer a dedicated verifier over free-form shell when both exist. If it cannot be satisfied, state the blocker plainly; do not claim success or invent verification."
	if len(t.suggestedTools) > 0 {
		guidance = "Use one of these verification-capable tools next: " + strings.Join(t.suggestedTools, ", ") + ". Prefer a dedicated verifier over free-form shell when both exist. If verification cannot be satisfied, state the blocker plainly; do not claim success or invent evidence."
	}
	return "<point_completion_gate>\nThe candidate final answer was not accepted because deterministic local evidence is incomplete.\nEvidence: " + string(encoded) + "\n" + guidance + "\n</point_completion_gate>"
}

func commandFromArguments(arguments json.RawMessage) string {
	var input struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(arguments, &input) != nil {
		return ""
	}
	return input.Command
}

func completionRequirementError(requirements []completionRequirement) error {
	messages := make([]string, 0, len(requirements))
	for _, requirement := range requirements {
		messages = append(messages, requirement.Code+": "+requirement.Message)
	}
	return fmt.Errorf("agent final answer rejected by completion gate: %s", strings.Join(messages, "; "))
}

func commandOutcome(result domain.ToolResult) (exitCode int, timedOut bool, structured bool) {
	if len(result.Output) == 0 {
		return 0, false, false
	}
	var output struct {
		ExitCode *int `json:"exitCode"`
		TimedOut bool `json:"timedOut"`
	}
	if json.Unmarshal(result.Output, &output) != nil || output.ExitCode == nil {
		return 0, false, false
	}
	return *output.ExitCode, output.TimedOut, true
}

func toolCallCompletedSuccessfully(result domain.ToolResult) bool {
	if !result.OK {
		return false
	}
	exitCode, timedOut, structured := commandOutcome(result)
	return !structured || (exitCode == 0 && !timedOut)
}
