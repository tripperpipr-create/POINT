package agent

import (
	"encoding/json"
	"local-agent-workbench/internal/domain"
	"slices"
	"testing"
)

func TestCompletionCriteriaSeparateReproductionDiagnosticsAndRegressions(t *testing.T) {
	p := domain.DefaultProfile()
	p.AllowedTools = []string{"run_command"}
	expected := 1
	b := domain.TaskBrief{Version: 1, Criteria: []domain.AcceptanceCriterion{{ID: "repro", Text: "Reproduce error", Kind: "reproduction", Tool: "run_command", Arguments: commandArguments(t, "python reproduce.py"), ExpectedExitCode: &expected}}}
	tr := newCompletionTracker(p, "Find bugs", nil, &b)
	tr.ObserveTool("run_command", commandArguments(t, "python reproduce.py"), commandResult(t, 1, false), 0)
	if got := tr.Missing(0, nil); len(got) != 0 {
		t.Fatal(got)
	}
	if tr.Evidence(0).Status != "verified" {
		t.Fatal(tr.Evidence(0))
	}
	tr.ObserveTool("run_command", commandArguments(t, "go test ./legacy"), commandResult(t, 1, false), 0)
	if got := tr.Missing(0, nil); len(got) != 0 {
		t.Fatal("diagnostic became mandatory", got)
	}
	if tr.Evidence(0).Status != "needs_review" {
		t.Fatal("diagnostic hidden")
	}
	tr.ObserveTool("run_command", commandArguments(t, "go test ./current"), commandResult(t, 0, false), 0)
	tr.ObserveTool("run_command", commandArguments(t, "go test ./current"), commandResult(t, 1, false), 0)
	if len(tr.Missing(0, nil)) == 0 || tr.Evidence(0).Status != "blocked" {
		t.Fatal("regression hidden")
	}
}
func TestCompletionCheckIdentityAndLatestRevision(t *testing.T) {
	p := domain.DefaultProfile()
	p.AllowedTools = []string{"run_command"}
	b := domain.TaskBrief{Version: 2, Criteria: []domain.AcceptanceCriterion{{ID: "check", Text: "Tests", Kind: "verification", Tool: "run_command", Arguments: json.RawMessage(`{"command":"go test ./...","cwd":"sub"}`)}}}
	tr := newCompletionTracker(p, "Tests", nil, &b)
	tr.ObserveTool("run_command", json.RawMessage(`{"command":"go test ./...","cwd":"other"}`), commandResult(t, 0, false), 0)
	if tr.Evidence(0).Status != "blocked" {
		t.Fatal("wrong cwd accepted")
	}
	args := json.RawMessage(`{"reason":"changed explanation","timeoutSeconds":20,"cwd":"sub/.","command":"go test ./..."}`)
	tr.ObserveTool("run_command", args, commandResult(t, 0, false), 0)
	if tr.Evidence(0).Status != "verified" {
		t.Fatal(tr.Evidence(0))
	}
	if tr.Evidence(1).Status != "blocked" {
		t.Fatal("stale evidence accepted")
	}
	tr.ObserveTool("run_command", args, commandResult(t, 0, true), 0)
	if tr.Evidence(0).Status != "blocked" {
		t.Fatal("timeout accepted")
	}
}
func TestTaskAuthorityNeverWidensProfile(t *testing.T) {
	b, err := domain.ApproveTaskBrief(domain.NormalizeTaskBrief(domain.TaskBrief{Mode: domain.TaskModeProject, ResultKind: "report", Goal: "Audit", Criteria: []domain.AcceptanceCriterion{{ID: "report", Kind: "manual", Text: "Report"}}, Permissions: domain.TaskPermissions{ExecuteCommands: true, NetworkHosts: []string{"allowed.test"}}}))
	if err != nil {
		t.Fatal(err)
	}
	p := domain.DefaultProfile()
	p.AllowedTools = []string{"read_file", "run_command", "propose_patch", "db_exec"}
	p.ToolPolicies = map[string]string{"run_command": "ASK", "network:other.test": "ALLOW", "network:denied.test": "DENY", "network:allowed.test": "ALLOW"}
	narrowed, err := RestrictTaskProfile(p, &b)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(narrowed.AllowedTools, "propose_patch") || slices.Contains(narrowed.AllowedTools, "db_exec") {
		t.Fatal("report retained write tools")
	}
	if narrowed.ToolPolicies["run_command"] != "ASK" || narrowed.ToolPolicies["network:denied.test"] != "DENY" || narrowed.ToolPolicies["network:other.test"] != "DENY" {
		t.Fatal(narrowed.ToolPolicies)
	}
	if narrowed.ToolPolicies["network:allowed.test"] != "ALLOW" {
		t.Fatalf("brief host not frozen as ALLOW: %#v", narrowed.ToolPolicies)
	}
	if p.ToolPolicies["network:other.test"] != "ALLOW" || len(p.AllowedTools) != 4 {
		t.Fatal("source profile mutated")
	}
}

func TestTaskAuthorityBlocksCustomIDsAndDoesNotInventManualChecks(t *testing.T) {
	b, err := domain.ApproveTaskBrief(domain.NormalizeTaskBrief(domain.TaskBrief{Mode: domain.TaskModePrecise, Goal: "Edit prose", ResultKind: "workspace_change", Permissions: domain.TaskPermissions{WriteFiles: true}, Criteria: []domain.AcceptanceCriterion{{ID: "c", Kind: "manual", Text: "Agreed prose"}}}))
	if err != nil {
		t.Fatal(err)
	}
	profile := domain.DefaultProfile()
	profile.AllowedTools = []string{"run_command", "my-legacy-command", "propose_patch"}
	narrowed, err := RestrictTaskProfile(profile, &b, []domain.CustomTool{{ID: "my-legacy-command", Kind: domain.CustomToolCommand}})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(narrowed.AllowedTools, "my-legacy-command") || slices.Contains(narrowed.AllowedTools, "run_command") {
		t.Fatal("command permission bypass")
	}
	tracker := newCompletionTracker(profile, "Edit prose", nil, &b)
	if got := tracker.Missing(1, []string{"README.md"}); len(got) != 0 {
		t.Fatal("extra mandatory command invented", got)
	}
}
