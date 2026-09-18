package app

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/security"
)

// CompletionCheckRunner executes one approved completion command against the
// delivered revision. It is an interface so the gate contract can be proven
// without a toolchain on the machine running the tests.
type CompletionCheckRunner interface {
	Run(ctx context.Context, directory, command string) (int, string, error)
}

type shellCompletionCheckRunner struct{}

func (shellCompletionCheckRunner) Run(ctx context.Context, directory, command string) (int, string, error) {
	output, err := completionShellCommand(ctx, directory, command).CombinedOutput()
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		// A failing check is evidence, not an infrastructure error: the exit
		// code belongs in the bundle so the gate can retain a real failure.
		return exitError.ExitCode(), string(output), nil
	}
	if err != nil {
		return 0, string(output), err
	}
	return 0, string(output), nil
}

// completionCheckTimeout bounds one approved command. Without a ceiling a hung
// service start would hold the quest in applying forever, and the user would
// see neither a result nor a reason.
const completionCheckTimeout = 15 * time.Minute

// completionCheckSummaryLimit keeps the tail of the output: a failure explains
// itself at the end, and the bundle must stay readable.
const completionCheckSummaryLimit = 2000

// runCompletionProfileV2 executes the approved profile on the delivered
// revision. Commands enter the approval digest, so nothing here can be
// substituted at runtime; this function only records what happened.
func (a *App) runCompletionProfileV2(ctx context.Context, order domain.WorkOrder, directory string) []domain.VerificationCheck {
	runner := a.completionCheckRunner
	if runner == nil {
		runner = shellCompletionCheckRunner{}
	}
	results := make([]domain.VerificationCheck, 0, len(order.Completion.Checks))
	for _, required := range order.Completion.Checks {
		if required.Kind == domain.CompletionCheckAcceptance || strings.TrimSpace(required.Command) == "" {
			continue
		}
		started := time.Now()
		attemptCtx, cancel := context.WithTimeout(ctx, completionCheckTimeout)
		code, output, err := runner.Run(attemptCtx, directory, required.Command)
		cancel()
		check := domain.VerificationCheck{
			ID: domain.CompletionCheckEvidenceID(required.Kind), Kind: required.Kind,
			Command: required.Command, DurationMs: time.Since(started).Milliseconds(),
			Summary: completionCheckSummaryV2(output, err),
		}
		if err == nil {
			value := code
			check.ExitCode = &value
			check.Satisfied = code == required.ExpectedExitCode
		}
		results = append(results, check)
	}
	return results
}

func completionCheckSummaryV2(output string, err error) string {
	text := strings.TrimSpace(output)
	if err != nil {
		text = strings.TrimSpace("Команда не запустилась: " + err.Error() + "\n" + text)
	}
	text = security.Redact(text)
	if runes := []rune(text); len(runes) > completionCheckSummaryLimit {
		text = "…" + string(runes[len(runes)-completionCheckSummaryLimit:])
	}
	return text
}

func failedCompletionCheckKindsV2(checks []domain.VerificationCheck) []string {
	failed := make([]string, 0, len(checks))
	for _, check := range checks {
		if !check.Satisfied {
			failed = append(failed, check.Kind)
		}
	}
	return failed
}

func completionCheckSatisfiedV2(checks []domain.VerificationCheck, kind string) bool {
	id := domain.CompletionCheckEvidenceID(kind)
	for _, check := range checks {
		if check.ID == id {
			return check.Satisfied
		}
	}
	return false
}

func orderRequiresCompletionCheckV2(order domain.WorkOrder, kind string) bool {
	for _, check := range order.Completion.Checks {
		if check.Kind == kind {
			return true
		}
	}
	return false
}
