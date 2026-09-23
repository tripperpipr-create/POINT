package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/osproc"
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
		var code int
		var output string
		var err error
		if required.Kind == "health" && strings.TrimSpace(required.URL) != "" {
			switch runner.(type) {
			case shellCompletionCheckRunner:
				code, output, err = runManagedHealthCheckV2(attemptCtx, directory, required.Command, required.URL)
			default:
				code, output, err = runner.Run(attemptCtx, directory, required.Command)
			}
		} else {
			code, output, err = runner.Run(attemptCtx, directory, required.Command)
		}
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

func runManagedHealthCheckV2(ctx context.Context, directory, command, probeURL string) (int, string, error) {
	parts := strings.Fields(command)
	if len(parts) < 2 || !strings.EqualFold(parts[0], "php") || parts[1] != "-S" {
		return 0, "", errors.New("managed health check requires an approved php -S command")
	}
	var output bytes.Buffer
	cmd := osproc.CommandContext(ctx, parts[0], parts[1:]...)
	cmd.Dir, cmd.Stdout, cmd.Stderr = directory, &output, &output
	if err := cmd.Start(); err != nil {
		return 0, output.String(), err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	}()
	client := &http.Client{Timeout: 2 * time.Second}
	for {
		select {
		case err := <-done:
			if err == nil {
				err = errors.New("temporary PHP server stopped before the health check")
			}
			return 1, output.String(), err
		case <-ctx.Done():
			return 0, output.String(), ctx.Err()
		case <-time.After(250 * time.Millisecond):
			response, requestErr := client.Get(probeURL)
			if requestErr != nil {
				continue
			}
			_ = response.Body.Close()
			if response.StatusCode >= 200 && response.StatusCode < 400 {
				return 0, fmt.Sprintf("GET %s: %s\n%s", probeURL, response.Status, output.String()), nil
			}
			if response.StatusCode >= 500 {
				return 1, fmt.Sprintf("GET %s: %s\n%s", probeURL, response.Status, output.String()), nil
			}
		}
	}
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
