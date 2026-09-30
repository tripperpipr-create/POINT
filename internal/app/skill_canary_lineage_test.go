package app

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

func saveCanaryOutcomes(t *testing.T, application *App, skill domain.SkillDefinition, workspaceID, agentID, prefix string, statuses ...domain.RunStatus) {
	t.Helper()
	attribution := domain.SkillDefinitionAttribution(skill)
	for index, status := range statuses {
		health := "healthy"
		if status != domain.RunCompleted {
			health = "failed"
		}
		outcome := canaryOutcome(attribution, status, health, time.Now().UTC().Add(time.Duration(index)*time.Minute))
		outcome.ID = fmt.Sprintf("%s-outcome-%d", prefix, index)
		outcome.RunID = fmt.Sprintf("%s-run-%d", prefix, index)
		outcome.WorkspaceID, outcome.ProjectAgentID = workspaceID, agentID
		if err := application.store.SaveSkillOutcome(context.Background(), outcome); err != nil {
			t.Fatal(err)
		}
	}
}

// Старая ревизия деградирует у агента, до которого новая не дошла. Откат
// линейки запрещён более новой ревизией; раньше это давало ошибку на каждом
// прогоне. Теперь старая ревизия снимается, новая остаётся.
func TestRegressedOlderRevisionIsUnequippedInsteadOfWedgingCanary(t *testing.T) {
	application := newTestApp(t)
	ctx := context.Background()
	view, err := application.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	older := domain.SkillDefinition{ID: "skill-learned-lineage", Name: "Lineage", Instructions: "Old way",
		Configuration: map[string]any{"revision": 1, "managedBy": agentLearningManagedBy, "promotionStatus": "project_only"}, CreatedAt: now, UpdatedAt: now}
	newer := domain.SkillDefinition{ID: "skill-learned-lineage-r2", Name: "Lineage", Instructions: "New way",
		Configuration: map[string]any{"revision": 2, "managedBy": agentLearningManagedBy, "promotionStatus": "project_only",
			"familyId": older.ID, "supersedesSkillId": older.ID}, CreatedAt: now, UpdatedAt: now}
	for _, skill := range []domain.SkillDefinition{older, newer} {
		if err = application.store.SaveSkill(ctx, skill); err != nil {
			t.Fatal(err)
		}
	}
	stale, err := application.SaveProjectAgent(domain.ProjectAgent{WorkspaceID: view.Workspace.ID, Name: "Stale", Provider: domain.ProviderOllama,
		BaseURL: "http://127.0.0.1:11434", PrimaryModel: "qwen2.5-coder:7b", SkillIDs: []string{older.ID}})
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := application.SaveProjectAgent(domain.ProjectAgent{WorkspaceID: view.Workspace.ID, Name: "Fresh", Provider: domain.ProviderOllama,
		BaseURL: "http://127.0.0.1:11434", PrimaryModel: "qwen2.5-coder:7b", SkillIDs: []string{newer.ID}})
	if err != nil {
		t.Fatal(err)
	}
	improvements := []domain.AgentImprovement{
		{ID: "improvement-lineage-1", WorkspaceID: view.Workspace.ID, ProjectAgentID: stale.ID, SourceRunID: "lineage-source-1",
			SkillID: older.ID, Kind: "skill_created", Status: "applied_unproven", PromotionStatus: "project_only",
			AfterSkill: &older, AfterSkillIDs: []string{older.ID}, CreatedAt: now.Add(-2 * time.Hour), UpdatedAt: now},
		{ID: "improvement-lineage-2", WorkspaceID: view.Workspace.ID, ProjectAgentID: fresh.ID, SourceRunID: "lineage-source-2",
			SkillID: newer.ID, Kind: "skill_updated", Status: "applied_unproven", PromotionStatus: "project_only",
			BeforeSkill: &older, AfterSkill: &newer, BeforeSkillIDs: []string{older.ID}, AfterSkillIDs: []string{newer.ID},
			CreatedAt: now.Add(-time.Hour), UpdatedAt: now},
	}
	for _, item := range improvements {
		if err = application.store.SaveAgentImprovement(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	saveCanaryOutcomes(t, application, older, view.Workspace.ID, stale.ID, "lineage", domain.RunFailed, domain.RunFailed, domain.RunFailed)
	if err = application.evaluateAppliedSkillCanary(ctx, older.ID); err != nil {
		t.Fatalf("canary wedged on an older revision: %v", err)
	}
	first, _ := application.store.GetAgentImprovement(ctx, "improvement-lineage-1")
	second, _ := application.store.GetAgentImprovement(ctx, "improvement-lineage-2")
	if first.Status != "rolled_back" || !improvementIsApplied(second.Status) {
		t.Fatalf("older=%s newer=%s", first.Status, second.Status)
	}
	staleAgent, _ := application.store.GetProjectAgent(ctx, stale.ID)
	freshAgent, _ := application.store.GetProjectAgent(ctx, fresh.ID)
	if slices.Contains(staleAgent.SkillIDs, older.ID) || !slices.Contains(freshAgent.SkillIDs, newer.ID) {
		t.Fatalf("stale=%#v fresh=%#v", staleAgent.SkillIDs, freshAgent.SkillIDs)
	}
}

// Доказанный навык без базы сравнения: два провала из трёх — наблюдение,
// три из трёх — откат.
func TestProvenSkillWithoutBaselineNeedsEveryRecentRunToFail(t *testing.T) {
	application := newTestApp(t)
	ctx := context.Background()
	view, err := application.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	skill := domain.SkillDefinition{ID: "skill-learned-proven", Name: "Proven", Instructions: "Proven way",
		Configuration: map[string]any{"revision": 1, "managedBy": agentLearningManagedBy, "promotionStatus": "project_only"}, CreatedAt: now, UpdatedAt: now}
	if err = application.store.SaveSkill(ctx, skill); err != nil {
		t.Fatal(err)
	}
	agent, err := application.SaveProjectAgent(domain.ProjectAgent{WorkspaceID: view.Workspace.ID, Name: "Agent", Provider: domain.ProviderOllama,
		BaseURL: "http://127.0.0.1:11434", PrimaryModel: "qwen2.5-coder:7b", SkillIDs: []string{skill.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if err = application.store.SaveAgentImprovement(ctx, domain.AgentImprovement{
		ID: "improvement-proven", WorkspaceID: view.Workspace.ID, ProjectAgentID: agent.ID, SourceRunID: "proven-source",
		SkillID: skill.ID, Kind: "skill_created", Status: "applied_proven", PromotionStatus: "project_only",
		AfterSkill: &skill, AfterSkillIDs: []string{skill.ID}, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	saveCanaryOutcomes(t, application, skill, view.Workspace.ID, agent.ID, "watch", domain.RunFailed, domain.RunCompleted, domain.RunFailed)
	if err = application.evaluateAppliedSkillCanary(ctx, skill.ID); err != nil {
		t.Fatal(err)
	}
	stored, _ := application.store.GetAgentImprovement(ctx, "improvement-proven")
	if stored.Status != "applied_proven" || stored.CanaryEvaluation == nil || stored.CanaryEvaluation.Status != "pending" {
		t.Fatalf("two of three failures rolled back a proven skill: status=%s canary=%#v", stored.Status, stored.CanaryEvaluation)
	}
	saveCanaryOutcomes(t, application, skill, view.Workspace.ID, agent.ID, "fail", domain.RunFailed, domain.RunFailed, domain.RunFailed)
	if err = application.evaluateAppliedSkillCanary(ctx, skill.ID); err != nil {
		t.Fatal(err)
	}
	stored, _ = application.store.GetAgentImprovement(ctx, "improvement-proven")
	if stored.Status != "rolled_back" {
		t.Fatalf("three of three failures did not roll back: %s", stored.Status)
	}
}
