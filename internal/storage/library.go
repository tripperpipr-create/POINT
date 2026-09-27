package storage

// Машинные определения — общие для всех проектов: профили агентов, свои
// инструменты и workflow, а также прогоны workflow по проекту.

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	_ "modernc.org/sqlite"

	"local-agent-workbench/internal/domain"
)

func (s *SQLite) SaveProfile(ctx context.Context, p domain.AgentProfile) error {
	tools, _ := json.Marshal(p.AllowedTools)
	goals, _ := json.Marshal(p.Goals)
	rules, _ := json.Marshal(p.Rules)
	_, err := s.db.ExecContext(ctx, `INSERT INTO profiles(id,name,role_description,system_prompt,goals,rules,connection_id,provider,provider_preset,base_url,api_version,model,temperature,max_output_tokens,context_window_tokens,reasoning_effort,allowed_tools,max_steps,max_duration_seconds,approval_mode,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,role_description=excluded.role_description,system_prompt=excluded.system_prompt,goals=excluded.goals,rules=excluded.rules,connection_id=excluded.connection_id,provider=excluded.provider,provider_preset=excluded.provider_preset,base_url=excluded.base_url,api_version=excluded.api_version,model=excluded.model,temperature=excluded.temperature,max_output_tokens=excluded.max_output_tokens,context_window_tokens=excluded.context_window_tokens,reasoning_effort=excluded.reasoning_effort,allowed_tools=excluded.allowed_tools,max_steps=excluded.max_steps,max_duration_seconds=excluded.max_duration_seconds,approval_mode=excluded.approval_mode,updated_at=excluded.updated_at`,
		p.ID, p.Name, p.RoleDescription, p.SystemPrompt, string(goals), string(rules), p.ConnectionID, p.Provider, p.ProviderPreset, p.BaseURL, p.APIVersion, p.Model, p.Temperature, p.MaxOutputTokens, p.ContextWindowTokens, p.ReasoningEffort, string(tools), p.MaxSteps, p.MaxDurationSeconds, p.ApprovalMode, formatTime(p.CreatedAt), formatTime(p.UpdatedAt))
	return err
}

func (s *SQLite) ListProfiles(ctx context.Context) ([]domain.AgentProfile, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,role_description,system_prompt,goals,rules,connection_id,provider,provider_preset,base_url,api_version,model,temperature,max_output_tokens,context_window_tokens,reasoning_effort,allowed_tools,max_steps,max_duration_seconds,approval_mode,created_at,updated_at FROM profiles ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.AgentProfile
	for rows.Next() {
		var p domain.AgentProfile
		var tools, goals, rules, created, updated string
		if err = rows.Scan(&p.ID, &p.Name, &p.RoleDescription, &p.SystemPrompt, &goals, &rules, &p.ConnectionID, &p.Provider, &p.ProviderPreset, &p.BaseURL, &p.APIVersion, &p.Model, &p.Temperature, &p.MaxOutputTokens, &p.ContextWindowTokens, &p.ReasoningEffort, &tools, &p.MaxSteps, &p.MaxDurationSeconds, &p.ApprovalMode, &created, &updated); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(tools), &p.AllowedTools)
		_ = json.Unmarshal([]byte(goals), &p.Goals)
		_ = json.Unmarshal([]byte(rules), &p.Rules)
		p.CreatedAt = parseTime(created)
		p.UpdatedAt = parseTime(updated)
		result = append(result, p)
	}
	return result, rows.Err()
}

func (s *SQLite) DeleteProfile(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM profiles WHERE id=?`, id)
	return err
}

func (s *SQLite) SaveCustomTool(ctx context.Context, tool domain.CustomTool) error {
	configuration, _ := json.Marshal(struct {
		Program              string                       `json:"program,omitempty"`
		Arguments            []string                     `json:"arguments,omitempty"`
		Parameters           []domain.CustomToolParameter `json:"parameters,omitempty"`
		ProvidesVerification bool                         `json:"providesVerification,omitempty"`
		Revision             int                          `json:"revision,omitempty"`
		TrustedRuns          int                          `json:"trustedRuns,omitempty"`
	}{Program: tool.Program, Arguments: tool.Arguments, Parameters: tool.Parameters, ProvidesVerification: tool.ProvidesVerification, Revision: tool.Revision, TrustedRuns: tool.TrustedRuns})
	_, err := s.db.ExecContext(ctx, `INSERT INTO custom_tools(id,kind,display_name,description,command,cwd,timeout_seconds,configuration,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET kind=excluded.kind,display_name=excluded.display_name,description=excluded.description,command=excluded.command,cwd=excluded.cwd,timeout_seconds=excluded.timeout_seconds,configuration=excluded.configuration,updated_at=excluded.updated_at`,
		tool.ID, tool.Kind, tool.DisplayName, tool.Description, tool.Command, tool.CWD, tool.TimeoutSeconds, string(configuration), formatTime(tool.CreatedAt), formatTime(tool.UpdatedAt))
	return err
}

func (s *SQLite) ListCustomTools(ctx context.Context) ([]domain.CustomTool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,kind,display_name,description,command,cwd,timeout_seconds,configuration,created_at,updated_at FROM custom_tools ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.CustomTool
	for rows.Next() {
		var tool domain.CustomTool
		var configuration, created, updated string
		if err = rows.Scan(&tool.ID, &tool.Kind, &tool.DisplayName, &tool.Description, &tool.Command, &tool.CWD, &tool.TimeoutSeconds, &configuration, &created, &updated); err != nil {
			return nil, err
		}
		var config struct {
			Program              string                       `json:"program,omitempty"`
			Arguments            []string                     `json:"arguments,omitempty"`
			Parameters           []domain.CustomToolParameter `json:"parameters,omitempty"`
			ProvidesVerification bool                         `json:"providesVerification,omitempty"`
			Revision             int                          `json:"revision,omitempty"`
			TrustedRuns          int                          `json:"trustedRuns,omitempty"`
		}
		_ = json.Unmarshal([]byte(configuration), &config)
		tool.Program, tool.Arguments, tool.Parameters, tool.ProvidesVerification = config.Program, config.Arguments, config.Parameters, config.ProvidesVerification
		tool.Revision, tool.TrustedRuns = config.Revision, config.TrustedRuns
		tool.CreatedAt, tool.UpdatedAt = parseTime(created), parseTime(updated)
		result = append(result, tool)
	}
	return result, rows.Err()
}

func (s *SQLite) DeleteCustomTool(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM custom_tools WHERE id=?`, id)
	return err
}

