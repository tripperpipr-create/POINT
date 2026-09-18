package app

import (
	"context"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

func TestSkillCanaryWaitsForMinimumExactCandidateSample(t *testing.T) {
	now := time.Now().UTC()
	candidate := domain.SkillAttribution{SkillID: "skill-a", Revision: 2, Digest: "candidate"}
	outcomes := []domain.SkillOutcome{
		canaryOutcome(candidate, domain.RunCompleted, "healthy", now),
		canaryOutcome(candidate, domain.RunCompleted, "healthy", now.Add(-time.Minute)),
		canaryOutcome(domain.SkillAttribution{SkillID: "skill-a", Revision: 2, Digest: "wrong"}, domain.RunFailed, "failed", now.Add(-2*time.Minute)),
	}
	evaluation := evaluateSkillCanary(candidate, nil, outcomes, now)
	if evaluation.Status != "pending" || evaluation.CandidateMetrics.Runs != 2 || len(evaluation.Reasons) != 1 {
		t.Fatalf("evaluation=%#v", evaluation)
	}
}

func TestSkillCanaryReportsTransparentBeforeAfterRegression(t *testing.T) {
	now := time.Now().UTC()
	baseline := domain.SkillAttribution{SkillID: "skill-a", Revision: 1, Digest: "baseline"}
	candidate := domain.SkillAttribution{SkillID: "skill-a", Revision: 2, Digest: "candidate"}
	var outcomes []domain.SkillOutcome
	for index := 0; index < 3; index++ {
		base := canaryOutcome(baseline, domain.RunCompleted, "healthy", now.Add(time.Duration(-index-10)*time.Minute))
		base.ToolCalls = 4
		base.VerificationRequired, base.VerificationRecorded = true, true
		outcomes = append(outcomes, base)
	}
	for index := 0; index < 3; index++ {
		status, health := domain.RunFailed, "failed"
		if index == 0 {
			status, health = domain.RunCompleted, "healthy"
		}
		item := canaryOutcome(candidate, status, health, now.Add(time.Duration(-index)*time.Minute))
		item.ToolCalls, item.ToolFailures = 4, 2
		item.VerificationRequired = true
		outcomes = append(outcomes, item)
	}
	evaluation := evaluateSkillCanary(candidate, &baseline, outcomes, now)
	if evaluation.Status != "regressed" || evaluation.CandidateMetrics.Completed != 1 || evaluation.BaselineMetrics == nil || evaluation.BaselineMetrics.Completed != 3 || len(evaluation.Reasons) < 3 {
		t.Fatalf("evaluation=%#v", evaluation)
	}
}

func TestSkillCanaryAcceptsNewSkillWithoutOpaqueScore(t *testing.T) {
	now := time.Now().UTC()
	candidate := domain.SkillAttribution{SkillID: "skill-new", Revision: 1, Digest: "candidate"}
	outcomes := []domain.SkillOutcome{
		canaryOutcome(candidate, domain.RunCompleted, "healthy", now),
		canaryOutcome(candidate, domain.RunCompleted, "healthy", now.Add(-time.Minute)),
		canaryOutcome(candidate, domain.RunCompleted, "healthy", now.Add(-2*time.Minute)),
	}
	evaluation := evaluateSkillCanary(candidate, nil, outcomes, now)
	if evaluation.Status != "healthy" || evaluation.CandidateMetrics.CompletionRate != 1 || evaluation.CandidateMetrics.HealthyRate != 1 {
		t.Fatalf("evaluation=%#v", evaluation)
	}
}

func TestProvenSkillCanaryRegressionAutomaticallyRollsBackLatestRevision(t *testing.T) {
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
	now := time.Now().UTC()
	before := domain.SkillDefinition{ID: "skill-canary", Name: "Canary", Instructions: "Stable", Configuration: map[string]any{"revision": 1}, CreatedAt: now, UpdatedAt: now}
	after := before
	after.Instructions = "Regressed"
	after.Configuration = map[string]any{"revision": 2, "promotionStatus": "candidate"}
	if err = application.store.SaveSkill(context.Background(), after); err != nil {
		t.Fatal(err)
	}
	agent, err := application.SaveProjectAgent(domain.ProjectAgent{WorkspaceID: view.Workspace.ID, Name: "Agent", Provider: domain.ProviderOllama, BaseURL: "http://127.0.0.1:11434", PrimaryModel: "qwen2.5-coder:7b", SkillIDs: []string{after.ID}})
	if err != nil {
		t.Fatal(err)
	}
	improvement := domain.AgentImprovement{
		ID: "improvement-canary", WorkspaceID: view.Workspace.ID, ProjectAgentID: agent.ID, SourceRunID: "source-run",
		SkillID: after.ID, Kind: "skill_updated", Status: "applied", PromotionStatus: "candidate",
		BeforeSkill: &before, AfterSkill: &after, BeforeSkillIDs: []string{after.ID}, AfterSkillIDs: []string{after.ID},
		CreatedAt: now, UpdatedAt: now,
	}
	if err = application.store.SaveAgentImprovement(context.Background(), improvement); err != nil {
		t.Fatal(err)
	}
	attribution := domain.SkillDefinitionAttribution(after)
	for index := 0; index < 3; index++ {
		outcome := canaryOutcome(attribution, domain.RunFailed, "failed", now.Add(time.Duration(index)*time.Minute))
		outcome.ID = "outcome-" + string(rune('a'+index))
		outcome.RunID = "run-" + string(rune('a'+index))
		outcome.WorkspaceID, outcome.ProjectAgentID = view.Workspace.ID, agent.ID
		if err = application.store.SaveSkillOutcome(context.Background(), outcome); err != nil {
			t.Fatal(err)
		}
	}
	if err = application.evaluateAppliedSkillCanary(context.Background(), after.ID); err != nil {
		t.Fatal(err)
	}
	storedImprovement, err := application.store.GetAgentImprovement(context.Background(), improvement.ID)
	if err != nil || storedImprovement.Status != "rolled_back" || storedImprovement.CanaryEvaluation == nil || !storedImprovement.CanaryEvaluation.AutomaticRollback {
		t.Fatalf("improvement=%#v err=%v", storedImprovement, err)
	}
	skills, err := application.store.ListSkills(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range skills {
		if skill.ID == before.ID && revisionNumber(skill.Configuration["revision"]) != 1 {
			t.Fatalf("Skill revision was not restored: %#v", skill)
		}
	}
}

func canaryOutcome(attribution domain.SkillAttribution, status domain.RunStatus, health string, created time.Time) domain.SkillOutcome {
	return domain.SkillOutcome{
		SkillID: attribution.SkillID, SkillRevision: attribution.Revision, SkillDigest: attribution.Digest,
		RunStatus: status, Health: health, CreatedAt: created,
	}
}
