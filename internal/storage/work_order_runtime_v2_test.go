package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

// TestWorkOrderRuntimeCarriesStallAndStages закрепляет ключевое решение экрана
// выполнения: этапы и причина затыка приходят вместе с самим нарядом, который
// наблюдатель и так опрашивает, а не отдельным запросом /api/state/runtime.
func TestWorkOrderRuntimeCarriesStallAndStages(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "hub-v2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	order, err := store.SaveWorkOrderV2(ctx, storageWorkOrder())
	if err != nil {
		t.Fatal(err)
	}
	approval, err := store.ApproveWorkOrderV2(ctx, order.ID, order.Version, domain.WorkOrderDigest(order), "runtime-once")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	flow := domain.FlowGraph{
		ID: "flow_runtime_v2", WorkspaceID: order.WorkspaceID, Name: "Milestone",
		Nodes: []domain.FlowNode{
			{ID: "node_writer", Kind: domain.FlowNodeAgent, Name: "Написать API", AgentID: "agent_writer"},
			{ID: "node_verifier", Kind: domain.FlowNodeAgent, Name: "Проверить", AgentID: "agent_verifier"},
		},
		CreatedAt: now, UpdatedAt: now,
	}
	if err = store.SaveFlow(ctx, flow); err != nil {
		t.Fatal(err)
	}
	startError := `apply stage model binding: model "Qwen3.8-27B" is not in connection "connection_06b4cde" catalog`
	run := domain.FlowRun{
		ID: "flowrun_runtime_v2", FlowID: flow.ID, WorkspaceID: order.WorkspaceID, QuestID: approval.QuestID,
		Status: domain.RunRunning, StartedAt: now,
		NodeStates: map[string]domain.FlowNodeState{
			"node_writer": {Status: "waiting_agent", StartedAt: &now, Output: map[string]any{
				"waitReason": "start_failed", "startError": startError, "executionId": "execution_writer",
			}},
			"node_verifier": {Status: "pending"},
		},
	}
	if err = store.SaveFlowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	quest, err := storageQuestByID(ctx, store, order.WorkspaceID, approval.QuestID)
	if err != nil {
		t.Fatal(err)
	}
	quest.FlowID, quest.FlowRunID = flow.ID, run.ID
	if err = store.SaveQuest(ctx, quest); err != nil {
		t.Fatal(err)
	}
	reloaded, err := store.GetWorkOrderV2(ctx, order.ID)
	if err != nil {
		t.Fatal(err)
	}
	runtime := reloaded.Runtime
	if runtime == nil || len(runtime.Stages) != 2 {
		t.Fatalf("runtime stages are missing: %#v", runtime)
	}
	if runtime.Stages[0].ID != "node_writer" || runtime.Stages[0].Name != "Написать API" || runtime.Stages[0].WaitReason != "start_failed" {
		t.Fatalf("first stage lost flow order or state: %#v", runtime.Stages[0])
	}
	if runtime.Stages[1].ID != "node_verifier" || runtime.Stages[1].Status != "pending" {
		t.Fatalf("second stage=%#v", runtime.Stages[1])
	}
	if runtime.Stall == nil || runtime.Stall.NodeID != "node_writer" || runtime.Stall.Error != startError {
		t.Fatalf("stall reason did not reach the card: %#v", runtime.Stall)
	}

	// Ожидание ключа — такой же затык. Пока Stall заполнялся только на
	// start_failed, узел, ждущий credential, стоял без причины и без кнопки:
	// карточка писала «квест выполняется» у работы, которая не движется.
	run.NodeStates["node_writer"] = domain.FlowNodeState{
		Status: "waiting_agent", StartedAt: &now,
		Output: map[string]any{"waitReason": "waiting_api_key", "executionId": "execution_writer"},
	}
	if err = store.SaveFlowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	reloaded, err = store.GetWorkOrderV2(ctx, order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Runtime.Stall == nil || reloaded.Runtime.Stall.WaitReason != "waiting_api_key" {
		t.Fatalf("waiting_api_key is not reported as a stall: %#v", reloaded.Runtime.Stall)
	}

	// Flow leaves the node at waiting_agent during a live model execution.
	// The card must use the execution's actual status and clear the old wait.
	if err = store.SaveExecution(ctx, domain.ExecutionInstance{
		ID: "execution_writer", WorkspaceID: order.WorkspaceID, QuestID: approval.QuestID,
		FlowRunID: run.ID, FlowNodeID: "node_writer", RunID: "run_writer", Status: domain.RunRunning, StartedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	reloaded, err = store.GetWorkOrderV2(ctx, order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Runtime.Stall != nil || reloaded.Runtime.Stages[0].Status != "running" || reloaded.Runtime.Stages[0].RunID != "run_writer" || reloaded.Runtime.Stages[0].WaitReason != "" {
		t.Fatalf("live execution still appears stalled: %#v", reloaded.Runtime)
	}

	// Узел без причины ожидания затыком не считается: иначе карточка звала бы
	// человека к работе, которая идёт сама.
	run.NodeStates["node_writer"] = domain.FlowNodeState{Status: "running", StartedAt: &now, Output: map[string]any{"executionId": "execution_writer"}}
	if err = store.SaveFlowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	reloaded, err = store.GetWorkOrderV2(ctx, order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Runtime.Stall != nil {
		t.Fatalf("running node must not look stalled: %#v", reloaded.Runtime.Stall)
	}

	run.NodeStates["node_writer"] = domain.FlowNodeState{Status: string(domain.RunFailed), StartedAt: &now, Output: map[string]any{
		"executionId": "execution_writer", "error": "work contract forbids change to composer.json",
	}}
	if err = store.SaveFlowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	reloaded, err = store.GetWorkOrderV2(ctx, order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Runtime.Stall == nil || reloaded.Runtime.Stall.WaitReason != "stage_failed" || reloaded.Runtime.Stall.Error != "work contract forbids change to composer.json" {
		t.Fatalf("failed stage reason is hidden: %#v", reloaded.Runtime.Stall)
	}
}

func storageQuestByID(ctx context.Context, store *SQLite, workspaceID, questID string) (domain.Quest, error) {
	quests, err := store.ListQuests(ctx, workspaceID)
	if err != nil {
		return domain.Quest{}, err
	}
	for _, quest := range quests {
		if quest.ID == questID {
			return quest, nil
		}
	}
	return domain.Quest{}, context.Canceled
}
