package app

import (
	"context"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

func TestTeamBlockerMarksProjectControllerForReplan(t *testing.T) {
	application, world := outcomeWorld(t)
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
	quest := domain.Quest{
		ID: "q-block", WorkspaceID: world.ID, Title: "Ship", Kind: "project", Brief: &brief,
		ControllerState: controllerExecutingWave, Status: domain.QuestActive, CreatedAt: now, UpdatedAt: now,
	}
	if err = application.store.SaveQuest(ctx, quest); err != nil {
		t.Fatal(err)
	}
	flow := domain.FlowGraph{
		ID: "flow-block", WorkspaceID: world.ID, Name: "wave",
		Nodes: []domain.FlowNode{{ID: "n1", Kind: domain.FlowNodeAgent, Name: "Write", AgentID: "agent-a"}},
	}
	if err = application.store.SaveFlow(ctx, flow); err != nil {
		t.Fatal(err)
	}
	if err = application.store.SaveFlowRun(ctx, domain.FlowRun{
		ID: "fr-block", FlowID: flow.ID, WorkspaceID: world.ID, QuestID: quest.ID,
		Status: domain.RunRunning, StartedAt: now,
		NodeStates: map[string]domain.FlowNodeState{"n1": {Status: "running"}},
	}); err != nil {
		t.Fatal(err)
	}
	event, err := application.PublishTeamEvent(ctx, domain.TeamEvent{
		Kind: "blocker", Message: "schema conflict", FlowRunID: "fr-block",
		FlowNodeID: "n1", FromAgentID: "agent-a", ToAgentID: "agent-a", QuestID: quest.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if event.ID == "" {
		t.Fatal("event was not persisted")
	}
	saved := domain.Quest{}
	quests, err := application.store.ListQuests(ctx, world.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range quests {
		if item.ID == quest.ID {
			saved = item
		}
	}
	if saved.ControllerState != controllerExecutingWave {
		t.Fatalf("controller=%q", saved.ControllerState)
	}
	updatedFlow, err := application.store.GetFlow(ctx, flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	foundWave := false
	for _, node := range updatedFlow.Nodes {
		if node.Name == "Resolve blocker" && node.AgentID == "agent-a" {
			foundWave = true
		}
	}
	if !foundWave {
		t.Fatalf("future wave was not appended: %#v", updatedFlow.Nodes)
	}
	inbox, err := application.TeamInbox(ctx, "fr-block", "agent-a", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(inbox) != 1 || inbox[0].DeliveredAt == nil {
		t.Fatalf("inbox=%#v", inbox)
	}
}
