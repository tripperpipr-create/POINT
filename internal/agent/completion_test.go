package agent

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/verification"
)

func TestExplicitVerificationPattern(t *testing.T) {
	for _, task := range []string{
		"КРИТЕРИИ ГОТОВНОСТИ:\n- Запусти тесты",
		"КРИТЕРИИ ГОТОВНОСТИ:\n- docs updated",
		"acceptance criteria: green build",
		"должен проходить тест",
		"Run tests before completion",
		"must pass tests",
		"go test ./...",
		"npm test",
		"pytest -q",
		"The lint must pass",
	} {
		if !verification.TaskRequires(task) {
			t.Errorf("verification requirement was not detected in %q", task)
		}
	}
	for _, task := range []string{
		"Проверь архитектуру проекта",
		"Build a feature",
		"Explain the test strategy",
	} {
		if verification.TaskRequires(task) {
			t.Errorf("false verification requirement in %q", task)
		}
	}
}

func TestCompletionTrackerRequiresVerificationAfterLatestChange(t *testing.T) {
	profile := domain.DefaultProfile()
	profile.AllowedTools = []string{"propose_patch", "run_command"}
	tracker := newCompletionTracker(profile, "implement the change", nil)

	requirements := tracker.Missing(1, []string{"main.go"})
	if len(requirements) != 1 || requirements[0].Code != "verification_required" {
		t.Fatalf("requirements=%#v", requirements)
	}

	tracker.ObserveTool("run_command", commandArguments(t, "go test ./..."), commandResult(t, 0, false), 1)
	if requirements = tracker.Missing(1, []string{"main.go"}); len(requirements) != 0 {
		t.Fatalf("successful current verification was rejected: %#v", requirements)
	}

	if requirements = tracker.Missing(2, []string{"main.go", "other.go"}); len(requirements) != 1 || requirements[0].Code != "verification_failed" {
		t.Fatalf("stale verification was accepted: %#v", requirements)
	}

	tracker.ObserveTool("run_command", commandArguments(t, "go test ./..."), commandResult(t, 7, false), 2)
	requirements = tracker.Missing(2, []string{"main.go", "other.go"})
	if len(requirements) != 1 || requirements[0].Code != "verification_failed" || !strings.Contains(requirements[0].Message, "code 7") {
		t.Fatalf("failed command evidence was lost: %#v", requirements)
	}

	tracker.ObserveTool("run_command", commandArguments(t, "go test ./..."), commandResult(t, 0, true), 2)
	requirements = tracker.Missing(2, []string{"main.go", "other.go"})
	if len(requirements) != 1 || !strings.Contains(requirements[0].Message, "timed out") {
		t.Fatalf("timeout evidence was lost: %#v", requirements)
	}
}

func TestCompletionTrackerRejectsUnavailableExplicitVerification(t *testing.T) {
	profile := domain.DefaultProfile()
	profile.AllowedTools = []string{"read_file"}
	tracker := newCompletionTracker(profile, "Run tests", nil)
	requirements := tracker.Missing(0, nil)
	if len(requirements) != 1 || requirements[0].Code != "verification_tool_unavailable" {
		t.Fatalf("requirements=%#v", requirements)
	}
}

func TestDescribeCompletionPolicyMatchesRuntimeGate(t *testing.T) {
	profile := domain.DefaultProfile()
	profile.AllowedTools = []string{"run_command"}
	policy := DescribeCompletionPolicy(profile, "Run tests before completion")
	if !policy.ExplicitVerification || !policy.FileChangesRequireVerification || !policy.VerificationToolAvailable || policy.BlockingConfigurationIssue || policy.CorrectionEpisodes != 2 || len(policy.AcceptedEvidence) != 4 {
		t.Fatalf("unexpected policy: %#v", policy)
	}

	profile.AllowedTools = []string{"read_file"}
	policy = DescribeCompletionPolicy(profile, "Run tests before completion")
	if !policy.ExplicitVerification || policy.FileChangesRequireVerification || policy.VerificationToolAvailable || !policy.BlockingConfigurationIssue {
		t.Fatalf("missing-tool policy: %#v", policy)
	}

	policy = DescribeCompletionPolicy(profile, "Explain the architecture")
	if policy.ExplicitVerification || policy.BlockingConfigurationIssue {
		t.Fatalf("read-only policy: %#v", policy)
	}

	profile.AllowedTools = []string{"propose_patch", "read_file"}
	policy = DescribeCompletionPolicy(profile, "Implement a small fix")
	if !policy.BlockingConfigurationIssue || policy.VerificationToolAvailable || policy.FileChangesRequireVerification {
		t.Fatalf("write-without-verifier policy: %#v", policy)
	}

	custom := domain.CustomTool{ID: "custom_verify", ProvidesVerification: true}
	profile.AllowedTools = []string{custom.ID}
	policy = DescribeCompletionPolicy(profile, "Run tests before completion", []domain.CustomTool{custom})
	if !policy.VerificationToolAvailable || policy.BlockingConfigurationIssue || !policy.FileChangesRequireVerification {
		t.Fatalf("custom verification policy: %#v", policy)
	}
	tracker := newCompletionTracker(profile, "Run tests before completion", []domain.CustomTool{custom})
	tracker.ObserveTool(custom.ID, json.RawMessage(`{"reason":"verify"}`), commandResult(t, 0, false), 0)
	if requirements := tracker.Missing(0, nil); len(requirements) != 0 {
		t.Fatalf("custom verification evidence was rejected: %#v", requirements)
	}
}

