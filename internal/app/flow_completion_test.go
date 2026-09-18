package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/flowruntime"
)

func TestFlowFailureFinalizesQuestAndSkipsScheduling(t *testing.T) {
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
	primary, err := application.SaveProjectAgent(domain.ProjectAgent{
		Name: "Primary", Provider: domain.ProviderOllama, ProviderPreset: "ollama", PrimaryModel: "test", MaxSteps: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	reviewer, err := application.SaveProjectAgent(domain.ProjectAgent{
		Name: "Reviewer", Provider: domain.ProviderOllama, ProviderPreset: "ollama", PrimaryModel: "test", MaxSteps: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	flow, err := application.SaveFlow(domain.FlowGraph{
		Name: "Parallel fail",
		Nodes: []domain.FlowNode{
			{ID: "input", Kind: domain.FlowNodeInput},
			{ID: "fork", Kind: domain.FlowNodeParallel},
			{ID: "a", Kind: domain.FlowNodeAgent, Name: "A", AgentID: primary.ID},
			{ID: "b", Kind: domain.FlowNodeAgent, Name: "B", AgentID: reviewer.ID},
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
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	quest := domain.Quest{
		ID: domain.NewID("quest"), WorkspaceID: view.Workspace.ID, Title: "Ship",
		Status: domain.QuestActive, FlowID: flow.ID, CreatedAt: now, UpdatedAt: now,
	}
	if err = application.store.SaveQuest(context.Background(), quest); err != nil {
		t.Fatal(err)
	}
	flowRun, err := application.StartFlowRun(flow.ID, quest.ID, map[string]any{"task": "parallel"})
	if err != nil {
		t.Fatal(err)
	}
	pending := domain.ExecutionInstance{
		ID: domain.NewID("exec"), WorkspaceID: view.Workspace.ID, ProjectAgentID: reviewer.ID,
		QuestID: quest.ID, FlowRunID: flowRun.ID, FlowNodeID: "b", Task: "B",
		Status: domain.RunPending, StartedAt: now,
	}
	if err = application.store.SaveExecution(context.Background(), pending); err != nil {
		t.Fatal(err)
	}

	runtime := flowruntime.Runtime{Store: application.store}
	flowRun, err = runtime.CompleteAgentNode(context.Background(), flowRun.ID, "a", false, map[string]any{
		"error": "branch A failed", "status": domain.RunFailed,
	})
	if err != nil {
		t.Fatal(err)
	}
	if flowRun.Status != domain.RunFailed {
		t.Fatalf("flow status=%s", flowRun.Status)
	}
	application.cancelSiblingFlowExecutions(flowRun.ID, "")
	application.finalizeQuestAfterFlow(quest.ID, false)

	quests, err := application.store.ListQuests(context.Background(), view.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	var savedQuest domain.Quest
	for _, item := range quests {
		if item.ID == quest.ID {
			savedQuest = item
			break
		}
	}
	if savedQuest.ID == "" || savedQuest.Status != domain.QuestFailed {
		t.Fatalf("quest=%#v", savedQuest)
	}
	exec, err := application.findExecution(pending.ID)
	if err != nil {
		t.Fatal(err)
	}
	if exec.Status != domain.RunCancelled {
		t.Fatalf("sibling execution status=%s", exec.Status)
	}
	if err = application.scheduleFlowAgentExecutionsFromRun(flowRun); err != nil {
		t.Fatal(err)
	}
}

func TestLaunchPendingCloudExecutionUsesProvidedCredential(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	authorization := make(chan string, 1)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case authorization <- r.Header.Get("Authorization"):
		default:
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Cloud execution completed.\"}}]}\n\ndata: [DONE]\n\n")
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
	projectAgent, err := application.SaveProjectAgent(domain.ProjectAgent{
		Name:               "Cloud Agent",
		Provider:           domain.ProviderOpenAI,
		ProviderPreset:     "openai",
		BaseURL:            provider.URL,
		PrimaryModel:       "test-model",
		MaxSteps:           2,
		MaxDurationSeconds: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	execution, err := application.StartSandboxedExecution(projectAgent.ID, "Explain the result.", "")
	if err != nil {
		t.Fatal(err)
	}
	run, err := application.LaunchPendingExecution(execution.ID, "secret-from-keychain")
	if err != nil {
		t.Fatal(err)
	}

	select {
	case header := <-authorization:
		if header != "Bearer secret-from-keychain" {
			t.Fatalf("authorization=%q", header)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("provider did not receive the cloud execution")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		details, detailsErr := application.RunDetails(run.ID)
		if detailsErr != nil {
			t.Fatal(detailsErr)
		}
		if details.Run.Status == domain.RunCompleted {
			return
		}
		if details.Run.Status == domain.RunFailed {
			t.Fatalf("run failed: %s", details.Run.Error)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("cloud execution did not complete")
}
