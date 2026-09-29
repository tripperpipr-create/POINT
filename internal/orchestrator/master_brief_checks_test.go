package orchestrator

import (
	"encoding/json"
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
)

// Q08: критерий «npm run verify || true» проходит при упавшей сборке.
// Мастер получает отказ с найденным фрагментом, а не принятое задание.
func TestProposeBriefRejectsExitMaskingCriterion(t *testing.T) {
	brief := validBrief()
	brief.ResultKind = "workspace_change"
	brief.Permissions.WriteFiles = true
	brief.Criteria = []domain.AcceptanceCriterion{{ID: "verify", Text: "Проверка проходит", Kind: "verification", Tool: "run_command", Arguments: json.RawMessage(`{"command":"npm run verify || true"}`)}}
	raw, _ := json.Marshal(map[string]any{"title": "Сборка", "brief": brief})
	actions := &masterActions{}
	result := actions.proposeBrief(raw)
	if result.OK || result.Error == nil || !strings.Contains(result.Error.Message, "|| true") || actions.brief != nil {
		t.Fatalf("задание с маскировкой кода выхода принято: %#v", result)
	}
	brief.Criteria[0].Arguments = json.RawMessage(`{"command":"npm run verify"}`)
	raw, _ = json.Marshal(map[string]any{"title": "Сборка", "brief": brief})
	if result = (&masterActions{}).proposeBrief(raw); !result.OK {
		t.Fatalf("обычная команда отвергнута: %#v", result.Error)
	}
}
