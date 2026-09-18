package flowruntime

import (
	"testing"

	"local-agent-workbench/internal/domain"
)

func TestVerifyInputsRequiresMergedResultWhenConfigured(t *testing.T) {
	run := domain.FlowRun{NodeStates: map[string]domain.FlowNodeState{
		"join": {Status: "completed", Output: map[string]any{"status": string(domain.RunCompleted), "result": "ok", "sandboxLineage": "merged_parallel_join"}},
	}}
	ok, checks := verifyInputs(map[string]any{"requireResult": true, "requireMergedResult": true}, []domain.FlowEdge{{From: "join"}}, run)
	if ok || checks[0]["mergedResult"] != false {
		t.Fatalf("missing merge must fail: ok=%v checks=%#v", ok, checks)
	}
	run.NodeStates["join"].Output["mergedResultVerified"] = true
	run.NodeStates["join"].Output["mergeChangeSetId"] = "cs-1"
	ok, checks = verifyInputs(map[string]any{"requireResult": true, "requireMergedResult": true}, []domain.FlowEdge{{From: "join"}}, run)
	if !ok || checks[0]["mergedResult"] != true {
		t.Fatalf("verified merge must pass: ok=%v checks=%#v", ok, checks)
	}
}

func TestVerifyInputsRequiresBriefCriteriaEvidence(t *testing.T) {
	run := domain.FlowRun{NodeStates: map[string]domain.FlowNodeState{
		"agent": {Status: "completed", Output: map[string]any{"status": string(domain.RunCompleted), "result": "done"}},
	}}
	criteria := []any{map[string]any{"id": "c1", "kind": "manual"}}
	ok, _ := verifyInputs(map[string]any{"requireResult": true, "criteria": criteria}, []domain.FlowEdge{{From: "agent"}}, run)
	if ok {
		t.Fatal("result without completion evidence must fail brief criteria")
	}
	run.NodeStates["agent"].Output["completionStatus"] = "accepted_after_revision"
	ok, _ = verifyInputs(map[string]any{"requireResult": true, "criteria": criteria}, []domain.FlowEdge{{From: "agent"}}, run)
	if !ok {
		t.Fatal("completion evidence must satisfy brief criteria")
	}
}
