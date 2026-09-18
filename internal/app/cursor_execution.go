// Интерактивные исполнения Cursor.
//
// У них нет headless RunID: работу ведёт IDE, а ядро только помечает начало и
// конец. Отсюда и отдельный путь завершения.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"local-agent-workbench/internal/changesets"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/flowruntime"
)

// CursorExecutionLaunch is the reviewed sandbox task handed to the interactive
// Cursor SDK owned by the IDE process. The local core remains the source of
// truth for execution and Flow state before and after that external turn.
type CursorExecutionLaunch struct {
	Execution   domain.ExecutionInstance `json:"execution"`
	Profile     domain.AgentProfile      `json:"profile"`
	SandboxPath string                   `json:"sandboxPath"`
}

type CursorExecutionCompletion struct {
	Status string `json:"status"`
	Result string `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

func (a *App) findExecution(executionID string) (domain.ExecutionInstance, error) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return domain.ExecutionInstance{}, err
	}
	executions, err := a.store.ListExecutions(context.Background(), ws.ID, 500)
	if err != nil {
		return domain.ExecutionInstance{}, err
	}
	for _, exec := range executions {
		if exec.ID == executionID {
			return exec, nil
		}
	}
	return domain.ExecutionInstance{}, fmt.Errorf("execution %s not found", executionID)
}

func (a *App) BeginCursorExecution(ctx context.Context, executionID string) (CursorExecutionLaunch, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	exec, err := a.findExecution(executionID)
	if err != nil {
		return CursorExecutionLaunch{}, err
	}
	brief, err := a.taskBriefForQuest(ctx, exec.WorkspaceID, exec.QuestID)
	if err != nil {
		return CursorExecutionLaunch{}, err
	}
	if brief != nil {
		return CursorExecutionLaunch{}, errors.New("Cursor runtime не поддерживает контроль прав и критериев утверждённого задания; используйте встроенного исполнителя")
	}
	if exec.Status != domain.RunPending && exec.Status != domain.RunInterrupted {
		return CursorExecutionLaunch{}, fmt.Errorf("Cursor-исполнение %s не ожидает запуска", executionID)
	}
	agentItem, err := a.store.GetProjectAgent(ctx, exec.ProjectAgentID)
	if err != nil {
		return CursorExecutionLaunch{}, err
	}
	if agentItem.WorkspaceID != exec.WorkspaceID {
		return CursorExecutionLaunch{}, errors.New("Cursor-агент принадлежит другому проекту")
	}
	if agentItem.Provider != domain.ProviderCursor {
		return CursorExecutionLaunch{}, errors.New("интерактивный запуск доступен только для Cursor Agent")
	}
	profile := domain.ProfileFromProjectAgent(agentItem)
	if err = a.enrichProjectAgentForRun(exec.WorkspaceID, agentItem, &profile, nil); err != nil {
		return CursorExecutionLaunch{}, err
	}
	sandboxRecord, err := a.store.GetSandboxByExecution(ctx, exec.ID)
	if err != nil {
		return CursorExecutionLaunch{}, fmt.Errorf("не удалось открыть песочницу Cursor-исполнения: %w", err)
	}
	if info, statErr := os.Stat(sandboxRecord.Path); statErr != nil || !info.IsDir() {
		if statErr == nil {
			statErr = errors.New("путь не является папкой")
		}
		return CursorExecutionLaunch{}, fmt.Errorf("песочница Cursor-исполнения недоступна: %w", statErr)
	}
	now := time.Now().UTC()
	exec.Status = domain.RunRunning
	exec.Error = ""
	exec.Result = ""
	exec.RunID = ""
	exec.StartedAt = now
	exec.FinishedAt = nil
	exec.DurationMs = 0
	if err = a.store.SaveExecution(ctx, exec); err != nil {
		return CursorExecutionLaunch{}, err
	}
	slog.Info("interactive Cursor execution claimed", "execution_id", exec.ID, "quest_id", exec.QuestID, "project_agent_id", exec.ProjectAgentID)
	return CursorExecutionLaunch{Execution: exec, Profile: profile, SandboxPath: sandboxRecord.Path}, nil
}

func (a *App) CompleteCursorExecution(ctx context.Context, executionID string, completion CursorExecutionCompletion) (domain.ExecutionInstance, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if len([]rune(completion.Result)) > 64*1024 || len([]rune(completion.Error)) > 8*1024 {
		return domain.ExecutionInstance{}, errors.New("результат Cursor-исполнения слишком велик")
	}
	exec, err := a.findExecution(executionID)
	if err != nil {
		return domain.ExecutionInstance{}, err
	}
	ws, err := a.requireWorkspace()
	if err != nil {
		return domain.ExecutionInstance{}, err
	}
	if ws.ID != exec.WorkspaceID {
		return domain.ExecutionInstance{}, errors.New("Cursor-исполнение принадлежит другому проекту")
	}
	agentItem, err := a.store.GetProjectAgent(ctx, exec.ProjectAgentID)
	if err != nil {
		return domain.ExecutionInstance{}, err
	}
	if agentItem.Provider != domain.ProviderCursor {
		return domain.ExecutionInstance{}, errors.New("завершить через Cursor можно только Cursor-исполнение")
	}
	if exec.Status == domain.RunCompleted || exec.Status == domain.RunFailed || exec.Status == domain.RunCancelled {
		return exec, nil
	}
	if exec.Status != domain.RunRunning {
		return domain.ExecutionInstance{}, fmt.Errorf("Cursor-исполнение %s не запущено", executionID)
	}
	status := strings.ToLower(strings.TrimSpace(completion.Status))
	switch status {
	case "completed", "success", "succeeded":
		exec.Status = domain.RunCompleted
	case "cancelled", "canceled":
		exec.Status = domain.RunCancelled
	case "failed", "error":
		exec.Status = domain.RunFailed
	default:
		return domain.ExecutionInstance{}, fmt.Errorf("неизвестный статус Cursor-исполнения %q", completion.Status)
	}
	exec.Result = strings.TrimSpace(completion.Result)
	exec.Error = strings.TrimSpace(completion.Error)
	if exec.Status == domain.RunFailed && exec.Error == "" {
		exec.Error = "Cursor Agent завершился с ошибкой"
	}
	if exec.Status == domain.RunCancelled && exec.Error == "" {
		exec.Error = "Cursor Agent остановлен пользователем"
	}
	now := time.Now().UTC()
	exec.FinishedAt = &now
	exec.DurationMs = now.Sub(exec.StartedAt).Milliseconds()
	if err = a.store.SaveExecution(ctx, exec); err != nil {
		return domain.ExecutionInstance{}, err
	}

	success := exec.Status == domain.RunCompleted
	a.awardProjectAgentOutcome(exec.ProjectAgentID, success)
	if sandboxRecord, loadErr := a.store.GetSandboxByExecution(ctx, exec.ID); loadErr == nil {
		baselinePath, dependencies, lineageErr := a.changeSetLineage(exec.WorkspaceID, sandboxRecord)
		if lineageErr != nil {
			slog.Warn("Cursor execution change set lineage unavailable", "execution_id", exec.ID, "error", lineageErr)
		} else {
			applier := changesets.Applier{Store: a.store}
			if _, buildErr := applier.BuildFromSandbox(ctx, changesets.BuildRequest{
				WorkspaceID: exec.WorkspaceID, ExecutionID: exec.ID, QuestID: exec.QuestID,
				Title: "Changes from Cursor · " + exec.ID, WorkspacePath: ws.Path,
				BaselinePath: baselinePath, SandboxPath: sandboxRecord.Path, DependsOn: dependencies,
			}); buildErr != nil {
				slog.Warn("Cursor execution change set unavailable", "execution_id", exec.ID, "error", buildErr)
			}
		}
	}

	if exec.FlowRunID == "" || exec.FlowNodeID == "" {
		if exec.QuestID != "" {
			a.finalizeQuestAfterFlow(exec.QuestID, success)
		}
	} else {
		if !success {
			if recoveredRun, recovered, recoveryErr := a.recoverFlowNodeFailure(exec.FlowRunID, exec.FlowNodeID, exec); recoveryErr != nil {
				slog.Warn("Cursor flow node recovery unavailable", "execution_id", exec.ID, "error", recoveryErr)
			} else if recovered {
				if scheduleErr := a.scheduleFlowAgentExecutionsFromRun(recoveredRun); scheduleErr != nil {
					return exec, scheduleErr
				}
				slog.Info("interactive Cursor execution scheduled for recovery", "execution_id", exec.ID, "flow_run_id", exec.FlowRunID, "flow_node_id", exec.FlowNodeID)
				return exec, nil
			}
		}
		childStatus := domain.QuestFailed
		if success {
			childStatus = domain.QuestCompleted
		} else if exec.Status == domain.RunCancelled {
			childStatus = domain.QuestCancelled
		}
		a.setFlowChildQuestStatus(exec.FlowRunID, exec.FlowNodeID, childStatus)
		flowRuntime := flowruntime.Runtime{Store: a.store}
		completionOutput := a.flowAttemptOutput(exec.FlowRunID, exec.FlowNodeID, exec.ID, map[string]any{
			"executionId": exec.ID, "result": exec.Result, "status": exec.Status,
			"error": exec.Error, "externalRuntime": "cursor",
		})
		flowRun, completeErr := flowRuntime.CompleteAgentNode(ctx, exec.FlowRunID, exec.FlowNodeID, success, completionOutput)
		if completeErr != nil {
			return exec, completeErr
		}
		if flowRun.Status == domain.RunFailed || flowRun.Status == domain.RunCancelled {
			a.cancelSiblingFlowExecutions(flowRun.ID, exec.ID)
			a.closeUnfinishedFlowChildQuests(flowRun.ID, false)
			if flowRun.QuestID != "" {
				a.finalizeQuestAfterFlow(flowRun.QuestID, false)
			}
		} else if flowRun.Status == domain.RunCompleted {
			a.closeUnfinishedFlowChildQuests(flowRun.ID, true)
			if flowRun.QuestID != "" {
				a.finalizeQuestAfterFlow(flowRun.QuestID, success)
			}
		} else if scheduleErr := a.scheduleFlowAgentExecutionsFromRun(flowRun); scheduleErr != nil {
			return exec, scheduleErr
		}
	}
	slog.Info("interactive Cursor execution completed", "execution_id", exec.ID, "status", exec.Status, "duration_ms", exec.DurationMs)
	return exec, nil
}
