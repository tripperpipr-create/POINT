package app

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"local-agent-workbench/internal/diagnostics"
	"local-agent-workbench/internal/domain"
)

func TestStatisticsBreakdownsCorrelateExecutionsAcrossHubScopes(t *testing.T) {
	cost := int64(25)
	result := buildStatisticsBreakdowns(
		[]domain.ProjectAgent{{ID: "agent-1", Name: "Backend"}},
		[]domain.Team{{ID: "team-1", Name: "Feature Team"}},
		[]domain.Quest{{ID: "quest-1", Title: "OAuth", TeamID: "team-1", FlowID: "flow-1"}},
		[]domain.FlowGraph{{ID: "flow-1", Name: "Feature Flow"}},
		[]domain.ExecutionInstance{
			{ID: "exec-1", ProjectAgentID: "agent-1", QuestID: "quest-1", Status: domain.RunCompleted, DurationMs: 1000},
			{ID: "exec-2", ProjectAgentID: "agent-1", QuestID: "quest-1", Status: domain.RunFailed, DurationMs: 3000},
		},
		[]domain.UsageRecord{
			{ExecutionID: "exec-1", ProjectAgentID: "agent-1", QuestID: "quest-1", Provider: "openai", Model: "model-a", TotalTokens: 100, CostCents: &cost},
			{ExecutionID: "exec-2", ProjectAgentID: "agent-1", QuestID: "quest-1", Provider: "openai", Model: "model-a", TotalTokens: 200},
		},
		map[string]diagnostics.RunDiagnostics{
			"run-1": {
				RunID: "run-1", Health: diagnostics.HealthHealthy,
				Verification: diagnostics.VerificationMetrics{Required: true, Recorded: true},
				Tools:        diagnostics.ToolMetrics{Calls: 4, Succeeded: 3, Failed: 1},
			},
			"run-2": {
				RunID: "run-2", Health: diagnostics.HealthAttention,
				Verification: diagnostics.VerificationMetrics{Required: true, Recorded: false},
				Tools:        diagnostics.ToolMetrics{Calls: 2, Succeeded: 2},
			},
		},
	)
	for _, key := range []string{"agentStats", "teamStats", "questStats", "flowStats", "modelStats", "providerStats"} {
		buckets, ok := result[key].([]statisticsBucket)
		if !ok || len(buckets) != 1 {
			t.Fatalf("%s=%#v", key, result[key])
		}
		bucket := buckets[0]
		if bucket.Executions != 2 || bucket.Completed != 1 || bucket.Failed != 1 {
			t.Fatalf("%s execution totals=%#v", key, bucket)
		}
		if bucket.SuccessRate != 50 || bucket.TotalTokens != 300 || bucket.AverageTokens != 150 {
			t.Fatalf("%s aggregate=%#v", key, bucket)
		}
		if bucket.AverageTimeMs != 2000 || bucket.KnownCostCents != 25 {
			t.Fatalf("%s averages=%#v", key, bucket)
		}
	}
}

