package app

import (
	"context"
	"fmt"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

// Разбор квеста связывает наблюдение, его источник и то, что из него выросло:
// дефект хода Мастера → задание обучения методики; решение человека по
// критерию; прогон агента → выученный навык и его канарейка.
func TestQuestRetrospectiveLinksObservationsToWhatWasLearned(t *testing.T) {
	application := newTestApp(t)
	ctx := context.Background()
	workspace, approval := approvedTestWorkOrder(t, application, "retrospective")
	questID := approval.QuestID
	now := time.Now().UTC()
	planning := domain.SkillAttribution{SkillID: "master-planning", Digest: "planning-digest"}
	for i := 0; i < 3; i++ {
		op := domain.MasterOperation{ID: fmt.Sprintf("retro-op-%d", i), WorkspaceID: workspace.ID, Phase: "planning", QuestID: questID,
			Skills: []domain.SkillAttribution{planning}, Replay: `{"format":2,"request":{}}`, CreatedAt: now.Add(time.Duration(i) * time.Second)}
		if i == 0 {
			op.ContractError = true
			op.AddDefect("plan_rejected", "stage asks the executor for Docker the sandbox lacks")
		}
		if err := application.store.SaveMasterOperation(ctx, op); err != nil {
			t.Fatal(err)
		}
	}
	if err := application.store.QueueMasterLearning(ctx, workspace.ID, "planning", planning.SkillID, "base"); err != nil {
		t.Fatal(err)
	}
	application.recordMasterEvidenceDetail(ctx, approval.WorkOrder, questID, "manual_review", "rejected", "ui", "ui: окно не открывается")

	if err := application.store.SaveExecution(ctx, domain.ExecutionInstance{ID: "retro-execution", WorkspaceID: workspace.ID, QuestID: questID, RunID: "retro-run", StartedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := application.store.SaveLearningSignal(ctx, domain.LearningSignal{ID: "retro-signal", WorkspaceID: workspace.ID, RunID: "retro-run",
		Kind: domain.LearningSignalKind("verification_gap"), Status: "consumed", Summary: "обязательная проверка не записана", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	skill := domain.SkillDefinition{ID: "skill-learned-retro", Name: "Проверка перед отчётом", Instructions: "Запиши результат проверки."}
	if err := application.store.SaveAgentImprovement(ctx, domain.AgentImprovement{ID: "retro-improvement", WorkspaceID: workspace.ID, ProjectAgentID: "agent",
		SourceRunID: "retro-run", SkillID: skill.ID, Kind: "skill_recovery", Status: "applied_unproven", AfterSkill: &skill,
		CanaryEvaluation: &domain.SkillCanaryEvaluation{Status: "pending", Reasons: []string{"need 3 candidate runs; observed 1"}}, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	// Чужой прогон в разбор не попадает.
	if err := application.store.SaveAgentImprovement(ctx, domain.AgentImprovement{ID: "foreign-improvement", WorkspaceID: workspace.ID, SourceRunID: "other-run",
		Kind: "skill_created", Status: "applied_unproven", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}

	retro, err := application.QuestRetrospective(ctx, questID)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]QuestRetrospectiveObservation{}
	for _, observation := range retro.Observations {
		kinds[observation.Source+":"+observation.Kind] = observation
	}
	if kinds["master:planning/plan_rejected"].Detail == "" || kinds["human:manual_review"].Detail != "ui: окно не открывается" || kinds["agent:verification_gap"].Detail == "" {
		t.Fatalf("observations=%#v", retro.Observations)
	}
	if len(retro.MasterLearning) != 1 || retro.MasterLearning[0].SkillID != planning.SkillID {
		t.Fatalf("master learning=%#v", retro.MasterLearning)
	}
	if len(retro.AgentLearning) != 1 || retro.AgentLearning[0].SkillName != skill.Name || retro.AgentLearning[0].Canary != "pending" {
		t.Fatalf("agent learning=%#v", retro.AgentLearning)
	}
}
