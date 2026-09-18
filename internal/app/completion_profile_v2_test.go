package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
)

type scriptedCompletionRunner struct {
	results  map[string]int
	failures map[string]error
}

func (runner *scriptedCompletionRunner) Run(_ context.Context, _, command string) (int, string, error) {
	if err, ok := runner.failures[command]; ok {
		return 0, "", err
	}
	return runner.results[command], "output of " + command, nil
}

func completionProfileOrder() domain.WorkOrder {
	return domain.NormalizeWorkOrder(domain.WorkOrder{
		State: "ready", Goal: "Ship", Scope: []string{"api"},
		Criteria:  []domain.AcceptanceCriterion{{ID: "c1", Kind: "verification", Text: "works"}},
		Workspace: domain.WorkspacePlan{Mode: "existing", Path: `C:\Point\Projects\ship`, Isolation: "snapshot"},
		Stack:     domain.StackPresetRef{ID: "api", Version: "1", Category: "api", Source: "benchmark"},
		Routing:   domain.ModelRoutingPolicy{Mode: "fixed", FixedConnectionID: "connection", FixedModel: "model"},
		Budget:    domain.BudgetEnvelope{Preset: "small", Tokens: 1000, ActiveSeconds: 60, MaxParallel: 1, MaxAttempts: 1},
		Delivery:  domain.DeliveryPolicy{ApplyMode: "automatic", CommitMode: "none", KeepPartialDays: 30},
		Completion: domain.CompletionProfile{ID: "mvp-api", Version: "1", Checks: []domain.CompletionCheck{
			{Kind: domain.CompletionCheckAcceptance},
			{Kind: "build", Command: "composer install"},
			{Kind: "automated_tests", Command: "vendor/bin/phpunit"},
			{Kind: "service_start", Command: "docker compose up -d --wait"},
		}},
	})
}

func TestCompletionProfileRecordsApprovedCommandsAsEvidence(t *testing.T) {
	runner := &scriptedCompletionRunner{results: map[string]int{"vendor/bin/phpunit": 2}}
	application := &App{completionCheckRunner: runner}
	order := completionProfileOrder()

	checks := application.runCompletionProfileV2(context.Background(), order, `C:\Point\Projects\ship`)

	if len(checks) != 3 {
		t.Fatalf("acceptance has no command and must not be executed: %#v", checks)
	}
	for index, expected := range []struct{ kind, command string }{
		{"build", "composer install"},
		{"automated_tests", "vendor/bin/phpunit"},
		{"service_start", "docker compose up -d --wait"},
	} {
		check := checks[index]
		if check.ID != domain.CompletionCheckEvidenceID(expected.kind) || check.Kind != expected.kind || check.Command != expected.command {
			t.Fatalf("check %d = %#v", index, check)
		}
		if check.ExitCode == nil {
			t.Fatalf("check %d has no exit code", index)
		}
	}
	if !checks[0].Satisfied || checks[1].Satisfied || !checks[2].Satisfied {
		t.Fatalf("a nonzero exit code must not be satisfied: %#v", checks)
	}
	if !strings.Contains(checks[0].Summary, "composer install") {
		t.Fatalf("summary lost the command output: %q", checks[0].Summary)
	}
	if !completionCheckSatisfiedV2(checks, "service_start") || completionCheckSatisfiedV2(checks, "automated_tests") {
		t.Fatal("satisfied lookup does not follow the recorded evidence")
	}
	if !orderRequiresCompletionCheckV2(order, "service_start") || orderRequiresCompletionCheckV2(order, "health") {
		t.Fatal("profile lookup does not follow the approved order")
	}
}

