package app

import (
	"context"

	"local-agent-workbench/internal/diagnostics"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/orchestrator"
)

// projectAgentSelectionSignals derives ranking evidence from durable outcomes.
// RunCompleted by itself is only an attempt: confirmation requires an applied,
// non-reverted Change Set or healthy completion/verification evidence.
func (a *App) projectAgentSelectionSignals(ctx context.Context, workspaceID string) map[string]orchestrator.CandidateSignal {
	result := map[string]orchestrator.CandidateSignal{}
	if agents, listErr := a.store.ListProjectAgents(ctx, workspaceID); listErr == nil {
		for _, agent := range agents {
			result[agent.ID] = orchestrator.CandidateSignal{}
		}
	}
	executions, err := a.store.ListExecutions(ctx, workspaceID, 500)
	if err != nil {
		return result
	}
	byRun := map[string]domain.ExecutionInstance{}
	confirmed := map[string]bool{}
	latencyTotal, latencyCount := map[string]int64{}, map[string]int64{}
	for _, execution := range executions {
		signal := result[execution.ProjectAgentID]
		switch execution.Status {
		case domain.RunPending, domain.RunRunning, domain.RunWaiting, domain.RunPaused:
			signal.ActiveExecutions++
		case domain.RunCompleted, domain.RunFailed, domain.RunCancelled, domain.RunInterrupted:
			signal.Attempts++
			if execution.DurationMs > 0 {
				latencyTotal[execution.ProjectAgentID] += execution.DurationMs
				latencyCount[execution.ProjectAgentID]++
			}
		}
		result[execution.ProjectAgentID] = signal
		if execution.RunID != "" {
			byRun[execution.RunID] = execution
		}
	}
	if sets, listErr := a.store.ListChangeSets(ctx, workspaceID); listErr == nil {
		for _, set := range sets {
			if set.Status != domain.ChangeSetApplied || confirmed[set.ExecutionID] {
				continue
			}
			for _, execution := range executions {
				if execution.ID == set.ExecutionID && execution.Status == domain.RunCompleted {
					signal := result[execution.ProjectAgentID]
					signal.ConfirmedSuccesses++
					result[execution.ProjectAgentID] = signal
					confirmed[execution.ID] = true
					break
				}
			}
		}
	}
	if runs, listErr := a.store.ListRunsForWorkspace(ctx, workspaceID, 50); listErr == nil {
		for _, run := range runs {
			execution, ok := byRun[run.ID]
			if !ok || confirmed[execution.ID] || run.Status != domain.RunCompleted {
				continue
			}
			report, reportErr := a.runDiagnostics(ctx, run)
			if reportErr != nil || report.Health != diagnostics.HealthHealthy || report.Completion.Rejected ||
				(report.Verification.Required && !report.Verification.Recorded) {
				continue
			}
			signal := result[execution.ProjectAgentID]
			signal.ConfirmedSuccesses++
			result[execution.ProjectAgentID] = signal
			confirmed[execution.ID] = true
		}
	}
	costTotal, costCount := map[string]int64{}, map[string]int64{}
	if usage, listErr := a.store.ListUsageRecords(ctx, workspaceID, 1000); listErr == nil {
		for _, record := range usage {
			if record.ProjectAgentID == "" || record.CostCents == nil {
				continue
			}
			costTotal[record.ProjectAgentID] += *record.CostCents
			costCount[record.ProjectAgentID]++
		}
	}
	for id, signal := range result {
		if latencyCount[id] > 0 {
			signal.AverageLatencyMs = latencyTotal[id] / latencyCount[id]
		}
		if costCount[id] > 0 {
			signal.AverageCostCents = costTotal[id] / costCount[id]
		}
		result[id] = signal
	}
	return result
}
