package app

import (
	"context"
	"encoding/json"
	"testing"
)

func TestNarrowStateSnapshotsKeepStableArrayContracts(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	if _, err = application.OpenWorkspace(t.TempDir()); err != nil {
		t.Fatal(err)
	}

	runtimeState, err := application.RuntimeState()
	if err != nil {
		t.Fatal(err)
	}
	assertJSONArrays(t, runtimeState, []string{
		"runs", "runDiagnostics", "workflowRuns", "changes", "quests", "flows",
		"flowRuns", "executions", "changeSets", "questProposals", "companionActionProposals",
		"learningSignals", "skillOutcomes",
	})

	guildState, err := application.GuildState()
	if err != nil {
		t.Fatal(err)
	}
	assertJSONArrays(t, guildState, []string{
		"blueprints", "projectAgents", "skills", "projectSkills", "teams", "skillCuration",
	})
	if len(guildState.Skills) == 0 {
		t.Fatal("guild snapshot did not seed the built-in skills")
	}
}

func assertJSONArrays(t *testing.T, value any, keys []string) {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, key := range keys {
		item, ok := decoded[key]
		if !ok {
			t.Fatalf("narrow state omitted %q", key)
		}
		if _, ok := item.([]any); !ok {
			t.Fatalf("narrow state field %q is %T, want JSON array", key, item)
		}
	}
}
