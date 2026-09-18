// Завершение прогона, события и запись в хранилище.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/observability"
	"local-agent-workbench/internal/security"
)

type runCorrelation struct {
	WorkspaceID         string
	ExecutionID         string
	QuestID             string
	FlowRunID           string
	FlowNodeID          string
	CompletionCheckKind string
}

func (e *Engine) complete(active *activeRun, result string) {
	now := time.Now().UTC()
	e.update(active, func(r *domain.Run) { r.Status = domain.RunCompleted; r.Result = result; r.FinishedAt = &now })
	run := e.snapshot(active)
	if err := e.saveRun(run); err != nil {
		slog.Error("persist completed run failed", "run_id", run.ID, "error", err)
	}
	e.publishOrLog(context.Background(), run, domain.EventRunCompleted, "agent", map[string]any{"result": result, "changedFiles": run.ChangedFiles, "toolsUsed": run.ToolsUsed, "requestCount": run.RequestCount, "durationMs": run.DurationMs})
	durationMs := run.DurationMs
	if durationMs == 0 && !run.StartedAt.IsZero() {
		durationMs = time.Since(run.StartedAt).Milliseconds()
	}
	slog.Info("agent run completed",
		"run_id", run.ID,
		"duration_ms", durationMs,
		"requests", run.RequestCount,
		"tools", run.ToolsUsed,
		"changed_files", len(run.ChangedFiles),
		"result_preview", observability.Snippet(security.Redact(result), 240),
	)
}

func (e *Engine) fail(active *activeRun, err error) {
	now := time.Now().UTC()
	e.update(active, func(r *domain.Run) {
		r.Status = domain.RunFailed
		r.Error = security.Redact(err.Error())
		r.FinishedAt = &now
	})
	run := e.snapshot(active)
	if saveErr := e.saveRun(run); saveErr != nil {
		slog.Error("persist failed run failed", "run_id", run.ID, "error", saveErr)
	}
	e.publishOrLog(context.Background(), run, domain.EventRunFailed, "agent", map[string]any{"error": run.Error})
	slog.Error("agent run failed",
		"run_id", run.ID,
		"error", run.Error,
		"requests", run.RequestCount,
		"tools", run.ToolsUsed,
	)
}

func (e *Engine) finishContext(active *activeRun, err error) {
	now := time.Now().UTC()
	status := domain.RunCancelled
	message := "Run cancelled"
	if errors.Is(err, context.DeadlineExceeded) {
		status = domain.RunFailed
		message = "Run timed out"
	}
	e.update(active, func(r *domain.Run) { r.Status = status; r.Error = message; r.FinishedAt = &now })
	run := e.snapshot(active)
	if saveErr := e.saveRun(run); saveErr != nil {
		slog.Error("persist cancelled run failed", "run_id", run.ID, "error", saveErr)
	}
	kind := domain.EventRunCancelled
	if status == domain.RunFailed {
		kind = domain.EventRunFailed
	}
	e.publishOrLog(context.Background(), run, kind, "user", map[string]any{"reason": message})
	slog.Warn("agent run stopped",
		"run_id", run.ID,
		"status", string(status),
		"reason", message,
		"requests", run.RequestCount,
	)
}

func (e *Engine) publish(ctx context.Context, run domain.Run, kind domain.EventType, actor string, data any) error {
	correlation := e.correlationForRun(run.ID)
	data = correlateEventData(data, correlation)
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	raw = json.RawMessage(security.Redact(string(raw)))
	event := domain.Event{
		ID: domain.NewID("evt"), WorkspaceID: run.WorkspaceID, RunID: run.ID, AgentID: run.AgentID,
		ExecutionID: correlation.ExecutionID, QuestID: correlation.QuestID,
		FlowRunID: correlation.FlowRunID, FlowNodeID: correlation.FlowNodeID,
		Type: kind, Step: run.Step, Actor: actor, Data: raw, CreatedAt: time.Now().UTC(),
	}
	if err = e.repo.Append(ctx, event); err != nil {
		return err
	}
	if e.onEvent != nil {
		e.onEvent(event)
	}
	return nil
}

func (e *Engine) correlationForRun(runID string) runCorrelation {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if active := e.active[runID]; active != nil {
		return active.correlation
	}
	return runCorrelation{}
}

func correlateEventData(data any, correlation runCorrelation) any {
	if correlation.ExecutionID == "" && correlation.QuestID == "" && correlation.FlowRunID == "" && correlation.FlowNodeID == "" {
		return data
	}
	apply := func(payload map[string]any) map[string]any {
		if correlation.ExecutionID != "" {
			payload["executionId"] = correlation.ExecutionID
		}
		if correlation.QuestID != "" {
			payload["questId"] = correlation.QuestID
		}
		if correlation.FlowRunID != "" {
			payload["flowRunId"] = correlation.FlowRunID
		}
		if correlation.FlowNodeID != "" {
			payload["flowNodeId"] = correlation.FlowNodeID
		}
		return payload
	}
	switch typed := data.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed)+4)
		for key, value := range typed {
			out[key] = value
		}
		return apply(out)
	default:
		raw, err := json.Marshal(data)
		if err != nil {
			return data
		}
		var payload map[string]any
		if err = json.Unmarshal(raw, &payload); err != nil || payload == nil {
			return data
		}
		return apply(payload)
	}
}

func (e *Engine) publishOrLog(ctx context.Context, run domain.Run, kind domain.EventType, actor string, data any) {
	if err := e.publish(ctx, run, kind, actor, data); err != nil {
		failures := e.publishFailures.Add(1)
		slog.Warn("agent event publish failed", "run_id", run.ID, "event", kind, "failures", failures, "error", err)
	}
}

func (e *Engine) saveRun(run domain.Run) error {
	safe := run
	safe.Task = security.Redact(safe.Task)
	safe.Error = security.Redact(safe.Error)
	safe.Result = security.Redact(safe.Result)
	safe.ContextItems = append([]domain.RunContextItem(nil), safe.ContextItems...)
	for index := range safe.ContextItems {
		safe.ContextItems[index].Content = security.Redact(safe.ContextItems[index].Content)
	}
	return e.repo.SaveRun(context.Background(), safe)
}

func (e *Engine) savePatch(patch domain.PatchProposal) error {
	return e.repo.SavePatch(context.Background(), patch)
}
