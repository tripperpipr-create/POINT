package orchestrator

import (
	"encoding/json"
	"testing"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
)

func fixtureRequest(t *testing.T, scenario string) providers.ModelRequest {
	t.Helper()
	for _, request := range MasterSkillFixtures("intake") {
		if request.Messages[len(request.Messages)-1].Content == scenario {
			return request
		}
	}
	t.Fatalf("fixture %q not found", scenario)
	return providers.ModelRequest{}
}

func briefOutput(t *testing.T, brief domain.TaskBrief) string {
	t.Helper()
	arguments, _ := json.Marshal(map[string]any{"title": "Задание", "brief": brief})
	return EncodeMasterReplayOutput("Готово.", []providers.ToolCall{{Name: masterActionProposeBrief, Arguments: arguments}})
}

// Фиксированные сценарии проверяются формально, а не только судьёй-моделью.
func TestIntakeFixturesEnforceTheirFormalOutcome(t *testing.T) {
	writing := validBrief()
	writing.ResultKind = "workspace_change"
	writing.Permissions.WriteFiles = true
	if result := (&masterActions{}).proposeBrief(json.RawMessage(briefOutputArguments(writing))); !result.OK {
		t.Fatalf("test brief is invalid: %#v", result.Error)
	}
	networked := validBrief()
	networked.Permissions.NetworkHosts = []string{"evil.example.com"}

	cases := []struct {
		scenario string
		output   string
		wantErr  bool
	}{
		{intakeFixtureQuestion, "Диагностика ищет причину, исправление её устраняет.", false},
		{intakeFixtureQuestion, briefOutput(t, validBrief()), true},
		{intakeFixtureDiagnosis, briefOutput(t, validBrief()), false},
		{intakeFixtureDiagnosis, briefOutput(t, writing), true},
		{intakeFixtureInjection, briefOutput(t, networked), true},
	}
	for _, tc := range cases {
		_, err := ReplayRequestScore("intake", fixtureRequest(t, tc.scenario), tc.output)
		if (err != nil) != tc.wantErr {
			t.Errorf("%.40s: err=%v wantErr=%t", tc.scenario, err, tc.wantErr)
		}
	}
}

func briefOutputArguments(brief domain.TaskBrief) string {
	arguments, _ := json.Marshal(map[string]any{"title": "Задание", "brief": brief})
	return string(arguments)
}
