package agent_test

import (
	"testing"

	"local-agent-workbench/internal/agent"
	"local-agent-workbench/internal/domain"
)

func TestRestrictTaskProfileFreezesBriefNetworkHosts(t *testing.T) {
	profile := domain.AgentProfile{
		ID: "p", AllowedTools: []string{"run_command"},
		ToolPolicies: map[string]string{
			"network:old.example": "ALLOW",
			"network":             "ALLOW",
		},
	}
	brief, err := domain.ApproveTaskBrief(domain.NormalizeTaskBrief(domain.TaskBrief{
		Mode: domain.TaskModePrecise, Goal: "Fetch", ResultKind: "report",
		Criteria:    []domain.AcceptanceCriterion{{ID: "c", Kind: "manual", Text: "Done"}},
		Permissions: domain.TaskPermissions{ExecuteCommands: true, NetworkHosts: []string{"api.example.com"}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	narrowed, err := agent.RestrictTaskProfile(profile, &brief)
	if err != nil {
		t.Fatal(err)
	}
	if narrowed.ToolPolicies["network"] != "DENY" {
		t.Fatalf("network=%q", narrowed.ToolPolicies["network"])
	}
	if narrowed.ToolPolicies["network:api.example.com"] != "ALLOW" {
		t.Fatalf("brief host not promoted: %#v", narrowed.ToolPolicies)
	}
	if narrowed.ToolPolicies["network:old.example"] != "DENY" {
		t.Fatalf("profile host outside brief still allowed: %#v", narrowed.ToolPolicies)
	}
}
