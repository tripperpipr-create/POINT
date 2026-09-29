package app

import (
	"context"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

// Q01: v1-контроллер проекта писал квест полным снимком без сверки статуса и
// переводил закрытый квест в paused или active — вкладки снова показывали его
// активным.

func saveProjectQuestForTest(t *testing.T, application *App, quest domain.Quest) {
	t.Helper()
	now := time.Now().UTC()
	quest.Kind, quest.Title, quest.CreatedAt, quest.UpdatedAt = "project", "Ship", now, now
	if domain.IsTerminalQuestStatus(quest.Status) {
		quest.FinishedAt = &now
	}
	if err := application.store.SaveQuest(context.Background(), quest); err != nil {
		t.Fatal(err)
	}
}

func questStatusForTest(t *testing.T, application *App, id string) domain.Quest {
	t.Helper()
	quest, err := application.store.GetQuest(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return quest
}

func TestReconcileKeepsCancelledProjectWithOpenPrerequisite(t *testing.T) {
	application, world := outcomeWorld(t)
	saveProjectQuestForTest(t, application, domain.Quest{
		ID: "quest-cancelled-project", WorkspaceID: world.ID, Status: domain.QuestCancelled,
		ControllerState: controllerExecutingWave, PrerequisiteIDs: []string{"quest-prep-open"},
	})
	if _, err := application.ReconcileProjectController(context.Background(), "quest-cancelled-project"); err != nil {
		t.Fatal(err)
	}
	if got := questStatusForTest(t, application, "quest-cancelled-project"); got.Status != domain.QuestCancelled || got.ControllerState != controllerExecutingWave {
		t.Fatalf("сверка контроллера вернула отменённый проект: status=%s controller=%s", got.Status, got.ControllerState)
	}
}

func TestProjectControllerStateKeepsCompletedProject(t *testing.T) {
	application, world := outcomeWorld(t)
	saveProjectQuestForTest(t, application, domain.Quest{
		ID: "quest-completed-project", WorkspaceID: world.ID, Status: domain.QuestCompleted, ControllerState: controllerVerifying,
	})
	application.setProjectControllerState(context.Background(), "quest-completed-project", controllerNeedsUser)
	if got := questStatusForTest(t, application, "quest-completed-project"); got.Status != domain.QuestCompleted {
		t.Fatalf("поздний сигнал потока поставил завершённому проекту паузу: %s", got.Status)
	}
}

// cancelledProjectWithFlowForTest — отменённый проект с Flow из одного этапа
// агента: перепланирование такого квеста прежде проходило и возвращало его
// в active.
func cancelledProjectWithFlowForTest(t *testing.T, application *App, world domain.Workspace, id string) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	if err := application.store.SaveProjectAgent(ctx, domain.ProjectAgent{
		ID: "agent-a", WorkspaceID: world.ID, Name: "Writer", Provider: "ollama", PrimaryModel: "qwen",
		AllowedTools: []string{"read_file"}, MaxSteps: 8, MaxDurationSeconds: 60, MaxOutputTokens: 1024,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	brief, err := domain.ApproveTaskBrief(domain.NormalizeTaskBrief(domain.TaskBrief{
		Mode: domain.TaskModeProject, Goal: "Ship", ResultKind: "workspace_change",
		Criteria: []domain.AcceptanceCriterion{{ID: "c1", Text: "Done", Kind: "manual"}},
		Budget:   domain.TaskBudget{Tokens: 1000, ActiveSeconds: 60, MaxParallel: 1, MaxReplans: 2, MaxAttempts: 2},
	}))
	if err != nil {
		t.Fatal(err)
	}
	flow := domain.FlowGraph{
		ID: "flow-" + id, WorkspaceID: world.ID, Name: "wave",
		Nodes: []domain.FlowNode{{ID: "n1", Kind: domain.FlowNodeAgent, Name: "Write", AgentID: "agent-a"}},
	}
	if err = application.store.SaveFlow(ctx, flow); err != nil {
		t.Fatal(err)
	}
	saveProjectQuestForTest(t, application, domain.Quest{
		ID: id, WorkspaceID: world.ID, Status: domain.QuestCancelled, Brief: &brief,
		ControllerState: controllerExecutingWave, FlowID: flow.ID,
	})
	if err = application.store.SaveFlowRun(ctx, domain.FlowRun{
		ID: "fr-" + id, FlowID: flow.ID, WorkspaceID: world.ID, QuestID: id,
		Status: domain.RunCancelled, StartedAt: now,
		NodeStates: map[string]domain.FlowNodeState{"n1": {Status: "cancelled"}},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestReplanRefusesCancelledProject(t *testing.T) {
	application, world := outcomeWorld(t)
	cancelledProjectWithFlowForTest(t, application, world, "quest-cancelled-replan")
	if _, err := application.ReplanQuest(context.Background(), ReplanQuestRequest{
		QuestID: "quest-cancelled-replan", Reason: "late blocker",
		Stages: []domain.ReplanStagePatch{{AgentID: "agent-a", Name: "Fix", Instruction: "Fix"}},
	}); err == nil {
		t.Fatal("перепланирование отменённого квеста принято")
	}
	if got := questStatusForTest(t, application, "quest-cancelled-replan"); got.Status != domain.QuestCancelled {
		t.Fatalf("отмена потеряна: %s", got.Status)
	}
}

func TestTeamBlockerLeavesCancelledProjectClosed(t *testing.T) {
	application, world := outcomeWorld(t)
	cancelledProjectWithFlowForTest(t, application, world, "quest-cancelled-blocker")
	if _, err := application.PublishTeamEvent(context.Background(), domain.TeamEvent{
		Kind: "blocker", Message: "late blocker", FlowRunID: "fr-quest-cancelled-blocker",
		FlowNodeID: "n1", FromAgentID: "agent-a", ToAgentID: "agent-a", QuestID: "quest-cancelled-blocker",
	}); err != nil {
		t.Fatal(err)
	}
	got := questStatusForTest(t, application, "quest-cancelled-blocker")
	if got.Status != domain.QuestCancelled || got.ControllerState != controllerExecutingWave {
		t.Fatalf("блокер вернул отменённый проект: status=%s controller=%s", got.Status, got.ControllerState)
	}
}