func TestSkillVersionStatisticsKeepsExactDigestsSeparate(t *testing.T) {
	now := time.Now().UTC()
	outcomes := []domain.SkillOutcome{
		{RunID: "run-1", SkillID: "skill-a", SkillName: "API", SkillRevision: 2, SkillDigest: "digest-old", PromotionStatus: "candidate", RunStatus: domain.RunCompleted, Health: string(diagnostics.HealthHealthy), ToolCalls: 4, VerificationRequired: true, VerificationRecorded: true, CreatedAt: now},
		{RunID: "run-2", SkillID: "skill-a", SkillName: "API", SkillRevision: 2, SkillDigest: "digest-old", PromotionStatus: "candidate", RunStatus: domain.RunFailed, Health: string(diagnostics.HealthFailed), ToolCalls: 3, ToolFailures: 2, FeedbackCount: 1, CompletionRevisions: 1, ApprovalDenied: 1, VerificationRequired: true, CreatedAt: now.Add(time.Minute)},
		// Same displayed revision but a different payload must never be merged.
		{RunID: "run-3", SkillID: "skill-a", SkillName: "API", SkillRevision: 2, SkillDigest: "digest-rewritten", PromotionStatus: "promoted", RunStatus: domain.RunCompleted, Health: string(diagnostics.HealthHealthy), ToolCalls: 2, CreatedAt: now.Add(2 * time.Minute)},
		// Idempotency guard: a duplicate run/skill outcome cannot inflate evidence.
		{RunID: "run-3", SkillID: "skill-a", SkillName: "API", SkillRevision: 2, SkillDigest: "digest-rewritten", RunStatus: domain.RunCompleted, Health: string(diagnostics.HealthHealthy), ToolCalls: 99, CreatedAt: now.Add(3 * time.Minute)},
	}
	stats := buildSkillVersionStatistics(outcomes)
	if len(stats) != 2 {
		t.Fatalf("skill version stats=%#v", stats)
	}
	var old, rewritten statisticsSkillVersionEvidence
	for _, item := range stats {
		switch item.Digest {
		case "digest-old":
			old = item
		case "digest-rewritten":
			rewritten = item
		}
	}
	if old.Runs != 2 || old.Completed != 1 || old.Healthy != 1 || old.Failed != 1 || old.ToolCalls != 7 || old.ToolFailures != 2 || old.ToolSuccessRate == nil || *old.ToolSuccessRate < 71 || *old.ToolSuccessRate > 72 {
		t.Fatalf("old exact version evidence=%#v", old)
	}
	if old.VerificationRequired != 2 || old.VerificationSatisfied != 1 || old.VerificationRate == nil || *old.VerificationRate != 50 || old.FeedbackRuns != 1 || old.RevisionRuns != 1 || old.ApprovalDenied != 1 {
		t.Fatalf("old correction evidence=%#v", old)
	}
	if rewritten.Runs != 1 || rewritten.ToolCalls != 2 || rewritten.PromotionStatus != "promoted" {
		t.Fatalf("rewritten exact version evidence=%#v", rewritten)
	}
}

func TestStatisticsBreakdownsExposeEvidenceInsteadOfSyntheticQualityScore(t *testing.T) {
	result := buildStatisticsBreakdowns(
		[]domain.ProjectAgent{{ID: "agent-1", Name: "Backend"}}, nil,
		[]domain.Quest{{ID: "quest-1", Title: "OAuth"}}, nil,
		[]domain.ExecutionInstance{
			{ID: "exec-1", RunID: "run-1", ProjectAgentID: "agent-1", QuestID: "quest-1", Status: domain.RunCompleted},
			{ID: "exec-2", RunID: "run-2", ProjectAgentID: "agent-1", QuestID: "quest-1", Status: domain.RunCompleted},
			// A deterministic execution has no Run diagnostics. Coverage must expose
			// that gap instead of silently treating it as healthy.
			{ID: "exec-3", ProjectAgentID: "agent-1", QuestID: "quest-1", Status: domain.RunCompleted},
		},
		nil,
		map[string]diagnostics.RunDiagnostics{
			"run-1": {
				RunID: "run-1", Health: diagnostics.HealthHealthy,
				Verification: diagnostics.VerificationMetrics{Required: true, Recorded: true},
				Tools:        diagnostics.ToolMetrics{Calls: 4, Succeeded: 3, Failed: 1},
			},
			"run-2": {
				RunID: "run-2", Health: diagnostics.HealthAttention,
				Verification: diagnostics.VerificationMetrics{Required: true},
				Tools:        diagnostics.ToolMetrics{Calls: 2, Succeeded: 2},
			},
		},
	)
	buckets := result["agentStats"].([]statisticsBucket)
	if len(buckets) != 1 {
		t.Fatalf("agentStats=%#v", buckets)
	}
	quality := buckets[0].Quality
	if quality.AssessedExecutions != 2 || quality.HealthyExecutions != 1 || quality.AttentionExecutions != 1 {
		t.Fatalf("quality coverage/health=%#v", quality)
	}
	if quality.VerificationRequired != 2 || quality.VerificationSatisfied != 1 || quality.VerificationRate == nil || *quality.VerificationRate != 50 {
		t.Fatalf("verification evidence=%#v", quality)
	}
	if quality.ToolCalls != 6 || quality.ToolSucceeded != 5 || quality.ToolFailed != 1 || quality.ToolSuccessRate == nil || *quality.ToolSuccessRate < 83 || *quality.ToolSuccessRate > 84 {
		t.Fatalf("tool evidence=%#v", quality)
	}
	if len(quality.Recommendations) != 1 || quality.Recommendations[0].Code != "verification_gap" || quality.Recommendations[0].Step != "skills" {
		t.Fatalf("verification recommendation=%#v", quality.Recommendations)
	}
	if questQuality := result["questStats"].([]statisticsBucket)[0].Quality; len(questQuality.Recommendations) != 0 {
		t.Fatalf("recommendations must remain agent-specific: %#v", questQuality.Recommendations)
	}
}

