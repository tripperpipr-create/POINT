package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"local-agent-workbench/internal/agent"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/flowruntime"
	"local-agent-workbench/internal/osproc"
	"local-agent-workbench/internal/sandbox"
)

// Compare real pre-accept/shadow/on on an isolated copy of the recovered result.
// The source SQLite is opened read-only; no quest or delivery in it is changed.
func TestDependencyVerificationLive(t *testing.T) {
	if os.Getenv("POINT_DEPENDENCY_VERIFY_LIVE") != "1" {
		t.Skip("explicit recovered-result verification opt-in required")
	}
	ctx := context.Background()
	base, err := filepath.Abs(filepath.Join("..", "..", "build", "quest-dependency-verification-"+time.Now().UTC().Format("20060102-150405")))
	if err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(base, "data")
	if err = os.MkdirAll(data, 0700); err != nil {
		t.Fatal(err)
	}
	live := filepath.Join(os.Getenv("APPDATA"), "Point", "User", "globalStorage", "local-agent.local-agent-workbench", "hub-v2.db")
	py := `import sqlite3,sys,pathlib; src=sqlite3.connect(pathlib.Path(sys.argv[1]).as_uri()+'?mode=ro',uri=True); dst=sqlite3.connect(sys.argv[2]); src.backup(dst); dst.close(); src.close()`
	if out, e := osproc.Command("python", "-c", py, live, filepath.Join(data, "hub-v2.db")).CombinedOutput(); e != nil {
		t.Fatalf("read-only backup: %v %s", e, out)
	}
	a, err := New(data)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Shutdown(ctx)
	q, err := a.store.GetQuest(ctx, "quest_6b59b5c8a7b402ee88cfc9ba")
	if err != nil {
		t.Fatal(err)
	}
	approval, err := a.store.WorkOrderApprovalByQuestV2(ctx, q.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.OpenWorkspace(approval.WorkOrder.Workspace.Path); err != nil {
		t.Fatal(err)
	}
	flow, err := a.store.GetFlowRun(ctx, q.FlowRunID)
	if err != nil {
		t.Fatal(err)
	}
	graph, ok, err := flowruntime.FlowFromSnapshot(flow)
	if err != nil || !ok {
		t.Fatal("flow snapshot unavailable")
	}
	var writer domain.ExecutionInstance
	var accept domain.FlowNode
	for _, n := range graph.Nodes {
		if domain.FlowNodeStageRole(n) == domain.StageRoleAccept {
			accept = n
		}
		if domain.FlowNodeWriteFiles(n) && flow.NodeStates[n.ID].Status == "completed" {
			id, _ := flow.NodeStates[n.ID].Output["executionId"].(string)
			writer, err = a.store.GetExecution(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	old, err := a.store.GetSandbox(ctx, writer.SandboxID)
	if err != nil {
		t.Fatal(err)
	}
	before, err := sandbox.TreeDigestWithRules(old.Path, old.FileRulesVersion)
	if err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(base, "project")
	if err = sandbox.CopyPortable(ctx, old.Path, project, old.FileRulesVersion); err != nil {
		t.Fatal(err)
	}
	b := sandbox.NewContainerBackend(filepath.Join(base, "sandboxes"))
	b.Image = old.BackendImageDigest
	if err = b.Probe(ctx); err != nil {
		t.Fatal(err)
	}
	record, err := b.Create(ctx, sandbox.CreateRequest{WorkspaceID: q.WorkspaceID, QuestID: q.ID, ExecutionID: writer.ID, WorkspacePath: project, StorageMode: "volume", FileRulesVersion: old.FileRulesVersion, Image: old.BackendImageDigest})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close(ctx, record, project)
	a.sandboxBackend = b
	if err = a.store.SaveSandbox(ctx, record); err != nil {
		t.Fatal(err)
	}
	writer.SandboxID = record.ID
	if err = a.store.SaveExecution(ctx, writer); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POINT_VERIFY_SERVICE", "shadow")
	started := time.Now()
	pre := a.VerifyBeforeCompletion(ctx, agent.StageVerifyRequest{ExecutionID: writer.ID, FlowRunID: flow.ID, FlowNodeID: writer.FlowNodeID, RunID: writer.RunID})
	preMs := time.Since(started).Milliseconds()
	if !pre.Ran || !pre.Passed {
		t.Fatalf("live pre-accept: %+v", pre)
	}
	approval.WorkOrder.Dependencies = effectiveDependencyPlan(q, approval.WorkOrder.Dependencies)
	checker, err := a.store.GetProjectAgent(ctx, accept.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	hosts := bootstrapNetworkHosts(a, flow, checker)
	policy := "DENY"
	if len(hosts) > 0 {
		policy = "ALLOWLIST"
	}
	input := criteriaBatchInput{Context: ctx, QuestID: q.ID, RunID: domain.NewID("probe"), FlowRunID: flow.ID, FlowNodeID: accept.ID, Criteria: approval.WorkOrder.Criteria, Sandbox: record, NetworkPolicy: policy, NetworkHosts: hosts, WorkOrder: &approval.WorkOrder}
	started = time.Now()
	shadow, note, err := a.acceptCriteriaBatch(ctx, input, flow, writer.ID)
	shadowMs := time.Since(started).Milliseconds()
	if err != nil || !shadow.AllOK || note["agrees"] != true || note["wouldReuse"] == nil {
		t.Fatalf("live shadow mismatch: %+v %v %v", shadow, note, err)
	}
	t.Setenv("POINT_VERIFY_SERVICE", "on")
	started = time.Now()
	reuse, reuseNote, err := a.acceptCriteriaBatch(ctx, input, flow, writer.ID)
	reuseMs := time.Since(started).Milliseconds()
	if err != nil || !reuse.AllOK || reuseNote["reusedFrom"] == nil {
		t.Fatalf("live reuse: %+v %v %v", reuse, reuseNote, err)
	}
	for _, c := range reuse.Criteria {
		if c.Check == nil || c.Check.ReusedFrom == nil {
			t.Fatalf("missing explicit reuse evidence: %+v", c)
		}
	}
	after, err := sandbox.TreeDigestWithRules(old.Path, old.FileRulesVersion)
	if err != nil || before != after {
		t.Fatal("saved writer changed")
	}
	proof := map[string]any{"questId": q.ID, "sourceTree": before, "imageDigest": record.BackendImageDigest, "storageMode": "volume", "preAccept": pre, "shadow": shadow, "shadowNote": note, "reuse": reuse, "reuseNote": reuseNote, "preAcceptMs": preMs, "shadowMs": shadowMs, "reuseMs": reuseMs, "productionDatabaseChanged": false}
	raw, err := json.MarshalIndent(proof, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(base, "result.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	fmt.Println("live shadow/reuse proof:", filepath.Join(base, "result.json"))
}
