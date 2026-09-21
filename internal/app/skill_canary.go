package app

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
)

const (
	minimumCanaryRuns   = 3
	maximumCanaryRuns   = 3
	maximumBaselineRuns = 5
)

// evaluateSkillCanary compares operational evidence for two exact immutable
// Skill versions. It intentionally has no weighted or synthetic score: each
// regression is an explicit percentage-point threshold over visible counters.
func evaluateSkillCanary(candidate domain.SkillAttribution, baseline *domain.SkillAttribution, outcomes []domain.SkillOutcome, now time.Time) domain.SkillCanaryEvaluation {
	evaluation := domain.SkillCanaryEvaluation{
		SchemaVersion: 1, Status: "pending", Candidate: candidate,
		MinimumCandidateRuns: minimumCanaryRuns, Reasons: []string{}, EvaluatedAt: now.UTC(),
	}
	candidateOutcomes := matchingSkillOutcomes(outcomes, candidate, maximumCanaryRuns)
	evaluation.CandidateMetrics = skillCanaryMetrics(candidateOutcomes)
	if baseline != nil {
		copy := *baseline
		evaluation.Baseline = &copy
		baselineOutcomes := matchingSkillOutcomes(outcomes, copy, maximumBaselineRuns)
		metrics := skillCanaryMetrics(baselineOutcomes)
		evaluation.BaselineMetrics = &metrics
	}
	if len(candidateOutcomes) < minimumCanaryRuns {
		evaluation.Reasons = append(evaluation.Reasons,
			fmt.Sprintf("need %d candidate runs; observed %d", minimumCanaryRuns, len(candidateOutcomes)))
		return evaluation
	}

	severe := severeCanaryOutcomes(candidateOutcomes)
	if baseline == nil {
		if severe >= 2 {
			evaluation.Status = "regressed"
			evaluation.Reasons = append(evaluation.Reasons,
				fmt.Sprintf("%d of %d candidate runs failed completion, health, or required verification", severe, len(candidateOutcomes)))
			return evaluation
		}
		evaluation.Status = "healthy"
		evaluation.Reasons = append(evaluation.Reasons, "candidate met the standalone acceptance gate")
		return evaluation
	}
	if evaluation.BaselineMetrics == nil || evaluation.BaselineMetrics.Runs < minimumCanaryRuns {
		if severe >= 2 {
			evaluation.Status = "regressed"
			evaluation.Reasons = append(evaluation.Reasons,
				fmt.Sprintf("%d of %d candidate runs failed completion, health, or required verification", severe, len(candidateOutcomes)))
			return evaluation
		}
		evaluation.Reasons = append(evaluation.Reasons,
			fmt.Sprintf("need %d baseline runs for an exact before/after comparison; observed %d", minimumCanaryRuns, evaluation.BaselineMetrics.Runs))
		return evaluation
	}

	candidateMetrics := evaluation.CandidateMetrics
	baselineMetrics := *evaluation.BaselineMetrics
	if candidateMetrics.CompletionRate-baselineMetrics.CompletionRate <= -0.25 {
		evaluation.Reasons = append(evaluation.Reasons, rateRegressionReason("completion", baselineMetrics.CompletionRate, candidateMetrics.CompletionRate))
	}
	if candidateMetrics.HealthyRate-baselineMetrics.HealthyRate <= -0.25 {
		evaluation.Reasons = append(evaluation.Reasons, rateRegressionReason("healthy runs", baselineMetrics.HealthyRate, candidateMetrics.HealthyRate))
	}
	if candidateMetrics.VerificationRequired >= 2 && baselineMetrics.VerificationRequired >= 2 &&
		candidateMetrics.VerificationRate-baselineMetrics.VerificationRate <= -0.25 {
		evaluation.Reasons = append(evaluation.Reasons, rateRegressionReason("required verification", baselineMetrics.VerificationRate, candidateMetrics.VerificationRate))
	}
	if candidateMetrics.ToolCalls >= 5 && baselineMetrics.ToolCalls >= 5 &&
		candidateMetrics.ToolFailureRate-baselineMetrics.ToolFailureRate >= 0.20 {
		evaluation.Reasons = append(evaluation.Reasons, rateIncreaseReason("tool failures", baselineMetrics.ToolFailureRate, candidateMetrics.ToolFailureRate))
	}
	if len(evaluation.Reasons) > 0 {
		evaluation.Status = "regressed"
		return evaluation
	}
	evaluation.Status = "healthy"
	evaluation.Reasons = append(evaluation.Reasons, "no measured regression crossed a declared gate")
	return evaluation
}

