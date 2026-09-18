package storage

import (
	"context"
	"database/sql"
	"fmt"

	"local-agent-workbench/internal/domain"
)

func (s *SQLite) SaveBlueprint(ctx context.Context, b domain.AgentBlueprint) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO agent_blueprints(
  id, name, role_description, personality, mission, system_prompt, goals, rules, constraints_json, skill_ids,
  allowed_tools, tool_policies, connection_id, provider, provider_preset, base_url, primary_model, fallback_models,
  temperature, max_output_tokens, context_window_tokens, reasoning_effort, max_steps, max_duration_seconds,
  approval_mode, created_at, updated_at
) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET
  name=excluded.name, role_description=excluded.role_description, personality=excluded.personality,
  mission=excluded.mission, system_prompt=excluded.system_prompt, goals=excluded.goals, rules=excluded.rules,
  constraints_json=excluded.constraints_json, skill_ids=excluded.skill_ids, allowed_tools=excluded.allowed_tools,
  tool_policies=excluded.tool_policies, connection_id=excluded.connection_id,
  provider=excluded.provider, provider_preset=excluded.provider_preset,
  base_url=excluded.base_url, primary_model=excluded.primary_model, fallback_models=excluded.fallback_models,
  temperature=excluded.temperature, max_output_tokens=excluded.max_output_tokens,
  context_window_tokens=excluded.context_window_tokens, reasoning_effort=excluded.reasoning_effort,
  max_steps=excluded.max_steps, max_duration_seconds=excluded.max_duration_seconds,
  approval_mode=excluded.approval_mode, updated_at=excluded.updated_at`,
		b.ID, b.Name, b.RoleDescription, b.Personality, b.Mission, b.SystemPrompt,
		marshalJSON(b.Goals), marshalJSON(b.Rules), marshalJSON(b.Constraints), marshalJSON(b.SkillIDs),
		marshalJSON(b.AllowedTools), marshalJSON(b.ToolPolicies), b.ConnectionID, b.Provider, b.ProviderPreset, b.BaseURL,
		b.PrimaryModel, marshalJSON(b.FallbackModels), b.Temperature, b.MaxOutputTokens, b.ContextWindowTokens,
		b.ReasoningEffort, b.MaxSteps, b.MaxDurationSeconds, b.ApprovalMode, formatTime(b.CreatedAt), formatTime(b.UpdatedAt))
	return err
}

func (s *SQLite) ListBlueprints(ctx context.Context) ([]domain.AgentBlueprint, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, name, role_description, personality, mission, system_prompt, goals, rules, constraints_json, skill_ids,
       allowed_tools, tool_policies, connection_id, provider, provider_preset, base_url, primary_model, fallback_models,
       temperature, max_output_tokens, context_window_tokens, reasoning_effort, max_steps, max_duration_seconds,
       approval_mode, created_at, updated_at
FROM agent_blueprints ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.AgentBlueprint
	for rows.Next() {
		var b domain.AgentBlueprint
		var goals, rules, constraints, skills, tools, policies, fallbacks, created, updated string
		if err = rows.Scan(&b.ID, &b.Name, &b.RoleDescription, &b.Personality, &b.Mission, &b.SystemPrompt,
			&goals, &rules, &constraints, &skills, &tools, &policies, &b.ConnectionID, &b.Provider, &b.ProviderPreset, &b.BaseURL,
			&b.PrimaryModel, &fallbacks, &b.Temperature, &b.MaxOutputTokens, &b.ContextWindowTokens, &b.ReasoningEffort,
			&b.MaxSteps, &b.MaxDurationSeconds, &b.ApprovalMode, &created, &updated); err != nil {
			return nil, err
		}
		unmarshalJSON(goals, &b.Goals)
		unmarshalJSON(rules, &b.Rules)
		unmarshalJSON(constraints, &b.Constraints)
		unmarshalJSON(skills, &b.SkillIDs)
		unmarshalJSON(tools, &b.AllowedTools)
		unmarshalJSON(policies, &b.ToolPolicies)
		unmarshalJSON(fallbacks, &b.FallbackModels)
		b.CreatedAt, b.UpdatedAt = parseTime(created), parseTime(updated)
		result = append(result, b)
	}
	return result, rows.Err()
}

func (s *SQLite) GetBlueprint(ctx context.Context, id string) (domain.AgentBlueprint, error) {
	list, err := s.ListBlueprints(ctx)
	if err != nil {
		return domain.AgentBlueprint{}, err
	}
	for _, item := range list {
		if item.ID == id {
			return item, nil
		}
	}
	return domain.AgentBlueprint{}, sql.ErrNoRows
}

func (s *SQLite) DeleteBlueprint(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM agent_blueprints WHERE id=?`, id)
	return err
}

