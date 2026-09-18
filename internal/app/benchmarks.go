package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"local-agent-workbench/internal/diagnostics"
	"local-agent-workbench/internal/domain"
)

type AgentBenchmarkEvaluationRequest struct {
	BenchmarkSetID string            `json:"benchmarkSetId"`
	Label          string            `json:"label"`
	CaseRuns       map[string]string `json:"caseRuns"`
}

type AgentBenchmarkComparisonRequest struct {
	BeforeID string `json:"beforeId"`
	AfterID  string `json:"afterId"`
}

func (a *App) SaveAgentBenchmarkSet(set domain.AgentBenchmarkSet) (domain.AgentBenchmarkSet, error) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return domain.AgentBenchmarkSet{}, err
	}
	ctx := context.Background()
	if set.WorkspaceID != "" && set.WorkspaceID != ws.ID {
		return domain.AgentBenchmarkSet{}, errors.New("benchmark set belongs to another workspace")
	}
	set.WorkspaceID = ws.ID
	set.Name = strings.TrimSpace(set.Name)
	set.Description = strings.TrimSpace(set.Description)
	if set.Name == "" || len([]rune(set.Name)) > 120 {
		return domain.AgentBenchmarkSet{}, errors.New("benchmark name is required and must not exceed 120 characters")
	}
	if len([]rune(set.Description)) > 4096 {
		return domain.AgentBenchmarkSet{}, errors.New("benchmark description exceeds 4096 characters")
	}
	agent, err := a.store.GetProjectAgent(ctx, strings.TrimSpace(set.ProjectAgentID))
	if err != nil {
		return domain.AgentBenchmarkSet{}, err
	}
	if agent.WorkspaceID != ws.ID {
		return domain.AgentBenchmarkSet{}, errForeignWorld
	}
	set.ProjectAgentID = agent.ID
	set.SkillID = strings.TrimSpace(set.SkillID)
	if set.SkillID != "" && !slices.Contains(agent.SkillIDs, set.SkillID) {
		return domain.AgentBenchmarkSet{}, errors.New("benchmark Skill is not equipped by the selected project agent")
	}
	if len(set.Cases) == 0 || len(set.Cases) > 50 {
		return domain.AgentBenchmarkSet{}, errors.New("benchmark set must contain between 1 and 50 cases")
	}
	caseIDs := map[string]bool{}
	for index := range set.Cases {
		item := &set.Cases[index]
		item.Name, item.Task = strings.TrimSpace(item.Name), strings.TrimSpace(item.Task)
		if item.ID == "" {
			item.ID = domain.NewID("benchmarkcase")
		}
		if caseIDs[item.ID] {
			return domain.AgentBenchmarkSet{}, fmt.Errorf("duplicate benchmark case id %q", item.ID)
		}
		caseIDs[item.ID] = true
		if item.Name == "" || len([]rune(item.Name)) > 120 {
			return domain.AgentBenchmarkSet{}, fmt.Errorf("benchmark case %d name is required and must not exceed 120 characters", index+1)
		}
		if item.Task == "" || len([]rune(item.Task)) > 8000 {
			return domain.AgentBenchmarkSet{}, fmt.Errorf("benchmark case %q task is required and must not exceed 8000 characters", item.Name)
		}
		if item.ExpectedStatus == "" {
			item.ExpectedStatus = domain.RunCompleted
		}
		if !terminalLearningStatus(item.ExpectedStatus) {
			return domain.AgentBenchmarkSet{}, fmt.Errorf("benchmark case %q expected status must be terminal", item.Name)
		}
		if item.MaximumToolFailures < 0 || item.MaximumToolFailures > 1000 {
			return domain.AgentBenchmarkSet{}, fmt.Errorf("benchmark case %q maximum tool failures must be between 0 and 1000", item.Name)
		}
	}
	now := time.Now().UTC()
	var previous *domain.AgentBenchmarkSet
	if strings.TrimSpace(set.ID) != "" {
		stored, getErr := a.store.GetAgentBenchmarkSet(ctx, strings.TrimSpace(set.ID))
		if getErr != nil {
			return domain.AgentBenchmarkSet{}, getErr
		}
		if stored.WorkspaceID != ws.ID {
			return domain.AgentBenchmarkSet{}, errForeignWorld
		}
		previous = &stored
		set.ID, set.CreatedAt = stored.ID, stored.CreatedAt
	} else {
		set.ID, set.CreatedAt = domain.NewID("benchmark"), now
	}
	set.Digest = agentBenchmarkSetDigest(set)
	set.Revision = 1
	if previous != nil {
		set.Revision = previous.Revision
		if set.Digest != previous.Digest {
			set.Revision++
		}
	}
	set.UpdatedAt = now
	if err = a.store.SaveAgentBenchmarkSet(ctx, set); err != nil {
		return domain.AgentBenchmarkSet{}, err
	}
	return set, nil
}

