package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/flowruntime"
	"local-agent-workbench/internal/sandbox"
)

func TestFlowCreatesOnlyAgentNodeChildQuestsAndReusesChildForRetry(t *testing.T) {
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
	boot, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	agent := domain.ProjectAgentFromBlueprint(view.Workspace.ID, boot.Blueprints[0])
	agent.Provider, agent.ProviderPreset, agent.PrimaryModel = domain.ProviderOllama, "ollama", "qwen2.5-coder:7b"
	agent.BaseURL = "http://127.0.0.1:11434"
	agent, err = application.SaveProjectAgent(agent)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := application.SaveQuest(domain.Quest{Title: "Ship feature", Status: domain.QuestActive, BudgetTokens: 50000})
	if err != nil {
		t.Fatal(err)
	}
	flow, err := application.SaveFlow(domain.FlowGraph{
		Name: "Retry flow",
		Nodes: []domain.FlowNode{
			{ID: "input", Kind: domain.FlowNodeInput},
			{ID: "work", Kind: domain.FlowNodeAgent, Name: "Implement", AgentID: agent.ID, FailurePolicy: domain.FlowFailurePolicy{Mode: "retry", MaxRetries: 1}},
			{ID: "output", Kind: domain.FlowNodeOutput},
		},
		Edges: []domain.FlowEdge{{ID: "a", From: "input", To: "work"}, {ID: "b", From: "work", To: "output"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	flowRun, err := application.StartFlowRun(flow.ID, parent.ID, map[string]any{"task": parent.Title})
	if err != nil {
		t.Fatal(err)
	}
	quests, err := application.store.ListQuests(context.Background(), view.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	var child domain.Quest
	for _, quest := range quests {
		if quest.FlowRunID == flowRun.ID {
			if child.ID != "" {
				t.Fatalf("control nodes unexpectedly created child quests: %#v", quests)
			}
			child = quest
		}
	}
	if child.ParentID != parent.ID || child.FlowNodeID != "work" || child.AssignedAgentID != agent.ID || child.Status != domain.QuestActive {
		t.Fatalf("child quest links=%#v", child)
	}
	executions, err := application.store.ListExecutions(context.Background(), view.Workspace.ID, 20)
	if err != nil || len(executions) != 1 || executions[0].QuestID != child.ID {
		t.Fatalf("first attempt=%#v err=%v", executions, err)
	}
	first := executions[0]
	first.Status, first.Error = domain.RunFailed, "temporary provider outage"
	if err = application.store.SaveExecution(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	recoveredRun, recovered, err := application.recoverFlowNodeFailure(flowRun.ID, "work", first)
	if err != nil || !recovered {
		t.Fatalf("recovered=%v err=%v", recovered, err)
	}
	if err = application.scheduleFlowAgentExecutionsFromRun(recoveredRun); err != nil {
		t.Fatal(err)
	}
	executions, err = application.store.ListExecutions(context.Background(), view.Workspace.ID, 20)
	if err != nil || len(executions) != 2 {
		t.Fatalf("retry attempts=%#v err=%v", executions, err)
	}
	for _, execution := range executions {
		if execution.QuestID != child.ID || execution.Snapshot.SchemaVersion != 3 {
			t.Fatalf("retry escaped child/snapshot invariant: %#v", execution)
		}
	}
}

func TestFlowFallbackCreatesFreshExecutionWithReadyExplicitAgent(t *testing.T) {
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
	boot, _ := application.Bootstrap()
	primary := domain.ProjectAgentFromBlueprint(view.Workspace.ID, boot.Blueprints[0])
	primary.Provider, primary.ProviderPreset, primary.PrimaryModel = domain.ProviderOllama, "ollama", "qwen2.5-coder:7b"
	primary.BaseURL = "http://127.0.0.1:11434"
	primary.Name = "Primary"
	primary, err = application.SaveProjectAgent(primary)
	if err != nil {
		t.Fatal(err)
	}
	fallback := domain.ProjectAgentFromBlueprint(view.Workspace.ID, boot.Blueprints[0])
	fallback.Provider, fallback.ProviderPreset, fallback.PrimaryModel = domain.ProviderOllama, "ollama", "qwen2.5-coder:7b"
	fallback.BaseURL = "http://127.0.0.1:11434"
	fallback.Name = "Fallback"
	fallback, err = application.SaveProjectAgent(fallback)
	if err != nil {
		t.Fatal(err)
	}
	parent, _ := application.SaveQuest(domain.Quest{Title: "Fallback", Status: domain.QuestActive})
	flow, err := application.SaveFlow(domain.FlowGraph{
		Name: "Fallback flow",
		Nodes: []domain.FlowNode{
			{ID: "input", Kind: domain.FlowNodeInput},
			{ID: "work", Kind: domain.FlowNodeAgent, AgentID: primary.ID, FailurePolicy: domain.FlowFailurePolicy{Mode: "fallback_agent", FallbackAgentID: fallback.ID}},
			{ID: "output", Kind: domain.FlowNodeOutput},
		},
		Edges: []domain.FlowEdge{{From: "input", To: "work"}, {From: "work", To: "output"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	flowRun, err := application.StartFlowRun(flow.ID, parent.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	executions, _ := application.store.ListExecutions(context.Background(), view.Workspace.ID, 20)
	first := executions[0]
	first.Status, first.Error = domain.RunFailed, "temporary transport error"
	_ = application.store.SaveExecution(context.Background(), first)
	recoveredRun, recovered, err := application.recoverFlowNodeFailure(flowRun.ID, "work", first)
	if err != nil || !recovered {
		t.Fatalf("recovered=%v err=%v", recovered, err)
	}
	if err = application.scheduleFlowAgentExecutionsFromRun(recoveredRun); err != nil {
		t.Fatal(err)
	}
	executions, _ = application.store.ListExecutions(context.Background(), view.Workspace.ID, 20)
	foundFallback := false
	for _, execution := range executions {
		foundFallback = foundFallback || execution.ProjectAgentID == fallback.ID
	}
	if !foundFallback {
		t.Fatalf("fallback execution missing: %#v", executions)
	}
}

func TestFlowRescheduleReusesRememberedOrchestratorAPIKey(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	started := make(chan string, 2)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case started <- r.Header.Get("Authorization"):
		default:
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"done\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer provider.Close()

	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	if _, err = application.OpenWorkspace(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	agentItem, err := application.SaveProjectAgent(domain.ProjectAgent{
		Name: "Cloud", Provider: domain.ProviderOpenAI, ProviderPreset: "openai",
		BaseURL: provider.URL, PrimaryModel: "test-model", MaxSteps: 2, MaxDurationSeconds: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	flow, err := application.SaveFlow(domain.FlowGraph{
		Name: "Two stages",
		Nodes: []domain.FlowNode{
			{ID: "input", Kind: domain.FlowNodeInput},
			{ID: "first", Kind: domain.FlowNodeAgent, AgentID: agentItem.ID},
			{ID: "second", Kind: domain.FlowNodeAgent, AgentID: agentItem.ID},
			{ID: "output", Kind: domain.FlowNodeOutput},
		},
		Edges: []domain.FlowEdge{
			{From: "input", To: "first"}, {From: "first", To: "second"}, {From: "second", To: "output"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	quest, err := application.SaveQuest(domain.Quest{Title: "Reuse key", Status: domain.QuestActive, BudgetTokens: 20000})
	if err != nil {
		t.Fatal(err)
	}
	runtime := flowruntime.Runtime{Store: application.store}
	flowRun, err := runtime.Start(context.Background(), flowruntime.StartRequest{
		FlowID: flow.ID, WorkspaceID: quest.WorkspaceID, QuestID: quest.ID,
		Input: map[string]any{"task": quest.Title},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = application.scheduleFlowAgentExecutions(quest, flow, flowRun, "orchestrator-secret"); err != nil {
		t.Fatal(err)
	}
	select {
	case auth := <-started:
		if auth != "Bearer orchestrator-secret" {
			t.Fatalf("first auth=%q", auth)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("first stage did not start")
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		current, getErr := application.store.GetFlowRun(context.Background(), flowRun.ID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if state := current.NodeStates["second"]; state.Status != "" {
			if reason, _ := state.Output["waitReason"].(string); reason == "waiting_api_key" {
				t.Fatalf("second stage lost orchestrator credential: %#v", state.Output)
			}
			if state.Output["executionId"] != nil || state.Status == "running" || state.Status == "completed" {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	current, _ := application.store.GetFlowRun(context.Background(), flowRun.ID)
	t.Fatalf("second stage did not auto-start: %#v", current.NodeStates)
}

func TestCompletedFlowAgentPersistsExecutionIDAndSeedsNextSandbox(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Stage complete.\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer provider.Close()
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	root := t.TempDir()
	if err = os.WriteFile(filepath.Join(root, "state.txt"), []byte("live"), 0o644); err != nil {
		t.Fatal(err)
	}
	view, err := application.OpenWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	makeAgent := func(name string) domain.ProjectAgent {
		agent, saveErr := application.SaveProjectAgent(domain.ProjectAgent{
			Name: name, Provider: domain.ProviderOpenAI, ProviderPreset: "openai", BaseURL: provider.URL,
			PrimaryModel: "test-model", MaxSteps: 2, MaxDurationSeconds: 5,
		})
		if saveErr != nil {
			t.Fatal(saveErr)
		}
		return agent
	}
	firstAgent := makeAgent("First")
	secondAgent := makeAgent("Second")
	flow, err := application.SaveFlow(domain.FlowGraph{
		Name: "Sequential lineage",
		Nodes: []domain.FlowNode{
			{ID: "input", Kind: domain.FlowNodeInput},
			{ID: "first", Kind: domain.FlowNodeAgent, AgentID: firstAgent.ID},
			{ID: "second", Kind: domain.FlowNodeAgent, AgentID: secondAgent.ID},
			{ID: "output", Kind: domain.FlowNodeOutput},
		},
		Edges: []domain.FlowEdge{{From: "input", To: "first"}, {From: "first", To: "second"}, {From: "second", To: "output"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	flowRun, err := application.StartFlowRun(flow.ID, "", map[string]any{"task": "lineage"})
	if err != nil {
		t.Fatal(err)
	}
	executions, err := application.store.ListExecutions(context.Background(), view.Workspace.ID, 20)
	if err != nil || len(executions) != 1 {
		t.Fatalf("initial executions=%#v err=%v", executions, err)
	}
	parent := executions[0]
	parentSandbox, err := application.store.GetSandbox(context.Background(), parent.SandboxID)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(parentSandbox.Path, "state.txt"), []byte("stage-one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err = application.LaunchPendingExecution(parent.ID, "flow-secret"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(4 * time.Second)
	var child domain.ExecutionInstance
	for time.Now().Before(deadline) {
		executions, err = application.store.ListExecutions(context.Background(), view.Workspace.ID, 20)
		if err != nil {
			t.Fatal(err)
		}
		for _, execution := range executions {
			if execution.FlowNodeID == "second" {
				child = execution
				break
			}
		}
		if child.ID != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if child.ID == "" {
		t.Fatalf("child execution missing all=%#v", executions)
	}
	reloaded, err := application.store.GetFlowRun(context.Background(), flowRun.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.NodeStates["first"].Output["executionId"] != parent.ID {
		t.Fatalf("first output=%#v parent=%s", reloaded.NodeStates["first"].Output, parent.ID)
	}
	if reloaded.NodeStates["second"].Output["seedExecutionId"] != parent.ID || reloaded.NodeStates["second"].Output["sandboxLineage"] != "inherited" {
		t.Fatalf("second lineage=%#v", reloaded.NodeStates["second"].Output)
	}
	if reason, _ := reloaded.NodeStates["second"].Output["waitReason"].(string); reason == "waiting_api_key" {
		t.Fatalf("second stage should reuse the launched flow credential, waitReason=%q", reason)
	}
	childSandbox, err := application.store.GetSandbox(context.Background(), child.SandboxID)
	if err != nil {
		t.Fatal(err)
	}
	if childSandbox.ParentExecutionID != parent.ID || childSandbox.BaselinePath == "" {
		t.Fatalf("child sandbox=%#v", childSandbox)
	}
	seeded, err := os.ReadFile(filepath.Join(childSandbox.Path, "state.txt"))
	if err != nil || string(seeded) != "stage-one" {
		t.Fatalf("seeded state=%q err=%v", seeded, err)
	}
	live, err := os.ReadFile(filepath.Join(root, "state.txt"))
	if err != nil || string(live) != "live" {
		t.Fatalf("live state=%q err=%v", live, err)
	}
}

func TestSequentialExecutionSandboxAndChangeSetLineage(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	root := t.TempDir()
	filePath := filepath.Join(root, "state.txt")
	if err = os.WriteFile(filePath, []byte("live"), 0o644); err != nil {
		t.Fatal(err)
	}
	workspaceRecord, err := application.OpenWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	boot, err := application.Bootstrap()
	if err != nil || len(boot.Blueprints) == 0 {
		t.Fatalf("bootstrap blueprints=%d err=%v", len(boot.Blueprints), err)
	}
	firstAgent := domain.ProjectAgentFromBlueprint(workspaceRecord.Workspace.ID, boot.Blueprints[0])
	firstAgent.Name = "Implementer"
	firstAgent, err = application.SaveProjectAgent(firstAgent)
	if err != nil {
		t.Fatal(err)
	}
	secondAgent := domain.ProjectAgentFromBlueprint(workspaceRecord.Workspace.ID, boot.Blueprints[0])
	secondAgent.Name = "Reviewer"
	secondAgent, err = application.SaveProjectAgent(secondAgent)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := application.StartSandboxedExecution(firstAgent.ID, "stage one", "")
	if err != nil {
		t.Fatal(err)
	}
	parentSandbox, err := application.store.GetSandbox(context.Background(), parent.SandboxID)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(parentSandbox.Path, "state.txt"), []byte("stage-one"), 0o644); err != nil {
		t.Fatal(err)
	}
	parent.Status = domain.RunCompleted
	now := time.Now().UTC()
	parent.FinishedAt = &now
	if err = application.store.SaveExecution(context.Background(), parent); err != nil {
		t.Fatal(err)
	}
	parentSet, err := application.BuildChangeSet(parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	child, err := application.startSandboxedExecution(secondAgent.ID, "stage two", "", parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	childSandbox, err := application.store.GetSandbox(context.Background(), child.SandboxID)
	if err != nil {
		t.Fatal(err)
	}
	seeded, err := os.ReadFile(filepath.Join(childSandbox.Path, "state.txt"))
	if err != nil || string(seeded) != "stage-one" {
		t.Fatalf("child seed=%q err=%v", seeded, err)
	}
	if err = os.WriteFile(filepath.Join(childSandbox.Path, "state.txt"), []byte("stage-two"), 0o644); err != nil {
		t.Fatal(err)
	}
	child.Status = domain.RunCompleted
	child.FinishedAt = &now
	if err = application.store.SaveExecution(context.Background(), child); err != nil {
		t.Fatal(err)
	}
	childSet, err := application.BuildChangeSet(child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(childSet.DependsOn) != 1 || childSet.DependsOn[0] != parentSet.ID {
		t.Fatalf("child dependencies=%v parent=%s", childSet.DependsOn, parentSet.ID)
	}
	if len(childSet.Items) != 1 || childSet.Items[0].OriginalContent != "stage-one" || childSet.Items[0].ProposedContent != "stage-two" {
		t.Fatalf("child incremental set=%#v", childSet.Items)
	}
	if _, err = application.ApplyChangeSet(childSet.ID); err == nil || !strings.Contains(err.Error(), "prerequisite") {
		t.Fatalf("dependent set applied before parent: %v", err)
	}
	if _, err = application.ApplyChangeSet(parentSet.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = application.ApplyChangeSet(childSet.ID); err != nil {
		t.Fatal(err)
	}
	live, err := os.ReadFile(filePath)
	if err != nil || string(live) != "stage-two" {
		t.Fatalf("applied chain=%q err=%v", live, err)
	}
	if _, err = application.RevertChangeSet(parentSet.ID); err == nil || !strings.Contains(err.Error(), "dependent") {
		t.Fatalf("parent reverted before child: %v", err)
	}
	if _, err = application.RevertChangeSet(childSet.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = application.RevertChangeSet(parentSet.ID); err != nil {
		t.Fatal(err)
	}
	live, err = os.ReadFile(filePath)
	if err != nil || string(live) != "live" {
		t.Fatalf("reverted chain=%q err=%v", live, err)
	}
}

func TestParallelSandboxMergeCreatesAggregateChangeSet(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	root := t.TempDir()
	for path, content := range map[string]string{"a.txt": "a-base", "b.txt": "b-base"} {
		if err = os.WriteFile(filepath.Join(root, path), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	view, err := application.OpenWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	makeAgent := func(name string) domain.ProjectAgent {
		agent, saveErr := application.SaveProjectAgent(domain.ProjectAgent{
			Name: name, Provider: domain.ProviderOpenAI, ProviderPreset: "openai", PrimaryModel: "test-model",
			MaxSteps: 2, MaxDurationSeconds: 5,
		})
		if saveErr != nil {
			t.Fatal(saveErr)
		}
		return agent
	}
	branchAgent := makeAgent("Branch")
	mergeAgent := makeAgent("Integrator")
	branchA, err := application.StartSandboxedExecution(branchAgent.ID, "Branch A", "")
	if err != nil {
		t.Fatal(err)
	}
	branchB, err := application.StartSandboxedExecution(branchAgent.ID, "Branch B", "")
	if err != nil {
		t.Fatal(err)
	}
	for index, branch := range []*domain.ExecutionInstance{&branchA, &branchB} {
		record, loadErr := application.store.GetSandbox(context.Background(), branch.SandboxID)
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		path, content := "a.txt", "a-branch"
		if index == 1 {
			path, content = "b.txt", "b-branch"
		}
		if err = os.WriteFile(filepath.Join(record.Path, path), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		branch.Status, branch.FinishedAt = domain.RunCompleted, &now
		if err = application.store.SaveExecution(context.Background(), *branch); err != nil {
			t.Fatal(err)
		}
	}
	setA, err := application.BuildChangeSet(branchA.ID)
	if err != nil {
		t.Fatal(err)
	}
	setB, err := application.BuildChangeSet(branchB.ID)
	if err != nil {
		t.Fatal(err)
	}
	mergedExec, merged, mergeSet, err := application.startMergedSandboxedExecution(
		mergeAgent.ID, "Integrate", "", "flow-run", "integrator", []string{branchA.ID, branchB.ID}, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if mergedExec.ID == "" || len(merged.Conflicts) != 0 || mergeSet == nil || mergeSet.Kind != "merge" {
		t.Fatalf("exec=%#v merged=%#v mergeSet=%#v", mergedExec, merged, mergeSet)
	}
	if len(mergeSet.Supersedes) != 2 || len(mergeSet.Items) != 2 {
		t.Fatalf("aggregate merge set=%#v", mergeSet)
	}
	for _, sourceID := range []string{setA.ID, setB.ID} {
		source, loadErr := application.store.GetChangeSet(context.Background(), sourceID)
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		if source.Status != domain.ChangeSetSuperseded || source.SupersededBy != mergeSet.ID {
			t.Fatalf("source set not superseded: %#v", source)
		}
		if _, applyErr := application.ApplyChangeSet(source.ID); applyErr == nil {
			t.Fatalf("superseded source %s remained independently applicable", source.ID)
		}
	}
	mergedRecord, err := application.store.GetSandbox(context.Background(), mergedExec.SandboxID)
	if err != nil {
		t.Fatal(err)
	}
	if len(mergedRecord.ParentExecutionIDs) != 2 || len(mergedRecord.BaselineChangeSetIDs) != 1 || mergedRecord.BaselineChangeSetIDs[0] != mergeSet.ID {
		t.Fatalf("merged lineage=%#v", mergedRecord)
	}
	for path, expected := range map[string]string{"a.txt": "a-branch", "b.txt": "b-branch"} {
		content, readErr := os.ReadFile(filepath.Join(mergedRecord.Path, path))
		if readErr != nil || string(content) != expected {
			t.Fatalf("merged %s=%q err=%v", path, content, readErr)
		}
	}
	if _, err = application.ApplyChangeSet(mergeSet.ID); err != nil {
		t.Fatal(err)
	}
	for path, expected := range map[string]string{"a.txt": "a-branch", "b.txt": "b-branch"} {
		content, readErr := os.ReadFile(filepath.Join(root, path))
		if readErr != nil || string(content) != expected {
			t.Fatalf("live %s=%q err=%v workspace=%s", path, content, readErr, view.Workspace.ID)
		}
	}
	if err = os.WriteFile(filepath.Join(mergedRecord.Path, "a.txt"), []byte("integrated"), 0o644); err != nil {
		t.Fatal(err)
	}
	childSet, err := application.BuildChangeSet(mergedExec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(childSet.DependsOn) != 1 || childSet.DependsOn[0] != mergeSet.ID || len(childSet.Items) != 1 {
		t.Fatalf("post-merge child set=%#v", childSet)
	}
}

func TestParallelFlowWaitsForExplicitSandboxMergeResolution(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	root := t.TempDir()
	if err = os.WriteFile(filepath.Join(root, "shared.txt"), []byte("base"), 0o644); err != nil {
		t.Fatal(err)
	}
	view, err := application.OpenWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	makeAgent := func(name string) domain.ProjectAgent {
		agent, saveErr := application.SaveProjectAgent(domain.ProjectAgent{
			Name: name, Provider: domain.ProviderOpenAI, ProviderPreset: "openai", PrimaryModel: "test-model",
			MaxSteps: 2, MaxDurationSeconds: 5,
		})
		if saveErr != nil {
			t.Fatal(saveErr)
		}
		return agent
	}
	agentA, agentB, integrator := makeAgent("A"), makeAgent("B"), makeAgent("Integrator")
	flow, err := application.SaveFlow(domain.FlowGraph{
		Name: "Parallel merge resolution",
		Nodes: []domain.FlowNode{
			{ID: "input", Kind: domain.FlowNodeInput},
			{ID: "parallel", Kind: domain.FlowNodeParallel},
			{ID: "a", Kind: domain.FlowNodeAgent, AgentID: agentA.ID},
			{ID: "b", Kind: domain.FlowNodeAgent, AgentID: agentB.ID},
			{ID: "join", Kind: domain.FlowNodeJoin},
			{ID: "integrate", Kind: domain.FlowNodeAgent, AgentID: integrator.ID},
			{ID: "output", Kind: domain.FlowNodeOutput},
		},
		Edges: []domain.FlowEdge{
			{From: "input", To: "parallel"}, {From: "parallel", To: "a"}, {From: "parallel", To: "b"},
			{From: "a", To: "join"}, {From: "b", To: "join"}, {From: "join", To: "integrate"}, {From: "integrate", To: "output"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	flowRun, err := application.StartFlowRun(flow.ID, "", map[string]any{"task": "merge"})
	if err != nil {
		t.Fatal(err)
	}
	executions, err := application.store.ListExecutions(context.Background(), view.Workspace.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	byNode := map[string]domain.ExecutionInstance{}
	for _, execution := range executions {
		byNode[execution.FlowNodeID] = execution
	}
	if byNode["a"].ID == "" || byNode["b"].ID == "" {
		t.Fatalf("parallel executions=%#v", executions)
	}
	runtime := flowruntime.Runtime{Store: application.store}
	current := flowRun
	for _, nodeID := range []string{"a", "b"} {
		execution := byNode[nodeID]
		record, loadErr := application.store.GetSandbox(context.Background(), execution.SandboxID)
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		if err = os.WriteFile(filepath.Join(record.Path, "shared.txt"), []byte("from-"+nodeID), 0o644); err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		execution.Status, execution.FinishedAt = domain.RunCompleted, &now
		if err = application.store.SaveExecution(context.Background(), execution); err != nil {
			t.Fatal(err)
		}
		if _, err = application.BuildChangeSet(execution.ID); err != nil {
			t.Fatal(err)
		}
		current, err = runtime.CompleteAgentNode(context.Background(), flowRun.ID, nodeID, true, map[string]any{
			"executionId": execution.ID, "result": "done " + nodeID,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = application.scheduleFlowAgentExecutionsFromRun(current); err != nil {
		t.Fatal(err)
	}
	waiting, err := application.store.GetFlowRun(context.Background(), flowRun.ID)
	if err != nil {
		t.Fatal(err)
	}
	state := waiting.NodeStates["integrate"]
	conflictCount := 0
	switch value := state.Output["mergeConflictCount"].(type) {
	case int:
		conflictCount = value
	case float64:
		conflictCount = int(value)
	}
	if state.Output["waitReason"] != "sandbox_merge_conflict" || conflictCount != 1 {
		t.Fatalf("merge conflict state=%#v", state)
	}
	if _, exists := state.Output["executionId"]; exists {
		t.Fatalf("integrator execution created before conflict resolution: %#v", state.Output)
	}
	resolved, err := application.ResolveFlowSandboxMerge(flowRun.ID, "integrate", sandbox.MergeResolution{
		Path: "shared.txt", Strategy: "use_parent", ExecutionID: byNode["b"].ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	state = resolved.NodeStates["integrate"]
	if state.Output["sandboxLineage"] != "merged_parallel_join" || state.Output["executionId"] == nil {
		t.Fatalf("resolved lineage=%#v", state.Output)
	}
	integratorExec, err := application.findExecution(fmt.Sprint(state.Output["executionId"]))
	if err != nil {
		t.Fatal(err)
	}
	integratorSandbox, err := application.store.GetSandbox(context.Background(), integratorExec.SandboxID)
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(integratorSandbox.Path, "shared.txt"))
	if err != nil || string(content) != "from-b" {
		t.Fatalf("resolved sandbox=%q err=%v", content, err)
	}
}

func TestParallelRootBranchesReuseOneImmutableFlowSnapshot(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	root := t.TempDir()
	path := filepath.Join(root, "state.txt")
	if err = os.WriteFile(path, []byte("flow-start"), 0o644); err != nil {
		t.Fatal(err)
	}
	view, err := application.OpenWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := application.SaveProjectAgent(domain.ProjectAgent{
		Name: "Branch", Provider: domain.ProviderOpenAI, ProviderPreset: "openai", PrimaryModel: "test-model",
		MaxSteps: 2, MaxDurationSeconds: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := application.startSandboxedExecutionWithSeed(agent.ID, "A", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	first.FlowRunID = "flow-run"
	first.FlowNodeID = "a"
	if err = application.store.SaveExecution(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("user-edited-during-flow"), 0o644); err != nil {
		t.Fatal(err)
	}
	flowRun := domain.FlowRun{ID: "flow-run", WorkspaceID: view.Workspace.ID}
	seed := application.rootFlowSeedPath(flowRun)
	if seed == "" {
		t.Fatal("flow root seed was not found")
	}
	second, err := application.startSandboxedExecutionWithSeed(agent.ID, "B", "", "", seed)
	if err != nil {
		t.Fatal(err)
	}
	secondRecord, err := application.store.GetSandbox(context.Background(), second.SandboxID)
	if err != nil {
		t.Fatal(err)
	}
	for _, snapshotPath := range []string{secondRecord.Path, secondRecord.BaselinePath} {
		content, readErr := os.ReadFile(filepath.Join(snapshotPath, "state.txt"))
		if readErr != nil || string(content) != "flow-start" {
			t.Fatalf("branch forked from live drift: %s=%q err=%v", snapshotPath, content, readErr)
		}
	}
}

func TestResumeActiveFlowReusesInterruptedExecution(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	workspaceRoot := t.TempDir()
	view, err := application.OpenWorkspace(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	projectAgent, err := application.SaveProjectAgent(domain.ProjectAgent{
		Name: "Cloud Agent", Provider: domain.ProviderOpenAI, ProviderPreset: "openai",
		BaseURL: "https://api.openai.com/v1", PrimaryModel: "test-model", MaxSteps: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	flow, err := application.SaveFlow(domain.FlowGraph{
		Name: "Resume flow",
		Nodes: []domain.FlowNode{
			{ID: "input", Kind: domain.FlowNodeInput},
			{ID: "agent", Kind: domain.FlowNodeAgent, AgentID: projectAgent.ID},
			{ID: "output", Kind: domain.FlowNodeOutput},
		},
		Edges: []domain.FlowEdge{{ID: "e1", From: "input", To: "agent"}, {ID: "e2", From: "agent", To: "output"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	flowRun, err := application.StartFlowRun(flow.ID, "", map[string]any{"task": "resume"})
	if err != nil {
		t.Fatal(err)
	}
	executions, err := application.store.ListExecutions(context.Background(), view.Workspace.ID, 20)
	if err != nil || len(executions) != 1 {
		t.Fatalf("executions=%#v err=%v", executions, err)
	}
	stale := executions[0]
	stale.Status = domain.RunRunning
	stale.RunID = "stale-run"
	if err = application.store.SaveExecution(context.Background(), stale); err != nil {
		t.Fatal(err)
	}
	if err = application.ResumeActiveFlowRuns(view.Workspace.ID); err != nil {
		t.Fatal(err)
	}
	executions, err = application.store.ListExecutions(context.Background(), view.Workspace.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(executions) != 1 || executions[0].ID != stale.ID || executions[0].Status != domain.RunInterrupted {
		t.Fatalf("executions=%#v", executions)
	}
	reloaded, err := application.store.GetFlowRun(context.Background(), flowRun.ID)
	if err != nil || reloaded.NodeStates["agent"].Output["executionId"] != stale.ID {
		t.Fatalf("flowRun=%#v err=%v", reloaded, err)
	}
}

func TestToolFlowNodeCreatesSandboxedSingleToolExecution(t *testing.T) {
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
	projectAgent, err := application.SaveProjectAgent(domain.ProjectAgent{
		Name: "Tool Agent", Provider: domain.ProviderOpenAI, ProviderPreset: "openai",
		BaseURL: "https://api.openai.com/v1", PrimaryModel: "test-model", MaxSteps: 20,
		AllowedTools: []string{"list_files"},
	})
	if err != nil {
		t.Fatal(err)
	}
	flow, err := application.SaveFlow(domain.FlowGraph{
		Name: "Tool flow",
		Nodes: []domain.FlowNode{
			{ID: "input", Kind: domain.FlowNodeInput},
			{ID: "tool", Kind: domain.FlowNodeTool, AgentID: projectAgent.ID, ToolName: "list_files", Config: map[string]any{"arguments": map[string]any{"maxDepth": 2}}},
			{ID: "output", Kind: domain.FlowNodeOutput},
		},
		Edges: []domain.FlowEdge{{ID: "e1", From: "input", To: "tool"}, {ID: "e2", From: "tool", To: "output"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	flowRun, err := application.StartFlowRun(flow.ID, "", map[string]any{"task": "inspect"})
	if err != nil {
		t.Fatal(err)
	}
	executions, err := application.store.ListExecutions(context.Background(), view.Workspace.ID, 20)
	if err != nil || len(executions) != 1 {
		t.Fatalf("executions=%#v err=%v", executions, err)
	}
	if executions[0].FlowRunID != flowRun.ID || executions[0].FlowNodeID != "tool" ||
		executions[0].SandboxID == "" || executions[0].Status != domain.RunCompleted || executions[0].Task != "Tool: list_files" {
		t.Fatalf("execution=%#v", executions[0])
	}
	if flowRun.Status != domain.RunCompleted || flowRun.NodeStates["tool"].Status != "completed" {
		t.Fatalf("deterministic tool did not complete the flow: %#v", flowRun)
	}
}

func TestBoundedLoopSchedulesFreshToolExecutionPerIteration(t *testing.T) {
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
	projectAgent, err := application.SaveProjectAgent(domain.ProjectAgent{
		Name: "Loop Tool Agent", Provider: domain.ProviderOpenAI, ProviderPreset: "openai",
		BaseURL: "https://api.openai.com/v1", PrimaryModel: "test-model", MaxSteps: 20,
		AllowedTools: []string{"list_files"},
	})
	if err != nil {
		t.Fatal(err)
	}
	flow, err := application.SaveFlow(domain.FlowGraph{
		Name: "Bounded tool loop",
		Nodes: []domain.FlowNode{
			{ID: "input", Kind: domain.FlowNodeInput},
			{ID: "loop", Kind: domain.FlowNodeLoop, Config: map[string]any{"maxIterations": 2}},
			{ID: "tool", Kind: domain.FlowNodeTool, AgentID: projectAgent.ID, ToolName: "list_files", Config: map[string]any{"arguments": map[string]any{"maxDepth": 1}}},
			{ID: "output", Kind: domain.FlowNodeOutput},
		},
		Edges: []domain.FlowEdge{
			{ID: "entry", From: "input", To: "loop"},
			{ID: "continue", From: "loop", To: "tool", Condition: "continue"},
			{ID: "back", From: "tool", To: "loop"},
			{ID: "done", From: "loop", To: "output", Condition: "done"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	flowRun, err := application.StartFlowRun(flow.ID, "", map[string]any{"task": "inspect twice"})
	if err != nil {
		t.Fatal(err)
	}
	if flowRun.Status != domain.RunCompleted || flowRun.NodeStates["loop"].Attempts != 3 || flowRun.NodeStates["tool"].Attempts != 2 {
		t.Fatalf("bounded loop did not complete deterministically: %#v", flowRun)
	}
	executions, err := application.store.ListExecutions(context.Background(), view.Workspace.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(executions) != 2 {
		t.Fatalf("expected one fresh sandboxed execution per iteration, got %#v", executions)
	}
	for _, execution := range executions {
		if execution.FlowRunID != flowRun.ID || execution.FlowNodeID != "tool" || execution.SandboxID == "" || execution.Status != domain.RunCompleted {
			t.Fatalf("invalid loop execution: %#v", execution)
		}
	}
}
