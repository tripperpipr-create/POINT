package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

// failedStageQuestForTest — квест, у которого Flow упал на втором этапе:
// первый пройден, второй провалился, третий пропущен из-за провала.
func failedStageQuestForTest(t *testing.T) (*App, domain.Quest, domain.FlowRun) {
	t.Helper()
	application, quest, _ := approvedHostCheckQuestForTest(t, 1)
	ctx := context.Background()
	now := time.Now().UTC()
	agents, err := application.store.ListProjectAgents(ctx, quest.WorkspaceID)
	if err != nil || len(agents) == 0 {
		t.Fatalf("roster agents=%d err=%v", len(agents), err)
	}
	agentID := agents[0].ID
	graph := domain.FlowGraph{ID: "flow-stage", WorkspaceID: quest.WorkspaceID, Name: "stages",
		Nodes: []domain.FlowNode{
			{ID: "implement", Kind: domain.FlowNodeAgent, Name: "Реализация", AgentID: agentID},
			{ID: "verify", Kind: domain.FlowNodeAgent, Name: "Проверка сборки", AgentID: agentID},
			{ID: "integrate", Kind: domain.FlowNodeAgent, Name: "Интеграция", AgentID: agentID},
		},
		Edges: []domain.FlowEdge{{ID: "e1", From: "implement", To: "verify"}, {ID: "e2", From: "verify", To: "integrate"}},
	}
	if err := application.store.SaveExecution(ctx, domain.ExecutionInstance{ID: "execution-verify-1", WorkspaceID: quest.WorkspaceID, QuestID: quest.ID,
		FlowRunID: "flowrun-stage", FlowNodeID: "verify", RunID: "run-verify-1", Status: domain.RunFailed,
		Error: "workspace mutation audit failed after executable tool started: context deadline exceeded", StartedAt: now}); err != nil {
		t.Fatal(err)
	}
	run := domain.FlowRun{ID: "flowrun-stage", FlowID: graph.ID, WorkspaceID: quest.WorkspaceID, QuestID: quest.ID, Status: domain.RunFailed,
		Error: "Проверка сборки: workspace mutation audit failed", StartedAt: now, FinishedAt: &now,
		Snapshot: map[string]any{"graph": graph},
		NodeStates: map[string]domain.FlowNodeState{
			"implement": {Status: "completed", Attempts: 1, Output: map[string]any{"completed": true}},
			"verify": {Status: "failed", Attempts: 1, Error: "workspace mutation audit failed after executable tool started: context deadline exceeded",
				Output: map[string]any{"executionId": "execution-verify-1", "attemptExecutionIds": []any{"execution-verify-1"}}},
			"integrate": {Status: "skipped", Error: "пропущен: другой этап Flow завершился с ошибкой", Output: map[string]any{"skippedDueToPeerFailure": true}},
		}}
	if err := application.store.SaveFlowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	for _, stage := range []domain.Quest{
		{ID: "stage-verify", WorkspaceID: quest.WorkspaceID, ParentID: quest.ID, FlowRunID: run.ID, FlowNodeID: "verify", Title: "Проверка сборки", Status: domain.QuestFailed, CreatedAt: now, UpdatedAt: now, FinishedAt: &now},
		{ID: "stage-integrate", WorkspaceID: quest.WorkspaceID, ParentID: quest.ID, FlowRunID: run.ID, FlowNodeID: "integrate", Title: "Интеграция", Status: domain.QuestCancelled, CreatedAt: now, UpdatedAt: now, FinishedAt: &now},
	} {
		if err := application.store.SaveQuest(ctx, stage); err != nil {
			t.Fatal(err)
		}
	}
	quest.FlowID, quest.FlowRunID = graph.ID, run.ID
	if err := application.store.SaveQuest(ctx, quest); err != nil {
		t.Fatal(err)
	}
	approval, err := application.store.WorkOrderApprovalByQuestV2(ctx, quest.ID)
	if err != nil {
		t.Fatal(err)
	}
	runtimes, err := application.store.ListMilestoneRuntimesV2(ctx, quest.ID, approval.WorkOrder.Version)
	if err != nil || len(runtimes) == 0 {
		t.Fatalf("milestones=%d err=%v", len(runtimes), err)
	}
	runtimes[0].Status, runtimes[0].FlowID, runtimes[0].FlowRunID = domain.QuestRunning, graph.ID, run.ID
	if err = application.store.SaveMilestoneRuntimeV2(ctx, quest.ID, approval.WorkOrder.ID, approval.WorkOrder.Version, runtimes[0]); err != nil {
		t.Fatal(err)
	}
	return application, quest, run
}