func (a *App) ListAgentBenchmarkSets() ([]domain.AgentBenchmarkSet, error) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return nil, err
	}
	return a.store.ListAgentBenchmarkSets(context.Background(), ws.ID)
}

func (a *App) EvaluateAgentBenchmark(request AgentBenchmarkEvaluationRequest) (domain.AgentBenchmarkEvaluation, error) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return domain.AgentBenchmarkEvaluation{}, err
	}
	ctx := context.Background()
	set, err := a.store.GetAgentBenchmarkSet(ctx, strings.TrimSpace(request.BenchmarkSetID))
	if err != nil {
		return domain.AgentBenchmarkEvaluation{}, err
	}
	if set.WorkspaceID != ws.ID {
		return domain.AgentBenchmarkEvaluation{}, errForeignWorld
	}
	request.Label = strings.TrimSpace(request.Label)
	if request.Label == "" || len([]rune(request.Label)) > 80 {
		return domain.AgentBenchmarkEvaluation{}, errors.New("benchmark evaluation label is required and must not exceed 80 characters")
	}
	if len(request.CaseRuns) != len(set.Cases) {
		return domain.AgentBenchmarkEvaluation{}, errors.New("benchmark evaluation requires exactly one Run for every case")
	}
	evaluation := domain.AgentBenchmarkEvaluation{
		ID: domain.NewID("benchmarkeval"), WorkspaceID: ws.ID, ProjectAgentID: set.ProjectAgentID,
		BenchmarkSetID: set.ID, SetRevision: set.Revision, SetDigest: set.Digest,
		Label: request.Label, Cases: make([]domain.AgentBenchmarkCaseOutcome, 0, len(set.Cases)), CreatedAt: time.Now().UTC(),
	}
	usedRuns := map[string]bool{}
	for _, benchmarkCase := range set.Cases {
		runID := strings.TrimSpace(request.CaseRuns[benchmarkCase.ID])
		if runID == "" {
			return domain.AgentBenchmarkEvaluation{}, fmt.Errorf("benchmark case %q has no Run", benchmarkCase.Name)
		}
		if usedRuns[runID] {
			return domain.AgentBenchmarkEvaluation{}, errors.New("one Run cannot provide evidence for multiple benchmark cases")
		}
		usedRuns[runID] = true
		run, getErr := a.store.GetRun(ctx, runID)
		if getErr != nil {
			return domain.AgentBenchmarkEvaluation{}, getErr
		}
		if run.WorkspaceID != ws.ID || (run.ProfileID != set.ProjectAgentID && run.AgentID != set.ProjectAgentID) {
			return domain.AgentBenchmarkEvaluation{}, fmt.Errorf("benchmark Run %q belongs to another workspace or agent", runID)
		}
		if strings.TrimSpace(run.Task) != benchmarkCase.Task {
			return domain.AgentBenchmarkEvaluation{}, fmt.Errorf("benchmark Run %q task does not match case %q", runID, benchmarkCase.Name)
		}
		if !terminalLearningStatus(run.Status) || run.FinishedAt == nil {
			return domain.AgentBenchmarkEvaluation{}, fmt.Errorf("benchmark Run %q is not terminal", runID)
		}
		if run.ConfigurationSnapshot.SchemaVersion < 2 || strings.TrimSpace(run.ConfigurationSnapshot.ConfigurationDigest) == "" {
			return domain.AgentBenchmarkEvaluation{}, fmt.Errorf("benchmark Run %q lacks schema v2 exact configuration attribution", runID)
		}
		report, reportErr := a.runDiagnostics(ctx, run)
		if reportErr != nil {
			return domain.AgentBenchmarkEvaluation{}, reportErr
		}
		outcome := benchmarkCaseOutcome(benchmarkCase, run, report)
		if set.SkillID != "" && !benchmarkAttributionContains(outcome.SkillAttributions, set.SkillID) {
			outcome.Passed = false
			outcome.Reasons = append(outcome.Reasons, "required benchmark Skill was not loaded")
		}
		evaluation.Cases = append(evaluation.Cases, outcome)
		accumulateBenchmarkMetrics(&evaluation.Metrics, outcome)
	}
	if err = a.store.SaveAgentBenchmarkEvaluation(ctx, evaluation); err != nil {
		return domain.AgentBenchmarkEvaluation{}, err
	}
	return evaluation, nil
}