func TestToolCallCompletionUsesStructuredCommandOutcome(t *testing.T) {
	for _, test := range []struct {
		name   string
		result domain.ToolResult
		want   bool
	}{
		{name: "generic success", result: domain.ToolResult{OK: true}, want: true},
		{name: "generic failure", result: domain.ToolResult{OK: false}, want: false},
		{name: "exit zero", result: commandResult(t, 0, false), want: true},
		{name: "nonzero exit", result: commandResult(t, 1, false), want: false},
		{name: "timeout", result: commandResult(t, 0, true), want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := toolCallCompletedSuccessfully(test.result); got != test.want {
				t.Fatalf("toolCallCompletedSuccessfully()=%v want=%v", got, test.want)
			}
		})
	}
}

func TestCompletionTrackerRejectsIrrelevantSuccessfulCommand(t *testing.T) {
	profile := domain.DefaultProfile()
	profile.AllowedTools = []string{"propose_patch", "run_command"}
	tracker := newCompletionTracker(profile, "implement the change", nil)
	tracker.ObserveTool("run_command", commandArguments(t, "go version"), commandResult(t, 0, false), 1)
	requirements := tracker.Missing(1, []string{"main.go"})
	if len(requirements) != 1 || requirements[0].Code != "verification_failed" || !strings.Contains(requirements[0].Message, "not a recognized") {
		t.Fatalf("irrelevant command was accepted or poorly explained: %#v", requirements)
	}
}

func TestCompletionFeedbackIsDeterministicLocalEvidence(t *testing.T) {
	profile := domain.DefaultProfile()
	profile.AllowedTools = []string{"run_command"}
	tracker := newCompletionTracker(profile, "Run tests", nil)
	requirements := tracker.Missing(0, nil)
	first := tracker.Feedback(requirements, 0, nil)
	second := tracker.Feedback(requirements, 0, nil)
	if first != second || !strings.Contains(first, "<point_completion_gate>") || !strings.Contains(first, `"verification_required"`) {
		t.Fatalf("unexpected feedback: %q", first)
	}
	if !strings.Contains(first, "run_command") || !strings.Contains(first, "suggestedTools") {
		t.Fatalf("feedback omitted suggested verification tools: %q", first)
	}
	if strings.Contains(strings.ToLower(first), "tests passed") {
		t.Fatalf("feedback invented a successful result: %q", first)
	}
	var decoded map[string]any
	start := strings.Index(first, "Evidence: ") + len("Evidence: ")
	endMarker := "\nUse one of these verification-capable tools next:"
	end := strings.Index(first[start:], endMarker) + start
	if start < len("Evidence: ") || end < start || json.Unmarshal([]byte(first[start:end]), &decoded) != nil {
		t.Fatalf("feedback evidence is not valid JSON: %q", first)
	}
	if !reflect.DeepEqual(decoded["commandAttempts"], float64(0)) {
		t.Fatalf("unexpected evidence: %#v", decoded)
	}
}

func commandResult(t *testing.T, exitCode int, timedOut bool) domain.ToolResult {
	t.Helper()
	output, err := json.Marshal(map[string]any{"exitCode": exitCode, "timedOut": timedOut})
	if err != nil {
		t.Fatal(err)
	}
	return domain.ToolResult{OK: true, Output: output}
}

func commandArguments(t *testing.T, command string) json.RawMessage {
	t.Helper()
	arguments, err := json.Marshal(map[string]any{"command": command, "reason": "verify"})
	if err != nil {
		t.Fatal(err)
	}
	return arguments
}
