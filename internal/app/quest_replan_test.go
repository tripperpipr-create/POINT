package app

import (
	"context"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/flowruntime"
)

func TestReplanQuestUpdatesPendingStageAndEnforcesLimit(t *testing.T) {
	a, ws := outcomeWorld(t)
	ctx := context.Background()
	now := time.Now().UTC()
	agent := domain.ProjectAgent{ID: "agent-1", WorkspaceID: ws.ID, Name: "Dev", Provider: "ollama", PrimaryModel: "qwen", AllowedTools: []string{"read_file"}, CreatedAt: now, UpdatedAt: now}
	if err := a.store.SaveProjectAgent(ctx, agent); err != nil {
		t.Fatal(err)
	}
	brief, err := domain.ApproveTaskBrief(domain.NormalizeTaskBrief(domain.TaskBrief{
		Mode: domain.TaskModePrecise, Goal: "Ship fix", ResultKind: "workspace_change",
		Criteria: []domain.AcceptanceCriterion{{ID: "c1", Text: "Tests pass", Kind: "manual"}},
		Budget:   domain.TaskBudget{Tokens: 1000, ActiveSeconds: 60, MaxParallel: 1, MaxReplans: 1, MaxAttempts: 2},
	}))
	if err != nil {
		t.Fatal(err)
	}
	nodeID := "node-agent"
	flow := domain.FlowGraph{
		ID: "flow-1", WorkspaceID: ws.ID, Name: "plan", UpdatedAt: now, CreatedAt: now,
		Nodes: []domain.FlowNode{
			{ID: "node-in", Kind: domain.FlowNodeInput, Name: "In"},
			{ID: nodeID, Kind: domain.FlowNodeAgent, Name: "Do", AgentID: agent.ID, Config: map[string]any{"instruction": "old"}},
			{ID: "node-out", Kind: domain.FlowNodeOutput, Name: "Out"},
		},
		Edges: []domain.FlowEdge{{ID: "e1", From: "node-in", To: nodeID}, {ID: "e2", From: nodeID, To: "node-out"}},
	}
	if err = a.store.SaveFlow(ctx, flow); err != nil {
		t.Fatal(err)
	}
	quest := domain.Quest{
		ID: "quest-replan", WorkspaceID: ws.ID, Title: "Ship", Status: domain.QuestActive,
		Brief: &brief, FlowID: flow.ID, CreatedAt: now, UpdatedAt: now,
	}
	if err = a.store.SaveQuest(ctx, quest); err != nil {
		t.Fatal(err)
	}
	result, err := a.ReplanQuest(ctx, ReplanQuestRequest{
		QuestID: quest.ID, Reason: "Need different instruction", CriterionIDs: []string{"c1"},
		Stages: []domain.ReplanStagePatch{{NodeID: nodeID, Instruction: "new instruction"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Replan.Seq != 1 || len(result.Replan.UpdatedNodes) != 1 {
		t.Fatalf("replan=%#v", result.Replan)
	}
	updated, err := a.store.GetFlow(ctx, flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	var instruction string
	for _, node := range updated.Nodes {
		if node.ID == nodeID {
			instruction, _ = node.Config["instruction"].(string)
		}
	}
	if instruction != "new instruction" {
		t.Fatalf("instruction=%q", instruction)
	}
	if _, err = a.ReplanQuest(ctx, ReplanQuestRequest{
		QuestID: quest.ID, Reason: "again", Stages: []domain.ReplanStagePatch{{NodeID: nodeID, Instruction: "third"}},
	}); err == nil {
		t.Fatal("expected MaxReplans=1 to block second replan")
	}
}

func TestReplanQuestRejectsFinishedStageAndDuplicateDigest(t *testing.T) {
	a, ws := outcomeWorld(t)
	ctx := context.Background()
	now := time.Now().UTC()
	agent := domain.ProjectAgent{ID: "agent-2", WorkspaceID: ws.ID, Name: "Dev", Provider: "ollama", PrimaryModel: "qwen", AllowedTools: []string{"read_file"}, CreatedAt: now, UpdatedAt: now}
	if err := a.store.SaveProjectAgent(ctx, agent); err != nil {
		t.Fatal(err)
	}
	brief, err := domain.ApproveTaskBrief(domain.NormalizeTaskBrief(domain.TaskBrief{
		Mode: domain.TaskModePrecise, Goal: "Ship", ResultKind: "code",
		Criteria: []domain.AcceptanceCriterion{{ID: "c1", Text: "Done", Kind: "manual"}},
		Budget:   domain.TaskBudget{Tokens: 1000, ActiveSeconds: 60, MaxParallel: 1, MaxReplans: 6, MaxAttempts: 2},
	}))
	if err != nil {
		t.Fatal(err)
	}
	nodeID := "node-done"
	flow := domain.FlowGraph{
		ID: "flow-2", WorkspaceID: ws.ID, Name: "plan", UpdatedAt: now, CreatedAt: now,
		Nodes: []domain.FlowNode{{ID: nodeID, Kind: domain.FlowNodeAgent, Name: "Do", AgentID: agent.ID, Config: map[string]any{"instruction": "a"}}},
	}
	if err = a.store.SaveFlow(ctx, flow); err != nil {
		t.Fatal(err)
	}
	flowRun := domain.FlowRun{
		ID: "fr-1", FlowID: flow.ID, WorkspaceID: ws.ID, QuestID: "quest-2", Status: domain.RunRunning,
		NodeStates: map[string]domain.FlowNodeState{nodeID: {Status: "completed"}},
		StartedAt:  now,
	}
	if err = a.store.SaveFlowRun(ctx, flowRun); err != nil {
		t.Fatal(err)
	}
	quest := domain.Quest{
		ID: "quest-2", WorkspaceID: ws.ID, Title: "Ship", Status: domain.QuestActive,
		Brief: &brief, FlowID: flow.ID, FlowRunID: flowRun.ID, CreatedAt: now, UpdatedAt: now,
	}
	if err = a.store.SaveQuest(ctx, quest); err != nil {
		t.Fatal(err)
	}
	if _, err = a.ReplanQuest(ctx, ReplanQuestRequest{
		QuestID: quest.ID, Reason: "late", Stages: []domain.ReplanStagePatch{{NodeID: nodeID, Instruction: "b"}},
	}); err == nil {
		t.Fatal("completed stage must reject replan")
	}
}

func TestReplanQuestUpdatesSnapshotGraphAndStopsWaitingAgent(t *testing.T) {
	a, ws := outcomeWorld(t)
	ctx := context.Background()
	now := time.Now().UTC()
	agentRec := domain.ProjectAgent{
		ID: "agent-replan-live", WorkspaceID: ws.ID, Name: "Dev", Provider: "ollama", PrimaryModel: "qwen",
		AllowedTools: []string{"read_file"}, MaxSteps: 4, MaxDurationSeconds: 60, MaxOutputTokens: 1024,
		ContextWindowTokens: 8192, CreatedAt: now, UpdatedAt: now,
	}
	if err := a.store.SaveProjectAgent(ctx, agentRec); err != nil {
		t.Fatal(err)
	}
	brief, err := domain.ApproveTaskBrief(domain.NormalizeTaskBrief(domain.TaskBrief{
		Mode: domain.TaskModePrecise, Goal: "Ship", ResultKind: "code",
		Criteria: []domain.AcceptanceCriterion{{ID: "c1", Text: "Done", Kind: "manual"}},
		Budget:   domain.TaskBudget{Tokens: 1000, ActiveSeconds: 60, MaxParallel: 1, MaxReplans: 6, MaxAttempts: 2},
	}))
	if err != nil {
		t.Fatal(err)
	}
	nodeID := "node-live"
	flow := domain.FlowGraph{
		ID: "flow-live", WorkspaceID: ws.ID, Name: "plan", UpdatedAt: now, CreatedAt: now,
		Nodes: []domain.FlowNode{
			{ID: "in", Kind: domain.FlowNodeInput, Name: "In"},
			{ID: nodeID, Kind: domain.FlowNodeAgent, Name: "Do", AgentID: agentRec.ID, Config: map[string]any{"instruction": "old live"}},
			{ID: "out", Kind: domain.FlowNodeOutput, Name: "Out"},
		},
		Edges: []domain.FlowEdge{{ID: "e1", From: "in", To: nodeID}, {ID: "e2", From: nodeID, To: "out"}},
	}
	if err = a.store.SaveFlow(ctx, flow); err != nil {
		t.Fatal(err)
	}
	exec := domain.ExecutionInstance{
		ID: "exec-live", WorkspaceID: ws.ID, ProjectAgentID: agentRec.ID, QuestID: "quest-live",
		FlowRunID: "fr-live", FlowNodeID: nodeID, Status: domain.RunRunning, StartedAt: now,
	}
	if err = a.store.SaveExecution(ctx, exec); err != nil {
		t.Fatal(err)
	}
	flowRun := domain.FlowRun{
		ID: "fr-live", FlowID: flow.ID, WorkspaceID: ws.ID, QuestID: "quest-live", Status: domain.RunWaiting,
		NodeStates: map[string]domain.FlowNodeState{
			nodeID: {Status: "waiting_agent", Output: map[string]any{"executionId": exec.ID}},
		},
		Snapshot:  map[string]any{"graph": flow},
		StartedAt: now,
	}
	if err = a.store.SaveFlowRun(ctx, flowRun); err != nil {
		t.Fatal(err)
	}
	// Parent quest intentionally omits FlowRunID — resolve via ListFlowRunsByFlowID.
	quest := domain.Quest{
		ID: "quest-live", WorkspaceID: ws.ID, Title: "Ship", Status: domain.QuestActive,
		Brief: &brief, FlowID: flow.ID, CreatedAt: now, UpdatedAt: now,
	}
	if err = a.store.SaveQuest(ctx, quest); err != nil {
		t.Fatal(err)
	}
	result, err := a.ReplanQuest(ctx, ReplanQuestRequest{
		QuestID: quest.ID, Reason: "revise mid-flight", CriterionIDs: []string{"c1"},
		Stages: []domain.ReplanStagePatch{{NodeID: nodeID, Instruction: "new live"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Replan.FlowRunID != flowRun.ID {
		t.Fatalf("expected resolved flow run %s, got %s", flowRun.ID, result.Replan.FlowRunID)
	}
	if len(result.Replan.StoppedRuns) == 0 {
		t.Fatal("expected stopped execution")
	}
	reloaded, err := a.store.GetFlowRun(ctx, flowRun.ID)
	if err != nil {
		t.Fatal(err)
	}
	snap, ok, err := flowruntime.FlowFromSnapshot(reloaded)
	if err != nil || !ok {
		t.Fatalf("snapshot graph missing: ok=%v err=%v", ok, err)
	}
	var instruction string
	for _, node := range snap.Nodes {
		if node.ID == nodeID {
			instruction, _ = node.Config["instruction"].(string)
		}
	}
	if instruction != "new live" {
		t.Fatalf("snapshot instruction=%q", instruction)
	}
	state := reloaded.NodeStates[nodeID]
	if state.Status != "waiting_agent" {
		t.Fatalf("status=%q want waiting_agent", state.Status)
	}
	cancelled, err := a.store.GetExecution(ctx, exec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != domain.RunCancelled {
		t.Fatalf("execution status=%s", cancelled.Status)
	}
	if newID, _ := state.Output["executionId"].(string); newID == exec.ID {
		t.Fatal("replaced stage must not keep the cancelled executionId")
	}
}

func TestReplanQuestAllowsBlockedNodeWithoutFlowRunIDOnQuest(t *testing.T) {
	a, ws := outcomeWorld(t)
	ctx := context.Background()
	now := time.Now().UTC()
	agentRec := domain.ProjectAgent{ID: "agent-blocked", WorkspaceID: ws.ID, Name: "Dev", Provider: "ollama", PrimaryModel: "qwen", AllowedTools: []string{"read_file"}, CreatedAt: now, UpdatedAt: now}
	if err := a.store.SaveProjectAgent(ctx, agentRec); err != nil {
		t.Fatal(err)
	}
	brief, err := domain.ApproveTaskBrief(domain.NormalizeTaskBrief(domain.TaskBrief{
		Mode: domain.TaskModePrecise, Goal: "Ship", ResultKind: "code",
		Criteria: []domain.AcceptanceCriterion{{ID: "c1", Text: "Done", Kind: "manual"}},
		Budget:   domain.TaskBudget{Tokens: 1000, ActiveSeconds: 60, MaxParallel: 1, MaxReplans: 6, MaxAttempts: 2},
	}))
	if err != nil {
		t.Fatal(err)
	}
	nodeID := "node-blocked"
	flow := domain.FlowGraph{
		ID: "flow-blocked", WorkspaceID: ws.ID, Name: "plan", UpdatedAt: now, CreatedAt: now,
		Nodes: []domain.FlowNode{{ID: nodeID, Kind: domain.FlowNodeAgent, Name: "Do", AgentID: agentRec.ID, Config: map[string]any{"instruction": "before"}}},
	}
	if err = a.store.SaveFlow(ctx, flow); err != nil {
		t.Fatal(err)
	}
	flowRun := domain.FlowRun{
		ID: "fr-blocked", FlowID: flow.ID, WorkspaceID: ws.ID, QuestID: "quest-blocked", Status: domain.RunRunning,
		NodeStates: map[string]domain.FlowNodeState{nodeID: {Status: "blocked"}},
		Snapshot:   map[string]any{"graph": flow},
		StartedAt:  now,
	}
	if err = a.store.SaveFlowRun(ctx, flowRun); err != nil {
		t.Fatal(err)
	}
	quest := domain.Quest{
		ID: "quest-blocked", WorkspaceID: ws.ID, Title: "Ship", Status: domain.QuestActive,
		Brief: &brief, FlowID: flow.ID, CreatedAt: now, UpdatedAt: now,
	}
	if err = a.store.SaveQuest(ctx, quest); err != nil {
		t.Fatal(err)
	}
	if _, err = a.ReplanQuest(ctx, ReplanQuestRequest{
		QuestID: quest.ID, Reason: "prep", Stages: []domain.ReplanStagePatch{{NodeID: nodeID, Instruction: "after"}},
	}); err != nil {
		t.Fatal(err)
	}
	reloaded, err := a.store.GetFlowRun(ctx, flowRun.ID)
	if err != nil {
		t.Fatal(err)
	}
	snap, ok, err := flowruntime.FlowFromSnapshot(reloaded)
	if err != nil || !ok {
		t.Fatal("snapshot missing")
	}
	for _, node := range snap.Nodes {
		if node.ID == nodeID {
			if got, _ := node.Config["instruction"].(string); got != "after" {
				t.Fatalf("instruction=%q", got)
			}
		}
	}
	if reloaded.NodeStates[nodeID].Status != "blocked" {
		t.Fatalf("blocked status should stay blocked, got %s", reloaded.NodeStates[nodeID].Status)
	}
}

func TestReplanQuestCreatesFutureWaveWithoutRewritingHistory(t *testing.T) {
	a, ws := outcomeWorld(t)
	ctx := context.Background()
	now := time.Now().UTC()
	agent := domain.ProjectAgent{ID: "agent-wave", WorkspaceID: ws.ID, Name: "Dev", Provider: "ollama", PrimaryModel: "qwen", AllowedTools: []string{"read_file"}, MaxSteps: 8, MaxDurationSeconds: 60, MaxOutputTokens: 1024, CreatedAt: now, UpdatedAt: now}
	if err := a.store.SaveProjectAgent(ctx, agent); err != nil {
		t.Fatal(err)
	}
	brief, err := domain.ApproveTaskBrief(domain.NormalizeTaskBrief(domain.TaskBrief{
		Mode: domain.TaskModeProject, Goal: "Ship", ResultKind: "workspace_change",
		Criteria: []domain.AcceptanceCriterion{{ID: "c1", Text: "Done", Kind: "manual"}},
		Budget:   domain.TaskBudget{Tokens: 1000, ActiveSeconds: 60, MaxParallel: 1, MaxReplans: 6, MaxAttempts: 2},
	}))
	if err != nil {
		t.Fatal(err)
	}
	doneID := "node-done"
	flow := domain.FlowGraph{
		ID: "flow-wave", WorkspaceID: ws.ID, Name: "plan", UpdatedAt: now, CreatedAt: now,
		Nodes: []domain.FlowNode{
			{ID: "node-in", Kind: domain.FlowNodeInput, Name: "In"},
			{ID: doneID, Kind: domain.FlowNodeAgent, Name: "Done", AgentID: agent.ID},
			{ID: "node-out", Kind: domain.FlowNodeOutput, Name: "Out"},
		},
		Edges: []domain.FlowEdge{{ID: "e1", From: "node-in", To: doneID}, {ID: "e2", From: doneID, To: "node-out"}},
	}
	if err = a.store.SaveFlow(ctx, flow); err != nil {
		t.Fatal(err)
	}
	quest := domain.Quest{
		ID: "quest-wave", WorkspaceID: ws.ID, Title: "Ship", Kind: "project", Status: domain.QuestActive,
		Brief: &brief, FlowID: flow.ID, CreatedAt: now, UpdatedAt: now,
	}
	if err = a.store.SaveQuest(ctx, quest); err != nil {
		t.Fatal(err)
	}
	result, err := a.ReplanQuest(ctx, ReplanQuestRequest{
		QuestID: quest.ID, Reason: "next wave",
		Stages: []domain.ReplanStagePatch{{AgentID: agent.ID, Name: "Wave 2", Instruction: "implement contracts"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Replan.UpdatedNodes) != 1 {
		t.Fatalf("updated=%#v", result.Replan.UpdatedNodes)
	}
	updated, err := a.store.GetFlow(ctx, flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, node := range updated.Nodes {
		if node.ID == doneID && node.Name != "Done" {
			t.Fatal("completed history was rewritten")
		}
		if node.Name == "Wave 2" {
			found = true
		}
	}
	if !found {
		t.Fatalf("future wave missing: %#v", updated.Nodes)
	}
}
