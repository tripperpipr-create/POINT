package agent

import (
	"slices"
	"testing"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/policy"
)

// Q05: запрет одного инструмента не обходится другим — ни заданием, ни
// доверием, ни автоподтверждением в сильной песочнице.

func approvedProjectBriefForTest(t *testing.T, permissions domain.TaskPermissions) *domain.TaskBrief {
	t.Helper()
	brief, err := domain.ApproveTaskBrief(domain.NormalizeTaskBrief(domain.TaskBrief{
		Mode: domain.TaskModeProject, Goal: "Ship", ResultKind: "workspace_change",
		Criteria:    []domain.AcceptanceCriterion{{ID: "c1", Text: "Done", Kind: "manual"}},
		Permissions: permissions,
	}))
	if err != nil {
		t.Fatal(err)
	}
	return &brief
}

func TestDeniedRunCommandIsNotBypassedByAutoApprovedCustomTool(t *testing.T) {
	brief := approvedProjectBriefForTest(t, domain.TaskPermissions{WriteFiles: true, ExecuteCommands: true})
	profile := domain.AgentProfile{
		AllowedTools: []string{"run_command", "customtool_deploy"},
		ToolPolicies: map[string]string{"run_command": "DENY"},
	}
	restricted, err := RestrictTaskProfile(profile, brief)
	if err != nil {
		t.Fatal(err)
	}
	if !(policy.Engine{}).Evaluate(restricted, "run_command").Denied {
		t.Fatal("явный DENY на run_command снят заданием")
	}
	if TaskAutoApproves(brief, restricted, "customtool_deploy", true) {
		t.Fatal("при запрете run_command команда под другим именем исполняется без подтверждения")
	}
	if !(policy.Engine{}).Evaluate(restricted, "customtool_deploy").RequiresApproval {
		t.Fatal("самодельный инструмент исполняется без окна")
	}
	// Без запрета run_command право задания в сильной песочнице прежнее.
	profile.ToolPolicies = nil
	if !TaskAutoApproves(brief, profile, "customtool_deploy", true) || !TaskAutoApproves(brief, profile, "run_command", true) {
		t.Fatal("автоподтверждение задания в сильной песочнице пропало")
	}
	if TaskAutoApproves(brief, profile, "run_command", false) {
		t.Fatal("автоподтверждение без сильной песочницы")
	}
}

func TestTaskExcludesExternalWritesEvenWhenProfileAllows(t *testing.T) {
	brief := approvedProjectBriefForTest(t, domain.TaskPermissions{WriteFiles: true, ExecuteCommands: true})
	profile := domain.AgentProfile{
		AllowedTools: []string{"read_file", "db_exec", "ssh_exec_remote", "docker_control"},
		ToolPolicies: map[string]string{"db_exec": "ALLOW", "ssh_exec_remote": "ALLOW", "docker_control": "ALLOW"},
	}
	restricted, err := RestrictTaskProfile(profile, brief)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(restricted.AllowedTools, []string{"read_file"}) {
		t.Fatalf("внешняя запись осталась у задания: %v", restricted.AllowedTools)
	}
	for _, tool := range []string{"db_exec", "ssh_exec_remote", "docker_control"} {
		if TaskToolExclusion(tool, brief, nil) != TaskExcludesExternalWrite {
			t.Fatalf("%s: причина исключения %q", tool, TaskToolExclusion(tool, brief, nil))
		}
	}
}

func TestTaskWithoutCommandsExcludesEveryCommandTool(t *testing.T) {
	brief := approvedProjectBriefForTest(t, domain.TaskPermissions{WriteFiles: true})
	custom := []domain.CustomTool{{ID: "lint_project"}}
	profile := domain.AgentProfile{AllowedTools: []string{"read_file", "propose_patch", "run_command", "customtool_x", "lint_project"}}
	restricted, err := RestrictTaskProfile(profile, brief, custom)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(restricted.AllowedTools, []string{"read_file", "propose_patch"}) {
		t.Fatalf("задание без команд оставило командный инструмент: %v", restricted.AllowedTools)
	}
}

// Сеть задания: хост профиля вне брифа закрыт, общий network — DENY, явный
// DENY хоста бриф не открывает.
func TestTaskNetworkFreezesToBriefHosts(t *testing.T) {
	brief := approvedProjectBriefForTest(t, domain.TaskPermissions{NetworkHosts: []string{"registry.npmjs.org", "evil.example"}})
	profile := domain.AgentProfile{ToolPolicies: map[string]string{
		"network:github.com": "ALLOW", "network:evil.example": "DENY", "network": "ALLOW",
	}}
	restricted, err := RestrictTaskProfile(profile, brief)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"network": "DENY", "network:github.com": "DENY", "network:evil.example": "DENY", "network:registry.npmjs.org": "ALLOW"}
	for key, value := range want {
		if restricted.ToolPolicies[key] != value {
			t.Fatalf("%s=%q, ждали %q (%v)", key, restricted.ToolPolicies[key], value, restricted.ToolPolicies)
		}
	}
}