func (s *SQLite) DeleteProjectAgent(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM project_agents WHERE id=?`, id)
	return err
}

func (s *SQLite) SaveProjectAgent(ctx context.Context, a domain.ProjectAgent) error {
	var existingWorkspace string
	err := s.db.QueryRowContext(ctx, `SELECT workspace_id FROM project_agents WHERE id=?`, a.ID).Scan(&existingWorkspace)
	if err == nil && existingWorkspace != a.WorkspaceID {
		return fmt.Errorf("project agent %q belongs to another workspace", a.ID)
	}
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO project_agents(
  id, workspace_id, blueprint_id, parent_agent_id, temporary, name, role_description, personality, mission, system_prompt, goals, rules,
  constraints_json, project_rules, skill_ids, allowed_tools, tool_policies, connection_id, provider, provider_preset, base_url,
  primary_model, fallback_models, temperature, max_output_tokens, context_window_tokens, reasoning_effort,
  max_steps, max_duration_seconds, approval_mode, experience, level, tasks_completed, success_count, created_at, updated_at
) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET
  parent_agent_id=excluded.parent_agent_id, temporary=excluded.temporary,
  name=excluded.name, role_description=excluded.role_description, personality=excluded.personality,
  mission=excluded.mission, system_prompt=excluded.system_prompt, goals=excluded.goals, rules=excluded.rules,
  constraints_json=excluded.constraints_json, project_rules=excluded.project_rules, skill_ids=excluded.skill_ids,
  allowed_tools=excluded.allowed_tools, tool_policies=excluded.tool_policies,
  connection_id=excluded.connection_id, provider=excluded.provider,
  provider_preset=excluded.provider_preset, base_url=excluded.base_url, primary_model=excluded.primary_model,
  fallback_models=excluded.fallback_models, temperature=excluded.temperature, max_output_tokens=excluded.max_output_tokens,
  context_window_tokens=excluded.context_window_tokens, reasoning_effort=excluded.reasoning_effort,
  max_steps=excluded.max_steps, max_duration_seconds=excluded.max_duration_seconds, approval_mode=excluded.approval_mode,
  experience=excluded.experience, level=excluded.level, tasks_completed=excluded.tasks_completed,
  success_count=excluded.success_count, updated_at=excluded.updated_at`,
		a.ID, a.WorkspaceID, a.BlueprintID, a.ParentAgentID, a.Temporary, a.Name, a.RoleDescription, a.Personality, a.Mission, a.SystemPrompt,
		marshalJSON(a.Goals), marshalJSON(a.Rules), marshalJSON(a.Constraints), marshalJSON(a.ProjectRules),
		marshalJSON(a.SkillIDs), marshalJSON(a.AllowedTools), marshalJSON(a.ToolPolicies), a.ConnectionID, a.Provider, a.ProviderPreset,
		a.BaseURL, a.PrimaryModel, marshalJSON(a.FallbackModels), a.Temperature, a.MaxOutputTokens, a.ContextWindowTokens,
		a.ReasoningEffort, a.MaxSteps, a.MaxDurationSeconds, a.ApprovalMode, a.Experience, a.Level, a.TasksCompleted,
		a.SuccessCount, formatTime(a.CreatedAt), formatTime(a.UpdatedAt))
	return err
}

