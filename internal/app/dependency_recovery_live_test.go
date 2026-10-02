package app

import (
	"context"
	"encoding/json"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/environment"
	"local-agent-workbench/internal/flowruntime"
	"local-agent-workbench/internal/sandbox"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Explicit operator probe: only retries the failed Accept on the saved writer.
// Uses the existing digest-checked proposal/control path; no implementation rerun.
func TestDependencyRecoveryLiveAccept(t *testing.T) {
	data, id := os.Getenv("POINT_DEPENDENCY_RECOVERY_DATA"), os.Getenv("POINT_DEPENDENCY_RECOVERY_QUEST")
	if data == "" || id == "" {
		t.Skip("explicit live recovery variables required")
	}
	ctx := context.Background()
	a, err := New(data)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Shutdown(ctx)
	q, err := a.WorkOrderQuestV2(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if q.Status != domain.QuestAwaitingUser {
		t.Fatalf("quest must await approval, got %s", q.Status)
	}
	approval, err := a.store.WorkOrderApprovalByQuestV2(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.OpenWorkspace(approval.WorkOrder.Workspace.Path); err != nil {
		t.Fatal(err)
	}
	run, err := a.store.GetFlowRun(ctx, q.FlowRunID)
	if err != nil {
		t.Fatal(err)
	}
	flow, ok, err := flowruntime.FlowFromSnapshot(run)
	if err != nil || !ok {
		t.Fatalf("flow snapshot: %v", err)
	}
	_, failedID, _ := stageFailureRecord(q)
	accept := false
	for _, n := range flow.Nodes {
		if n.ID == failedID && domain.FlowNodeStageRole(n) == domain.StageRoleAccept {
			accept = true
		}
	}
	if !accept {
		t.Fatal("recovery only supports failed Accept")
	}
	var writer domain.SandboxRecord
	writerID := ""
	for _, n := range flow.Nodes {
		if run.NodeStates[n.ID].Status == "completed" && domain.FlowNodeWriteFiles(n) {
			execID, _ := run.NodeStates[n.ID].Output["executionId"].(string)
			exec, err := a.store.GetExecution(ctx, execID)
			if err != nil {
				t.Fatal(err)
			}
			writer, err = a.store.GetSandbox(ctx, exec.SandboxID)
			if err != nil {
				t.Fatal(err)
			}
			writerID = exec.ID
		}
	}
	if writerID == "" {
		t.Fatal("saved writer missing")
	}
	before, err := sandbox.TreeDigestWithRules(writer.Path, writer.FileRulesVersion)
	if err != nil {
		t.Fatal(err)
	}
	plan := environment.DependencyPlanFor(writer.Path, approval.WorkOrder.Criteria)
	if plan == nil {
		t.Fatal("no dependency plan discovered")
	}
	if _, err = dependencyFingerprint(writer.Path, plan); err != nil {
		t.Fatal(err)
	}
	proposal, err := a.ProposeStageRetryV2(ctx, q.WorkspaceID, StageRetryProposalInput{QuestID: id, Dependencies: plan, Diagnosis: "Prepare dependencies once from locked manifests before the original acceptance criteria; preserve the completed writer."})
	if err != nil {
		t.Fatal(err)
	}
	if !proposal.NeedsApproval {
		t.Fatal("dependency amendment lost approval requirement")
	}
	started := time.Now()
	result, err := a.ControlWorkOrderQuestV2(ctx, id, "retry", WorkOrderQuestControlRequest{Source: StageRetrySourceHuman, ProposalDigest: proposal.Digest})
	if err != nil {
		t.Fatal(err)
	}
	after, err := sandbox.TreeDigestWithRules(writer.Path, writer.FileRulesVersion)
	if err != nil || before != after {
		t.Fatal("writer changed during recovery")
	}
	latest, err := a.store.GetFlowRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	for nid, state := range run.NodeStates {
		if state.Status == "completed" && latest.NodeStates[nid].Status != "completed" {
			t.Fatal("completed stage was restarted")
		}
	}
	quest, err := a.WorkOrderQuestV2(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	report := map[string]any{"questId": id, "proposal": proposal, "control": result, "flowStatus": latest.Status, "questStatus": quest.Status, "writerExecutionId": writerID, "writerTree": before, "acceptDurationMs": time.Since(started).Milliseconds(), "nodes": latest.NodeStates}
	raw, _ := json.MarshalIndent(report, "", "  ")
	out := filepath.Join("..", "..", "build", "quest-dependency-recovery.json")
	if err = os.WriteFile(out, raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("recovery evidence: %s", out)
	if latest.Status != domain.RunCompleted {
		t.Fatalf("Accept failed: %s", latest.Error)
	}
}
