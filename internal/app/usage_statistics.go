package app

import (
	"context"
	"time"

	"local-agent-workbench/internal/connections"
	"local-agent-workbench/internal/diagnostics"
	"local-agent-workbench/internal/domain"
)

func (a *App) SaveConnection(req connections.UpsertRequest) (domain.Connection, error) {
	manager := connections.Manager{Store: a.store}
	return manager.Upsert(context.Background(), req)
}

func (a *App) RecordUsage(record domain.UsageRecord) (domain.UsageRecord, error) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return domain.UsageRecord{}, err
	}
	if record.WorkspaceID != "" && record.WorkspaceID != ws.ID {
		return domain.UsageRecord{}, errForeignWorld
	}
	record.WorkspaceID = ws.ID
	// HTTP clients must not spoof spend that hard-stop budget sums.
	record.CostCents = nil
	if record.ID == "" {
		record.ID = domain.NewID("usage")
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now().UTC()
	}
	if err := a.store.InsertUsageRecord(context.Background(), record); err != nil {
		return domain.UsageRecord{}, err
	}
	return record, nil
}

// Statistics exposes raw evidence and bounded coverage windows. It never
// collapses agent or Skill quality into an opaque composite score.
func (a *App) Statistics(workspaceID string) (map[string]any, error) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return nil, err
	}
	if workspaceID != "" && workspaceID != ws.ID {
		return nil, errForeignWorld
	}
	workspaceID = ws.ID
	ctx := context.Background()
	usage, err := a.store.ListUsageRecords(ctx, workspaceID, 500)
	if err != nil {
		return nil, err
	}
	var tokens int64
	var knownCost int64
	costKnown := false
	byModel := map[string]int64{}
	byProvider := map[string]int64{}
	for _, record := range usage {
		tokens += record.TotalTokens
		byModel[record.Model] += record.TotalTokens
		byProvider[record.Provider] += record.TotalTokens
		if record.CostCents != nil {
			knownCost += *record.CostCents
			costKnown = true
		}
	}
	now := time.Now().UTC()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	dailyCost := a.usageCostCents(usage, dayStart)
	monthlyCost := a.usageCostCents(usage, monthStart)
	reservations, err := a.store.ListBudgetReservations(ctx, workspaceID, 5000)
	if err != nil {
		return nil, err
	}
	var dailyReserved, monthlyReserved, dailyConservative, monthlyConservative int64
	for _, reservation := range reservations {
		if reservation.CreatedAt.Before(monthStart) {
			continue
		}
		switch reservation.Status {
		case domain.BudgetReserved:
			monthlyReserved += reservation.ReservedCents
			if !reservation.CreatedAt.Before(dayStart) {
				dailyReserved += reservation.ReservedCents
			}
		case domain.BudgetConservative:
			monthlyConservative += reservation.ReservedCents
			if !reservation.CreatedAt.Before(dayStart) {
				dailyConservative += reservation.ReservedCents
			}
		}
	}
	dailySpent := dailyCost + dailyConservative
	monthlySpent := monthlyCost + monthlyConservative
	budget, err := a.loadHubBudgetSettings(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	agents, _ := a.store.ListProjectAgents(ctx, workspaceID)
	teams, _ := a.store.ListTeams(ctx, workspaceID)
	quests, _ := a.store.ListQuests(ctx, workspaceID)
	flows, _ := a.store.ListFlows(ctx, workspaceID)
	executions, _ := a.store.ListExecutions(ctx, workspaceID, 2000)
	skillOutcomes, err := a.store.ListSkillOutcomes(ctx, workspaceID, 1000)
	if err != nil {
		return nil, err
	}
	improvements, err := a.store.ListAgentImprovements(ctx, workspaceID, 100)
	if err != nil {
		return nil, err
	}
	benchmarkSets, err := a.store.ListAgentBenchmarkSets(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	benchmarkEvaluations, err := a.store.ListAgentBenchmarkEvaluations(ctx, workspaceID, 100)
	if err != nil {
		return nil, err
	}
	compatibilityUsage, err := a.store.ListCompatibilityUsage(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	compatibilityUsage = publicCompatibilityUsage(compatibilityUsage)
	var compatibilityUsageTotal int64
	compatibilityVersions := map[string]bool{}
	for _, item := range compatibilityUsage {
		compatibilityUsageTotal += item.Count
		compatibilityVersions[item.ApplicationVersion] = true
	}
	// A Skill or portable Memory can be revised from another project of the
	// same blueprint. Compute rollback ownership over both global histories.
	rollbackAvailable := map[string]bool{}
	for _, improvement := range improvements {
		var global []domain.AgentImprovement
		if improvement.SkillID != "" {
			items, listErr := a.store.ListAgentImprovementsForSkill(ctx, improvement.SkillID, 1000)
			if listErr != nil {
				return nil, listErr
			}
			global = mergeAgentImprovements(global, items)
			if improvement.BlueprintID != "" {
				items, listErr = a.store.ListAgentImprovementsForBlueprint(ctx, improvement.BlueprintID, 1000)
				if listErr != nil {
					return nil, listErr
				}
				global = mergeAgentImprovements(global, items)
			} else {
				global = mergeAgentImprovements(global, improvements)
			}
		}
		if improvement.MemoryID != "" {
			items, listErr := a.store.ListAgentImprovementsForMemory(ctx, improvement.MemoryID, 1000)
			if listErr != nil {
				return nil, listErr
			}
			global = mergeAgentImprovements(global, items)
		}
		if improvement.InstructionSignature != "" {
			items, listErr := a.store.ListAgentImprovementsForInstruction(ctx, improvement.InstructionSignature, 1000)
			if listErr != nil {
				return nil, listErr
			}
			global = mergeAgentImprovements(global, items)
		}
		for id, available := range learningRollbackAvailability(global) {
			rollbackAvailable[id] = available
		}
	}
	improvementsApplied, improvementsRolledBack, memoryCandidates, memoriesPromoted, memoriesConfirmed := 0, 0, 0, 0, 0
	instructionCandidates, instructionsPromoted, instructionsConfirmed := 0, 0, 0
	for index := range improvements {
		improvements[index].RollbackAvailable = rollbackAvailable[improvements[index].ID]
		switch improvements[index].Status {
		case "applied", "applied_unproven", "applied_proven":
			if improvements[index].RollbackAvailable {
				improvementsApplied++
			}
			switch improvements[index].MemoryStatus {
			case "candidate":
				memoryCandidates++
			case "promoted":
				memoriesPromoted++
			case "confirmed":
				memoriesConfirmed++
			}
			switch improvements[index].InstructionStatus {
			case "candidate":
				instructionCandidates++
			case "promoted":
				instructionsPromoted++
			case "confirmed":
				instructionsConfirmed++
			}
		case "rolled_back":
			improvementsRolledBack++
		}
	}
	// Quality comes from immutable run evidence. The explicit window and
	// coverage prevent a partial sample being presented as an all-time measure.
	runs, err := a.store.ListRunsForWorkspace(ctx, workspaceID, diagnosticsMemoLimit)
	if err != nil {
		return nil, err
	}
	qualityByRun := make(map[string]diagnostics.RunDiagnostics, len(runs))
	qualityDiagnosticsUnavailable := 0
	for _, run := range runs {
		switch run.Status {
		case domain.RunCompleted, domain.RunFailed, domain.RunCancelled, domain.RunInterrupted:
		default:
			continue
		}
		report, reportErr := a.runDiagnostics(ctx, run)
		if reportErr != nil {
			qualityDiagnosticsUnavailable++
			continue
		}
		qualityByRun[run.ID] = report
	}
	stats := map[string]any{
		"workspaceId": workspaceID, "totalTokens": tokens, "byModel": byModel, "byProvider": byProvider,
		"agents": len(agents), "quests": len(quests), "usageCount": len(usage),
		"qualityRunsAnalyzed": len(qualityByRun), "qualityHistoryWindow": len(runs),
		"qualityHistoryLimit": diagnosticsMemoLimit, "qualityHistoryWindowLimited": len(runs) == diagnosticsMemoLimit,
		"skillOutcomeCount": len(skillOutcomes), "skillOutcomeHistoryLimit": 1000,
		"skillOutcomeHistoryLimited": len(skillOutcomes) == 1000, "skillVersionStats": buildSkillVersionStatistics(skillOutcomes),
		"benchmarkSets": benchmarkSets, "benchmarkEvaluations": benchmarkEvaluations,
		"benchmarkEvaluationLimit": 100, "benchmarkHistoryLimited": len(benchmarkEvaluations) == 100,
		"compatibilityUsage": compatibilityUsage, "compatibilityUsageTotal": compatibilityUsageTotal,
		"compatibilityReleaseVersions": len(compatibilityVersions), "agentImprovements": improvements,
		"agentImprovementsApplied": improvementsApplied, "agentImprovementsRolledBack": improvementsRolledBack,
		"agentMemoryCandidates": memoryCandidates, "agentMemoriesPromoted": memoriesPromoted,
		"agentMemoriesConfirmed": memoriesConfirmed, "agentInstructionCandidates": instructionCandidates,
		"agentInstructionsPromoted": instructionsPromoted, "agentInstructionsConfirmed": instructionsConfirmed,
	}
	if qualityDiagnosticsUnavailable > 0 {
		stats["qualityDiagnosticsUnavailable"] = qualityDiagnosticsUnavailable
	}
	for key, value := range buildStatisticsBreakdowns(agents, teams, quests, flows, executions, usage, qualityByRun) {
		stats[key] = value
	}
	for key, value := range buildBudgetPhaseTotals(usage) {
		stats[key] = value
	}
	if costKnown {
		stats["knownCostCents"] = knownCost
	}
	if budget.DailyCents > 0 {
		stats["budgetDailyCents"] = budget.DailyCents
		stats["dailyCostCents"] = dailyCost
		stats["dailySpentCents"] = dailySpent
		stats["dailyReservedCents"] = dailyReserved
		dailyAvailable := budget.DailyCents - dailySpent - dailyReserved
		if dailyAvailable < 0 {
			dailyAvailable = 0
		}
		stats["dailyAvailableCents"] = dailyAvailable
		if warning := budgetWarning(dailySpent+dailyReserved, budget.DailyCents); warning != "" {
			stats["dailyBudgetWarning"] = warning
		}
		if budget.HardStop && dailyAvailable == 0 {
			stats["budgetBlockReason"] = "daily hard limit has no available reserved capacity"
		}
	}
	if budget.MonthlyCents > 0 {
		stats["budgetMonthlyCents"] = budget.MonthlyCents
		stats["monthlyCostCents"] = monthlyCost
		stats["monthlySpentCents"] = monthlySpent
		stats["monthlyReservedCents"] = monthlyReserved
		monthlyAvailable := budget.MonthlyCents - monthlySpent - monthlyReserved
		if monthlyAvailable < 0 {
			monthlyAvailable = 0
		}
		stats["monthlyAvailableCents"] = monthlyAvailable
		if warning := budgetWarning(monthlySpent+monthlyReserved, budget.MonthlyCents); warning != "" {
			stats["monthlyBudgetWarning"] = warning
		}
		if budget.HardStop && monthlyAvailable == 0 {
			stats["budgetBlockReason"] = "monthly hard limit has no available reserved capacity"
		}
	}
	if budget.HardStop {
		stats["budgetHardStop"] = true
	}
	return stats, nil
}