func TestMasterProposesOnlyExecutableCompletionChecks(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	order := domain.WorkOrder{
		WorkspaceID: "workspace", Workspace: domain.WorkspacePlan{Mode: "existing", Path: root},
		Stack:    domain.StackPresetRef{ID: "api", Version: "1", Category: "api"},
		Delivery: domain.DeliveryPolicy{KeepServicesRunning: true, ApplicationURL: "http://localhost:8080"},
	}

	profile := masterCompletionProfileV2(order)

	commands := map[string]string{}
	for _, check := range profile.Checks {
		commands[check.Kind] = check.Command
	}
	if commands["automated_tests"] != "go test ./..." {
		t.Fatalf("test command must come from the manifest that exists: %#v", profile.Checks)
	}
	if commands["service_start"] == "" || commands["health"] == "" {
		t.Fatalf("a promised running application needs a start and a health command: %#v", profile.Checks)
	}
	if _, ok := commands[domain.CompletionCheckAcceptance]; !ok {
		t.Fatal("acceptance criteria are always part of the profile")
	}
	// Checks nobody can execute are exactly what stopped meaning anything.
	for _, kind := range []string{"browser_journey", "accessibility", "dependency_audit", "secret_scan", "performance"} {
		if _, present := commands[kind]; present {
			t.Fatalf("profile promises %q without a command", kind)
		}
	}
	if err := domain.ValidateWorkOrder(domain.NormalizeWorkOrder(domain.WorkOrder{
		State: "ready", Goal: "ship", Scope: []string{"api"},
		Criteria:   []domain.AcceptanceCriterion{{ID: "c1", Kind: "verification", Text: "works", Tool: "run_command", Arguments: json.RawMessage(`{"command":"go test ./..."}`)}},
		Workspace:  domain.WorkspacePlan{Mode: "existing", Path: root, Isolation: "snapshot"},
		Stack:      domain.StackPresetRef{ID: "api", Version: "1", Category: "api", Source: "benchmark"},
		Routing:    domain.ModelRoutingPolicy{Mode: "fixed", FixedConnectionID: "connection", FixedModel: "model"},
		Budget:     domain.BudgetEnvelope{Preset: "small", Tokens: 1000, ActiveSeconds: 60, MaxParallel: 1, MaxAttempts: 1},
		Delivery:   domain.DeliveryPolicy{ApplyMode: "automatic", CommitMode: "none", KeepPartialDays: 30},
		Completion: profile,
	})); err != nil {
		t.Fatalf("the proposed profile must be approvable: %v", err)
	}
}

func TestShellRunnerKeepsTheApprovedCommandIntact(t *testing.T) {
	runner := shellCompletionCheckRunner{}
	root := t.TempDir()

	// Quotes inside an approved command must reach the shell as written: the
	// default argv escaping turns them into \" on Windows, which cmd.exe runs
	// as a different command than the one the user approved.
	code, output, err := runner.Run(context.Background(), root, `echo "x&y"`)
	if err != nil || code != 0 {
		t.Fatalf("quoted command code=%d err=%v output=%q", code, err, output)
	}
	if !strings.Contains(output, "x&y") || strings.Contains(output, `\"`) {
		t.Fatalf("quoted command reached the shell mangled: %q", output)
	}

	code, _, err = runner.Run(context.Background(), root, "exit 3")
	if err != nil || code != 3 {
		t.Fatalf("a failing command must report its exit code: code=%d err=%v", code, err)
	}
}

func TestCompletionCheckThatCannotStartIsNotSatisfied(t *testing.T) {
	runner := &scriptedCompletionRunner{failures: map[string]error{"composer install": errors.New("composer not found")}}
	application := &App{completionCheckRunner: runner}

	checks := application.runCompletionProfileV2(context.Background(), completionProfileOrder(), `C:\Point\Projects\ship`)

	// No exit code means nothing was measured: the gate must see an unmet
	// check rather than a zero that looks like success.
	if checks[0].ExitCode != nil || checks[0].Satisfied {
		t.Fatalf("unstarted check = %#v", checks[0])
	}
	if !strings.Contains(checks[0].Summary, "composer not found") {
		t.Fatalf("summary hides the reason: %q", checks[0].Summary)
	}
}