func (s *SQLite) SaveWorkflow(ctx context.Context, workflow domain.AgentWorkflow) error {
	steps, _ := json.Marshal(workflow.Steps)
	_, err := s.db.ExecContext(ctx, `INSERT INTO workflows(id,name,description,steps,created_at,updated_at)
VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,description=excluded.description,steps=excluded.steps,updated_at=excluded.updated_at`,
		workflow.ID, workflow.Name, workflow.Description, string(steps), formatTime(workflow.CreatedAt), formatTime(workflow.UpdatedAt))
	return err
}

func (s *SQLite) ListWorkflows(ctx context.Context) ([]domain.AgentWorkflow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,description,steps,created_at,updated_at FROM workflows ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.AgentWorkflow
	for rows.Next() {
		var workflow domain.AgentWorkflow
		var steps, created, updated string
		if err = rows.Scan(&workflow.ID, &workflow.Name, &workflow.Description, &steps, &created, &updated); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(steps), &workflow.Steps)
		workflow.CreatedAt, workflow.UpdatedAt = parseTime(created), parseTime(updated)
		result = append(result, workflow)
	}
	return result, rows.Err()
}

func (s *SQLite) DeleteWorkflow(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM workflows WHERE id=?`, id)
	return err
}

func (s *SQLite) SaveWorkflowRun(ctx context.Context, run domain.WorkflowRun) error {
	contextItems, _ := json.Marshal(run.ContextItems)
	snapshot, _ := json.Marshal(run.Snapshot)
	stepRuns, _ := json.Marshal(run.StepRuns)
	var finished any
	if run.FinishedAt != nil {
		finished = formatTime(*run.FinishedAt)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO workflow_runs(id,workflow_id,workspace_id,task,context_items,snapshot,status,current_step,step_runs,error,result,started_at,finished_at,duration_ms)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET status=excluded.status,current_step=excluded.current_step,step_runs=excluded.step_runs,error=excluded.error,result=excluded.result,finished_at=excluded.finished_at,duration_ms=excluded.duration_ms`,
		run.ID, run.WorkflowID, run.WorkspaceID, run.Task, string(contextItems), string(snapshot), run.Status, run.CurrentStep, string(stepRuns), run.Error, run.Result, formatTime(run.StartedAt), finished, run.DurationMs)
	return err
}

func (s *SQLite) ListWorkflowRunsForWorkspace(ctx context.Context, workspaceID string, limit int) ([]domain.WorkflowRun, error) {
	if strings.TrimSpace(workspaceID) == "" {
		return []domain.WorkflowRun{}, nil
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,workflow_id,workspace_id,task,context_items,snapshot,status,current_step,step_runs,error,result,started_at,finished_at,duration_ms FROM workflow_runs WHERE workspace_id=? ORDER BY started_at DESC LIMIT ?`, workspaceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.WorkflowRun
	for rows.Next() {
		run, scanErr := scanWorkflowRun(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, run)
	}
	if result == nil {
		result = []domain.WorkflowRun{}
	}
	return result, rows.Err()
}

func (s *SQLite) GetWorkflowRun(ctx context.Context, id string) (domain.WorkflowRun, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id,workflow_id,workspace_id,task,context_items,snapshot,status,current_step,step_runs,error,result,started_at,finished_at,duration_ms FROM workflow_runs WHERE id=?`, id)
	return scanWorkflowRun(row)
}

func scanWorkflowRun(row scanner) (domain.WorkflowRun, error) {
	var run domain.WorkflowRun
	var contextItems, snapshot, stepRuns, started string
	var finished sql.NullString
	err := row.Scan(&run.ID, &run.WorkflowID, &run.WorkspaceID, &run.Task, &contextItems, &snapshot, &run.Status, &run.CurrentStep, &stepRuns, &run.Error, &run.Result, &started, &finished, &run.DurationMs)
	if err != nil {
		return run, err
	}
	_ = json.Unmarshal([]byte(contextItems), &run.ContextItems)
	_ = json.Unmarshal([]byte(snapshot), &run.Snapshot)
	_ = json.Unmarshal([]byte(stepRuns), &run.StepRuns)
	run.StartedAt = parseTime(started)
	if finished.Valid {
		value := parseTime(finished.String)
		run.FinishedAt = &value
	}
	return run, nil
}
