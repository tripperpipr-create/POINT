package app

import (
	"context"
	"encoding/json"
	"local-agent-workbench/internal/domain"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPreciseTaskExecutesOnceAndStopsForUserReview(t *testing.T) {
	var requests atomic.Int32
	var contractSeen atomic.Bool
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		var body struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
			Tools []json.RawMessage `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		for _, tool := range body.Tools {
			if strings.Contains(string(tool), `"run_command"`) || strings.Contains(string(tool), `"propose_patch"`) {
				t.Error("task permissions not applied to model tools")
			}
		}
		for _, m := range body.Messages {
			if strings.Contains(m.Content, "point_task_completion_contract") && strings.Contains(m.Content, "Return Clamp") {
				contractSeen.Store(true)
			}
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"role": "assistant", "content": "func Clamp(x, min, max int) int { if min > max { panic(\"invalid range\") }; if x < min { return min }; if x > max { return max }; return x }"}, "done": true, "prompt_eval_count": 300, "eval_count": 70})
	}))
	defer provider.Close()
	application, world := outcomeWorld(t)
	ctx := context.Background()
	now := time.Now().UTC()
	executor, err := application.SaveProjectAgent(domain.ProjectAgent{Name: "Function writer", Provider: domain.ProviderOllama, BaseURL: provider.URL, PrimaryModel: "scripted", AllowedTools: []string{"read_file", "run_command", "propose_patch"}, MaxSteps: 3, MaxDurationSeconds: 20, MaxOutputTokens: 1024, ContextWindowTokens: 16384})
	if err != nil {
		t.Fatal(err)
	}
	b := domain.NormalizeTaskBrief(domain.TaskBrief{State: "ready", Mode: domain.TaskModePrecise, Goal: "Return Clamp without running tests or editing files", ResultKind: "code", Criteria: []domain.AcceptanceCriterion{{ID: "c1", Text: "Exact function contract", Kind: "manual"}}})
	proposal := domain.QuestProposal{ID: "precise", WorkspaceID: world.ID, Title: "Clamp", Task: b.Goal, Brief: &b, TeamAgentIDs: []string{executor.ID}, TeamAgentIDsLocked: true, Status: "pending", CreatedAt: now}
	if err = application.store.SaveQuestProposal(ctx, proposal); err != nil {
		t.Fatal(err)
	}
	started, err := application.DecideQuestProposal(QuestProposalDecision{ProposalID: proposal.ID, Action: QuestProposalStart, ExpectedVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	if started.Quest == nil || started.Flow == nil {
		t.Fatal("no task/flow")
	}
	agentNodes := 0
	for _, node := range started.Flow.Nodes {
		if node.Kind == domain.FlowNodeAgent {
			agentNodes++
		}
	}
	if agentNodes != 1 {
		t.Fatalf("precise task has %d executors", agentNodes)
	}
	var runID string
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		executions, e := application.store.ListExecutions(ctx, world.ID, 100)
		if e != nil {
			t.Fatal(e)
		}
		for _, execution := range executions {
			if execution.ProjectAgentID != executor.ID {
				continue
			}
			if execution.Status == domain.RunPending {
				run, e := application.LaunchPendingExecution(execution.ID, "")
				if e != nil {
					t.Fatal(e)
				}
				runID = run.ID
			}
			if execution.RunID != "" {
				runID = execution.RunID
			}
		}
		if runID != "" {
			run, e := application.store.GetRun(ctx, runID)
			if e != nil {
				t.Fatal(e)
			}
			if run.Status == domain.RunCompleted {
				break
			}
			if run.Status == domain.RunFailed {
				t.Fatal(run.Error)
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if runID == "" {
		t.Fatal("execution never started")
	}
	run, err := application.store.GetRun(ctx, runID)
	if err != nil || run.Status != domain.RunCompleted {
		t.Fatalf("run=%+v err=%v", run, err)
	}
	for _, name := range run.ConfigurationSnapshot.Profile.AllowedTools {
		if name == "run_command" || name == "propose_patch" {
			t.Fatal("runtime snapshot retained forbidden task tools")
		}
	}
	if requests.Load() != 1 || !contractSeen.Load() {
		t.Fatalf("requests=%d contract=%t", requests.Load(), contractSeen.Load())
	}
	// Wait for the persisted execution callback, not just the earlier Run event.
	for time.Now().Before(deadline) {
		out, e := application.QuestOutcome(ctx, started.Quest.ID)
		if e != nil {
			t.Fatal(e)
		}
		if out.Evidence != nil {
			if out.Verified || out.Evidence.Status != "needs_review" || out.Met != 0 {
				t.Fatalf("manual criterion was automatically completed: %+v", out)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("root task lost child execution evidence")
}