// Провал этапа — не вердикт: квест ждёт человека, пакет доказательств не
// тратится, в карточке — какой этап и почему.
func TestFailedStageHoldsQuestForDecisionInsteadOfVerdict(t *testing.T) {
	application, quest, _ := failedStageQuestForTest(t)
	ctx := context.Background()
	application.finalizeQuestAfterFlow(quest.ID, false)
	held, err := application.workOrderQuestV2(ctx, quest.WorkspaceID, quest.ID)
	if err != nil {
		t.Fatal(err)
	}
	message, _ := held.Controller["statusMessage"].(string)
	if held.Status != domain.QuestAwaitingUser || !strings.Contains(message, "Проверка сборки") || !strings.Contains(message, "Повторите этап") {
		t.Fatalf("failed stage did not wait for a decision: status=%s message=%q", held.Status, message)
	}
	if _, final, _ := application.store.WorkOrderVerdictV2(ctx, quest.ID); final {
		t.Fatal("a held stage failure spent the quest's only verdict")
	}
}

// «Повторить этап» продолжает тот же Flow: пройденный этап остаётся, проваленный
// получает новую попытку с отчётом о прежней, пропущенный снова ждёт.
func TestRetryFailedStageContinuesTheSameFlow(t *testing.T) {
	application, quest, run := failedStageQuestForTest(t)
	ctx := context.Background()
	application.finalizeQuestAfterFlow(quest.ID, false)
	result, err := application.ControlWorkOrderQuestV2(ctx, quest.ID, "retry", WorkOrderQuestControlRequest{})
	if err != nil && !strings.Contains(err.Error(), "agent") {
		t.Fatalf("retry failed: %v", err)
	}
	if result.Status != domain.QuestRunning {
		t.Fatalf("retry left the quest in %s", result.Status)
	}
	stored, err := application.store.GetFlowRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status == domain.RunFailed || stored.NodeStates["implement"].Status != "completed" {
		t.Fatalf("flow after retry: status=%s implement=%s err=%v verify=%#v flowErr=%s", stored.Status, stored.NodeStates["implement"].Status, err, stored.NodeStates["verify"], stored.Error)
	}
	verify := stored.NodeStates["verify"]
	if verify.Status == "failed" || verify.Attempts != 2 || verify.Output["previousExecutionId"] != "execution-verify-1" {
		t.Fatalf("failed stage did not get a fresh attempt: %#v", verify)
	}
	if integrate := stored.NodeStates["integrate"]; integrate.Status == "skipped" {
		t.Fatalf("skipped stage still skipped: %#v", integrate)
	}
	quests, _ := application.store.ListQuests(ctx, quest.WorkspaceID)
	for _, stage := range quests {
		if (stage.ID == "stage-verify" || stage.ID == "stage-integrate") && domain.IsTerminalQuestStatus(stage.Status) {
			t.Fatalf("stage quest %s stayed closed: %s", stage.ID, stage.Status)
		}
	}
	if _, err = application.ControlWorkOrderQuestV2(ctx, quest.ID, "retry", WorkOrderQuestControlRequest{}); err == nil {
		t.Fatal("a second retry of a running quest was accepted")
	}
}

// «Завершить квест» выносит вердикт тем же путём, что и прежде.
func TestFinalizeFailedStageGivesTheVerdict(t *testing.T) {
	application, quest, _ := failedStageQuestForTest(t)
	ctx := context.Background()
	application.finalizeQuestAfterFlow(quest.ID, false)
	result, err := application.ControlWorkOrderQuestV2(ctx, quest.ID, "finalize", WorkOrderQuestControlRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != domain.QuestBlocked {
		after, _ := application.workOrderQuestV2(ctx, quest.WorkspaceID, quest.ID)
		t.Fatalf("finalize gave %s controller=%v", result.Status, after.Controller)
	}
	if verdict, final, _ := application.store.WorkOrderVerdictV2(ctx, quest.ID); !final || verdict != domain.QuestBlocked {
		t.Fatalf("verdict=%s final=%t", verdict, final)
	}
}
