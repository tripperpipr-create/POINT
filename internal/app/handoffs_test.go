package app

import (
	"context"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

// Когда второй агент делает не то, надо различать «не понял задачу» и «ему не
// то передали». Цепочка передач отвечает именно на это.
func TestHandoffsShowWhatTheNextAgentReceived(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())

	world := openTestWorld(t, application)
	ctx := context.Background()

	forge := domain.ProjectAgent{ID: "a-forge", WorkspaceID: world.ID, Name: "ФОРДЖ", Provider: domain.ProviderOllama, PrimaryModel: "q", AllowedTools: []string{"read_file"}, MaxSteps: 30}
	guard := domain.ProjectAgent{ID: "a-guard", WorkspaceID: world.ID, Name: "СТРАЖ", Provider: domain.ProviderOllama, PrimaryModel: "q", AllowedTools: []string{"read_file"}, MaxSteps: 30}
	for _, agent := range []domain.ProjectAgent{forge, guard} {
		if _, err = application.SaveProjectAgent(agent); err != nil {
			t.Fatal(err)
		}
	}

	flow := domain.FlowGraph{
		ID: "flow-1", WorkspaceID: world.ID, Name: "Починка",
		Nodes: []domain.FlowNode{
			{ID: "n1", Kind: "agent", Name: "Правка", AgentID: "a-forge"},
			{ID: "n2", Kind: "agent", Name: "Ревью", AgentID: "a-guard"},
		},
		Edges: []domain.FlowEdge{{From: "n1", To: "n2"}},
	}
	if err = application.store.SaveFlow(ctx, flow); err != nil {
		t.Fatal(err)
	}
	if err = application.store.SaveChangeSet(ctx, domain.ChangeSet{
		ID: "cs-1", WorkspaceID: world.ID, ExecutionID: "ex-1", Title: "Правка движка",
		Status: domain.ChangeSetPending, CreatedAt: time.Now().UTC(),
		Items: []domain.ChangeItem{{ID: "i1", Path: "engine.go", Kind: "modify"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err = application.store.SaveFlowRun(ctx, domain.FlowRun{
		ID: "fr-1", FlowID: flow.ID, WorkspaceID: world.ID, Status: domain.RunRunning,
		StartedAt: time.Now().UTC(),
		NodeStates: map[string]domain.FlowNodeState{
			"n1": {Status: "completed", Output: map[string]any{
				"executionId":  "ex-1",
				"result":       "Заменил мьютекс на sync.Map",
				"changedFiles": []any{"engine.go", "scheduler.go"},
			}},
			"n2": {Status: "waiting_agent"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	chain, err := application.Handoffs(ctx, "fr-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(chain.Items) != 1 {
		t.Fatalf("ожидалась одна передача, получено %d: %+v", len(chain.Items), chain.Items)
	}
	item := chain.Items[0]
	if item.FromAgent != "ФОРДЖ" || item.ToAgent != "СТРАЖ" {
		t.Fatalf("участники передачи названы неверно: %+v", item)
	}
	if len(item.ChangedFiles) != 2 {
		t.Fatalf("получатель должен видеть, что тронул предшественник: %+v", item.ChangedFiles)
	}
	if len(item.ChangeSetIDs) != 1 || item.ChangeSetIDs[0] != "cs-1" {
		t.Fatalf("набор изменений предшественника не привязан: %+v", item.ChangeSetIDs)
	}
	if !item.Delivered {
		t.Fatal("получатель уже начал работу — передача считается доставленной")
	}
}

// Узел, чей предшественник ещё не закончил, обязан быть виден как ждущий:
// пустая цепочка без объяснения читается как поломка.
func TestHandoffsReportWaitingNodesInsteadOfSilence(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())

	world := openTestWorld(t, application)
	ctx := context.Background()

	flow := domain.FlowGraph{
		ID: "flow-2", WorkspaceID: world.ID, Name: "Ожидание",
		Nodes: []domain.FlowNode{
			{ID: "n1", Kind: "agent", Name: "Первый"},
			{ID: "n2", Kind: "agent", Name: "Второй"},
		},
		Edges: []domain.FlowEdge{{From: "n1", To: "n2"}},
	}
	if err = application.store.SaveFlow(ctx, flow); err != nil {
		t.Fatal(err)
	}
	if err = application.store.SaveFlowRun(ctx, domain.FlowRun{
		ID: "fr-2", FlowID: flow.ID, WorkspaceID: world.ID, Status: domain.RunRunning,
		StartedAt: time.Now().UTC(),
		NodeStates: map[string]domain.FlowNodeState{
			"n1": {Status: "waiting_agent"},
			"n2": {Status: "blocked"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	chain, err := application.Handoffs(ctx, "fr-2")
	if err != nil {
		t.Fatal(err)
	}
	if len(chain.Items) != 0 {
		t.Fatalf("передач ещё не было: %+v", chain.Items)
	}
	if len(chain.Waiting) != 1 || chain.Waiting[0] != "Второй" {
		t.Fatalf("ждущий узел не назван: %+v", chain.Waiting)
	}
	if chain.Items == nil {
		t.Fatal("Items обязан быть пустым списком, а не nil: клиент рендерит его напрямую")
	}
}