func (s *SQLite) ListProjectAgents(ctx context.Context, workspaceID string) ([]domain.ProjectAgent, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, workspace_id, blueprint_id, parent_agent_id, temporary, name, role_description, personality, mission, system_prompt, goals, rules,
       constraints_json, project_rules, skill_ids, allowed_tools, tool_policies, connection_id, provider, provider_preset, base_url,
       primary_model, fallback_models, temperature, max_output_tokens, context_window_tokens, reasoning_effort,
       max_steps, max_duration_seconds, approval_mode, experience, level, tasks_completed, success_count, created_at, updated_at
FROM project_agents WHERE workspace_id=? ORDER BY updated_at DESC`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.ProjectAgent
	for rows.Next() {
		var a domain.ProjectAgent
		var goals, rules, constraints, projectRules, skills, tools, policies, fallbacks, created, updated string
		if err = rows.Scan(&a.ID, &a.WorkspaceID, &a.BlueprintID, &a.ParentAgentID, &a.Temporary, &a.Name, &a.RoleDescription, &a.Personality, &a.Mission,
			&a.SystemPrompt, &goals, &rules, &constraints, &projectRules, &skills, &tools, &policies, &a.ConnectionID, &a.Provider,
			&a.ProviderPreset, &a.BaseURL, &a.PrimaryModel, &fallbacks, &a.Temperature, &a.MaxOutputTokens,
			&a.ContextWindowTokens, &a.ReasoningEffort, &a.MaxSteps, &a.MaxDurationSeconds, &a.ApprovalMode,
			&a.Experience, &a.Level, &a.TasksCompleted, &a.SuccessCount, &created, &updated); err != nil {
			return nil, err
		}
		unmarshalJSON(goals, &a.Goals)
		unmarshalJSON(rules, &a.Rules)
		unmarshalJSON(constraints, &a.Constraints)
		unmarshalJSON(projectRules, &a.ProjectRules)
		unmarshalJSON(skills, &a.SkillIDs)
		unmarshalJSON(tools, &a.AllowedTools)
		unmarshalJSON(policies, &a.ToolPolicies)
		unmarshalJSON(fallbacks, &a.FallbackModels)
		a.CreatedAt, a.UpdatedAt = parseTime(created), parseTime(updated)
		result = append(result, a)
	}
	return result, rows.Err()
}

func (s *SQLite) ListAllProjectAgents(ctx context.Context) ([]domain.ProjectAgent, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, workspace_id, blueprint_id, parent_agent_id, temporary, name, role_description, personality, mission, system_prompt, goals, rules,
       constraints_json, project_rules, skill_ids, allowed_tools, tool_policies, connection_id, provider, provider_preset, base_url,
       primary_model, fallback_models, temperature, max_output_tokens, context_window_tokens, reasoning_effort,
       max_steps, max_duration_seconds, approval_mode, experience, level, tasks_completed, success_count, created_at, updated_at
FROM project_agents ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.ProjectAgent
	for rows.Next() {
		var a domain.ProjectAgent
		var goals, rules, constraints, projectRules, skills, tools, policies, fallbacks, created, updated string
		if err = rows.Scan(&a.ID, &a.WorkspaceID, &a.BlueprintID, &a.ParentAgentID, &a.Temporary, &a.Name, &a.RoleDescription, &a.Personality, &a.Mission,
			&a.SystemPrompt, &goals, &rules, &constraints, &projectRules, &skills, &tools, &policies, &a.ConnectionID, &a.Provider,
			&a.ProviderPreset, &a.BaseURL, &a.PrimaryModel, &fallbacks, &a.Temperature, &a.MaxOutputTokens,
			&a.ContextWindowTokens, &a.ReasoningEffort, &a.MaxSteps, &a.MaxDurationSeconds, &a.ApprovalMode,
			&a.Experience, &a.Level, &a.TasksCompleted, &a.SuccessCount, &created, &updated); err != nil {
			return nil, err
		}
		unmarshalJSON(goals, &a.Goals)
		unmarshalJSON(rules, &a.Rules)
		unmarshalJSON(constraints, &a.Constraints)
		unmarshalJSON(projectRules, &a.ProjectRules)
		unmarshalJSON(skills, &a.SkillIDs)
		unmarshalJSON(tools, &a.AllowedTools)
		unmarshalJSON(policies, &a.ToolPolicies)
		unmarshalJSON(fallbacks, &a.FallbackModels)
		a.CreatedAt, a.UpdatedAt = parseTime(created), parseTime(updated)
		result = append(result, a)
	}
	return result, rows.Err()
}

func (s *SQLite) GetProjectAgent(ctx context.Context, id string) (domain.ProjectAgent, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, workspace_id, blueprint_id, parent_agent_id, temporary, name, role_description, personality, mission, system_prompt, goals, rules,
       constraints_json, project_rules, skill_ids, allowed_tools, tool_policies, connection_id, provider, provider_preset, base_url,
       primary_model, fallback_models, temperature, max_output_tokens, context_window_tokens, reasoning_effort,
       max_steps, max_duration_seconds, approval_mode, experience, level, tasks_completed, success_count, created_at, updated_at
FROM project_agents WHERE id=?`, id)
	var a domain.ProjectAgent
	var goals, rules, constraints, projectRules, skills, tools, policies, fallbacks, created, updated string
	if err := row.Scan(&a.ID, &a.WorkspaceID, &a.BlueprintID, &a.ParentAgentID, &a.Temporary, &a.Name, &a.RoleDescription, &a.Personality, &a.Mission,
		&a.SystemPrompt, &goals, &rules, &constraints, &projectRules, &skills, &tools, &policies, &a.ConnectionID, &a.Provider,
		&a.ProviderPreset, &a.BaseURL, &a.PrimaryModel, &fallbacks, &a.Temperature, &a.MaxOutputTokens,
		&a.ContextWindowTokens, &a.ReasoningEffort, &a.MaxSteps, &a.MaxDurationSeconds, &a.ApprovalMode,
		&a.Experience, &a.Level, &a.TasksCompleted, &a.SuccessCount, &created, &updated); err != nil {
		return domain.ProjectAgent{}, err
	}
	unmarshalJSON(goals, &a.Goals)
	unmarshalJSON(rules, &a.Rules)
	unmarshalJSON(constraints, &a.Constraints)
	unmarshalJSON(projectRules, &a.ProjectRules)
	unmarshalJSON(skills, &a.SkillIDs)
	unmarshalJSON(tools, &a.AllowedTools)
	unmarshalJSON(policies, &a.ToolPolicies)
	unmarshalJSON(fallbacks, &a.FallbackModels)
	a.CreatedAt, a.UpdatedAt = parseTime(created), parseTime(updated)
	return a, nil
}

