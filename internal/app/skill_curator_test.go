package app

import (
	"context"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

func TestDeprecatedSkillStaysHistoricalButCannotBeNewlyAssigned(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	view, err := application.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	skill, err := application.SaveSkill(domain.SkillDefinition{Name: "Legacy API", Instructions: "Inspect legacy API.", RequiredTools: []string{"read_file"}})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := application.SaveProjectAgent(domain.ProjectAgent{Name: "Existing", WorkspaceID: view.Workspace.ID, Provider: domain.ProviderOllama, BaseURL: "http://127.0.0.1:11434", PrimaryModel: "qwen2.5-coder:7b", SkillIDs: []string{skill.ID}, AllowedTools: []string{"read_file"}})
	if err != nil {
		t.Fatal(err)
	}
	skill.Configuration["lifecycleStatus"] = "deprecated"
	if _, err = application.SaveSkill(skill); err != nil {
		t.Fatal(err)
	}
	agent.RoleDescription = "Edited after deprecation"
	if _, err = application.SaveProjectAgent(agent); err != nil {
		t.Fatalf("existing historical assignment was broken: %v", err)
	}
	if _, err = application.SaveProjectAgent(domain.ProjectAgent{Name: "New", WorkspaceID: view.Workspace.ID, Provider: domain.ProviderOllama, BaseURL: "http://127.0.0.1:11434", PrimaryModel: "qwen2.5-coder:7b", SkillIDs: []string{skill.ID}, AllowedTools: []string{"read_file"}}); err == nil {
		t.Fatal("deprecated skill was newly assigned")
	}
	if _, err = application.EquipSkill(view.Workspace.ID, skill.ID, nil); err == nil {
		t.Fatal("deprecated skill was newly equipped")
	}
}

func TestSkillCuratorFindsDuplicateAndKeepsAttachedPrimary(t *testing.T) {
	now := time.Now().UTC()
	skills := []domain.SkillDefinition{
		{ID: "skill-old", Name: "API verification", Description: "Inspect API contracts and verify behavior", Instructions: "Inspect API contracts, change the bounded implementation, then run verification.", RequiredTools: []string{"read_file", "run_command"}, CreatedAt: now.Add(-100 * 24 * time.Hour)},
		{ID: "skill-used", Name: "API verification", Description: "Inspect API contracts and verify behavior", Instructions: "Inspect API contracts, change the bounded implementation, then run verification.", RequiredTools: []string{"read_file", "run_command"}, CreatedAt: now.Add(-20 * 24 * time.Hour)},
	}
	suggestions := buildSkillCuration(skills, []domain.ProjectAgent{{SkillIDs: []string{"skill-used"}}}, nil, nil, nil, now)
	var item domain.SkillCurationSuggestion
	for _, suggestion := range suggestions {
		if suggestion.Kind == "duplicate" {
			item = suggestion
		}
	}
	if item.Kind != "duplicate" || item.Action != "merge" || item.PrimarySkillID != "skill-used" || len(item.RelatedSkillIDs) != 1 || item.RelatedSkillIDs[0] != "skill-old" || item.Confidence < 0.9 {
		t.Fatalf("duplicate suggestion=%#v", item)
	}
}

func TestSkillCuratorFlagsDivergentSameNameWithoutResolvingIt(t *testing.T) {
	now := time.Now().UTC()
	skills := []domain.SkillDefinition{
		{ID: "skill-read", Name: "Deploy Guard", Instructions: "Inspect deployment configuration and verify the release plan.", RequiredTools: []string{"read_file"}, CreatedAt: now},
		{ID: "skill-ship", Name: "Deploy Guard", Instructions: "Publish artifacts and restart all remote production services.", RequiredTools: []string{"run_command", "server_execute"}, CreatedAt: now},
	}
	suggestions := buildSkillCuration(skills, nil, nil, nil, nil, now)
	if len(suggestions) != 1 || suggestions[0].Kind != "conflict" || suggestions[0].Action != "review" {
		t.Fatalf("conflict suggestion=%#v", suggestions)
	}
	if len(suggestions[0].Evidence) < 3 {
		t.Fatalf("conflict lacks evidence=%#v", suggestions[0])
	}
}

func TestSkillCuratorSuggestsOnlyUnboundStaleSkills(t *testing.T) {
	now := time.Now().UTC()
	old := now.Add(-200 * 24 * time.Hour)
	skills := []domain.SkillDefinition{
		{ID: "skill-stale", Name: "Old workflow", Instructions: "Inspect old workflow.", CreatedAt: old},
		{ID: "skill-attached", Name: "Attached workflow", Instructions: "Inspect attached workflow.", CreatedAt: old},
		{ID: "skill-recently-used", Name: "Observed workflow", Instructions: "Inspect observed workflow.", CreatedAt: old},
		{ID: "skill-deprecated", Name: "Deprecated workflow", Instructions: "Inspect deprecated workflow.", Configuration: map[string]any{"lifecycleStatus": "deprecated"}, CreatedAt: old},
	}
	outcomes := []domain.SkillOutcome{{SkillID: "skill-recently-used", RunID: "run-recent", CreatedAt: now.Add(-time.Hour)}}
	suggestions := buildSkillCuration(skills, nil, []domain.AgentBlueprint{{SkillIDs: []string{"skill-attached"}}}, nil, outcomes, now)
	if len(suggestions) != 1 || suggestions[0].Kind != "stale" || suggestions[0].Action != "deprecate" || suggestions[0].PrimarySkillID != "skill-stale" {
		t.Fatalf("stale suggestions=%#v", suggestions)
	}
}

func TestSkillCuratorTurnsRepeatedExactVersionFailuresIntoReview(t *testing.T) {
	now := time.Now().UTC()
	skills := []domain.SkillDefinition{{ID: "skill-api", Name: "API workflow", Instructions: "Inspect and verify.", CreatedAt: now}}
	outcomes := []domain.SkillOutcome{
		{SkillID: "skill-api", SkillName: "API workflow", SkillDigest: "digest-bad-version", RunID: "run-1", RunStatus: domain.RunFailed, Health: "failed", ToolCalls: 4, ToolFailures: 2, VerificationRequired: true, CreatedAt: now.Add(-3 * time.Hour)},
		{SkillID: "skill-api", SkillName: "API workflow", SkillDigest: "digest-bad-version", RunID: "run-2", RunStatus: domain.RunCompleted, Health: "attention", ToolCalls: 3, FeedbackCount: 1, CompletionRevisions: 1, VerificationRequired: true, CreatedAt: now.Add(-2 * time.Hour)},
		{SkillID: "skill-api", SkillName: "API workflow", SkillDigest: "digest-bad-version", RunID: "run-3", RunStatus: domain.RunCompleted, Health: "healthy", ToolCalls: 3, VerificationRequired: true, VerificationRecorded: true, CreatedAt: now.Add(-time.Hour)},
	}
	suggestions := buildSkillCuration(skills, nil, nil, nil, outcomes, now)
	if len(suggestions) != 1 {
		t.Fatalf("regression suggestions=%#v", suggestions)
	}
	item := suggestions[0]
	if item.Kind != "regression" || item.Action != "review" || item.PrimarySkillID != "skill-api" || item.Confidence < 0.6 || len(item.Evidence) != 4 {
		t.Fatalf("regression suggestion=%#v", item)
	}
}

func TestSkillCuratorIgnoresSingleNoisyExactVersionRun(t *testing.T) {
	now := time.Now().UTC()
	skills := []domain.SkillDefinition{{ID: "skill-api", Name: "API workflow", Instructions: "Inspect and verify.", CreatedAt: now}}
	outcomes := []domain.SkillOutcome{{SkillID: "skill-api", SkillDigest: "digest", RunID: "run-1", RunStatus: domain.RunFailed, Health: "failed", ToolCalls: 2, ToolFailures: 2, CreatedAt: now}}
	if suggestions := buildSkillCuration(skills, nil, nil, nil, outcomes, now); len(suggestions) != 0 {
		t.Fatalf("single noisy run created curator finding: %#v", suggestions)
	}
}
