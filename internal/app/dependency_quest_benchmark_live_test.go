package app

import (
	"context"
	"encoding/json"
	"fmt"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/environment"
	"local-agent-workbench/internal/filepolicy"
	"local-agent-workbench/internal/flowruntime"
	"local-agent-workbench/internal/osproc"
	"local-agent-workbench/internal/sandbox"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// Seven opt-in quests use a read-only SQLite backup, isolated source copies,
// fixed acceptance tests and the same Qwen route/image. Never modifies the live DB.
func TestDependencyQuestBenchmarkLive(t *testing.T) {
	if os.Getenv("POINT_DEPENDENCY_LIVE_BENCH") != "1" {
		t.Skip("explicit model benchmark opt-in required")
	}
	key := os.Getenv("POINT_LLMUX_API_KEY")
	if key == "" {
		t.Fatal("POINT_LLMUX_API_KEY is required; never pass it on the command line")
	}
	live := os.Getenv("POINT_DEPENDENCY_BENCH_DATA")
	if live == "" {
		live = filepath.Join(os.Getenv("APPDATA"), "Point", "User", "globalStorage", "local-agent.local-agent-workbench")
	}
	questID := os.Getenv("POINT_DEPENDENCY_BENCH_QUEST")
	if questID == "" {
		questID = "quest_e94cc95ffab7869553c15323"
	}
	base, _ := filepath.Abs(filepath.Join("..", "..", "build", "quest-dependency-live-"+time.Now().UTC().Format("20060102-150405")))
	os.MkdirAll(base, 0700)
	t.Setenv("POINT_AGENT_HUB_V2", "1")
	t.Setenv("POINT_SANDBOX_BACKEND", "docker")
	t.Setenv("POINT_LIVE_WORKSPACE", "0")
	t.Setenv("POINT_FILE_ISOLATION", "sandbox")
	t.Setenv("POINT_VERIFY_SERVICE", "shadow")
	t.Setenv("REDIS_ADDR", "")
	probeCtx, probeCancel := context.WithTimeout(context.Background(), 30*time.Second)
	probe := sandbox.NewContainerBackend(filepath.Join(base, "preflight"))
	probeErr := probe.Probe(probeCtx)
	probeCancel()
	if probeErr != nil {
		t.Fatalf("benchmark environment unavailable before model calls: %v", probeErr)
	}
	timings := map[string][]int64{}
	var reference string
	for _, comparison := range []struct {
		mode, verification string
		trial              int
	}{
		{"bind", "shadow", 1}, {"volume", "shadow", 1}, {"volume", "shadow", 2}, {"bind", "shadow", 2}, {"bind", "shadow", 3}, {"volume", "shadow", 3}, {"volume", "on", 1},
	} {
		mode, trial, verification := comparison.mode, comparison.trial, comparison.verification
		t.Run(fmt.Sprintf("%s-%s-%d", mode, verification, trial), func(t *testing.T) {
			t.Setenv("POINT_SANDBOX_WORKSPACE", mode)
			t.Setenv("POINT_VERIFY_SERVICE", verification)
			dir := filepath.Join(base, fmt.Sprintf("%s-%s-%d", mode, verification, trial))
			data := filepath.Join(dir, "data")
			project := filepath.Join(dir, "project")
			os.MkdirAll(data, 0700)
			py := `import sqlite3,sys,pathlib; src=sqlite3.connect(pathlib.Path(sys.argv[1]).as_uri()+'?mode=ro',uri=True); dst=sqlite3.connect(sys.argv[2]); src.backup(dst); dst.close(); src.close()`
			if output, err := osproc.Command("python", "-c", py, filepath.Join(live, "hub-v2.db"), filepath.Join(data, "hub-v2.db")).CombinedOutput(); err != nil {
				t.Fatalf("read-only backup: %v %s", err, output)
			}
			a, err := New(data)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Shutdown(context.Background())
			ctx := context.Background()
			original, err := a.store.WorkOrderApprovalByQuestV2(ctx, questID)
			if err != nil {
				t.Fatal(err)
			}
			q, err := a.store.GetQuest(ctx, questID)
			if err != nil {
				t.Fatal(err)
			}
			run, err := a.store.GetFlowRun(ctx, q.FlowRunID)
			if err != nil {
				t.Fatal(err)
			}
			flow, ok, err := flowruntime.FlowFromSnapshot(run)
			if err != nil || !ok {
				t.Fatal("original flow snapshot unavailable")
			}
			var writer domain.SandboxRecord
			agentIDs := map[string]bool{}
			for _, n := range flow.Nodes {
				if n.AgentID != "" {
					agentIDs[n.AgentID] = true
				}
				if domain.FlowNodeWriteFiles(n) && run.NodeStates[n.ID].Status == "completed" {
					id, _ := run.NodeStates[n.ID].Output["executionId"].(string)
					ex, e := a.store.GetExecution(ctx, id)
					if e != nil {
						t.Fatal(e)
					}
					writer, e = a.store.GetSandbox(ctx, ex.SandboxID)
					if e != nil {
						t.Fatal(e)
					}
				}
			}
			if writer.BaselinePath == "" {
				t.Fatal("immutable baseline unavailable")
			}
			if err = sandbox.CopyPortable(ctx, writer.BaselinePath, project, filepolicy.Current); err != nil {
				t.Fatal(err)
			}
			// Freeze declared Jest suites from the delivered writer tree.
			fixed := map[string]string{}
			for _, c := range original.WorkOrder.Criteria {
				if c.Kind != "verification" || c.Tool != "run_command" {
					continue
				}
				var args struct{ Command string }
				json.Unmarshal(c.Arguments, &args)
				plan := environment.DependencyPlanFor(writer.Path, []domain.AcceptanceCriterion{c})
				if plan == nil {
					t.Fatal("test project not found")
				}
				cwd := plan.Projects[0].Cwd
				for _, word := range strings.Fields(args.Command) {
					if !strings.HasSuffix(word, ".spec.ts") {
						continue
					}
					name := filepath.ToSlash(filepath.Join(cwd, word))
					raw, e := os.ReadFile(filepath.Join(writer.Path, filepath.FromSlash(name)))
					if e != nil {
						t.Fatal(e)
					}
					if e = os.WriteFile(filepath.Join(project, filepath.FromSlash(name)), raw, 0644); e != nil {
						t.Fatal(e)
					}
					fixed[name] = string(raw)
				}
			}
			if len(fixed) == 0 {
				t.Fatal("declared immutable test suites unavailable")
			}
			tree, e := sandbox.TreeDigestWithRules(project, filepolicy.Current)
			if e != nil {
				t.Fatal(e)
			}
			if reference == "" {
				reference = tree
			} else if reference != tree {
				t.Fatal("comparison sources differ")
			}
			for _, args := range [][]string{{"init", "--quiet"}, {"add", "-A"}, {"-c", "user.email=benchmark@point.local", "-c", "user.name=Point Benchmark", "commit", "--quiet", "--no-gpg-sign", "-m", "fixed benchmark baseline"}} {
				if out, e := osproc.Command("git", append([]string{"-C", project}, args...)...).CombinedOutput(); e != nil {
					t.Fatalf("fixture git: %v %s", e, out)
				}
			}
			view, err := a.OpenWorkspace(project)
			if err != nil {
				t.Fatal(err)
			}
			roster := domain.AgentRosterPlan{}
			ids := []string{}
			for id := range agentIDs {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			for _, id := range ids {
				agent, e := a.store.GetProjectAgent(ctx, id)
				if e != nil {
					t.Fatal(e)
				}
				agent.ID = ""
				agent.WorkspaceID = view.Workspace.ID
				agent.PrimaryModel = "Qwen3.8-27B"
				agent.FallbackModels = nil
				agent.SystemPrompt += "\nThe supplied acceptance test files are immutable. Implement against them; do not edit, delete or weaken them."
				saved, e := a.SaveProjectAgent(agent)
				if e != nil {
					t.Fatal(e)
				}
				roster.Permanent = append(roster.Permanent, domain.AgentDraft{ID: saved.ID, Existing: true, Name: saved.Name})
			}
			order := original.WorkOrder
			order.Criteria = append([]domain.AcceptanceCriterion(nil), original.WorkOrder.Criteria...)
			for i := range order.Criteria {
				if order.Criteria[i].Kind == "verification" && order.Criteria[i].Tool == "run_command" {
					order.Criteria[i].Deterministic = true
				}
			}
			order.ID = ""
			order.Version = 1
			order.State = "ready"
			order.ApprovedVersion = 0
			order.ApprovedDigest = ""
			order.Digest = ""
			order.ProposalID = ""
			order.ConversationID = ""
			order.CreatedAt = time.Time{}
			order.UpdatedAt = time.Time{}
			order.Runtime = nil
			order.Sources = nil
			order.WorkspaceID = view.Workspace.ID
			order.Workspace.Path = project
			order.Workspace.Mode = "existing"
			order.Workspace.Isolation = "snapshot"
			order.Roster = roster
			order.Sandbox.Image = writer.BackendImage
			order.Sandbox.ImageDigest = writer.BackendImageDigest
			order.Dependencies = environment.DependencyPlanFor(project, order.Criteria)
			order.Routing.Mode = "fixed"
			order.Routing.FixedModel = "Qwen3.8-27B"
			order.Routing.FallbackMode = "wait"
			order.Delivery.ApplyMode = "automatic"
			order.Delivery.CommitMode = "none"
			order = domain.NormalizeWorkOrder(order)
			order, e = a.SaveWorkOrderV2(ctx, order)
			if e != nil {
				t.Fatal(e)
			}
			started := time.Now()
			approval, e := a.ApproveWorkOrderV2(ctx, order.ID, ApproveWorkOrderV2Request{Version: order.Version, Digest: domain.WorkOrderDigest(order), IdempotencyKey: fmt.Sprintf("benchmark-%s-%d", mode, trial), APIKey: key})
			if e != nil {
				t.Fatal(e)
			}
			var current domain.Quest
			for time.Since(started) < 60*time.Minute {
				current, e = a.WorkOrderQuestV2(ctx, approval.QuestID)
				if e != nil {
					t.Fatal(e)
				}
				if domain.IsTerminalQuestStatus(current.Status) || current.Status == domain.QuestAwaitingUser || current.Status == domain.QuestBlocked || current.Status == domain.QuestNeedsReview {
					break
				}
				time.Sleep(time.Second)
			}
			for name, want := range fixed {
				raw, e := os.ReadFile(filepath.Join(project, filepath.FromSlash(name)))
				if e != nil || string(raw) != want {
					t.Errorf("acceptance tests changed: %s", name)
				}
			}
			latest, e := a.store.GetFlowRun(ctx, current.FlowRunID)
			if e != nil {
				t.Fatal(e)
			}
			bundle, e := a.EvidenceBundle(ctx, current.ID)
			if e != nil {
				t.Fatal(e)
			}
			if len(bundle.ModelCalls) == 0 {
				t.Error("no actual model-call evidence")
			}
			reused := false
			for _, check := range bundle.VerificationChecks {
				reused = reused || check.ReusedFromCheckID != ""
			}
			if verification == "on" && !reused {
				t.Error("volume/on did not preserve reused-check provenance")
			}
			for _, criterion := range order.Criteria {
				if criterion.Kind != "manual" {
					continue
				}
				pending := false
				for _, proof := range bundle.Criteria {
					if proof.CriterionID == criterion.ID {
						pending = !proof.Satisfied && proof.Status == "needs_review"
					}
				}
				if !pending {
					t.Errorf("manual criterion was lost or accepted: %s", criterion.ID)
				}
			}
			for _, call := range bundle.ModelCalls {
				if call.Model != "Qwen3.8-27B" {
					t.Errorf("model changed: %s", call.Model)
				}
			}
			executions, e := a.store.ListExecutions(ctx, view.Workspace.ID, 500)
			if e != nil {
				t.Fatal(e)
			}
			for _, execution := range executions {
				if execution.SandboxID == "" {
					continue
				}
				actual, e := a.store.GetSandbox(ctx, execution.SandboxID)
				if e != nil {
					t.Fatal(e)
				}
				if actual.BackendImageDigest != writer.BackendImageDigest || actual.StorageMode != mode {
					t.Errorf("comparison runtime differs: %s %s", actual.BackendImageDigest, actual.StorageMode)
				}
			}
			elapsed := latest.DurationMs
			if elapsed == 0 && latest.FinishedAt != nil {
				elapsed = latest.FinishedAt.Sub(latest.StartedAt).Milliseconds()
			}
			if bundle.DeliveryReceipt == nil || !bundle.DeliveryVerified {
				t.Error("delivery receipt/evidence unavailable")
			} else {
				elapsed = bundle.DeliveryReceipt.DeliveredAt.Sub(started).Milliseconds()
			}
			proof := map[string]any{"mode": mode, "verificationMode": verification, "trial": trial, "model": "Qwen3.8-27B", "sourceTree": tree, "imageDigest": writer.BackendImageDigest, "cacheState": "fresh quest download-cache scope", "criteria": order.Criteria, "flow": latest, "evidence": bundle, "questStatus": current.Status, "wallMs": elapsed, "approvedAt": started.UTC(), "unmeasured": []string{"whole-environment resources", "installation", "human wait"}, "database": filepath.Join(data, "hub-v2.db")}
			raw, _ := json.MarshalIndent(proof, "", "  ")
			if e = os.WriteFile(filepath.Join(dir, "result.json"), raw, 0600); e != nil {
				t.Fatal(e)
			}
			t.Logf("quest comparison proof: %s", dir)
			wantStatus := domain.QuestCompleted
			for _, criterion := range order.Criteria {
				if criterion.Kind == "manual" {
					wantStatus = domain.QuestNeedsReview
				}
			}
			if current.Status != wantStatus || latest.Status != domain.RunCompleted {
				t.Fatalf("full quest failed: %s %s", current.Status, latest.Error)
			}
			if verification == "shadow" {
				timings[mode] = append(timings[mode], elapsed)
			}
		})
	}
	if len(timings["bind"]) != 3 || len(timings["volume"]) != 3 {
		t.Fatal("all six comparable quests must pass before claiming acceleration")
	}
	sort.Slice(timings["bind"], func(i, j int) bool { return timings["bind"][i] < timings["bind"][j] })
	sort.Slice(timings["volume"], func(i, j int) bool { return timings["volume"][i] < timings["volume"][j] })
	improvement := 1 - float64(timings["volume"][1])/float64(timings["bind"][1])
	summary := map[string]any{"timingsMs": timings, "medianImprovement": improvement, "speedTargetPassed": improvement >= .30, "rolloutQualified": false, "unmeasured": []string{"whole-environment resources", "three-project cold/warm matrix"}, "model": "Qwen3.8-27B", "sourceTree": reference}
	raw, _ := json.MarshalIndent(summary, "", "  ")
	if err := os.WriteFile(filepath.Join(base, "summary.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if improvement < .30 {
		t.Errorf("full quest speed target not met: %.1f%%", improvement*100)
	}
}
