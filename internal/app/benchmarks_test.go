package app

import (
	"context"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

func TestPersonalBenchmarkSetEvaluatesExactRunsAndComparesBeforeAfter(t *testing.T) {
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
	agent, err := application.SaveProjectAgent(domain.ProjectAgent{
		Name: "Benchmark agent", Provider: domain.ProviderOllama, BaseURL: "http://127.0.0.1:11434", PrimaryModel: "qwen2.5-coder:7b", AllowedTools: []string{"read_file"},
	})
	if err != nil {
		t.Fatal(err)
	}
	set, err := application.SaveAgentBenchmarkSet(domain.AgentBenchmarkSet{
		ProjectAgentID: agent.ID, Name: "Personal regression set", Cases: []domain.AgentBenchmarkCase{{
			Name: "Inspect boundary", Task: "Inspect the workspace boundary", ExpectedStatus: domain.RunCompleted,
			RequireHealthy: true, MaximumToolFailures: 0,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if set.Revision != 1 || len(set.Digest) != 71 || len(set.Cases) != 1 || set.Cases[0].ID == "" {
		t.Fatalf("set=%#v", set)
	}
	now := time.Now().UTC()
	beforeRun := benchmarkTestRun(agent, view.Workspace.ID, set.Cases[0].Task, "run-benchmark-before", "model-before", domain.RunCompleted, now)
	afterRun := benchmarkTestRun(agent, view.Workspace.ID, set.Cases[0].Task, "run-benchmark-after", "model-after", domain.RunFailed, now.Add(time.Minute))
	if err = application.store.SaveRun(context.Background(), beforeRun); err != nil {
		t.Fatal(err)
	}
	if err = application.store.SaveRun(context.Background(), afterRun); err != nil {
		t.Fatal(err)
	}
	caseRuns := func(runID string) map[string]string { return map[string]string{set.Cases[0].ID: runID} }
	before, err := application.EvaluateAgentBenchmark(AgentBenchmarkEvaluationRequest{
		BenchmarkSetID: set.ID, Label: "before revision", CaseRuns: caseRuns(beforeRun.ID),
	})
	if err != nil || before.Metrics.Passed != 1 || before.Cases[0].ConfigurationDigest == "" || before.Cases[0].ProfileDigest == "" {
		t.Fatalf("before=%#v err=%v", before, err)
	}
	after, err := application.EvaluateAgentBenchmark(AgentBenchmarkEvaluationRequest{
		BenchmarkSetID: set.ID, Label: "after revision", CaseRuns: caseRuns(afterRun.ID),
	})
	if err != nil || after.Metrics.Passed != 0 || len(after.Cases[0].Reasons) == 0 {
		t.Fatalf("after=%#v err=%v", after, err)
	}
	comparison, err := application.CompareAgentBenchmarks(AgentBenchmarkComparisonRequest{BeforeID: before.ID, AfterID: after.ID})
	if err != nil || comparison.GatePassed || len(comparison.RegressedCases) != 1 || comparison.Before.Passed != 1 || comparison.After.Passed != 0 {
		t.Fatalf("comparison=%#v err=%v", comparison, err)
	}
	evaluations, err := application.ListAgentBenchmarkEvaluations(10)
	if err != nil || len(evaluations) != 2 || evaluations[0].SetDigest != set.Digest {
		t.Fatalf("evaluations=%#v err=%v", evaluations, err)
	}

	unchanged, err := application.SaveAgentBenchmarkSet(set)
	if err != nil || unchanged.Revision != 1 || unchanged.Digest != set.Digest {
		t.Fatalf("unchanged=%#v err=%v", unchanged, err)
	}
	changedInput := unchanged
	changedInput.Cases[0].Task = "Inspect the workspace and network boundaries"
	changed, err := application.SaveAgentBenchmarkSet(changedInput)
	if err != nil || changed.Revision != 2 || changed.Digest == set.Digest {
		t.Fatalf("changed=%#v err=%v", changed, err)
	}
}

func TestBenchmarkRejectsMismatchedTaskAndLegacyAttribution(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	view, _ := application.OpenWorkspace(t.TempDir())
	agent, _ := application.SaveProjectAgent(domain.ProjectAgent{Name: "Agent", Provider: domain.ProviderOllama, BaseURL: "http://127.0.0.1:11434", PrimaryModel: "qwen2.5-coder:7b"})
	set, err := application.SaveAgentBenchmarkSet(domain.AgentBenchmarkSet{ProjectAgentID: agent.ID, Name: "Strict set", Cases: []domain.AgentBenchmarkCase{{Name: "Case", Task: "Expected task"}}})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	run := benchmarkTestRun(agent, view.Workspace.ID, "Different task", "run-mismatch", "model", domain.RunCompleted, now)
	if err = application.store.SaveRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	caseRuns := func(runID string) map[string]string { return map[string]string{set.Cases[0].ID: runID} }
	if _, err = application.EvaluateAgentBenchmark(AgentBenchmarkEvaluationRequest{BenchmarkSetID: set.ID, Label: "mismatch", CaseRuns: caseRuns(run.ID)}); err == nil {
		t.Fatal("mismatched benchmark task was accepted")
	}
	run.ID, run.Task = "run-legacy", set.Cases[0].Task
	run.ConfigurationSnapshot.SchemaVersion = 1
	run.ConfigurationSnapshot.ConfigurationDigest = ""
	if err = application.store.SaveRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	if _, err = application.EvaluateAgentBenchmark(AgentBenchmarkEvaluationRequest{BenchmarkSetID: set.ID, Label: "legacy", CaseRuns: caseRuns(run.ID)}); err == nil {
		t.Fatal("legacy Run without exact configuration attribution was accepted")
	}
}

func benchmarkTestRun(agent domain.ProjectAgent, workspaceID, task, id, model string, status domain.RunStatus, started time.Time) domain.Run {
	finished := started.Add(time.Second)
	profile := domain.AgentProfile{ID: agent.ID, Provider: agent.Provider, Model: model, AllowedTools: append([]string(nil), agent.AllowedTools...)}
	return domain.Run{
		ID: id, AgentID: agent.ID, ProfileID: agent.ID, WorkspaceID: workspaceID, Task: task,
		Provider: string(agent.Provider), Model: model, Status: status, StartedAt: started, FinishedAt: &finished,
		ConfigurationSnapshot: domain.NewRunConfigurationSnapshot(Version, profile, nil, started),
	}
}