func TestStatisticsRecommendationsRequireRepeatedEvidenceAndRouteToAgentSettings(t *testing.T) {
	reports := map[string]diagnostics.RunDiagnostics{}
	executions := make([]domain.ExecutionInstance, 0, 3)
	for index := 1; index <= 3; index++ {
		runID := fmt.Sprintf("run-%d", index)
		succeeded, failed := 2, 0
		if index <= 2 {
			succeeded, failed = 1, 1
		}
		executions = append(executions, domain.ExecutionInstance{
			ID: fmt.Sprintf("exec-%d", index), RunID: runID,
			ProjectAgentID: "agent-1", Status: domain.RunCompleted,
		})
		reports[runID] = diagnostics.RunDiagnostics{
			RunID: runID, Health: diagnostics.HealthAttention,
			Verification: diagnostics.VerificationMetrics{Required: true, Recorded: index == 3},
			Tools: diagnostics.ToolMetrics{
				Calls: 2, Succeeded: succeeded, Failed: failed,
				Items: []diagnostics.ToolMetric{{Name: "run_command", Calls: 2, Succeeded: succeeded, Failed: failed}},
			},
			Approvals:  diagnostics.ApprovalMetrics{Requested: 1, Denied: map[bool]int{true: 1, false: 0}[index <= 2]},
			Completion: diagnostics.CompletionMetrics{Checks: 1, RevisionRequests: map[bool]int{true: 1, false: 0}[index <= 2]},
		}
	}
	result := buildStatisticsBreakdowns(
		[]domain.ProjectAgent{{ID: "agent-1", Name: "Backend"}}, nil, nil, nil,
		executions, nil, reports,
	)
	recommendations := result["agentStats"].([]statisticsBucket)[0].Quality.Recommendations
	if len(recommendations) != 4 {
		t.Fatalf("recommendations=%#v", recommendations)
	}
	wantSteps := map[string]string{
		"verification_gap":     "skills",
		"tool_instability":     "tools",
		"permission_friction":  "permissions",
		"completion_revisions": "rules",
	}
	for _, recommendation := range recommendations {
		if wantSteps[recommendation.Code] != recommendation.Step || recommendation.Evidence == "" || recommendation.ActionLabel == "" {
			t.Fatalf("recommendation=%#v", recommendation)
		}
		delete(wantSteps, recommendation.Code)
	}
	if len(wantSteps) != 0 {
		t.Fatalf("missing recommendation routes=%#v", wantSteps)
	}
}

func TestStatisticsRecommendationsIgnoreSingleNoisyRun(t *testing.T) {
	result := buildStatisticsBreakdowns(
		[]domain.ProjectAgent{{ID: "agent-1", Name: "Backend"}}, nil, nil, nil,
		[]domain.ExecutionInstance{{ID: "exec-1", RunID: "run-1", ProjectAgentID: "agent-1", Status: domain.RunFailed}}, nil,
		map[string]diagnostics.RunDiagnostics{"run-1": {
			RunID: "run-1", Health: diagnostics.HealthFailed,
			Verification: diagnostics.VerificationMetrics{Required: true},
			Tools:        diagnostics.ToolMetrics{Calls: 2, Failed: 2, Items: []diagnostics.ToolMetric{{Name: "run_command", Calls: 2, Failed: 2}}},
			Approvals:    diagnostics.ApprovalMetrics{Requested: 1, Denied: 1},
			Completion:   diagnostics.CompletionMetrics{Checks: 1, RevisionRequests: 1},
		}},
	)
	quality := result["agentStats"].([]statisticsBucket)[0].Quality
	if len(quality.Recommendations) != 0 {
		t.Fatalf("single run must not produce a development recommendation: %#v", quality.Recommendations)
	}
}

