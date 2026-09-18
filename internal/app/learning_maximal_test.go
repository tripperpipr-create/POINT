package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/diagnostics"
	"local-agent-workbench/internal/domain"
)

func TestLearningEvidencePackCapturesToolOutcomesWithoutFileBodies(t *testing.T) {
	now := time.Now().UTC()
	run := domain.Run{ID: "run-ev", WorkspaceID: "ws", ProfileID: "agent-1", Status: domain.RunFailed, Task: "secret path C:\\Users\\x\\repo"}
	events := []domain.Event{
		{Type: domain.EventToolFinished, ExecutionID: "exec-1", QuestID: "quest-1", Data: mustJSON(t, map[string]any{
			"tool": "read_file", "result": domain.ToolResult{OK: true},
		})},
		{Type: domain.EventToolFinished, Data: mustJSON(t, map[string]any{
			"tool": "list_files", "result": domain.ToolResult{OK: true},
		})},
		{Type: domain.EventToolFinished, Data: mustJSON(t, map[string]any{
			"tool": "search_text", "result": domain.ToolResult{OK: true},
		})},
		{Type: domain.EventToolFinished, Data: mustJSON(t, map[string]any{
			"tool": "run_command", "result": domain.ToolResult{OK: false, Error: &domain.ToolError{Code: "timeout", Message: "deadline exceeded at C:\\Users\\x\\repo\\main.go"}},
		})},
		{Type: domain.EventToolFinished, Data: mustJSON(t, map[string]any{
			"tool": "search_code", "result": domain.ToolResult{OK: false, Error: &domain.ToolError{Code: "denied", Message: "approval denied"}},
		})},
	}
	report := diagnostics.RunDiagnostics{
		Health: diagnostics.HealthFailed, StopReason: "tool_failed",
		Verification: diagnostics.VerificationMetrics{Required: true, Recorded: false},
		Approvals:    diagnostics.ApprovalMetrics{Denied: 1},
		Tools:        diagnostics.ToolMetrics{Calls: 5, Failed: 2},
	}
	pack := buildLearningEvidencePack(run, report, events, "agent-1")
	if pack.ToolCalls != 5 || pack.ToolFailures != 2 || pack.ExecutionID != "exec-1" {
		t.Fatalf("pack counters=%#v", pack)
	}
	if pack.Steps[3].ErrorClass != "timeout" || pack.Steps[4].ErrorClass != "denied" {
		t.Fatalf("error classes=%#v", pack.Steps)
	}
	if strings.Contains(pack.Steps[3].ErrorHint, `C:\Users`) {
		t.Fatalf("path leaked into hint: %q", pack.Steps[3].ErrorHint)
	}
	traj := trajectoryFromEvidencePack(pack)
	trigger := learningReviewTrigger(report, traj)
	if trigger != learningTriggerFailure {
		t.Fatalf("trigger=%q", trigger)
	}
	dir := t.TempDir()
	application := &App{dataDir: dir}
	if err := application.persistLearningEvidencePack(pack, "agent-1"); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, learningEvidencePackDirName, "agent-1"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("persisted=%v err=%v", entries, err)
	}
	_ = now
}

func TestFailureLearningEvalRejectsFeatureBuildAndStackTraces(t *testing.T) {
	now := time.Now().UTC()
	traj := learningTrajectory{
		Tools: []string{"read_file", "run_command"}, ToolCalls: 5,
		RunStatus: domain.RunFailed, Health: string(diagnostics.HealthFailed),
	}
	report := diagnostics.RunDiagnostics{Health: diagnostics.HealthFailed, Verification: diagnostics.VerificationMetrics{Required: true}}
	agent := domain.ProjectAgent{AllowedTools: []string{"read_file", "run_command"}}
	bad := learningReview{
		Decision: "create", Name: "Ship it", Instructions: "Implement the feature then panic: stack trace at C:\\Users\\x\\a.go",
	}
	evaluation := evaluateLearningCandidate(bad, nil, agent, traj, report, learningTriggerFailure, now)
	if evaluation.Passed {
		t.Fatalf("unsafe failure candidate passed: %#v", evaluation)
	}
	ok := learningReview{
		Decision: "create", Name: "Recover verification",
		Instructions: "When a required verification is missing, re-run the verifier with the existing allowed tools before claiming completion.",
	}
	evaluation = evaluateLearningCandidate(ok, nil, agent, traj, report, learningTriggerFailure, now)
	if !evaluation.Passed {
		t.Fatalf("safe recovery rejected: %s", learningEvaluationFailure(evaluation))
	}
}

func TestWorkflowDigestDiffersByVerificationContract(t *testing.T) {
	tools := []string{"read_file", "run_command"}
	a := workflowDigest("owner", "Backend", tools, false)
	b := workflowDigest("owner", "Backend", tools, true)
	if a == b {
		t.Fatal("verification contract must change workflow digest")
	}
	c := workflowDigest("owner", "Backend", tools, true)
	if b != c {
		t.Fatal("same inputs must be stable")
	}
}

