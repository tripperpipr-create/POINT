package flowruntime_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/flowruntime"
	"local-agent-workbench/internal/storage"
)

func TestPersistedFlowRuntimeCompletesLinearGraph(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().UTC()
	flow := domain.FlowGraph{
		ID: "flow-1", WorkspaceID: "ws", Name: "linear", CreatedAt: now, UpdatedAt: now,
		Nodes: []domain.FlowNode{
			{ID: "n1", Kind: domain.FlowNodeInput, Name: "in"},
			{ID: "n2", Kind: domain.FlowNodeAgent, Name: "agent", AgentID: "a1"},
			{ID: "n3", Kind: domain.FlowNodeOutput, Name: "out"},
		},
		Edges: []domain.FlowEdge{
			{ID: "e1", From: "n1", To: "n2"},
			{ID: "e2", From: "n2", To: "n3"},
		},
	}
	if err = store.SaveFlow(context.Background(), flow); err != nil {
		t.Fatal(err)
	}
	runtime := flowruntime.Runtime{Store: store}
	run, err := runtime.Start(context.Background(), flowruntime.StartRequest{
		FlowID: flow.ID, WorkspaceID: "ws", Input: map[string]any{"task": "demo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != domain.RunWaiting {
		t.Fatalf("expected waiting for agent node, got %s (%s)", run.Status, run.Error)
	}
	agentState := run.NodeStates["n2"]
	if agentState.Status != "waiting_agent" {
		t.Fatalf("agent node status=%s", agentState.Status)
	}
	run, err = runtime.CompleteAgentNode(context.Background(), run.ID, "n2", true, map[string]any{"ok": true})
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != domain.RunCompleted {
		t.Fatalf("expected completed after agent finish, got %s (%s)", run.Status, run.Error)
	}
	reloaded, err := store.GetFlowRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Status != domain.RunCompleted {
		t.Fatalf("persisted status = %s", reloaded.Status)
	}
}

func TestCompileLinearWorkflow(t *testing.T) {
	workflow := domain.AgentWorkflow{
		ID: "wf", Name: "legacy", Steps: []domain.WorkflowStep{
			{ID: "s1", Name: "one", ProfileID: "p1", Instruction: "do"},
		},
	}
	flow := flowruntime.CompileLinearWorkflow("ws", workflow)
	if flow.WorkspaceID != "ws" {
		t.Fatal("workspace missing")
	}
	kinds := map[domain.FlowNodeKind]int{}
	for _, node := range flow.Nodes {
		kinds[node.Kind]++
	}
	if kinds[domain.FlowNodeInput] != 1 || kinds[domain.FlowNodeAgent] != 1 || kinds[domain.FlowNodeOutput] != 1 {
		t.Fatalf("unexpected nodes: %+v", kinds)
	}
}

func TestFlowRunUsesImmutableGraphSnapshot(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "snapshot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	flow := domain.FlowGraph{
		ID: "flow-snapshot", WorkspaceID: "ws", Name: "snapshot", CreatedAt: now, UpdatedAt: now,
		Nodes: []domain.FlowNode{
			{ID: "input", Kind: domain.FlowNodeInput},
			{ID: "agent", Kind: domain.FlowNodeAgent, AgentID: "agent-1"},
			{ID: "output", Kind: domain.FlowNodeOutput},
		},
		Edges: []domain.FlowEdge{{ID: "e1", From: "input", To: "agent"}, {ID: "e2", From: "agent", To: "output"}},
	}
	if err = store.SaveFlow(context.Background(), flow); err != nil {
		t.Fatal(err)
	}
	runtime := flowruntime.Runtime{Store: store}
	run, err := runtime.Start(context.Background(), flowruntime.StartRequest{FlowID: flow.ID, WorkspaceID: "ws"})
	if err != nil {
		t.Fatal(err)
	}
	flow.Nodes = flow.Nodes[:2]
	flow.Edges = flow.Edges[:1]
	flow.UpdatedAt = time.Now().UTC()
	if err = store.SaveFlow(context.Background(), flow); err != nil {
		t.Fatal(err)
	}
	run, err = runtime.CompleteAgentNode(context.Background(), run.ID, "agent", true, map[string]any{
		"status": domain.RunCompleted, "result": "snapshot result",
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != domain.RunCompleted || !strings.Contains(run.Result, "snapshot result") {
		t.Fatalf("run=%#v", run)
	}
}

func TestConditionSkipsInactiveBranchAndVerifierChecksResults(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "condition.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	flow := domain.FlowGraph{
		ID: "flow-condition", WorkspaceID: "ws", Name: "condition", CreatedAt: now, UpdatedAt: now,
		Nodes: []domain.FlowNode{
			{ID: "input", Kind: domain.FlowNodeInput},
			{ID: "condition", Kind: domain.FlowNodeCondition, Config: map[string]any{"field": "approved"}},
			{ID: "pass", Kind: domain.FlowNodeAgent, AgentID: "agent-pass"},
			{ID: "fail", Kind: domain.FlowNodeAgent, AgentID: "agent-fail"},
			{ID: "join", Kind: domain.FlowNodeJoin},
			{ID: "verify", Kind: domain.FlowNodeVerifier},
			{ID: "output", Kind: domain.FlowNodeOutput},
		},
		Edges: []domain.FlowEdge{
			{ID: "e1", From: "input", To: "condition"},
			{ID: "e2", From: "condition", To: "pass", Condition: "true"},
			{ID: "e3", From: "condition", To: "fail", Condition: "false"},
			{ID: "e4", From: "pass", To: "join"},
			{ID: "e5", From: "fail", To: "join"},
			{ID: "e6", From: "join", To: "verify"},
			{ID: "e7", From: "verify", To: "output"},
		},
	}
	if err = store.SaveFlow(context.Background(), flow); err != nil {
		t.Fatal(err)
	}
	runtime := flowruntime.Runtime{Store: store}
	run, err := runtime.Start(context.Background(), flowruntime.StartRequest{
		FlowID: flow.ID, WorkspaceID: "ws", Input: map[string]any{"approved": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.NodeStates["pass"].Status != "waiting_agent" || run.NodeStates["fail"].Status != "skipped" {
		t.Fatalf("states=%#v", run.NodeStates)
	}
	run, err = runtime.CompleteAgentNode(context.Background(), run.ID, "pass", true, map[string]any{
		"status": domain.RunCompleted, "result": "accepted",
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != domain.RunCompleted {
		t.Fatalf("run=%#v", run)
	}
	if verified, _ := run.NodeStates["verify"].Output["verified"].(bool); !verified {
		t.Fatalf("verifier=%#v", run.NodeStates["verify"])
	}
}

func TestVerifierRejectsFailedUpstreamResult(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "verifier.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	flow := domain.FlowGraph{
		ID: "flow-verifier", WorkspaceID: "ws", Name: "verifier", CreatedAt: now, UpdatedAt: now,
		Nodes: []domain.FlowNode{
			{ID: "input", Kind: domain.FlowNodeInput},
			{ID: "agent", Kind: domain.FlowNodeAgent, AgentID: "agent-1"},
			{ID: "verify", Kind: domain.FlowNodeVerifier},
		},
		Edges: []domain.FlowEdge{{ID: "e1", From: "input", To: "agent"}, {ID: "e2", From: "agent", To: "verify"}},
	}
	if err = store.SaveFlow(context.Background(), flow); err != nil {
		t.Fatal(err)
	}
	runtime := flowruntime.Runtime{Store: store}
	run, err := runtime.Start(context.Background(), flowruntime.StartRequest{FlowID: flow.ID, WorkspaceID: "ws"})
	if err != nil {
		t.Fatal(err)
	}
	run, err = runtime.CompleteAgentNode(context.Background(), run.ID, "agent", true, map[string]any{
		"status": domain.RunFailed, "result": "bad result",
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != domain.RunFailed || run.NodeStates["verify"].Status != "failed" {
		t.Fatalf("run=%#v", run)
	}
}

func TestVerifierRequireResultChecksEveryParallelBranch(t *testing.T) {
	for _, test := range []struct {
		name       string
		second     string
		wantStatus domain.RunStatus
	}{
		{name: "all branch results", second: "independent review complete", wantStatus: domain.RunCompleted},
		{name: "empty branch result", second: "   ", wantStatus: domain.RunFailed},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, err := storage.Open(filepath.Join(t.TempDir(), "parallel-verifier.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			now := time.Now().UTC()
			flow := domain.FlowGraph{
				ID: "flow-parallel-verifier", WorkspaceID: "ws", Name: "parallel verifier", CreatedAt: now, UpdatedAt: now,
				Nodes: []domain.FlowNode{
					{ID: "input", Kind: domain.FlowNodeInput},
					{ID: "fork", Kind: domain.FlowNodeParallel},
					{ID: "a", Kind: domain.FlowNodeAgent, AgentID: "agent-a"},
					{ID: "b", Kind: domain.FlowNodeAgent, AgentID: "agent-b"},
					{ID: "join", Kind: domain.FlowNodeJoin},
					{ID: "verify", Kind: domain.FlowNodeVerifier, Config: map[string]any{"requireResult": true}},
					{ID: "output", Kind: domain.FlowNodeOutput},
				},
				Edges: []domain.FlowEdge{
					{ID: "e1", From: "input", To: "fork"},
					{ID: "e2", From: "fork", To: "a"},
					{ID: "e3", From: "fork", To: "b"},
					{ID: "e4", From: "a", To: "join"},
					{ID: "e5", From: "b", To: "join"},
					{ID: "e6", From: "join", To: "verify"},
					{ID: "e7", From: "verify", To: "output"},
				},
			}
			if err = store.SaveFlow(context.Background(), flow); err != nil {
				t.Fatal(err)
			}
			runtime := flowruntime.Runtime{Store: store}
			run, err := runtime.Start(context.Background(), flowruntime.StartRequest{FlowID: flow.ID, WorkspaceID: "ws"})
			if err != nil {
				t.Fatal(err)
			}
			run, err = runtime.CompleteAgentNode(context.Background(), run.ID, "a", true, map[string]any{
				"status": domain.RunCompleted, "result": "implementation complete",
			})
			if err != nil {
				t.Fatal(err)
			}
			if run.Status != domain.RunWaiting {
				t.Fatalf("parallel peer must still be pending: %#v", run)
			}
			run, err = runtime.CompleteAgentNode(context.Background(), run.ID, "b", true, map[string]any{
				"status": domain.RunCompleted, "result": test.second,
			})
			if err != nil {
				t.Fatal(err)
			}
			if run.Status != test.wantStatus {
				t.Fatalf("status=%s verifier=%#v", run.Status, run.NodeStates["verify"])
			}
			verified, _ := run.NodeStates["verify"].Output["verified"].(bool)
			if verified != (test.wantStatus == domain.RunCompleted) {
				t.Fatalf("verified=%v checks=%#v", verified, run.NodeStates["verify"].Output["checks"])
			}
		})
	}
}

func TestValidateGraphRejectsUnboundedCyclesAndInvalidLoopContracts(t *testing.T) {
	base := domain.FlowGraph{Nodes: []domain.FlowNode{
		{ID: "input", Kind: domain.FlowNodeInput},
		{ID: "output", Kind: domain.FlowNodeOutput},
	}, Edges: []domain.FlowEdge{{ID: "edge", From: "input", To: "output"}}}
	if err := flowruntime.ValidateGraph(base); err != nil {
		t.Fatalf("valid graph rejected: %v", err)
	}
	cycle := base
	cycle.Edges = append(append([]domain.FlowEdge(nil), base.Edges...), domain.FlowEdge{ID: "back", From: "output", To: "input"})
	if err := flowruntime.ValidateGraph(cycle); err == nil || !strings.Contains(err.Error(), "incoming") {
		t.Fatalf("input cycle should be rejected, got %v", err)
	}
	unreachable := base
	unreachable.Nodes = append(append([]domain.FlowNode(nil), base.Nodes...), domain.FlowNode{ID: "orphan", Kind: domain.FlowNodeOutput})
	if err := flowruntime.ValidateGraph(unreachable); err == nil || !strings.Contains(err.Error(), "unreachable") {
		t.Fatalf("unreachable node should be rejected, got %v", err)
	}
	loop := domain.FlowGraph{Nodes: []domain.FlowNode{{ID: "input", Kind: domain.FlowNodeInput}, {ID: "loop", Kind: domain.FlowNodeLoop}}, Edges: []domain.FlowEdge{{From: "input", To: "loop"}}}
	if err := flowruntime.ValidateGraph(loop); err == nil || !strings.Contains(err.Error(), "maxIterations") {
		t.Fatalf("unconfigured loop should be rejected, got %v", err)
	}
	unbounded := domain.FlowGraph{Nodes: []domain.FlowNode{
		{ID: "input", Kind: domain.FlowNodeInput}, {ID: "a", Kind: domain.FlowNodeCondition}, {ID: "b", Kind: domain.FlowNodeCondition},
	}, Edges: []domain.FlowEdge{{From: "input", To: "a"}, {From: "a", To: "b"}, {From: "b", To: "a"}}}
	if err := flowruntime.ValidateGraph(unbounded); err == nil || !strings.Contains(err.Error(), "unbounded cycle") {
		t.Fatalf("ordinary cycle should be rejected, got %v", err)
	}
}

func TestBoundedLoopRepeatsBodyAndCompletesAtLimit(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "loop.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	flow := domain.FlowGraph{
		ID: "flow-loop", WorkspaceID: "ws", Name: "bounded loop", CreatedAt: now, UpdatedAt: now,
		Nodes: []domain.FlowNode{
			{ID: "input", Kind: domain.FlowNodeInput},
			{ID: "loop", Kind: domain.FlowNodeLoop, Config: map[string]any{"maxIterations": 2}},
			{ID: "body", Kind: domain.FlowNodeAgent, AgentID: "agent-loop"},
			{ID: "output", Kind: domain.FlowNodeOutput},
		},
		Edges: []domain.FlowEdge{
			{ID: "entry", From: "input", To: "loop"},
			{ID: "continue", From: "loop", To: "body", Condition: "continue"},
			{ID: "back", From: "body", To: "loop"},
			{ID: "done", From: "loop", To: "output", Condition: "done"},
		},
	}
	if err = flowruntime.ValidateGraph(flow); err != nil {
		t.Fatalf("bounded loop rejected: %v", err)
	}
	if err = store.SaveFlow(context.Background(), flow); err != nil {
		t.Fatal(err)
	}
	runtime := flowruntime.Runtime{Store: store}
	run, err := runtime.Start(context.Background(), flowruntime.StartRequest{FlowID: flow.ID, WorkspaceID: "ws"})
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != domain.RunWaiting || run.NodeStates["body"].Status != "waiting_agent" || run.NodeStates["body"].Attempts != 1 {
		t.Fatalf("first iteration=%#v", run)
	}
	run, err = runtime.CompleteAgentNode(context.Background(), run.ID, "body", true, map[string]any{"result": "first"})
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != domain.RunWaiting || run.NodeStates["body"].Status != "waiting_agent" || run.NodeStates["body"].Attempts != 2 {
		t.Fatalf("second iteration=%#v", run)
	}
	run, err = runtime.CompleteAgentNode(context.Background(), run.ID, "body", true, map[string]any{"result": "second"})
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != domain.RunCompleted || run.NodeStates["loop"].Attempts != 3 {
		t.Fatalf("completed loop=%#v", run)
	}
	results, ok := run.NodeStates["loop"].Output["results"].([]any)
	if !ok || len(results) != 2 {
		t.Fatalf("loop results=%#v", run.NodeStates["loop"].Output)
	}
}

func TestParallelBranchFailureSkipsSiblingAndStaysTerminal(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "parallel-fail.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	flow := domain.FlowGraph{
		ID: "flow-parallel", WorkspaceID: "ws", Name: "critical", CreatedAt: now, UpdatedAt: now,
		Nodes: []domain.FlowNode{
			{ID: "input", Kind: domain.FlowNodeInput},
			{ID: "fork", Kind: domain.FlowNodeParallel},
			{ID: "a", Kind: domain.FlowNodeAgent, Name: "Model A", AgentID: "agent-a"},
			{ID: "b", Kind: domain.FlowNodeAgent, Name: "Model B", AgentID: "agent-b"},
			{ID: "join", Kind: domain.FlowNodeJoin},
			{ID: "output", Kind: domain.FlowNodeOutput},
		},
		Edges: []domain.FlowEdge{
			{ID: "e1", From: "input", To: "fork"},
			{ID: "e2", From: "fork", To: "a"},
			{ID: "e3", From: "fork", To: "b"},
			{ID: "e4", From: "a", To: "join"},
			{ID: "e5", From: "b", To: "join"},
			{ID: "e6", From: "join", To: "output"},
		},
	}
	if err = store.SaveFlow(context.Background(), flow); err != nil {
		t.Fatal(err)
	}
	runtime := flowruntime.Runtime{Store: store}
	run, err := runtime.Start(context.Background(), flowruntime.StartRequest{FlowID: flow.ID, WorkspaceID: "ws"})
	if err != nil {
		t.Fatal(err)
	}
	if run.NodeStates["a"].Status != "waiting_agent" || run.NodeStates["b"].Status != "waiting_agent" {
		t.Fatalf("expected both agents waiting, got %#v", run.NodeStates)
	}
	run, err = runtime.CompleteAgentNode(context.Background(), run.ID, "a", false, map[string]any{
		"error": "model A crashed", "status": domain.RunFailed,
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != domain.RunFailed {
		t.Fatalf("status=%s error=%s", run.Status, run.Error)
	}
	if !strings.Contains(run.Error, "model A crashed") {
		t.Fatalf("flow error=%q", run.Error)
	}
	if run.NodeStates["a"].Status != "failed" || run.NodeStates["a"].Error != "model A crashed" {
		t.Fatalf("failed node=%#v", run.NodeStates["a"])
	}
	if run.NodeStates["b"].Status != "skipped" {
		t.Fatalf("sibling should be skipped, got %#v", run.NodeStates["b"])
	}
	if run.NodeStates["join"].Status != "skipped" {
		t.Fatalf("join should be skipped after peer failure, got %#v", run.NodeStates["join"])
	}

	// Late sibling completion must not reopen the failed flow.
	before := run.Status
	run, err = runtime.CompleteAgentNode(context.Background(), run.ID, "b", true, map[string]any{"result": "too late"})
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != before || run.Status != domain.RunFailed {
		t.Fatalf("late completion resurrected flow: %s", run.Status)
	}
	if run.NodeStates["join"].Status == "completed" || run.NodeStates["output"].Status == "completed" {
		t.Fatalf("graph continued after terminal failure: %#v", run.NodeStates)
	}
}

func TestCompleteAgentNodePropagatesStructuredError(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "error-msg.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	flow := domain.FlowGraph{
		ID: "flow-err", WorkspaceID: "ws", Name: "err", CreatedAt: now, UpdatedAt: now,
		Nodes: []domain.FlowNode{
			{ID: "input", Kind: domain.FlowNodeInput},
			{ID: "agent", Kind: domain.FlowNodeAgent, AgentID: "a1"},
			{ID: "output", Kind: domain.FlowNodeOutput},
		},
		Edges: []domain.FlowEdge{
			{ID: "e1", From: "input", To: "agent"},
			{ID: "e2", From: "agent", To: "output"},
		},
	}
	if err = store.SaveFlow(context.Background(), flow); err != nil {
		t.Fatal(err)
	}
	runtime := flowruntime.Runtime{Store: store}
	run, err := runtime.Start(context.Background(), flowruntime.StartRequest{FlowID: flow.ID, WorkspaceID: "ws"})
	if err != nil {
		t.Fatal(err)
	}
	run, err = runtime.CompleteAgentNode(context.Background(), run.ID, "agent", false, map[string]any{
		"error": "quota exceeded",
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.NodeStates["agent"].Error != "quota exceeded" {
		t.Fatalf("error=%q", run.NodeStates["agent"].Error)
	}
	if run.Status != domain.RunFailed || !strings.Contains(run.Error, "quota exceeded") {
		t.Fatalf("flow=%s err=%q", run.Status, run.Error)
	}
}
