package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"local-agent-workbench/internal/domain"
)

func wallClockLimit(maxDurationSeconds, activeSeconds int) time.Duration {
	maxDuration := time.Duration(maxDurationSeconds) * time.Second
	if maxDuration <= 0 {
		maxDuration = 10 * time.Minute
	}
	if activeSeconds <= 0 {
		return maxDuration
	}
	// Active budget excludes approvals/pause; keep a generous wall ceiling so a
	// hung model still dies without treating user wait as failure.
	ceiling := time.Duration(activeSeconds*3)*time.Second + maxDuration
	if ceiling < maxDuration {
		return maxDuration
	}
	return ceiling
}

func (e *Engine) syncControllerState(active *activeRun, pauseReason string, resumable bool) {
	if active == nil {
		return
	}
	elapsed, extensions := active.clock.snapshot()
	budget := active.clock.budgetSeconds()
	totalBudget := active.clock.totalBudgetSeconds()
	remaining := 0
	if totalBudget > 0 {
		left := int64(totalBudget)*1000 - elapsed
		if left > 0 {
			remaining = int((left + 999) / 1000)
		}
	}
	e.update(active, func(r *domain.Run) {
		r.Controller = domain.RunControllerState{
			PauseReason:            pauseReason,
			ActiveSecondsBudget:    budget,
			ActiveElapsedMs:        elapsed,
			ActiveSecondsRemaining: remaining,
			ActiveTimeExtensions:   extensions,
			Resumable:              resumable,
			CheckpointSeq:          active.checkpointSeq,
		}
	})
}

func (e *Engine) persistRoundCheckpoint(active *activeRun, history *conversationHistory, completion *completionTracker, observations *observationTracker, completedToolCalls map[string]struct{}, currentModel string, remainingFallbacks []string, nextStep, completionRevisions, identicalToolPlans, toolPlanRecoveries, toolOutputBytes int, lastToolPlan, pauseReason, inFlightCallID string) error {
	if inFlightCallID == "" {
		active.clock.stop()
	}
	elapsed, extensions := active.clock.snapshot()
	historyJSON, err := history.marshal()
	if err != nil {
		return fmt.Errorf("marshal conversation checkpoint: %w", err)
	}
	completionJSON, err := completion.marshal()
	if err != nil {
		return fmt.Errorf("marshal completion checkpoint: %w", err)
	}
	observationsJSON, err := observations.marshal()
	if err != nil {
		return fmt.Errorf("marshal observation checkpoint: %w", err)
	}
	run := e.snapshot(active)
	var briefJSON, contextJSON []byte
	if active.taskBrief != nil {
		briefJSON, _ = json.Marshal(active.taskBrief)
	}
	contextJSON, _ = json.Marshal(run.ContextItems)
	keys := make([]string, 0, len(completedToolCalls))
	for key := range completedToolCalls {
		keys = append(keys, key)
	}
	active.checkpointSeq++
	checkpoint := domain.RunCheckpoint{
		RunID: run.ID, Seq: active.checkpointSeq, CreatedAt: time.Now().UTC(),
		SandboxPath: active.sandboxPath, ExecutionID: active.correlation.ExecutionID,
		QuestID: active.correlation.QuestID, FlowRunID: active.correlation.FlowRunID, FlowNodeID: active.correlation.FlowNodeID,
		WorkspaceRevision: e.currentWorkspaceRevision(active), ActiveElapsedMs: elapsed,
		ActiveSecondsBudget: active.clock.budgetSeconds(), ActiveTimeExtensions: extensions,
		PauseReason: pauseReason, InFlightCallID: inFlightCallID, CurrentModel: currentModel,
		RemainingFallbacks: append([]string(nil), remainingFallbacks...), NextStep: nextStep,
		CompletionRevisions: completionRevisions, IdenticalToolPlans: identicalToolPlans,
		LastToolPlan: lastToolPlan, ToolPlanRecoveries: toolPlanRecoveries, ToolOutputBytes: toolOutputBytes,
		CompletedToolCalls: keys, HistoryJSON: historyJSON, CompletionJSON: completionJSON,
		ObservationsJSON: observationsJSON, TaskBriefJSON: briefJSON, ContextItemsJSON: contextJSON,
		ChangedFiles: append([]string(nil), run.ChangedFiles...), ToolsUsed: append([]string(nil), run.ToolsUsed...),
		Step: run.Step, RequestCount: run.RequestCount,
		ActiveToolName: lastToolPlan, HeartbeatAt: time.Now().UTC(),
	}
	if active.taskBrief != nil {
		checkpoint.BriefVersion = active.taskBrief.Version
		checkpoint.BriefDigest = domain.TaskBriefDigest(*active.taskBrief)
	}
	if err = e.repo.SaveRunCheckpoint(context.Background(), checkpoint); err != nil {
		return err
	}
	e.syncControllerState(active, pauseReason, checkpoint.Resumable())
	return e.saveRun(e.snapshot(active))
}

func completedToolCallSet(keys []string) map[string]struct{} {
	result := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		result[key] = struct{}{}
	}
	return result
}
