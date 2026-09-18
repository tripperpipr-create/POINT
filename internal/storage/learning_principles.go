package storage

import (
	"context"
	"database/sql"
	"encoding/json"

	"local-agent-workbench/internal/domain"
)

func migrationLearningEffectAndPrinciplesV1(ctx context.Context, tx *sql.Tx) error {
	statements := []string{
		`ALTER TABLE agent_improvements ADD COLUMN effect TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE agent_improvements ADD COLUMN shadow_evaluation_json TEXT NOT NULL DEFAULT ''`,
		`CREATE TABLE learning_principles (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  project_agent_id TEXT NOT NULL,
  blueprint_id TEXT NOT NULL DEFAULT '',
  quest_id TEXT NOT NULL DEFAULT '',
  kind TEXT NOT NULL,
  key_text TEXT NOT NULL DEFAULT '',
  content TEXT NOT NULL,
  signature TEXT NOT NULL,
  source_run_id TEXT NOT NULL DEFAULT '',
  improvement_id TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
)`,
		`CREATE INDEX learning_principles_agent_created ON learning_principles(project_agent_id, created_at DESC)`,
		`CREATE INDEX learning_principles_signature ON learning_principles(signature, created_at DESC)`,
		`CREATE INDEX learning_principles_quest ON learning_principles(quest_id, created_at DESC)`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLite) SaveLearningPrinciple(ctx context.Context, item domain.LearningPrinciple) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO learning_principles(
  id,workspace_id,project_agent_id,blueprint_id,quest_id,kind,key_text,content,signature,source_run_id,improvement_id,created_at
) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET
  quest_id=excluded.quest_id, content=excluded.content, source_run_id=excluded.source_run_id,
  improvement_id=excluded.improvement_id`,
		item.ID, item.WorkspaceID, item.ProjectAgentID, item.BlueprintID, item.QuestID, item.Kind, item.Key,
		item.Content, item.Signature, item.SourceRunID, item.ImprovementID, formatTime(item.CreatedAt))
	return err
}

func (s *SQLite) ListLearningPrinciplesForAgent(ctx context.Context, projectAgentID string, limit int) ([]domain.LearningPrinciple, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id,workspace_id,project_agent_id,blueprint_id,quest_id,kind,key_text,content,signature,source_run_id,improvement_id,created_at
FROM learning_principles WHERE project_agent_id=? ORDER BY created_at DESC LIMIT ?`, projectAgentID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.LearningPrinciple, 0)
	for rows.Next() {
		var item domain.LearningPrinciple
		var created string
		if err = rows.Scan(&item.ID, &item.WorkspaceID, &item.ProjectAgentID, &item.BlueprintID, &item.QuestID,
			&item.Kind, &item.Key, &item.Content, &item.Signature, &item.SourceRunID, &item.ImprovementID, &created); err != nil {
			return nil, err
		}
		item.CreatedAt = parseTime(created)
		items = append(items, item)
	}
	return items, rows.Err()
}

func marshalOptionalShadowEvaluation(eval *domain.LearningShadowEvaluation) (string, error) {
	if eval == nil {
		return "", nil
	}
	encoded, err := json.Marshal(eval)
	return string(encoded), err
}
