package policy

import (
	"testing"

	"local-agent-workbench/internal/domain"
)

func TestDenyDoesNotEscapeThroughApproval(t *testing.T) {
	decision := Engine{}.Evaluate(domain.AgentProfile{
		AllowedTools: []string{"run_command"},
		ToolPolicies: map[string]string{"run_command": "DENY"},
		ApprovalMode: domain.ApprovalAlways,
	}, "run_command")
	if !decision.Denied {
		t.Fatalf("DENY must hard-block: %#v", decision)
	}
	if decision.RequiresApproval {
		t.Fatalf("DENY must not become an approval card: %#v", decision)
	}
	if decision.Policy != domain.ToolPolicyDeny {
		t.Fatalf("policy=%q", decision.Policy)
	}
}

func TestAskStillRequiresApproval(t *testing.T) {
	decision := Engine{}.Evaluate(domain.AgentProfile{
		AllowedTools: []string{"propose_patch"},
		ToolPolicies: map[string]string{"propose_patch": "ASK"},
	}, "propose_patch")
	if decision.Denied || !decision.RequiresApproval {
		t.Fatalf("ASK should require confirmation: %#v", decision)
	}
}

func TestAllowLowRiskDoesNotRequireApproval(t *testing.T) {
	decision := Engine{}.Evaluate(domain.AgentProfile{
		AllowedTools: []string{"read_file"},
		ToolPolicies: map[string]string{"read_file": "ALLOW"},
	}, "read_file")
	if decision.Denied || decision.RequiresApproval {
		t.Fatalf("ALLOW on LOW tool should pass: %#v", decision)
	}
}

func TestDockerControlRequiresApproval(t *testing.T) {
	decision := Engine{}.Evaluate(domain.AgentProfile{
		AllowedTools: []string{"docker_control"},
		ToolPolicies: map[string]string{"docker_control": "ALLOW"},
	}, "docker_control")
	if decision.Denied || !decision.RequiresApproval {
		t.Fatalf("docker_control must require approval even on ALLOW: %#v", decision)
	}
	if decision.Risk != domain.ToolRiskCritical {
		t.Fatalf("risk=%q", decision.Risk)
	}
}

func TestDockerInspectIsLowAllow(t *testing.T) {
	decision := Engine{}.Evaluate(domain.AgentProfile{
		AllowedTools: []string{"docker_inspect"},
		ToolPolicies: map[string]string{"docker_inspect": "ALLOW"},
	}, "docker_inspect")
	if decision.Denied || decision.RequiresApproval {
		t.Fatalf("docker_inspect ALLOW should pass: %#v", decision)
	}
	if decision.Risk != domain.ToolRiskLow {
		t.Fatalf("risk=%q", decision.Risk)
	}
}