func (a *App) ListAgentBenchmarkEvaluations(limit int) ([]domain.AgentBenchmarkEvaluation, error) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return nil, err
	}
	return a.store.ListAgentBenchmarkEvaluations(context.Background(), ws.ID, limit)
}

func (a *App) CompareAgentBenchmarks(request AgentBenchmarkComparisonRequest) (domain.AgentBenchmarkComparison, error) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return domain.AgentBenchmarkComparison{}, err
	}
	ctx := context.Background()
	before, err := a.store.GetAgentBenchmarkEvaluation(ctx, strings.TrimSpace(request.BeforeID))
	if err != nil {
		return domain.AgentBenchmarkComparison{}, err
	}
	after, err := a.store.GetAgentBenchmarkEvaluation(ctx, strings.TrimSpace(request.AfterID))
	if err != nil {
		return domain.AgentBenchmarkComparison{}, err
	}
	if before.WorkspaceID != ws.ID || after.WorkspaceID != ws.ID {
		return domain.AgentBenchmarkComparison{}, errForeignWorld
	}
	return compareAgentBenchmarkEvaluations(before, after)
}

func compareAgentBenchmarkEvaluations(before, after domain.AgentBenchmarkEvaluation) (domain.AgentBenchmarkComparison, error) {
	if before.BenchmarkSetID != after.BenchmarkSetID || before.SetRevision != after.SetRevision || before.SetDigest != after.SetDigest {
		return domain.AgentBenchmarkComparison{}, errors.New("before and after evaluations must use the exact same benchmark set revision and digest")
	}
	beforeByCase := map[string]domain.AgentBenchmarkCaseOutcome{}
	for _, outcome := range before.Cases {
		beforeByCase[outcome.CaseID] = outcome
	}
	comparison := domain.AgentBenchmarkComparison{
		BenchmarkSetID: before.BenchmarkSetID, SetRevision: before.SetRevision, SetDigest: before.SetDigest,
		BeforeID: before.ID, AfterID: after.ID, Before: before.Metrics, After: after.Metrics,
		RegressedCases: []string{}, RecoveredCases: []string{}, Reasons: []string{}, ComparedAt: time.Now().UTC(),
	}
	configurationChanged := false
	for _, outcome := range after.Cases {
		previous, ok := beforeByCase[outcome.CaseID]
		if !ok {
			return domain.AgentBenchmarkComparison{}, errors.New("before and after evaluations do not contain the same cases")
		}
		if outcome.ConfigurationDigest != previous.ConfigurationDigest {
			configurationChanged = true
		}
		if previous.Passed && !outcome.Passed {
			comparison.RegressedCases = append(comparison.RegressedCases, outcome.CaseName)
		}
		if !previous.Passed && outcome.Passed {
			comparison.RecoveredCases = append(comparison.RecoveredCases, outcome.CaseName)
		}
		delete(beforeByCase, outcome.CaseID)
	}
	if len(beforeByCase) != 0 {
		return domain.AgentBenchmarkComparison{}, errors.New("before and after evaluations do not contain the same cases")
	}
	if !configurationChanged {
		return domain.AgentBenchmarkComparison{}, errors.New("before and after evaluations use the same exact Run configuration for every case")
	}
	comparison.GatePassed = len(comparison.RegressedCases) == 0
	if comparison.GatePassed {
		comparison.Reasons = append(comparison.Reasons, "no previously passing benchmark case regressed")
	} else {
		comparison.Reasons = append(comparison.Reasons, fmt.Sprintf("%d previously passing benchmark case(s) regressed", len(comparison.RegressedCases)))
	}
	return comparison, nil
}

