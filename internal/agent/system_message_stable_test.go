package agent

import (
	"testing"

	"local-agent-workbench/internal/domain"
)

// Системное сообщение — начало каждого запроса прогона. Если оно хоть на байт
// разное от вызова к вызову, кэш префикса у провайдера промахивается сразу, и
// каждый ход заново считает весь контекст.
func TestSystemMessageIsByteStable(t *testing.T) {
	profile := domain.AgentProfile{
		AllowedTools: []string{"run_command", "verify-b", "verify-a", "read_file"},
		ToolPolicies: map[string]string{"network": "DENY", "network:b.example": "ALLOW", "network:a.example": "ALLOW", "network:c.example": "ALLOW"},
	}
	tools := []domain.CustomTool{
		{ID: "verify-b", DisplayName: "B", ProvidesVerification: true},
		{ID: "verify-a", DisplayName: "A", ProvidesVerification: true},
	}
	first := SystemMessage(profile, tools)
	for i := 0; i < 50; i++ {
		if got := SystemMessage(profile, tools); got != first {
			t.Fatalf("system message changed between calls:\n%s\n---\n%s", first, got)
		}
	}
}