func TestStatisticsReplaysPersistedRunEvidenceForAgentQuality(t *testing.T) {
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
		Name: "Backend", PrimaryModel: "test-model", Provider: domain.ProviderOpenAI,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	finished := now.Add(2 * time.Second)
	run := domain.Run{
		ID: "run-quality", AgentID: agent.ID, ProfileID: agent.ID,
		WorkspaceID: view.Workspace.ID, Task: "Run tests", Status: domain.RunCompleted,
		StartedAt: now, FinishedAt: &finished, DurationMs: 2000,
	}
	ctx := context.Background()
	if err = application.store.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	for index, event := range []domain.Event{
		{ID: "event-request", RunID: run.ID, AgentID: agent.ID, Type: domain.EventToolRequested, Data: json.RawMessage(`{"tool":"run_command","arguments":{"command":"go test ./..."}}`), CreatedAt: now.Add(time.Second)},
		{ID: "event-start", RunID: run.ID, AgentID: agent.ID, Type: domain.EventToolStarted, Data: json.RawMessage(`{"tool":"run_command"}`), CreatedAt: now.Add(1200 * time.Millisecond)},
		{ID: "event-finish", RunID: run.ID, AgentID: agent.ID, Type: domain.EventToolFinished, Data: json.RawMessage(`{"tool":"run_command","result":{"ok":true,"output":{"exitCode":0}}}`), CreatedAt: now.Add(1500 * time.Millisecond)},
	} {
		event.Step = index + 1
		if err = application.store.Append(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	if err = application.store.SaveExecution(ctx, domain.ExecutionInstance{
		ID: "execution-quality", WorkspaceID: view.Workspace.ID,
		ProjectAgentID: agent.ID, RunID: run.ID, Status: domain.RunCompleted,
		StartedAt: now, FinishedAt: &finished, DurationMs: 2000,
	}); err != nil {
		t.Fatal(err)
	}

	stats, err := application.Statistics(view.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stats["qualityRunsAnalyzed"] != 1 {
		t.Fatalf("qualityRunsAnalyzed=%#v", stats["qualityRunsAnalyzed"])
	}
	buckets := stats["agentStats"].([]statisticsBucket)
	if len(buckets) != 1 {
		t.Fatalf("agentStats=%#v", buckets)
	}
	quality := buckets[0].Quality
	if quality.AssessedExecutions != 1 || quality.HealthyExecutions != 1 {
		t.Fatalf("health evidence=%#v", quality)
	}
	if quality.VerificationRequired != 1 || quality.VerificationSatisfied != 1 {
		t.Fatalf("verification evidence=%#v", quality)
	}
	if quality.ToolSucceeded != 1 || quality.ToolFailed != 0 {
		t.Fatalf("tool evidence=%#v", quality)
	}
}

func TestBudgetPhaseTotalsSeparateDiscussionFromQuestPhases(t *testing.T) {
	phases := buildBudgetPhaseTotals([]domain.UsageRecord{
		{Outcome: "master_model", TotalTokens: 100},
		{Outcome: "companion_model", TotalTokens: 40},
		{Outcome: "orchestrator_plan", QuestID: "q1", TotalTokens: 200},
		{Outcome: "usage_reported", QuestID: "q1", TotalTokens: 300},
		{Outcome: "agent_self_improvement", QuestID: "q1", TotalTokens: 50},
	})
	if phases["discussionTokens"] != int64(140) {
		t.Fatalf("discussion=%#v", phases["discussionTokens"])
	}
	if phases["planTokens"] != int64(200) || phases["executionTokens"] != int64(300) || phases["learningTokens"] != int64(50) {
		t.Fatalf("quest phases=%#v", phases)
	}
	if phases["budgetCoverage"] != "partial" {
		t.Fatalf("coverage=%#v", phases["budgetCoverage"])
	}
	coverage := phases["phaseCoverage"].(map[string]string)
	if coverage["discussion"] != "partial" || coverage["plan"] != "full" {
		t.Fatalf("phaseCoverage=%#v", coverage)
	}

	result := buildStatisticsBreakdowns(
		nil, nil,
		[]domain.Quest{{ID: "q1", Title: "Root"}},
		nil, nil,
		[]domain.UsageRecord{
			{Outcome: "orchestrator_plan", QuestID: "q1", TotalTokens: 200},
			{Outcome: "usage_reported", QuestID: "q1", TotalTokens: 300},
			{Outcome: "agent_self_improvement", QuestID: "q1", TotalTokens: 50},
			{Outcome: "master_model", TotalTokens: 100},
		},
		nil,
	)
	quests := result["questStats"].([]statisticsBucket)
	if len(quests) != 1 {
		t.Fatalf("questStats=%#v", quests)
	}
	quest := quests[0]
	if quest.TotalTokens != 550 || quest.PlanTokens != 200 || quest.ExecutionTokens != 300 || quest.LearningTokens != 50 {
		t.Fatalf("quest phase fields=%#v", quest)
	}
	if quest.DiscussionTokens != 0 {
		t.Fatalf("discussion must not mix into questStats: %#v", quest)
	}
	if quest.BudgetCoverage != "partial" {
		t.Fatalf("quest budgetCoverage=%q", quest.BudgetCoverage)
	}
}
