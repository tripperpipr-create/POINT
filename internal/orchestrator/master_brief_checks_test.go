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

// Отвергнутый бриф оставляет причину, а не только счётчик: генератор
// кандидата методики должен знать, что именно исправлять.
func TestRejectedBriefKeepsItsReasonForLearning(t *testing.T) {
	brief := validBrief()
	brief.ResultKind = "workspace_change"
	brief.Permissions.WriteFiles = true
	brief.Criteria = []domain.AcceptanceCriterion{{ID: "verify", Text: "Проверка проходит", Kind: "verification", Tool: "run_command", Arguments: json.RawMessage(`{"command":"npm run verify || true"}`)}}
	raw, _ := json.Marshal(map[string]any{"title": "Сборка", "brief": brief})
	actions := &masterActions{}
	actions.proposeBrief(raw)
	actions.proposeBrief(json.RawMessage(`{"title":"x","brief":"строка вместо объекта"}`))
	if len(actions.rejectReasons) != 2 || !strings.Contains(actions.rejectReasons[0], "|| true") {
		t.Fatalf("reasons=%#v", actions.rejectReasons)
	}
	var op domain.MasterOperation
	for _, reason := range actions.rejectReasons {
		op.AddDefect("brief_rejected", reason)
	}
	op.AddDefect("long", strings.Repeat("я", 1000))
	if len(op.Defects) != 3 || len([]rune(op.Defects[2].Reason)) > 301 {
		t.Fatalf("defects=%#v", op.Defects)
	}
}

func TestRevisionNotesPromptAsksForRuleOnlyWhenNotesExist(t *testing.T) {
	if revisionNotesPrompt(nil) != "" || revisionNotesPrompt([]string{"  "}) != "" {
		t.Fatal("empty notes produced a prompt")
	}
	prompt := revisionNotesPrompt([]string{"человек изменил поля наряда: criteria"})
	if !strings.Contains(prompt, "suggest_memory") || !strings.Contains(prompt, "criteria") {
		t.Fatalf("prompt=%q", prompt)
	}
}
