package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
)

// A brief approved before the draft repair still carries the model's synonym in
// its signed content. Execution must resolve it instead of dying at launch, and
// the evidence tracker must look for the same tool the gate accepted.
func TestApprovedCriterionSynonymSurvivesLaunch(t *testing.T) {
	exit := 0
	brief := domain.TaskBrief{
		Version: 1, State: "approved", Mode: domain.TaskModePrecise, Goal: "Fix the parser", ResultKind: "workspace_change",
		Criteria: []domain.AcceptanceCriterion{
			{ID: "c1", Text: "Tests pass", Kind: "verification", Tool: "shell", Arguments: json.RawMessage(`{"command":"go test ./..."}`), ExpectedExitCode: &exit},
		},
		Permissions: domain.TaskPermissions{WriteFiles: true, ExecuteCommands: true},
		Budget:      domain.TaskBudget{Tokens: 1000, ActiveSeconds: 60, MaxParallel: 1, MaxReplans: 1, MaxAttempts: 1},
	}
	brief.ApprovedVersion = brief.Version
	brief.ApprovedDigest = domain.TaskBriefDigest(brief)
	if !domain.IsTaskBriefApproved(brief) {
		t.Fatal("fixture brief must be approved")
	}
	profile := domain.DefaultProfile()
	profile.AllowedTools = []string{"read_file", "propose_patch", "run_command"}

	if err := ValidateTaskVerification(profile, &brief, nil); err != nil {
		t.Fatalf("approved synonym rejected at the gate: %v", err)
	}
	execution := copyExecutionBrief(&brief)
	if execution.Criteria[0].Tool != "run_command" {
		t.Fatalf("execution copy kept %q", execution.Criteria[0].Tool)
	}
	if brief.Criteria[0].Tool != "shell" || !domain.IsTaskBriefApproved(brief) {
		t.Fatal("resolving a synonym must not touch the signed brief")
	}
	tracker := newCompletionTracker(profile, "fix the parser", nil, execution)
	tracker.ObserveTool("run_command", json.RawMessage(`{"command":"go test ./..."}`), domain.ToolResult{OK: true, Output: json.RawMessage(`{"exitCode":0}`)}, 1)
	evidence := tracker.contractEvidence(1)
	if evidence == nil || len(evidence.Criteria) != 1 {
		t.Fatal("criterion evidence missing")
	}
	if evidence.Criteria[0].Status != "satisfied" {
		t.Fatalf("criterion status %q, check %+v", evidence.Criteria[0].Status, evidence.Criteria[0].Check)
	}
}

// Naming a tool is not enabling it: the resolved name is still checked against
// the profile, and the refusal says what this agent actually has.
func TestCriterionToolStaysSubjectToTheProfile(t *testing.T) {
	exit := 0
	brief := domain.TaskBrief{
		Version: 1, State: "discussion", Mode: domain.TaskModePrecise, Goal: "Fix the parser", ResultKind: "code",
		Criteria: []domain.AcceptanceCriterion{
			{ID: "c1", Text: "Tests pass", Kind: "verification", Tool: "shell", Arguments: json.RawMessage(`{"command":"go test ./..."}`), ExpectedExitCode: &exit},
		},
	}
	profile := domain.DefaultProfile()
	profile.AllowedTools = []string{"read_file", "search_code"}
	err := ValidateTaskVerification(profile, &brief, nil)
	if err == nil {
		t.Fatal("a criterion naming a tool the agent lacks must be refused")
	}
	if !strings.Contains(err.Error(), "shell") || !strings.Contains(err.Error(), "run_command") {
		t.Fatalf("refusal must name the requested synonym and the way out: %v", err)
	}
	profile.AllowedTools = []string{"read_file", "run_command"}
	profile.ToolPolicies = map[string]string{"run_command": "DENY"}
	if err = ValidateTaskVerification(profile, &brief, nil); err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("a DENY policy on the resolved tool must still refuse: %v", err)
	}
}