func matchingSkillOutcomes(outcomes []domain.SkillOutcome, attribution domain.SkillAttribution, limit int) []domain.SkillOutcome {
	matched := make([]domain.SkillOutcome, 0, limit)
	for _, outcome := range outcomes {
		if outcome.SkillID == attribution.SkillID && outcome.SkillRevision == attribution.Revision &&
			strings.EqualFold(strings.TrimSpace(outcome.SkillDigest), strings.TrimSpace(attribution.Digest)) {
			matched = append(matched, outcome)
		}
	}
	sort.SliceStable(matched, func(left, right int) bool { return matched[left].CreatedAt.After(matched[right].CreatedAt) })
	if len(matched) > limit {
		matched = matched[:limit]
	}
	return matched
}

func skillCanaryMetrics(outcomes []domain.SkillOutcome) domain.SkillCanaryMetrics {
	metrics := domain.SkillCanaryMetrics{Runs: len(outcomes)}
	for _, outcome := range outcomes {
		if outcome.RunStatus == domain.RunCompleted {
			metrics.Completed++
		} else {
			metrics.Failed++
		}
		if strings.EqualFold(strings.TrimSpace(outcome.Health), "healthy") {
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
	if metrics.Runs > 0 {
		metrics.CompletionRate = float64(metrics.Completed) / float64(metrics.Runs)
		metrics.HealthyRate = float64(metrics.Healthy) / float64(metrics.Runs)
	}
	if metrics.ToolCalls > 0 {
		metrics.ToolFailureRate = float64(metrics.ToolFailures) / float64(metrics.ToolCalls)
	}
	if metrics.VerificationRequired > 0 {
		metrics.VerificationRate = float64(metrics.VerificationRecorded) / float64(metrics.VerificationRequired)
	}
	return metrics
}

func severeCanaryOutcomes(outcomes []domain.SkillOutcome) int {
	severe := 0
	for _, outcome := range outcomes {
		if outcome.RunStatus != domain.RunCompleted || !strings.EqualFold(strings.TrimSpace(outcome.Health), "healthy") ||
			(outcome.VerificationRequired && !outcome.VerificationRecorded) {
			severe++
		}
	}
	return severe
}

func rateRegressionReason(metric string, baseline, candidate float64) string {
	return fmt.Sprintf("%s fell from %.0f%% to %.0f%% (gate: 25 percentage points)", metric, baseline*100, candidate*100)
}

func rateIncreaseReason(metric string, baseline, candidate float64) string {
	return fmt.Sprintf("%s rose from %.0f%% to %.0f%% (gate: 20 percentage points)", metric, baseline*100, candidate*100)
}

// evaluateAppliedSkillCanary persists one visible evaluation and automatically
// restores the newest applied revision only when the deterministic gate proves
// degradation. This method is safe to call repeatedly after outcome upserts.
func (a *App) evaluateAppliedSkillCanary(ctx context.Context, skillID string) error {
	if strings.TrimSpace(skillID) == "" {
		return nil
	}
	a.learningReviewMu.Lock()
	defer a.learningReviewMu.Unlock()
	improvements, err := a.store.ListAgentImprovementsForSkill(ctx, skillID, 1000)
	if err != nil {
		return err
	}
	var item *domain.AgentImprovement
	for index := range improvements {
		candidate := improvements[index]
		if improvementIsApplied(candidate.Status) && candidate.AfterSkill != nil && (item == nil || candidate.CreatedAt.After(item.CreatedAt)) {
			copy := candidate
			item = &copy
		}
	}
	if item == nil {
		return nil
	}
	outcomes, err := a.store.ListSkillOutcomesForSkill(ctx, skillID, 1000)
	if err != nil {
		return err
	}
	candidateAttribution := domain.SkillDefinitionAttribution(*item.AfterSkill)
	candidateAttribution = observedSkillAttribution(outcomes, candidateAttribution)
	var baselineAttribution *domain.SkillAttribution
	if item.BeforeSkill != nil {
		attribution := domain.SkillDefinitionAttribution(*item.BeforeSkill)
		attribution = observedSkillAttribution(outcomes, attribution)
		baselineAttribution = &attribution
	}
	evaluation := evaluateSkillCanary(candidateAttribution, baselineAttribution, outcomes, time.Now().UTC())
	evaluation.Effect = canaryEffect(evaluation)
	if evaluation.Status == "healthy" && item.PromotionStatus == "candidate" {
		workspaceCount := distinctCanaryWorkspaces(matchingSkillOutcomes(outcomes, candidateAttribution, 1000))
		if workspaceCount < 2 {
			evaluation.Status = "pending"
			evaluation.Effect = "insufficient_sample"
			evaluation.Reasons = append(evaluation.Reasons,
				fmt.Sprintf("need candidate outcomes from 2 independent workspaces; observed %d", workspaceCount))
		} else {
			evaluation.Reasons = append(evaluation.Reasons,
				fmt.Sprintf("exact candidate passed across %d independent workspaces; explicit promotion is available", workspaceCount))
		}
	}
	item.CanaryEvaluation = &evaluation
	if agent, agentErr := a.store.GetProjectAgent(ctx, item.ProjectAgentID); agentErr == nil {
		shadow := a.evaluateLearningShadowBenchmark(ctx, agent, item.BeforeSkill, item.AfterSkill, false)
		item.ShadowEvaluation = &shadow
	}
	proofStatusFromCanary(item)
	if evaluation.Status != "regressed" {
		item.UpdatedAt = evaluation.EvaluatedAt
		return a.store.SaveAgentImprovement(ctx, *item)
	}
	rolledBack, err := a.rollbackAgentImprovementLocked(ctx, *item)
	if err != nil {
		return err
	}
	rolledBack.CanaryEvaluation.AutomaticRollback = true
	rolledBack.CanaryEvaluation.Reasons = append(rolledBack.CanaryEvaluation.Reasons, "latest applied Skill revision was automatically rolled back")
	rolledBack.UpdatedAt = time.Now().UTC()
	return a.store.SaveAgentImprovement(ctx, rolledBack)
}

func distinctCanaryWorkspaces(outcomes []domain.SkillOutcome) int {
	workspaces := map[string]bool{}
	for _, outcome := range outcomes {
		if workspaceID := strings.TrimSpace(outcome.WorkspaceID); workspaceID != "" {
			workspaces[workspaceID] = true
		}
	}
	return len(workspaces)
}

// PromoteAgentImprovement is an explicit user action. Background evaluation
// can mark a candidate healthy or roll it back, but it never distributes a
// Skill across a Blueprint on its own.
func (a *App) PromoteAgentImprovement(id string) (domain.AgentImprovement, error) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return domain.AgentImprovement{}, err
	}
	a.learningReviewMu.Lock()
	defer a.learningReviewMu.Unlock()
	ctx := context.Background()
	item, err := a.store.GetAgentImprovement(ctx, strings.TrimSpace(id))
	if err != nil {
		return domain.AgentImprovement{}, err
	}
	if err = a.guardAgentImprovementWorld(ctx, item, ws.ID); err != nil {
		return domain.AgentImprovement{}, err
	}
	if !improvementIsApplied(item.Status) || item.PromotionStatus != "candidate" || item.AfterSkill == nil {
		return domain.AgentImprovement{}, fmt.Errorf("only an applied Skill candidate can be promoted")
	}
	if item.Kind != "subagent_specialization" && (item.CanaryEvaluation == nil || item.CanaryEvaluation.Status != "healthy") {
		return domain.AgentImprovement{}, fmt.Errorf("Skill candidate has not passed the canary regression gate")
	}
	var lineage []domain.AgentImprovement
	if item.BlueprintID != "" {
		lineage, err = a.store.ListAgentImprovementsForBlueprint(ctx, item.BlueprintID, 1000)
	} else {
		lineage, err = a.store.ListAgentImprovements(ctx, item.WorkspaceID, 1000)
	}
	if err != nil {
		return domain.AgentImprovement{}, err
	}
	if !learningRollbackAvailability(lineage)[item.ID] {
		return domain.AgentImprovement{}, fmt.Errorf("a newer Skill/Memory/Rule improvement must be resolved first")
	}
	if item.Kind == "subagent_specialization" {
		err = a.promoteSubagentBlueprintLocked(ctx, &item)
	} else {
		err = a.promoteSkillCandidateLocked(ctx, &item)
	}
	if err != nil {
		return domain.AgentImprovement{}, err
	}
	item.UpdatedAt = time.Now().UTC()
	if err = a.store.SaveAgentImprovement(ctx, item); err != nil {
		return domain.AgentImprovement{}, err
	}
	if item.Kind == "subagent_specialization" {
		if err = a.store.DeleteTemporaryProjectAgent(ctx, item.ProjectAgentID, domain.AgentLifecycleEvent{
			Kind: "subagent_blueprint_accepted", Detail: map[string]any{"improvementId": item.ID, "blueprintId": item.BlueprintID},
		}); err != nil {
			return domain.AgentImprovement{}, err
		}
	}
	return item, nil
}

