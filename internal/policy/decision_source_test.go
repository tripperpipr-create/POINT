package policy

import (
	"testing"

	"local-agent-workbench/internal/domain"
)

// Q05: решение называет правило, которое его приняло.
func TestDecisionNamesItsSource(t *testing.T) {
	trusting := Engine{TrustedCustomTool: func(string) bool { return true }}
	cases := []struct {
		name    string
		engine  Engine
		profile domain.AgentProfile
		tool    string
		want    DecisionSource
		denied  bool
	}{
		{"умолчание каталога", Engine{}, domain.AgentProfile{}, "read_file", SourceCatalogDefault, false},
		{"явная запись профиля", Engine{}, domain.AgentProfile{ToolPolicies: map[string]string{"read_file": "DENY"}}, "read_file", SourceProfileExplicit, true},
		{"запрет сети по умолчанию", Engine{}, domain.AgentProfile{}, "http_request", SourceCatalogDefaultDeny, true},
		{"доверие", trusting, domain.AgentProfile{}, "customtool_lint", SourceTrustedCustomTool, false},
		{"доверие не снимает явного", trusting, domain.AgentProfile{ToolPolicies: map[string]string{"customtool_lint": "ASK"}}, "customtool_lint", SourceProfileExplicit, false},
		{"подтверждать всё", Engine{}, domain.AgentProfile{ApprovalMode: domain.ApprovalAlways, ToolPolicies: map[string]string{"read_file": "ALLOW"}}, "read_file", SourceApprovalModeAlways, false},
		{"явный ALLOW с окном встроенного", Engine{}, domain.AgentProfile{ToolPolicies: map[string]string{"run_command": "ALLOW"}}, "run_command", SourceBuiltinConfirmation, false},
	}
	for _, item := range cases {
		decision := item.engine.Evaluate(item.profile, item.tool)
		if decision.Source != item.want || decision.Denied != item.denied {
			t.Fatalf("%s: source=%s denied=%v, ждали %s denied=%v", item.name, decision.Source, decision.Denied, item.want, item.denied)
		}
	}
}