func (s *SQLite) EnsureProjectAgentsForWorkspace(ctx context.Context, workspaceID string) ([]domain.ProjectAgent, error) {
	existing, err := s.ListProjectAgents(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	byBlueprint := map[string]domain.ProjectAgent{}
	for _, agent := range existing {
		if agent.Temporary {
			continue
		}
		byBlueprint[agent.BlueprintID] = agent
	}
	blueprints, err := s.ListBlueprints(ctx)
	if err != nil {
		return nil, err
	}
	for _, blueprint := range blueprints {
		if _, ok := byBlueprint[blueprint.ID]; ok {
			continue
		}
		agent := domain.ProjectAgentFromBlueprint(workspaceID, blueprint)
		if err = s.SaveProjectAgent(ctx, agent); err != nil {
			return nil, err
		}
		existing = append(existing, agent)
	}
	return existing, nil
}

func (s *SQLite) SaveSkill(ctx context.Context, skill domain.SkillDefinition) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO skill_definitions(id,name,description,instructions,references_json,scripts_json,required_tools,permission_delta,configuration,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET name=excluded.name, description=excluded.description, instructions=excluded.instructions,
  references_json=excluded.references_json, scripts_json=excluded.scripts_json, required_tools=excluded.required_tools,
  permission_delta=excluded.permission_delta, configuration=excluded.configuration, updated_at=excluded.updated_at`,
		skill.ID, skill.Name, skill.Description, skill.Instructions, marshalJSON(skill.References), marshalJSON(skill.Scripts),
		marshalJSON(skill.RequiredTools), marshalJSON(skill.PermissionDelta), marshalJSON(skill.Configuration),
		formatTime(skill.CreatedAt), formatTime(skill.UpdatedAt))
	return err
}

func (s *SQLite) ListSkills(ctx context.Context) ([]domain.SkillDefinition, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,description,instructions,references_json,scripts_json,required_tools,permission_delta,configuration,created_at,updated_at FROM skill_definitions ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.SkillDefinition
	for rows.Next() {
		var skill domain.SkillDefinition
		var refs, scripts, tools, perms, config, created, updated string
		if err = rows.Scan(&skill.ID, &skill.Name, &skill.Description, &skill.Instructions, &refs, &scripts, &tools, &perms, &config, &created, &updated); err != nil {
			return nil, err
		}
		unmarshalJSON(refs, &skill.References)
		unmarshalJSON(scripts, &skill.Scripts)
		unmarshalJSON(tools, &skill.RequiredTools)
		unmarshalJSON(perms, &skill.PermissionDelta)
		unmarshalJSON(config, &skill.Configuration)
		skill.CreatedAt, skill.UpdatedAt = parseTime(created), parseTime(updated)
		result = append(result, skill)
	}
	return result, rows.Err()
}

func (s *SQLite) SaveProjectSkill(ctx context.Context, skill domain.ProjectSkillInstance) error {
	enabled := 0
	if skill.Enabled {
		enabled = 1
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO project_skills(id,workspace_id,skill_id,configuration,enabled,created_at,updated_at)
VALUES(?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET configuration=excluded.configuration, enabled=excluded.enabled, updated_at=excluded.updated_at`,
		skill.ID, skill.WorkspaceID, skill.SkillID, marshalJSON(skill.Configuration), enabled, formatTime(skill.CreatedAt), formatTime(skill.UpdatedAt))
	return err
}

func (s *SQLite) ListProjectSkills(ctx context.Context, workspaceID string) ([]domain.ProjectSkillInstance, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,workspace_id,skill_id,configuration,enabled,created_at,updated_at FROM project_skills WHERE workspace_id=? ORDER BY updated_at DESC`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.ProjectSkillInstance
	for rows.Next() {
		var skill domain.ProjectSkillInstance
		var config, created, updated string
		var enabled int
		if err = rows.Scan(&skill.ID, &skill.WorkspaceID, &skill.SkillID, &config, &enabled, &created, &updated); err != nil {
			return nil, err
		}
		unmarshalJSON(config, &skill.Configuration)
		skill.Enabled = enabled == 1
		skill.CreatedAt, skill.UpdatedAt = parseTime(created), parseTime(updated)
		result = append(result, skill)
	}
	return result, rows.Err()
}