func (a *App) promoteSubagentBlueprintLocked(ctx context.Context, item *domain.AgentImprovement) error {
	agent, err := a.store.GetProjectAgent(ctx, item.ProjectAgentID)
	if err != nil {
		return err
	}
	if !agent.Temporary || strings.TrimSpace(agent.ParentAgentID) == "" || agent.Status != domain.ProjectAgentEvaluationPending {
		return fmt.Errorf("Blueprint proposal no longer points to an evaluated temporary subagent")
	}
	candidate := *item.AfterSkill
	blueprintID := domain.NewID("blueprint")
	candidate.Configuration = cloneAnyMap(candidate.Configuration)
	candidate.Configuration["promotionStatus"] = "promoted"
	candidate.Configuration["promotionReason"] = "explicit_subagent_blueprint_acceptance"
	candidate.Configuration["ownerId"] = blueprintID
	candidate.Configuration["ownerKind"] = "blueprint"
	candidate.Configuration["blueprintId"] = blueprintID
	candidate.UpdatedAt = time.Now().UTC()
	if err = a.store.SaveSkill(ctx, candidate); err != nil {
		return err
	}
	skillIDs := append([]string(nil), agent.SkillIDs...)
	if !slices.Contains(skillIDs, candidate.ID) {
		skillIDs = append(skillIDs, candidate.ID)
	}
	blueprint := domain.AgentBlueprint{
		ID: blueprintID, Name: agent.Name, RoleDescription: agent.RoleDescription,
		Personality: agent.Personality, Mission: agent.Mission, SystemPrompt: agent.SystemPrompt,
		Goals: append([]string(nil), agent.Goals...), Rules: append([]string(nil), agent.Rules...),
		Constraints: append([]string(nil), agent.Constraints...), SkillIDs: skillIDs,
		AllowedTools: append([]string(nil), agent.AllowedTools...), ToolPolicies: cloneStringMap(agent.ToolPolicies),
		ConnectionID: agent.ConnectionID, Provider: agent.Provider, ProviderPreset: agent.ProviderPreset,
		BaseURL: agent.BaseURL, PrimaryModel: agent.PrimaryModel, FallbackModels: append([]string(nil), agent.FallbackModels...),
		Temperature: agent.Temperature, MaxOutputTokens: agent.MaxOutputTokens,
		ContextWindowTokens: agent.ContextWindowTokens, ReasoningEffort: agent.ReasoningEffort,
		MaxSteps: agent.MaxSteps, MaxDurationSeconds: agent.MaxDurationSeconds, ApprovalMode: agent.ApprovalMode,
	}
	if _, err = a.SaveBlueprint(blueprint); err != nil {
		return err
	}
	item.AfterSkill = skillPointer(candidate)
	item.SkillID = candidate.ID
	item.BlueprintID = blueprintID
	item.PromotionStatus = "promoted"
	item.AfterBlueprintSkillIDs = append([]string(nil), skillIDs...)
	item.Evidence = append(item.Evidence, "user explicitly accepted the evaluated temporary specialization as a reusable Blueprint")
	return nil
}

