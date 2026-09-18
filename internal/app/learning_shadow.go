package app

import (
	"context"
	"fmt"
	"time"

	"local-agent-workbench/internal/domain"
)

// evaluateLearningShadowBenchmark compares stored personal-benchmark evaluations
// for the agent's set via CompareAgentBenchmarks semantics. It never starts a
// live quest. At apply of a new skill revision, comparison is always deferred
// (candidate is not yet in case-runs).
func (a *App) evaluateLearningShadowBenchmark(ctx context.Context, agent domain.ProjectAgent, previous, candidate *domain.SkillDefinition, atApply bool) domain.LearningShadowEvaluation {
	now := time.Now().UTC()
	eval := domain.LearningShadowEvaluation{Status: "skipped_no_set", Reasons: []string{}, EvaluatedAt: now}
	sets, err := a.store.ListAgentBenchmarkSets(ctx, agent.WorkspaceID)
	if err != nil || len(sets) == 0 {
		eval.Reasons = append(eval.Reasons, "no personal benchmark set; effect stays unproven until canary evidence accumulates")
		return eval
	}
	var set *domain.AgentBenchmarkSet
	for index := range sets {
		if sets[index].ProjectAgentID == agent.ID {
			copy := sets[index]
			set = &copy
			break
		}
	}
	if set == nil {
		eval.Reasons = append(eval.Reasons, "benchmark sets exist in the workspace but none belong to this agent")
		return eval
	}
	eval.BenchmarkSetID = set.ID
	eval.BenchmarkSetName = set.Name
	eval.BaselineCases = len(set.Cases)
	if atApply {
		eval.Status = "deferred"
		eval.Reasons = append(eval.Reasons,
			"historical benchmark compare deferred at apply; candidate is not yet present in case-runs")
		return eval
	}
	if previous == nil || candidate == nil {
		eval.Status = "deferred"
		eval.Reasons = append(eval.Reasons, "shadow comparison deferred until both baseline and candidate skill revisions exist")
		return eval
	}
	evaluations, err := a.store.ListAgentBenchmarkEvaluations(ctx, agent.WorkspaceID, 100)
	if err != nil {
		eval.Status = "deferred"
		eval.Reasons = append(eval.Reasons, "could not load personal benchmark evaluations for shadow comparison")
		return eval
	}
	matching := make([]domain.AgentBenchmarkEvaluation, 0, 4)
	for _, item := range evaluations {
		if item.BenchmarkSetID != set.ID || item.SetRevision != set.Revision || item.SetDigest != set.Digest {
			continue
		}
		if item.ProjectAgentID != "" && item.ProjectAgentID != agent.ID {
			continue
		}
		matching = append(matching, item)
	}
	if len(matching) < 2 {
		eval.Status = "deferred"
		eval.CandidateCases = len(matching)
		eval.Reasons = append(eval.Reasons,
			fmt.Sprintf("need ≥2 evaluations of set revision %d; observed %d", set.Revision, len(matching)))
		return eval
	}
	// List is newest-first; take the two most recent matching evaluations.
	afterEval := matching[0]
	beforeEval := matching[1]
	eval.CandidateCases = 2
	comparison, err := compareAgentBenchmarkEvaluations(beforeEval, afterEval)
	if err != nil {
		eval.Status = "deferred"
		eval.Reasons = append(eval.Reasons, "historical benchmark compare deferred: "+err.Error())
		return eval
	}
	eval.Status = "compared"
	eval.Passed = comparison.GatePassed
	eval.Reasons = append(eval.Reasons, comparison.Reasons...)
	if eval.Passed {
		eval.Reasons = append(eval.Reasons, "historical benchmark compare passed regression gate")
	} else {
		eval.Reasons = append(eval.Reasons, "historical benchmark compare failed regression gate")
	}
	return eval
}
