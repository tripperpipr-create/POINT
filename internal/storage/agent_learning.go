package storage

import (
	"context"
	"encoding/json"

	"local-agent-workbench/internal/domain"
)

func (s *SQLite) SaveAgentImprovement(ctx context.Context, item domain.AgentImprovement) error {
	beforeSkill, err := marshalOptionalSkill(item.BeforeSkill)
	if err != nil {
		return err
	}
	afterSkill, err := marshalOptionalSkill(item.AfterSkill)
	if err != nil {
		return err
	}
	beforeMemory, err := marshalOptionalMemory(item.BeforeMemory)
	if err != nil {
		return err
	}
	afterMemory, err := marshalOptionalMemory(item.AfterMemory)
	if err != nil {
		return err
	}
	canaryEvaluation, err := marshalOptionalCanaryEvaluation(item.CanaryEvaluation)
	if err != nil {
		return err
	}
	shadowEvaluation, err := marshalOptionalShadowEvaluation(item.ShadowEvaluation)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO agent_improvements(
  id,workspace_id,project_agent_id,blueprint_id,source_run_id,skill_id,kind,status,promotion_status,trigger_text,evidence_json,
  before_skill_json,after_skill_json,before_skill_ids_json,after_skill_ids_json,
  before_blueprint_skill_ids_json,after_blueprint_skill_ids_json,before_agent_skill_ids_json,after_agent_skill_ids_json,
  memory_id,memory_status,memory_key,memory_signature,memory_source_workspaces_json,before_memory_json,after_memory_json,
  instruction_status,instruction_key,instruction_text,instruction_signature,instruction_source_workspaces_json,
  before_blueprint_rules_json,after_blueprint_rules_json,before_agent_rules_json,after_agent_rules_json,
  review_mode,model,failure,canary_evaluation_json,effect,shadow_evaluation_json,created_at,updated_at
) VALUES(
  ?,?,?,?,?,?,?,?,?,?,
  ?,?,?,?,?,?,?,?,?,?,
  ?,?,?,?,?,?,?,?,?,?,
  ?,?,?,?,?,?,?,?,?,?,
  ?,?,?
)
ON CONFLICT(id) DO UPDATE SET status=excluded.status, promotion_status=excluded.promotion_status,
  memory_status=excluded.memory_status,instruction_status=excluded.instruction_status,
  evidence_json=excluded.evidence_json,failure=excluded.failure,
  canary_evaluation_json=excluded.canary_evaluation_json,effect=excluded.effect,
  shadow_evaluation_json=excluded.shadow_evaluation_json,updated_at=excluded.updated_at`,
		item.ID, item.WorkspaceID, item.ProjectAgentID, item.BlueprintID, item.SourceRunID, item.SkillID, item.Kind, item.Status, item.PromotionStatus,
		item.Trigger, marshalJSON(item.Evidence), beforeSkill, afterSkill, marshalJSON(item.BeforeSkillIDs),
		marshalJSON(item.AfterSkillIDs), marshalJSON(item.BeforeBlueprintSkillIDs), marshalJSON(item.AfterBlueprintSkillIDs),
		marshalJSON(item.BeforeAgentSkillIDs), marshalJSON(item.AfterAgentSkillIDs),
		item.MemoryID, item.MemoryStatus, item.MemoryKey, item.MemorySignature, marshalJSON(item.MemorySourceWorkspaces), beforeMemory, afterMemory,
		item.InstructionStatus, item.InstructionKey, item.Instruction, item.InstructionSignature, marshalJSON(item.InstructionSourceWorkspaces),
		marshalJSON(item.BeforeBlueprintRules), marshalJSON(item.AfterBlueprintRules), marshalJSON(item.BeforeAgentRules), marshalJSON(item.AfterAgentRules),
		item.ReviewMode, item.Model, item.Failure, canaryEvaluation, item.Effect, shadowEvaluation, formatTime(item.CreatedAt), formatTime(item.UpdatedAt))
	return err
}

func (s *SQLite) GetAgentImprovement(ctx context.Context, id string) (domain.AgentImprovement, error) {
	row := s.db.QueryRowContext(ctx, agentImprovementSelect+` WHERE id=?`, id)
	return scanAgentImprovement(row)
}

func (s *SQLite) FindAgentImprovementByRun(ctx context.Context, runID string) (domain.AgentImprovement, error) {
	row := s.db.QueryRowContext(ctx, agentImprovementSelect+` WHERE source_run_id=?`, runID)
	return scanAgentImprovement(row)
}

func (s *SQLite) ListAgentImprovements(ctx context.Context, workspaceID string, limit int) ([]domain.AgentImprovement, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, agentImprovementSelect+` WHERE workspace_id=? ORDER BY updated_at DESC LIMIT ?`, workspaceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.AgentImprovement, 0)
	for rows.Next() {
		item, scanErr := scanAgentImprovement(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *SQLite) ListAgentImprovementsForSkill(ctx context.Context, skillID string, limit int) ([]domain.AgentImprovement, error) {
	return s.listAgentImprovementsBy(ctx, "skill_id", skillID, limit)
}

func (s *SQLite) ListAgentImprovementsForMemory(ctx context.Context, memoryID string, limit int) ([]domain.AgentImprovement, error) {
	return s.listAgentImprovementsBy(ctx, "memory_id", memoryID, limit)
}

func (s *SQLite) ListAgentImprovementsForBlueprint(ctx context.Context, blueprintID string, limit int) ([]domain.AgentImprovement, error) {
	return s.listAgentImprovementsBy(ctx, "blueprint_id", blueprintID, limit)
}

func (s *SQLite) ListAgentImprovementsForInstruction(ctx context.Context, signature string, limit int) ([]domain.AgentImprovement, error) {
	return s.listAgentImprovementsBy(ctx, "instruction_signature", signature, limit)
}

func (s *SQLite) listAgentImprovementsBy(ctx context.Context, column, value string, limit int) ([]domain.AgentImprovement, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	query := agentImprovementSelect + ` WHERE ` + column + `=? ORDER BY updated_at DESC LIMIT ?`
	rows, err := s.db.QueryContext(ctx, query, value, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.AgentImprovement, 0)
	for rows.Next() {
		item, scanErr := scanAgentImprovement(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

const agentImprovementSelect = `SELECT id,workspace_id,project_agent_id,blueprint_id,source_run_id,skill_id,kind,status,promotion_status,trigger_text,
evidence_json,before_skill_json,after_skill_json,before_skill_ids_json,after_skill_ids_json,
before_blueprint_skill_ids_json,after_blueprint_skill_ids_json,before_agent_skill_ids_json,after_agent_skill_ids_json,
memory_id,memory_status,memory_key,memory_signature,memory_source_workspaces_json,before_memory_json,after_memory_json,
instruction_status,instruction_key,instruction_text,instruction_signature,instruction_source_workspaces_json,
before_blueprint_rules_json,after_blueprint_rules_json,before_agent_rules_json,after_agent_rules_json,
review_mode,model,failure,canary_evaluation_json,effect,shadow_evaluation_json,created_at,updated_at
FROM agent_improvements`

func scanAgentImprovement(row scanner) (domain.AgentImprovement, error) {
	var item domain.AgentImprovement
	var evidence, beforeSkill, afterSkill, beforeIDs, afterIDs, beforeBlueprintIDs, afterBlueprintIDs, beforeAgentIDs, afterAgentIDs, memoryWorkspaces, beforeMemory, afterMemory, instructionWorkspaces, beforeBlueprintRules, afterBlueprintRules, beforeAgentRules, afterAgentRules, canaryEvaluation, shadowEvaluation, created, updated string
	if err := row.Scan(&item.ID, &item.WorkspaceID, &item.ProjectAgentID, &item.BlueprintID, &item.SourceRunID, &item.SkillID,
		&item.Kind, &item.Status, &item.PromotionStatus, &item.Trigger, &evidence, &beforeSkill, &afterSkill, &beforeIDs, &afterIDs,
		&beforeBlueprintIDs, &afterBlueprintIDs, &beforeAgentIDs, &afterAgentIDs,
		&item.MemoryID, &item.MemoryStatus, &item.MemoryKey, &item.MemorySignature, &memoryWorkspaces, &beforeMemory, &afterMemory,
		&item.InstructionStatus, &item.InstructionKey, &item.Instruction, &item.InstructionSignature, &instructionWorkspaces,
		&beforeBlueprintRules, &afterBlueprintRules, &beforeAgentRules, &afterAgentRules,
		&item.ReviewMode, &item.Model, &item.Failure, &canaryEvaluation, &item.Effect, &shadowEvaluation, &created, &updated); err != nil {
		return item, err
	}
	unmarshalJSON(evidence, &item.Evidence)
	unmarshalJSON(beforeIDs, &item.BeforeSkillIDs)
	unmarshalJSON(afterIDs, &item.AfterSkillIDs)
	unmarshalJSON(beforeBlueprintIDs, &item.BeforeBlueprintSkillIDs)
	unmarshalJSON(afterBlueprintIDs, &item.AfterBlueprintSkillIDs)
	unmarshalJSON(beforeAgentIDs, &item.BeforeAgentSkillIDs)
	unmarshalJSON(afterAgentIDs, &item.AfterAgentSkillIDs)
	unmarshalJSON(memoryWorkspaces, &item.MemorySourceWorkspaces)
	unmarshalJSON(instructionWorkspaces, &item.InstructionSourceWorkspaces)
	unmarshalJSON(beforeBlueprintRules, &item.BeforeBlueprintRules)
	unmarshalJSON(afterBlueprintRules, &item.AfterBlueprintRules)
	unmarshalJSON(beforeAgentRules, &item.BeforeAgentRules)
	unmarshalJSON(afterAgentRules, &item.AfterAgentRules)
	if canaryEvaluation != "" {
		var evaluation domain.SkillCanaryEvaluation
		if err := json.Unmarshal([]byte(canaryEvaluation), &evaluation); err != nil {
			return item, err
		}
		item.CanaryEvaluation = &evaluation
	}
	if shadowEvaluation != "" {
		var evaluation domain.LearningShadowEvaluation
		if err := json.Unmarshal([]byte(shadowEvaluation), &evaluation); err != nil {
			return item, err
		}
		item.ShadowEvaluation = &evaluation
	}
	if beforeSkill != "" {
		var skill domain.SkillDefinition
		if err := json.Unmarshal([]byte(beforeSkill), &skill); err != nil {
			return item, err
		}
		item.BeforeSkill = &skill
	}
	if afterSkill != "" {
		var skill domain.SkillDefinition
		if err := json.Unmarshal([]byte(afterSkill), &skill); err != nil {
			return item, err
		}
		item.AfterSkill = &skill
	}
	if beforeMemory != "" {
		var memory domain.MemoryRecord
		if err := json.Unmarshal([]byte(beforeMemory), &memory); err != nil {
			return item, err
		}
		item.BeforeMemory = &memory
	}
	if afterMemory != "" {
		var memory domain.MemoryRecord
		if err := json.Unmarshal([]byte(afterMemory), &memory); err != nil {
			return item, err
		}
		item.AfterMemory = &memory
	}
	item.CreatedAt, item.UpdatedAt = parseTime(created), parseTime(updated)
	return item, nil
}

func marshalOptionalSkill(skill *domain.SkillDefinition) (string, error) {
	if skill == nil {
		return "", nil
	}
	encoded, err := json.Marshal(skill)
	return string(encoded), err
}

func marshalOptionalMemory(memory *domain.MemoryRecord) (string, error) {
	if memory == nil {
		return "", nil
	}
	encoded, err := json.Marshal(memory)
	return string(encoded), err
}

func marshalOptionalCanaryEvaluation(evaluation *domain.SkillCanaryEvaluation) (string, error) {
	if evaluation == nil {
		return "", nil
	}
	encoded, err := json.Marshal(evaluation)
	return string(encoded), err
}