// RejectAgentImprovement is the negative user decision for an evaluated
// temporary specialization. No Blueprint is created and the quest-scoped
// specialist is removed while the evaluation record remains auditable.
func (a *App) RejectAgentImprovement(id string) (domain.AgentImprovement, error) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return domain.AgentImprovement{}, err
	}
	a.learningReviewMu.Lock()
	defer a.learningReviewMu.Unlock()
	ctx := context.Background()
	item, err := a.store.GetAgentImprovement(ctx, strings.TrimSpace(id))
	if err != nil {
		return domain.AgentImprovement{}, err
	}
	if err = a.guardAgentImprovementWorld(ctx, item, ws.ID); err != nil {
		return domain.AgentImprovement{}, err
	}
	if item.Kind != "subagent_specialization" || item.PromotionStatus != "candidate" {
		return domain.AgentImprovement{}, fmt.Errorf("only a pending subagent Blueprint proposal can be rejected")
	}
	item.PromotionStatus = "rejected"
	item.Evidence = append(item.Evidence, "user explicitly rejected the reusable Blueprint proposal")
	item.UpdatedAt = time.Now().UTC()
	if err = a.store.SaveAgentImprovement(ctx, item); err != nil {
		return domain.AgentImprovement{}, err
	}
	if err = a.store.DeleteTemporaryProjectAgent(ctx, item.ProjectAgentID, domain.AgentLifecycleEvent{
		Kind: "subagent_blueprint_rejected", Detail: map[string]any{"improvementId": item.ID},
	}); err != nil {
		return domain.AgentImprovement{}, err
	}
	return item, nil
}