func agentBenchmarkSetDigest(set domain.AgentBenchmarkSet) string {
	encoded, _ := json.Marshal(struct {
		ProjectAgentID string                      `json:"projectAgentId"`
		SkillID        string                      `json:"skillId,omitempty"`
		Name           string                      `json:"name"`
		Description    string                      `json:"description,omitempty"`
		Cases          []domain.AgentBenchmarkCase `json:"cases"`
	}{set.ProjectAgentID, set.SkillID, set.Name, set.Description, set.Cases})
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func benchmarkAttributionContains(items []domain.SkillAttribution, skillID string) bool {
	for _, item := range items {
		if item.SkillID == skillID && strings.TrimSpace(item.Digest) != "" {
			return true
		}
	}
	return false
}

func benchmarkCaseOutcome(benchmarkCase domain.AgentBenchmarkCase, run domain.Run, report diagnostics.RunDiagnostics) domain.AgentBenchmarkCaseOutcome {
	outcome := domain.AgentBenchmarkCaseOutcome{
		CaseID: benchmarkCase.ID, CaseName: benchmarkCase.Name, RunID: run.ID,
		ConfigurationDigest: run.ConfigurationSnapshot.ConfigurationDigest,
		ProfileDigest:       run.ConfigurationSnapshot.ProfileDigest,
		SkillAttributions:   append([]domain.SkillAttribution(nil), run.ConfigurationSnapshot.SkillAttributions...),
		RunStatus:           run.Status, Health: string(report.Health), ToolCalls: report.Tools.Calls, ToolFailures: report.Tools.Failed,
		VerificationRequired: report.Verification.Required, VerificationRecorded: report.Verification.Recorded,
		Passed: true, Reasons: []string{},
	}
	if run.Status != benchmarkCase.ExpectedStatus {
		outcome.Passed = false
		outcome.Reasons = append(outcome.Reasons, fmt.Sprintf("status is %s; expected %s", run.Status, benchmarkCase.ExpectedStatus))
	}
	if benchmarkCase.RequireHealthy && report.Health != diagnostics.HealthHealthy {
		outcome.Passed = false
		outcome.Reasons = append(outcome.Reasons, fmt.Sprintf("health is %s; expected healthy", report.Health))
	}
	if benchmarkCase.RequireVerification && !report.Verification.Recorded {
		outcome.Passed = false
		outcome.Reasons = append(outcome.Reasons, "required verification was not recorded")
	}
	if report.Tools.Failed > benchmarkCase.MaximumToolFailures {
		outcome.Passed = false
		outcome.Reasons = append(outcome.Reasons,
			fmt.Sprintf("tool failures are %d; maximum is %d", report.Tools.Failed, benchmarkCase.MaximumToolFailures))
	}
	if outcome.Passed {
		outcome.Reasons = append(outcome.Reasons, "all declared case criteria passed")
	}
	return outcome
}

func accumulateBenchmarkMetrics(metrics *domain.AgentBenchmarkMetrics, outcome domain.AgentBenchmarkCaseOutcome) {
	metrics.Cases++
	if outcome.Passed {
		metrics.Passed++
	}
	if outcome.RunStatus == domain.RunCompleted {
		metrics.Completed++
	}
	if strings.EqualFold(outcome.Health, "healthy") {
		metrics.Healthy++
	}
	metrics.ToolCalls += outcome.ToolCalls
	metrics.ToolFailures += outcome.ToolFailures
	if outcome.VerificationRequired {
		metrics.VerificationRequired++
		if outcome.VerificationRecorded {
			metrics.VerificationRecorded++
		}
	}
}