func TestLearningProofStatusApplyUnprovenCanaryImprovedProvenNeutralUnproven(t *testing.T) {
	if got := learningProofStatus(nil, nil); got != "applied_unproven" {
		t.Fatalf("apply/nil=%q", got)
	}
	deferred := &domain.LearningShadowEvaluation{Status: "deferred", Passed: false}
	if got := learningProofStatus(deferred, nil); got != "applied_unproven" {
		t.Fatalf("deferred=%q", got)
	}
	// Shadow alone can prove later; apply path never passes a compared+passed shadow for a new skill.
	passedShadow := &domain.LearningShadowEvaluation{Status: "compared", Passed: true}
	if got := learningProofStatus(passedShadow, nil); got != "applied_proven" {
		t.Fatalf("shadow pass=%q", got)
	}
	improved := &domain.SkillCanaryEvaluation{Effect: "improved", Status: "healthy"}
	if got := learningProofStatus(deferred, improved); got != "applied_proven" {
		t.Fatalf("canary improved=%q", got)
	}
	neutral := &domain.SkillCanaryEvaluation{Effect: "neutral", Status: "healthy"}
	if got := learningProofStatus(deferred, neutral); got != "applied_unproven" {
		t.Fatalf("canary neutral=%q", got)
	}
	item := &domain.AgentImprovement{Status: "applied_unproven", CanaryEvaluation: &domain.SkillCanaryEvaluation{
		Status: "healthy", Effect: "neutral",
	}}
	proofStatusFromCanary(item)
	if item.Status != "applied_unproven" || item.Effect != "neutral" {
		t.Fatalf("neutral proof=%#v", item)
	}
	item.CanaryEvaluation.Effect = "improved"
	proofStatusFromCanary(item)
	if item.Status != "applied_proven" {
		t.Fatalf("improved proof=%#v", item)
	}
}

func TestCanaryEffectImprovedVsInsufficient(t *testing.T) {
	pending := domain.SkillCanaryEvaluation{Status: "pending", CandidateMetrics: domain.SkillCanaryMetrics{Runs: 1}}
	if canaryEffect(pending) != "insufficient_sample" {
		t.Fatalf("pending effect=%q", canaryEffect(pending))
	}
	improved := domain.SkillCanaryEvaluation{
		Status:           "healthy",
		CandidateMetrics: domain.SkillCanaryMetrics{Runs: 3, CompletionRate: 1, HealthyRate: 1},
		BaselineMetrics:  &domain.SkillCanaryMetrics{Runs: 3, CompletionRate: 0.5, HealthyRate: 0.5},
	}
	if canaryEffect(improved) != "improved" {
		t.Fatalf("improved effect=%q", canaryEffect(improved))
	}
}