func (a *App) promoteSkillCandidateLocked(ctx context.Context, item *domain.AgentImprovement) error {
	blueprint, err := a.store.GetBlueprint(ctx, item.BlueprintID)
	if err != nil {
		return err
	}
	candidate := *item.AfterSkill
	candidate.Configuration = cloneAnyMap(candidate.Configuration)
	candidate.Configuration["promotionStatus"] = "promoted"
	candidate.Configuration["promotionReason"] = "explicit_after_canary_gate"
	candidate.UpdatedAt = time.Now().UTC()
	item.AfterSkill = skillPointer(candidate)
	item.PromotionStatus = "promoted"
	item.BeforeBlueprintSkillIDs = append([]string(nil), blueprint.SkillIDs...)
	superseded := strings.TrimSpace(fmt.Sprint(candidate.Configuration["supersedesSkillId"]))
	if superseded != "" {
		blueprint.SkillIDs = withoutString(blueprint.SkillIDs, superseded)
	}
	if !slices.Contains(blueprint.SkillIDs, candidate.ID) {
		blueprint.SkillIDs = append(blueprint.SkillIDs, candidate.ID)
	}
	item.AfterBlueprintSkillIDs = append([]string(nil), blueprint.SkillIDs...)
	if item.BeforeAgentSkillIDs == nil {
		item.BeforeAgentSkillIDs = map[string][]string{}
	}
	if item.AfterAgentSkillIDs == nil {
		item.AfterAgentSkillIDs = map[string][]string{}
	}
	agents, err := a.store.ListAllProjectAgents(ctx)
	if err != nil {
		return err
	}
	for index := range agents {
		agent := agents[index]
		if agent.BlueprintID != blueprint.ID || !stringSetContainsAll(agent.AllowedTools, candidate.RequiredTools) {
			continue
		}
		if _, captured := item.BeforeAgentSkillIDs[agent.ID]; !captured {
			item.BeforeAgentSkillIDs[agent.ID] = append([]string(nil), agent.SkillIDs...)
		}
		if superseded != "" {
			agent.SkillIDs = withoutString(agent.SkillIDs, superseded)
		}
		if !slices.Contains(agent.SkillIDs, candidate.ID) {
			agent.SkillIDs = append(agent.SkillIDs, candidate.ID)
		}
		agent.UpdatedAt = candidate.UpdatedAt
		if err = a.ensureLearnedProjectSkill(ctx, agent.WorkspaceID, candidate.ID, fmt.Sprint(candidate.Configuration["ownerId"]), fmt.Sprint(candidate.Configuration["ownerKind"]), "promoted", candidate.UpdatedAt); err != nil {
			return err
		}
		if err = a.store.SaveProjectAgent(ctx, agent); err != nil {
			return err
		}
		item.AfterAgentSkillIDs[agent.ID] = append([]string(nil), agent.SkillIDs...)
	}
	if after, ok := item.AfterAgentSkillIDs[item.ProjectAgentID]; ok {
		item.AfterSkillIDs = append([]string(nil), after...)
	}
	blueprint.UpdatedAt = candidate.UpdatedAt
	if err = a.store.SaveSkill(ctx, candidate); err != nil {
		return err
	}
	if err = a.store.SaveBlueprint(ctx, blueprint); err != nil {
		return err
	}
	if item.Kind == "subagent_specialization" {
		item.Evidence = append(item.Evidence, "Master usefulness review passed; user explicitly kept the temporary subagent specialization")
	} else {
		item.Evidence = append(item.Evidence, "canary regression gate passed; user explicitly promoted the exact candidate revision")
	}
	return nil
}

// observedSkillAttribution keeps revision selection anchored in the exact
// before/after snapshots while taking the digest from the persisted runtime
// outcome. Project configuration is part of that digest and may legitimately
// differ from the bare managed definition.
func observedSkillAttribution(outcomes []domain.SkillOutcome, expected domain.SkillAttribution) domain.SkillAttribution {
	var latest *domain.SkillOutcome
	for index := range outcomes {
		outcome := &outcomes[index]
		if outcome.SkillID != expected.SkillID || outcome.SkillRevision != expected.Revision || strings.TrimSpace(outcome.SkillDigest) == "" {
			continue
		}
		if latest == nil || outcome.CreatedAt.After(latest.CreatedAt) {
			latest = outcome
		}
	}
	if latest == nil {
		return expected
	}
	return domain.SkillAttribution{
		SkillID: latest.SkillID, Name: latest.SkillName, Revision: latest.SkillRevision,
		Digest: latest.SkillDigest, PromotionStatus: latest.PromotionStatus,
	}
}
