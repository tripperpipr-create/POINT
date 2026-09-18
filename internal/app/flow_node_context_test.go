package app

import (
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
)

func TestFlowNodeContextIncludesQuestAndUpstreamResult(t *testing.T) {
	flow := domain.FlowGraph{
		Nodes: []domain.FlowNode{
			{ID: "primary", Name: "Primary implementation", AgentID: "agent-a"},
			{ID: "reviewer", Name: "Reviewer", AgentID: "agent-b"},
		},
		Edges: []domain.FlowEdge{{ID: "edge", From: "primary", To: "reviewer"}},
	}
	run := domain.FlowRun{ID: "flowrun-1", NodeStates: map[string]domain.FlowNodeState{
		"primary": {Status: "completed", Output: map[string]any{"executionId": "exec-a", "result": "implemented OAuth", "status": domain.RunCompleted}},
	}}
	quest := domain.Quest{
		ID: "quest", Title: "OAuth", Objectives: []string{"callback"}, DefinitionOfDone: []string{"tests pass"},
	}
	inputs := flowNodeContext(quest, flow, run, "reviewer", []domain.ChangeSet{{
		ID: "changes-primary", ExecutionID: "exec-a", Title: "Primary changes", Status: domain.ChangeSetPending,
		Items: []domain.ChangeItem{{
			Path: "internal/auth.go", Kind: "modify",
			Diff: "@@ -1 +1 @@\n-token := old\n+token := rotated\n+api_key=sk-12345678901234567890",
		}},
	}})
	if len(inputs) != 2 {
		t.Fatalf("inputs=%#v", inputs)
	}
	if inputs[1].Label != "Передача от · Primary implementation" {
		t.Fatalf("handoff label=%q", inputs[1].Label)
	}
	combined := inputs[0].Content + inputs[1].Content
	if !strings.Contains(combined, "tests pass") || !strings.Contains(combined, "implemented OAuth") {
		t.Fatalf("context=%s", combined)
	}
	if !strings.Contains(combined, `"kind":"agent_handoff"`) || !strings.Contains(combined, "agent-a") {
		t.Fatalf("structured handoff missing: %s", combined)
	}
	if !strings.Contains(combined, `"changeSets"`) || !strings.Contains(combined, "internal/auth.go") || !strings.Contains(combined, "@@ -1 +1 @@") {
		t.Fatalf("reviewable Change Set diff missing from handoff: %s", combined)
	}
	if strings.Contains(combined, "sk-12345678901234567890") || !strings.Contains(combined, "[REDACTED]") {
		t.Fatalf("Change Set handoff did not redact a likely secret: %s", combined)
	}
	if !strings.Contains(inputs[0].Content, `"currentNode":"Reviewer"`) {
		t.Fatalf("coordination role missing: %s", inputs[0].Content)
	}
}

func TestFlowNodeContextTraversesJoinToEveryCompletedAgent(t *testing.T) {
	flow := domain.FlowGraph{
		Nodes: []domain.FlowNode{
			{ID: "a", Kind: domain.FlowNodeAgent, Name: "Implementation", AgentID: "agent-a"},
			{ID: "b", Kind: domain.FlowNodeAgent, Name: "Tests", AgentID: "agent-b"},
			{ID: "join", Kind: domain.FlowNodeJoin, Name: "Join"},
			{ID: "review", Kind: domain.FlowNodeAgent, Name: "Review", AgentID: "agent-c"},
		},
		Edges: []domain.FlowEdge{{From: "a", To: "join"}, {From: "b", To: "join"}, {From: "join", To: "review"}},
	}
	run := domain.FlowRun{ID: "flowrun", NodeStates: map[string]domain.FlowNodeState{
		"a":    {Status: "completed", Output: map[string]any{"executionId": "exec-a", "result": "implemented"}},
		"b":    {Status: "completed", Output: map[string]any{"executionId": "exec-b", "result": "tested"}},
		"join": {Status: "completed", Output: map[string]any{"joined": true}},
	}}
	inputs := flowNodeContext(domain.Quest{ID: "quest", Title: "Feature"}, flow, run, "review", []domain.ChangeSet{
		{ID: "set-a", ExecutionID: "exec-a", Status: domain.ChangeSetPending, Items: []domain.ChangeItem{{Path: "feature.go", Diff: "+feature"}}},
		{ID: "set-b", ExecutionID: "exec-b", Status: domain.ChangeSetPending, Items: []domain.ChangeItem{{Path: "feature_test.go", Diff: "+test"}}},
	})
	if len(inputs) != 3 {
		t.Fatalf("inputs=%#v", inputs)
	}
	combined := inputs[1].Content + inputs[2].Content
	for _, expected := range []string{"exec-a", "exec-b", "feature.go", "feature_test.go", "implemented", "tested"} {
		if !strings.Contains(combined, expected) {
			t.Fatalf("join handoff missing %q: %s", expected, combined)
		}
	}
	if got := completedUpstreamExecutionIDs(flow, run, "review"); len(got) != 2 {
		t.Fatalf("parallel upstream executions=%v", got)
	}
}
