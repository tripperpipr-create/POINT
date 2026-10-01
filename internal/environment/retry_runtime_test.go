package environment

import (
	"testing"

	"local-agent-workbench/internal/sandbox"
)

func TestRetryChoiceRequiresNodeVersionAndPutsItsImageFirst(t *testing.T) {
	base := sandbox.RuntimeRequirements{
		RequiredCommands: []string{"git"}, ToolVersions: map[string]string{"node": "24", "go": "1.25"},
		CandidateImages: []string{"other:1"}, VersionConflicts: []string{"node: 20 (.nvmrc) / 22 (ci)", "go: 1.24 / 1.25"},
	}
	got := RuntimeRequirementsWithRetryChoice(base, "node20")
	if got.ToolVersions["node"] != "20" || got.ToolVersions["go"] != "1.25" {
		t.Fatalf("versions=%v", got.ToolVersions)
	}
	if got.CandidateImages[0] != Node20ManagedImage {
		t.Fatalf("candidates=%v", got.CandidateImages)
	}
	if len(got.VersionConflicts) != 1 || got.VersionConflicts[0] != "go: 1.24 / 1.25" {
		t.Fatalf("the choice must settle node conflicts only: %v", got.VersionConflicts)
	}
	// Без команды node версия не проверялась бы вовсе: образ по умолчанию
	// прошёл бы как есть.
	commands := map[string]bool{}
	for _, command := range got.RequiredCommands {
		commands[command] = true
	}
	if !commands["node"] || !commands["npm"] || !commands["git"] {
		t.Fatalf("commands=%v", got.RequiredCommands)
	}
	if base.ToolVersions["node"] != "24" {
		t.Fatal("the stage's own requirements were mutated")
	}
}

func TestRetryChoiceRejectsUnknownNames(t *testing.T) {
	base := sandbox.RuntimeRequirements{ToolVersions: map[string]string{"node": "24"}}
	if got := RuntimeRequirementsWithRetryChoice(base, "ubuntu:latest"); got.ToolVersions["node"] != "24" {
		t.Fatalf("an unknown choice changed the runtime: %v", got.ToolVersions)
	}
	if _, ok := RetryRuntimeChoiceByID("node18"); ok {
		t.Fatal("node18 is not a managed pack")
	}
}
