package app

import (
	"context"
	"encoding/json"
	"fmt"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/workspace"
	"strings"
)

func projectWorkspaces(items []domain.Workspace) []domain.Workspace {
	projects := make([]domain.Workspace, 0, len(items))
	for _, w := range items {
		if !strings.HasPrefix(w.ID, "point-chat-") {
			projects = append(projects, w)
		}
	}
	return projects
}

func (a *App) runWorkspaceFS(ctx context.Context, run domain.Run) (*workspace.FS, error) {
	if run.ConfigurationSnapshot.Profile.ExecutionMode == "host_live" {
		scoped, err := a.WithMasterWorkspace(ctx, run.WorkspaceID)
		if err != nil {
			return nil, err
		}
		return a.masterScopedFS(scoped), nil
	}
	return a.fs()
}

func (a *App) hostCheckSummary(ctx context.Context, runID string) string {
	events, err := a.store.ListByRun(ctx, runID)
	if err != nil {
		return "\nПроверки: журнал недоступен: " + err.Error()
	}
	var checks []string
	for _, event := range events {
		if event.Type == domain.EventToolFinished {
			var payload struct {
				Tool   string            `json:"tool"`
				Result domain.ToolResult `json:"result"`
			}
			if json.Unmarshal(event.Data, &payload) != nil || (payload.Tool != "run_command" && payload.Tool != "validate_syntax") {
				continue
			}
			var output struct {
				ExitCode int  `json:"exitCode"`
				TimedOut bool `json:"timedOut"`
			}
			_ = json.Unmarshal(payload.Result.Output, &output)
			check := fmt.Sprintf("%s, шаг %d: exitCode=%d", payload.Tool, event.Step, output.ExitCode)
			if output.TimedOut {
				check += " (timeout)"
			}
			if payload.Result.Error != nil {
				check += ": " + payload.Result.Error.Message
			}
			checks = append(checks, check)
		}
	}
	if len(checks) == 0 {
		return "\nПроверки: не выполнены"
	}
	return "\nПроверки:\n" + strings.Join(checks, "\n")
}

func (a *App) masterFastState(ctx context.Context, orders []domain.WorkOrder) ([]domain.WorkOrder, *domain.Run, error) {
	visible := make([]domain.WorkOrder, 0, len(orders))
	var latest *domain.Run
	for _, order := range orders {
		if order.Workspace.Isolation != "host_live" {
			visible = append(visible, order)
			continue
		}
		if order.Runtime == nil {
			continue
		}
		execution, err := a.store.ListExecutions(ctx, order.WorkspaceID, 10000)
		if err != nil {
			return nil, nil, err
		}
		for _, item := range execution {
			if item.QuestID == order.Runtime.QuestID && item.RunID != "" {
				run, err := a.store.GetRun(ctx, item.RunID)
				if err != nil {
					return nil, nil, err
				}
				if latest == nil || run.StartedAt.After(latest.StartedAt) {
					r := publicRun(run)
					latest = &r
				}
			}
		}
	}
	return visible, latest, nil
}
