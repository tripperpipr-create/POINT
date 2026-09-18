package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTaskBriefApprovalBindsEveryMaterialField(t *testing.T) {
	draft := NormalizeTaskBrief(TaskBrief{Mode: TaskModePrecise, State: "ready", Goal: "Write a function", ResultKind: "code", Criteria: []AcceptanceCriterion{{ID: "c1", Text: "Function follows contract", Kind: "manual"}}})
	approved, err := ApproveTaskBrief(draft)
	if err != nil {
		t.Fatal(err)
	}
	if !IsTaskBriefApproved(approved) {
		t.Fatal("approval invalid")
	}
	mutations := map[string]func(*TaskBrief){
		"goal":       func(b *TaskBrief) { b.Goal = "Another goal" },
		"scope":      func(b *TaskBrief) { b.Scope = []string{"New feature"} },
		"budget":     func(b *TaskBrief) { b.Budget.Tokens++ },
		"permission": func(b *TaskBrief) { b.Permissions.ExecuteCommands = true },
		"mode":       func(b *TaskBrief) { b.Mode = TaskModeProject },
		"version":    func(b *TaskBrief) { b.Version++ },
		"criterion": func(b *TaskBrief) {
			b.Criteria = []AcceptanceCriterion{{ID: "c2", Text: "Easier result", Kind: "manual"}}
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			b := approved
			mutate(&b)
			if IsTaskBriefApproved(b) {
				t.Fatal("changed contract retained approval")
			}
		})
	}
	if IsTaskBriefApproved(NormalizeTaskBrief(approved)) {
		t.Fatal("untrusted input retained approval")
	}
}

func TestTaskBriefUnknownsAndReportWritesBlockApproval(t *testing.T) {
	b := NormalizeTaskBrief(TaskBrief{Mode: TaskModeProject, Goal: "Find bugs", ResultKind: "report", Criteria: []AcceptanceCriterion{{ID: "c1", Text: "Report with reproductions", Kind: "manual"}}})
	b.OpenQuestions = []string{"Which project?"}
	if _, err := ApproveTaskBrief(b); err == nil {
		t.Fatal("open question approved")
	}
	b.OpenQuestions = nil
	b.Permissions.WriteFiles = true
	if _, err := ApproveTaskBrief(b); err == nil {
		t.Fatal("report obtained source write authority")
	}
	b.Permissions.WriteFiles = false
	if _, err := ApproveTaskBrief(b); err != nil {
		t.Fatal(err)
	}
}

func TestPreciseTaskMayAuthorizeOneTemporarySubagent(t *testing.T) {
	b := NormalizeTaskBrief(TaskBrief{
		Mode: TaskModePrecise, State: "ready", Goal: "Implement and verify endpoint", ResultKind: "workspace_change",
		Criteria:    []AcceptanceCriterion{{ID: "c1", Text: "Endpoint works", Kind: "manual"}},
		Permissions: TaskPermissions{WriteFiles: true, ProvisionProjectAgents: true},
	})
	if b.Budget.MaxParallel != 1 || b.Budget.MaxProjectAgents != 1 {
		t.Fatalf("precise subagent bounds not normalized: %#v", b.Budget)
	}
	if err := ValidateTaskBrief(b); err != nil {
		t.Fatal(err)
	}
}

func TestTaskBriefValidationCollectsAllIndependentIssues(t *testing.T) {
	b := NormalizeTaskBrief(TaskBrief{
		Mode:       TaskModeProject,
		State:      "ready",
		Goal:       "Build service",
		ResultKind: "report",
		Permissions: TaskPermissions{
			WriteFiles:   true,
			NetworkHosts: []string{"*.example.com"},
		},
		Criteria: []AcceptanceCriterion{
			{ID: "c1", Text: "Health check passes", Kind: "verification"},
			{ID: "c2", Text: "Failure is reproduced", Kind: "reproduction", Tool: "execute_command", Arguments: json.RawMessage(`{"command":"false"}`)},
			{ID: "c3", Text: "Review output", Kind: "manual", Tool: "read_file"},
		},
		Decisions: []BriefDecision{{Topic: "stack"}},
	})
	issues := ValidateTaskBriefIssues(b)
	if len(issues) != 6 {
		t.Fatalf("expected all six defects, got %#v", issues)
	}
	err := ValidateTaskBrief(b)
	for _, want := range []string{
		"only a workspace_change task",
		"network destination",
		`criterion "c1" requires`,
		`reproduction criterion "c2"`,
		`manual criterion "c3"`,
		"decisions require",
	} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("aggregate error misses %q: %v", want, err)
		}
	}
}
