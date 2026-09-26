package app

import (
	"context"
	"strings"
	"testing"

	"local-agent-workbench/internal/diagnostics"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
)

// fmt.Sprint(nil) is "<nil>": every learned Skill without a familyId used to
// share that family, so any newer improvement blocked the rollback of any
// older regressed one and the canary stayed wedged on every run.
func TestUnrelatedLearnedSkillsDoNotShareAFamily(t *testing.T) {
	older := domain.AgentImprovement{AfterSkill: &domain.SkillDefinition{ID: "skill-learned-older", Configuration: map[string]any{}}}
	newer := domain.AgentImprovement{AfterSkill: &domain.SkillDefinition{ID: "skill-learned-newer", Configuration: map[string]any{}}}
	if skillImprovementFamily(older) == skillImprovementFamily(newer) {
		t.Fatalf("unrelated Skills share family %q", skillImprovementFamily(older))
	}
	// A revision stored while the bug was live carries the literal "<nil>";
	// its predecessor link still ties it to the right lineage.
	revision := domain.AgentImprovement{AfterSkill: &domain.SkillDefinition{ID: "skill-learned-older-r2", Configuration: map[string]any{
		"familyId": "<nil>", "supersedesSkillId": "skill-learned-older",
	}}}
	if got := skillImprovementFamily(revision); got != skillImprovementFamily(older) {
		t.Fatalf("revision family = %q, want lineage of %q", got, skillImprovementFamily(older))
	}
}

func TestLearnedSkillsAreCappedOldestFirst(t *testing.T) {
	ids := []string{"base-skill", "skill-learned-1", "skill-learned-2", "skill-learned-3", "skill-learned-4", "skill-learned-5", "skill-learned-6", "skill-learned-new"}
	got := capLearnedSkillIDs(ids, "skill-learned-new", 5)
	want := "base-skill,skill-learned-3,skill-learned-4,skill-learned-5,skill-learned-6,skill-learned-new"
	if strings.Join(got, ",") != want {
		t.Fatalf("capped = %v, want %s", got, want)
	}
	if kept := capLearnedSkillIDs([]string{"skill-learned-new", "skill-learned-a"}, "skill-learned-new", 1); strings.Join(kept, ",") != "skill-learned-new" {
		t.Fatalf("the Skill just learned must survive the cap: %v", kept)
	}
}

// One failure category keeps one recovery Skill: a new verification gap
// revises the equipped verification-gap Skill instead of adding the 19th.
func TestFailureRevisesEquippedRecoverySkillOfSameCategory(t *testing.T) {
	gap := diagnostics.RunDiagnostics{}
	gap.Verification.Required = true
	if category := learningFailureCategory(gap); category != "verification_gap" {
		t.Fatalf("category = %q", category)
	}
	learned := func(id, category string) domain.SkillDefinition {
		return domain.SkillDefinition{ID: id, Configuration: map[string]any{"managedBy": agentLearningManagedBy, "failureCategory": category, "promotionStatus": "project_only"}}
	}
	skills := []domain.SkillDefinition{learned("skill-learned-tools", "tool_failure"), learned("skill-learned-gap", "verification_gap")}
	agent := domain.ProjectAgent{SkillIDs: []string{"skill-learned-gap", "skill-learned-tools"}}
	previous := equippedRecoverySkill(skills, agent, "verification_gap")
	if previous == nil || previous.ID != "skill-learned-gap" {
		t.Fatalf("previous = %#v", previous)
	}
	if other := equippedRecoverySkill(skills, domain.ProjectAgent{SkillIDs: []string{"skill-learned-tools"}}, "verification_gap"); other != nil {
		t.Fatalf("a Skill of another category was reused: %#v", other)
	}
}

type masterLearningFreeModel struct{ called bool }

func (m *masterLearningFreeModel) Stream(_ context.Context, _ providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.called = true
	if err := emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "ok"}); err != nil {
		return err
	}
	return emit(providers.ModelEvent{Kind: providers.EventUsage, InputTokens: 900, OutputTokens: 100})
}

// llmux is free by the owner's decision: its learning is recorded but never
// deferred on "недостаточно бюджета обучения", which guards money only.
func TestMasterLearningOnFreeRuntimeIgnoresTheMoneyCeiling(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	cfg := domain.OrchestratorConfig{Provider: domain.ProviderOpenAI, ProviderPreset: "llmux", Model: "Qwen3.8-27B"}
	model := &masterLearningFreeModel{}
	output, tokens, err := a.masterLearningCall(ctx, model, cfg, "a", providers.ModelRequest{MaxOutputTokens: 20})
	if err != nil || !model.called || output != "ok" || tokens != 1000 {
		t.Fatalf("free learning call: output=%q tokens=%d called=%v err=%v", output, tokens, model.called, err)
	}
	budget, err := a.store.MasterLearningBudget(ctx, "a")
	if err != nil || budget.SpentTokens != 1000 || budget.ReservedTokens != 0 {
		t.Fatalf("free spend must stay visible without a reservation: %+v err=%v", budget, err)
	}
}
