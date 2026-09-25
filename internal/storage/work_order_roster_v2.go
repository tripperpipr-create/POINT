package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
)

// materializeWorkOrderRosterV2 создаёт утверждённых исполнителей. С reuse
// (повторное утверждение наряда) агент, которого прошлое утверждение уже
// создало под тем же идентификатором в этом проекте, берётся как есть.
func materializeWorkOrderRosterV2(ctx context.Context, tx *sql.Tx, order domain.WorkOrder, now time.Time, reuse bool) ([]string, error) {
	if len(order.Roster.Permanent) == 0 && len(order.Roster.Temporary) == 0 {
		return []string{}, nil
	}
	connectionID, model := order.Routing.FixedConnectionID, order.Routing.FixedModel
	if order.Routing.Mode == "auto" {
		connectionID, model = order.Routing.RouterConnectionID, order.Routing.RouterModel
	}
	var provider, preset, baseURL string
	if err := tx.QueryRowContext(ctx, `SELECT provider,preset_id,base_url FROM connections WHERE id=?`, connectionID).Scan(&provider, &preset, &baseURL); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("roster model connection %q does not exist", connectionID)
		}
		return nil, err
	}
	created := make([]string, 0, len(order.Roster.Permanent)+len(order.Roster.Temporary))
	parentIDs := make(map[string]string, len(order.Roster.Permanent))
	for _, draft := range order.Roster.Permanent {
		projectAgentID := draft.ID
		parentIDs[draft.ID] = projectAgentID
		existing := draft.Existing
		if !existing && reuse {
			var found int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM project_agents WHERE id=?`, projectAgentID).Scan(&found); err != nil {
				return nil, err
			}
			existing = found > 0
		}
		if existing {
			var workspaceID string
			if err := tx.QueryRowContext(ctx, `SELECT workspace_id FROM project_agents WHERE id=?`, projectAgentID).Scan(&workspaceID); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil, fmt.Errorf("approved existing agent %q does not exist", projectAgentID)
				}
				return nil, err
			}
			if workspaceID != order.WorkspaceID {
				return nil, fmt.Errorf("approved existing agent %q belongs to another project", projectAgentID)
			}
			created = append(created, projectAgentID)
			continue
		}
		var blueprint domain.AgentBlueprint
		if draft.BlueprintID != "" {
			var err error
			blueprint, err = blueprintV2Tx(ctx, tx, draft.BlueprintID)
			if err != nil {
				return nil, fmt.Errorf("load approved blueprint %q: %w", draft.BlueprintID, err)
			}
		} else {
			blueprint = domain.AgentBlueprint{
				ID: domain.NewID("blueprint"), Name: draft.Name, RoleDescription: draft.Role, Mission: draft.Mission,
				SystemPrompt: draft.Mission, Goals: []string{draft.Mission}, Rules: []string{"Respect the approved WorkOrder scope and evidence gates."},
				AllowedTools: draft.RequiredTools, ConnectionID: connectionID, Provider: domain.ProviderKind(provider), ProviderPreset: preset,
				BaseURL: baseURL, PrimaryModel: model, Temperature: 0.2,
				// Тот же пол, что у временного субагента ниже: размышление тратит
				// бюджет вывода первым, и 4096 на размышляющей модели теряет ход.
				MaxOutputTokens: domain.OutputBudgetForThinking(4096, model, "medium"), ContextWindowTokens: 32768,
				ReasoningEffort: "medium", MaxSteps: 24, MaxDurationSeconds: order.Budget.ActiveSeconds, ApprovalMode: domain.ApprovalSafe,
				CreatedAt: now, UpdatedAt: now,
			}
			if !draft.ProjectOnly {
				if err := insertBlueprintV2Tx(ctx, tx, blueprint); err != nil {
					return nil, err
				}
			} else {
				blueprint.ID = ""
			}
		}
		agent := projectAgentFromBlueprintV2(projectAgentID, order.WorkspaceID, blueprint, now)
		agent.Name, agent.RoleDescription, agent.Mission = draft.Name, draft.Role, draft.Mission
		if len(draft.RequiredTools) > 0 {
			agent.AllowedTools = append([]string(nil), draft.RequiredTools...)
		}
		if err := insertProjectAgentV2Tx(ctx, tx, agent); err != nil {
			return nil, err
		}
		created = append(created, agent.ID)
	}
	for _, temporary := range order.Roster.Temporary {
		parentID := parentIDs[temporary.ParentAgentID]
		if parentID == "" {
			return nil, fmt.Errorf("temporary subagent parent %q was not materialized", temporary.ParentAgentID)
		}
		agent := domain.ProjectAgent{
			ID: domain.NewID("subagent"), WorkspaceID: order.WorkspaceID, ParentAgentID: parentID, Temporary: true,
			Name: temporary.Role, RoleDescription: temporary.Role, Mission: temporary.Mission, SystemPrompt: temporary.Mission,
			Goals: []string{temporary.Mission}, Rules: []string{"Work only on the delegated bounded task."}, AllowedTools: temporary.RequiredTools,
			ConnectionID: connectionID, Provider: domain.ProviderKind(provider), ProviderPreset: preset, BaseURL: baseURL, PrimaryModel: model,
			Temperature: 0.1, MaxOutputTokens: domain.MinThinkingOutputTokens, ContextWindowTokens: 32768, ReasoningEffort: "medium",
			MaxSteps: 12, MaxDurationSeconds: order.Budget.ActiveSeconds, ApprovalMode: domain.ApprovalSafe, Level: 1, CreatedAt: now, UpdatedAt: now,
		}
		if err := insertProjectAgentV2Tx(ctx, tx, agent); err != nil {
			return nil, err
		}
		created = append(created, agent.ID)
	}
	return created, nil
}

func blueprintV2Tx(ctx context.Context, tx *sql.Tx, id string) (domain.AgentBlueprint, error) {
	var b domain.AgentBlueprint
	var goals, rules, constraints, skills, tools, policies, fallbacks, created, updated string
	err := tx.QueryRowContext(ctx, `SELECT id,name,role_description,personality,mission,system_prompt,goals,rules,constraints_json,skill_ids,allowed_tools,tool_policies,connection_id,provider,provider_preset,base_url,primary_model,fallback_models,temperature,max_output_tokens,context_window_tokens,reasoning_effort,max_steps,max_duration_seconds,approval_mode,created_at,updated_at FROM agent_blueprints WHERE id=?`, strings.TrimSpace(id)).Scan(
		&b.ID, &b.Name, &b.RoleDescription, &b.Personality, &b.Mission, &b.SystemPrompt, &goals, &rules, &constraints, &skills, &tools, &policies,
		&b.ConnectionID, &b.Provider, &b.ProviderPreset, &b.BaseURL, &b.PrimaryModel, &fallbacks, &b.Temperature, &b.MaxOutputTokens,
		&b.ContextWindowTokens, &b.ReasoningEffort, &b.MaxSteps, &b.MaxDurationSeconds, &b.ApprovalMode, &created, &updated)
	if err != nil {
		return b, err
	}
	unmarshalJSON(goals, &b.Goals)
	unmarshalJSON(rules, &b.Rules)
	unmarshalJSON(constraints, &b.Constraints)
	unmarshalJSON(skills, &b.SkillIDs)
	unmarshalJSON(tools, &b.AllowedTools)
	unmarshalJSON(policies, &b.ToolPolicies)
	unmarshalJSON(fallbacks, &b.FallbackModels)
	b.CreatedAt, b.UpdatedAt = parseTime(created), parseTime(updated)
	return b, nil
}

func insertBlueprintV2Tx(ctx context.Context, tx *sql.Tx, b domain.AgentBlueprint) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO agent_blueprints(id,name,role_description,personality,mission,system_prompt,goals,rules,constraints_json,skill_ids,allowed_tools,tool_policies,connection_id,provider,provider_preset,base_url,primary_model,fallback_models,temperature,max_output_tokens,context_window_tokens,reasoning_effort,max_steps,max_duration_seconds,approval_mode,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		b.ID, b.Name, b.RoleDescription, b.Personality, b.Mission, b.SystemPrompt, marshalJSON(b.Goals), marshalJSON(b.Rules), marshalJSON(b.Constraints), marshalJSON(b.SkillIDs), marshalJSON(b.AllowedTools), marshalJSON(b.ToolPolicies), b.ConnectionID, b.Provider, b.ProviderPreset, b.BaseURL, b.PrimaryModel, marshalJSON(b.FallbackModels), b.Temperature, b.MaxOutputTokens, b.ContextWindowTokens, b.ReasoningEffort, b.MaxSteps, b.MaxDurationSeconds, b.ApprovalMode, formatTime(b.CreatedAt), formatTime(b.UpdatedAt))
	return err
}

func projectAgentFromBlueprintV2(id, workspaceID string, b domain.AgentBlueprint, now time.Time) domain.ProjectAgent {
	return domain.ProjectAgent{ID: id, WorkspaceID: workspaceID, BlueprintID: b.ID, Name: b.Name, RoleDescription: b.RoleDescription, Personality: b.Personality, Mission: b.Mission, SystemPrompt: b.SystemPrompt, Goals: b.Goals, Rules: b.Rules, Constraints: b.Constraints, SkillIDs: b.SkillIDs, AllowedTools: b.AllowedTools, ToolPolicies: b.ToolPolicies, ConnectionID: b.ConnectionID, Provider: b.Provider, ProviderPreset: b.ProviderPreset, BaseURL: b.BaseURL, PrimaryModel: b.PrimaryModel, FallbackModels: b.FallbackModels, Temperature: b.Temperature, MaxOutputTokens: b.MaxOutputTokens, ContextWindowTokens: b.ContextWindowTokens, ReasoningEffort: b.ReasoningEffort, MaxSteps: b.MaxSteps, MaxDurationSeconds: b.MaxDurationSeconds, ApprovalMode: b.ApprovalMode, Level: 1, CreatedAt: now, UpdatedAt: now}
}

func insertProjectAgentV2Tx(ctx context.Context, tx *sql.Tx, a domain.ProjectAgent) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO project_agents(id,workspace_id,blueprint_id,status,role_family,parent_agent_id,owner_quest_id,temporary,name,role_description,personality,mission,system_prompt,goals,rules,constraints_json,project_rules,skill_ids,allowed_tools,tool_policies,connection_id,provider,provider_preset,base_url,primary_model,fallback_models,temperature,max_output_tokens,context_window_tokens,reasoning_effort,max_steps,max_duration_seconds,approval_mode,experience,level,tasks_completed,success_count,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		a.ID, a.WorkspaceID, a.BlueprintID, a.Status, a.RoleFamily, a.ParentAgentID, a.OwnerQuestID, a.Temporary, a.Name, a.RoleDescription, a.Personality, a.Mission, a.SystemPrompt, marshalJSON(a.Goals), marshalJSON(a.Rules), marshalJSON(a.Constraints), marshalJSON(a.ProjectRules), marshalJSON(a.SkillIDs), marshalJSON(a.AllowedTools), marshalJSON(a.ToolPolicies), a.ConnectionID, a.Provider, a.ProviderPreset, a.BaseURL, a.PrimaryModel, marshalJSON(a.FallbackModels), a.Temperature, a.MaxOutputTokens, a.ContextWindowTokens, a.ReasoningEffort, a.MaxSteps, a.MaxDurationSeconds, a.ApprovalMode, a.Experience, a.Level, a.TasksCompleted, a.SuccessCount, formatTime(a.CreatedAt), formatTime(a.UpdatedAt))
	return err
}