func TestShadowBenchmarkNoSetOneEvalDeferredTwoEvalsCompare(t *testing.T) {
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
		Name: "Backend", Provider: domain.ProviderOllama, PrimaryModel: "test",
		AllowedTools: []string{"read_file"},
	})
	if err != nil {
		t.Fatal(err)
	}
	eval := application.evaluateLearningShadowBenchmark(context.Background(), agent, nil, nil, true)
	if eval.Status != "skipped_no_set" {
		t.Fatalf("no set shadow=%#v", eval)
	}
	if status := learningProofStatus(&eval, nil); status != "applied_unproven" {
		t.Fatalf("status=%q", status)
	}

	set, err := application.SaveAgentBenchmarkSet(domain.AgentBenchmarkSet{
		ProjectAgentID: agent.ID, Name: "Personal set",
		Cases: []domain.AgentBenchmarkCase{{
			Name: "Inspect", Task: "Inspect the workspace boundary", ExpectedStatus: domain.RunCompleted,
			RequireHealthy: true, MaximumToolFailures: 0,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	beforeSkill := &domain.SkillDefinition{ID: "skill-before", Name: "Before", Instructions: "before", Configuration: map[string]any{"revision": 1}}
	afterSkill := &domain.SkillDefinition{ID: "skill-after", Name: "After", Instructions: "after", Configuration: map[string]any{"revision": 2}}
	atApply := application.evaluateLearningShadowBenchmark(context.Background(), agent, beforeSkill, afterSkill, true)
	if atApply.Status != "deferred" || atApply.BenchmarkSetID != set.ID {
		t.Fatalf("at apply=%#v", atApply)
	}
	oneEval := application.evaluateLearningShadowBenchmark(context.Background(), agent, beforeSkill, afterSkill, false)
	if oneEval.Status != "deferred" || !strings.Contains(strings.Join(oneEval.Reasons, " "), "≥2 evaluations") {
		t.Fatalf("one eval=%#v", oneEval)
	}

	now := time.Now().UTC()
	beforeRun := benchmarkTestRun(agent, view.Workspace.ID, set.Cases[0].Task, "run-shadow-before", "model-before", domain.RunCompleted, now)
	afterRun := benchmarkTestRun(agent, view.Workspace.ID, set.Cases[0].Task, "run-shadow-after", "model-after", domain.RunCompleted, now.Add(time.Minute))
	if err = application.store.SaveRun(context.Background(), beforeRun); err != nil {
		t.Fatal(err)
	}
	if err = application.store.SaveRun(context.Background(), afterRun); err != nil {
		t.Fatal(err)
	}
	caseRuns := func(runID string) map[string]string { return map[string]string{set.Cases[0].ID: runID} }
	before, err := application.EvaluateAgentBenchmark(AgentBenchmarkEvaluationRequest{
		BenchmarkSetID: set.ID, Label: "before", CaseRuns: caseRuns(beforeRun.ID),
	})
	if err != nil {
		t.Fatal(err)
	}
	after, err := application.EvaluateAgentBenchmark(AgentBenchmarkEvaluationRequest{
		BenchmarkSetID: set.ID, Label: "after", CaseRuns: caseRuns(afterRun.ID),
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = before
	_ = after
	compared := application.evaluateLearningShadowBenchmark(context.Background(), agent, beforeSkill, afterSkill, false)
	if compared.Status != "compared" || !compared.Passed {
		t.Fatalf("two evals compare=%#v", compared)
	}
	if learningProofStatus(&compared, nil) != "applied_proven" {
		t.Fatalf("proven via shadow=%q", learningProofStatus(&compared, nil))
	}

	// Regression gate fail: after run fails while before passed.
	failRun := benchmarkTestRun(agent, view.Workspace.ID, set.Cases[0].Task, "run-shadow-fail", "model-fail", domain.RunFailed, now.Add(2*time.Minute))
	if err = application.store.SaveRun(context.Background(), failRun); err != nil {
		t.Fatal(err)
	}
	if _, err = application.EvaluateAgentBenchmark(AgentBenchmarkEvaluationRequest{
		BenchmarkSetID: set.ID, Label: "failing", CaseRuns: caseRuns(failRun.ID),
	}); err != nil {
		t.Fatal(err)
	}
	regressed := application.evaluateLearningShadowBenchmark(context.Background(), agent, beforeSkill, afterSkill, false)
	if regressed.Status != "compared" || regressed.Passed {
		t.Fatalf("regression compare=%#v", regressed)
	}
}

func TestExactSkillRevisionStillLearnsMemory(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	reviewJSON, err := json.Marshal(map[string]any{
		"decision": "create", "name": "Evidence-first change", "description": "Reusable verified workflow.",
		"instructions":   "Inspect relevant context, make the bounded change, then record an explicit verifier result.",
		"memoryDecision": "learn", "memoryKey": "exact-revision-memory",
		"memory":              "Treat explicit verifier output as the completion criterion for implementation work.",
		"instructionDecision": "skip",
	})
	if err != nil {
		t.Fatal(err)
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		writePlannerSSE(t, w, string(reviewJSON), 80, 40)
	}))
	defer provider.Close()
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	tools := []string{"project_map", "list_files", "search_code", "read_file", "search_text"}
	blueprint, err := application.SaveBlueprint(domain.AgentBlueprint{
		Name: "QA", RoleDescription: "Permanent quality specialist", Provider: domain.ProviderOpenAI,
		ProviderPreset: "openai", BaseURL: provider.URL, PrimaryModel: "review-model", AllowedTools: tools,
	})
	if err != nil {
		t.Fatal(err)
	}
	view, err := application.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	agent, err := application.SaveProjectAgent(domain.ProjectAgentFromBlueprint(view.Workspace.ID, blueprint))
	if err != nil {
		t.Fatal(err)
	}
	firstRun := saveLearningRun(t, application, view.Workspace.ID, agent, "run-exact-first", time.Now().UTC())
	first, err := application.reviewAgentRun(context.Background(), firstRun, agent.ID, "transient-key")
	if err != nil {
		t.Fatal(err)
	}
	if first.AfterSkill == nil || first.SkillID == "" || first.Kind != "skill_created" {
		t.Fatalf("first skill apply=%#v", first)
	}
	skillID := first.SkillID
	attribution := domain.SkillDefinitionAttribution(*first.AfterSkill)
	secondRun := saveLearningRun(t, application, view.Workspace.ID, agent, "run-exact-second", time.Now().UTC().Add(time.Minute))
	secondRun.ConfigurationSnapshot.SkillAttributions = []domain.SkillAttribution{attribution}
	secondRun.ConfigurationSnapshot.ConfigurationDigest = "sha256:exact-second"
	if err = application.store.SaveRun(context.Background(), secondRun); err != nil {
		t.Fatal(err)
	}
	second, err := application.reviewAgentRun(context.Background(), secondRun, agent.ID, "transient-key")
	if err != nil {
		t.Fatal(err)
	}
	if second.Kind != "memory_learned" || second.MemoryStatus == "" || second.AfterMemory == nil {
		t.Fatalf("exact revision should still learn memory: %#v", second)
	}
	if second.SkillID != "" && second.SkillID != skillID {
		t.Fatalf("skill id changed on exact revision: first=%q second=%q", skillID, second.SkillID)
	}
	if second.AfterSkill != nil && second.AfterSkill.ID != skillID {
		t.Fatalf("new skill revision minted: %#v", second.AfterSkill)
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
