package app

import (
	"context"
	"testing"
	"time"

	"local-agent-workbench/internal/agent"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/policy"
)

// Q05: расчёт действующих прав — те же функции, что у исполнения. Если он
// разойдётся с движком, человек увидит одно, а исполнится другое.

func effectiveByTool(t *testing.T, permissions EffectivePermissions) map[string]EffectiveToolPermission {
	t.Helper()
	byTool := map[string]EffectiveToolPermission{}
	for _, entry := range permissions.Tools {
		byTool[entry.Tool] = entry
	}
	return byTool
}

func TestEffectivePermissionsNameEffectAndSourceUnderTask(t *testing.T) {
	brief, err := domain.ApproveTaskBrief(domain.NormalizeTaskBrief(domain.TaskBrief{
		Mode: domain.TaskModeProject, Goal: "Ship", ResultKind: "workspace_change",
		Criteria:    []domain.AcceptanceCriterion{{ID: "c1", Text: "Done", Kind: "manual"}},
		Permissions: domain.TaskPermissions{WriteFiles: true, ExecuteCommands: true, NetworkHosts: []string{"registry.npmjs.org"}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	profile := domain.AgentProfile{
		AllowedTools: []string{"read_file", "propose_patch", "run_command", "customtool_deploy", "ssh_exec_remote"},
		ToolPolicies: map[string]string{"run_command": "DENY", "network:github.com": "ALLOW", "ssh_exec_remote": "ALLOW"},
	}
	got, err := effectivePermissionsFor(profile, &brief, nil, true, policy.Engine{}, "ws", "agent", "quest")
	if err != nil {
		t.Fatal(err)
	}
	byTool := effectiveByTool(t, got)
	checks := []struct {
		tool, effect string
		source       policy.DecisionSource
		auto         bool
	}{
		{"read_file", EffectAllow, policy.SourceCatalogDefault, false},
		// Риск HIGH даёт ASK по умолчанию каталога; окно снимает задание.
		{"propose_patch", EffectAllow, policy.SourceCatalogDefault, true},
		{"run_command", EffectDeny, policy.SourceProfileExplicit, false},
		// Запрет run_command не обходится командой под другим именем.
		{"customtool_deploy", EffectAsk, policy.SourceCatalogDefault, false},
		{"ssh_exec_remote", EffectExcludedByJob, "", false},
		{"db_exec", EffectNotGranted, "", false},
	}
	for _, check := range checks {
		entry, ok := byTool[check.tool]
		if !ok || entry.Effect != check.effect || entry.Source != check.source || entry.AutoApproved != check.auto {
			t.Fatalf("%s: %#v, ждали effect=%s source=%s auto=%v", check.tool, entry, check.effect, check.source, check.auto)
		}
	}
	if byTool["ssh_exec_remote"].ExcludedBy != agent.TaskExcludesExternalWrite {
		t.Fatalf("причина исключения: %#v", byTool["ssh_exec_remote"])
	}
	if !got.TaskBriefApplied || got.Network.Source != "task_brief" || got.Network.Mode != "ALLOWLIST" ||
		len(got.Network.Allow) != 1 || got.Network.Allow[0] != "registry.npmjs.org:443/tls" {
		t.Fatalf("сеть задания: %#v", got.Network)
	}
	if len(got.Network.Deny) != 1 || got.Network.Deny[0] != "github.com" {
		t.Fatalf("хост профиля вне брифа не закрыт: %#v", got.Network)
	}
}

// Показанное совпадает с тем, что решит движок на вызове, для разных профилей
// и песочниц.
func TestEffectivePermissionsAgreeWithExecutionDecision(t *testing.T) {
	brief, err := domain.ApproveTaskBrief(domain.NormalizeTaskBrief(domain.TaskBrief{
		Mode: domain.TaskModeProject, Goal: "Ship", ResultKind: "workspace_change",
		Criteria:    []domain.AcceptanceCriterion{{ID: "c1", Text: "Done", Kind: "manual"}},
		Permissions: domain.TaskPermissions{WriteFiles: true, ExecuteCommands: true},
	}))
	if err != nil {
		t.Fatal(err)
	}
	profiles := []domain.AgentProfile{
		{AllowedTools: []string{"read_file", "propose_patch", "run_command", "customtool_x", "git_diff"}},
		{AllowedTools: []string{"read_file", "propose_patch", "run_command"}, ApprovalMode: domain.ApprovalAlways},
		{AllowedTools: []string{"read_file", "run_command", "customtool_x"}, ToolPolicies: map[string]string{"read_file": "ASK", "run_command": "DENY"}},
	}
	for index, profile := range profiles {
		for _, strong := range []bool{true, false} {
			for _, withBrief := range []bool{true, false} {
				var task *domain.TaskBrief
				if withBrief {
					task = &brief
				}
				got, err := effectivePermissionsFor(profile, task, nil, strong, policy.Engine{}, "ws", "agent", "")
				if err != nil {
					t.Fatal(err)
				}
				restricted, err := agent.RestrictTaskProfile(profile, task)
				if err != nil {
					t.Fatal(err)
				}
				for _, entry := range got.Tools {
					if !policy.ProfileGrants(restricted).Allows(entry.Tool) {
						if entry.Effect == EffectAllow || entry.Effect == EffectAsk || entry.Effect == EffectDeny {
							t.Fatalf("профиль %d: недоступный движку %s показан как %s", index, entry.Tool, entry.Effect)
						}
						continue
					}
					decision := (policy.Engine{}).Evaluate(restricted, entry.Tool)
					auto := agent.TaskAutoApproves(task, restricted, entry.Tool, strong)
					want := EffectAllow
					switch {
					case decision.Denied:
						want = EffectDeny
					case decision.RequiresApproval && !auto:
						want = EffectAsk
					}
					if entry.Effect != want {
						t.Fatalf("профиль %d strong=%v brief=%v: %s показан %s, движок решит %s", index, strong, withBrief, entry.Tool, entry.Effect, want)
					}
				}
			}
		}
	}
}

func TestEffectivePermissionsRefuseAgentOfAnotherWorld(t *testing.T) {
	application, world := outcomeWorld(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := application.store.SaveProjectAgent(ctx, domain.ProjectAgent{
		ID: "agent-foreign", WorkspaceID: "ws-foreign", Name: "Foreign", Provider: "ollama", PrimaryModel: "qwen",
		AllowedTools: []string{"read_file"}, MaxSteps: 8, MaxDurationSeconds: 60, MaxOutputTokens: 1024, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := application.EffectivePermissions(ctx, "agent-foreign", ""); err == nil {
		t.Fatal("права агента чужого проекта выданы")
	}
	if err := application.store.SaveProjectAgent(ctx, domain.ProjectAgent{
		ID: "agent-own", WorkspaceID: world.ID, Name: "Own", Provider: "ollama", PrimaryModel: "qwen",
		AllowedTools: []string{"read_file"}, MaxSteps: 8, MaxDurationSeconds: 60, MaxOutputTokens: 1024, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := application.store.SaveQuest(ctx, domain.Quest{ID: "quest-foreign", WorkspaceID: "ws-foreign", Title: "x", Status: domain.QuestActive, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := application.EffectivePermissions(ctx, "agent-own", "quest-foreign"); err == nil {
		t.Fatal("задание чужого проекта применено")
	}
	got, err := application.EffectivePermissions(ctx, "agent-own", "")
	if err != nil {
		t.Fatal(err)
	}
	if entry := effectiveByTool(t, got)["read_file"]; entry.Effect != EffectAllow || got.Network.Mode != "DENY" {
		t.Fatalf("права своего агента: %#v сеть=%#v", entry, got.Network)
	}
}
