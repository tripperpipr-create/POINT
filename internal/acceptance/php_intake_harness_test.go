package acceptance_test

import (
	"testing"

	"local-agent-workbench/internal/app"
)

func TestHeadlessDecisionActionEgressAndSupervision(t *testing.T) {
	hosts := []string{"repo.packagist.org", "github.com", "api.github.com"}
	remotes := []string{"https://github.com/systemeio/backend-test-task"}
	if got := headlessDecisionAction(app.DecisionEgress, "api.github.com", hosts, remotes, 0); got != "allow_quest" {
		t.Fatalf("api.github.com egress action=%q", got)
	}

	if got := headlessDecisionAction(app.DecisionEgress, "repo.packagist.org", hosts, remotes, 0); got != "allow_quest" {
		t.Fatalf("packagist: got %q", got)
	}
	if got := headlessDecisionAction(app.DecisionEgress, "https://github.com/systemeio/backend-test-task", hosts, remotes, 0); got != "allow_quest" {
		t.Fatalf("confirmed remote: got %q", got)
	}
	if got := headlessDecisionAction(app.DecisionEgress, "evil.example", hosts, remotes, 0); got != "deny" {
		t.Fatalf("unknown host: got %q", got)
	}
	if got := headlessDecisionAction(app.DecisionSupervision, "", nil, nil, 0); got != "continue" {
		t.Fatalf("first supervision: got %q", got)
	}
	if got := headlessDecisionAction(app.DecisionSupervision, "", nil, nil, 1); got != "stop" {
		t.Fatalf("second supervision: got %q", got)
	}
	if got := headlessDecisionAction(app.DecisionApproval, "", nil, nil, 0); got != "" {
		t.Fatalf("approval should not use this helper: got %q", got)
	}
}
