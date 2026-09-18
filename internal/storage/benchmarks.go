package storage

import (
	"context"

	"local-agent-workbench/internal/domain"
)

func (s *SQLite) SaveAgentBenchmarkSet(ctx context.Context, set domain.AgentBenchmarkSet) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO agent_benchmark_sets(id,workspace_id,project_agent_id,skill_id,name,description,revision,digest,cases_json,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET project_agent_id=excluded.project_agent_id,skill_id=excluded.skill_id,
  name=excluded.name,description=excluded.description,revision=excluded.revision,digest=excluded.digest,
  cases_json=excluded.cases_json,updated_at=excluded.updated_at`,
		set.ID, set.WorkspaceID, set.ProjectAgentID, set.SkillID, set.Name, set.Description,
		set.Revision, set.Digest, marshalJSON(set.Cases), formatTime(set.CreatedAt), formatTime(set.UpdatedAt))
	return err
}

func (s *SQLite) GetAgentBenchmarkSet(ctx context.Context, id string) (domain.AgentBenchmarkSet, error) {
	return scanAgentBenchmarkSet(s.db.QueryRowContext(ctx, agentBenchmarkSetSelect+` WHERE id=?`, id))
}

func (s *SQLite) ListAgentBenchmarkSets(ctx context.Context, workspaceID string) ([]domain.AgentBenchmarkSet, error) {
	rows, err := s.db.QueryContext(ctx, agentBenchmarkSetSelect+` WHERE workspace_id=? ORDER BY updated_at DESC`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.AgentBenchmarkSet, 0)
	for rows.Next() {
		item, scanErr := scanAgentBenchmarkSet(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

const agentBenchmarkSetSelect = `SELECT id,workspace_id,project_agent_id,skill_id,name,description,revision,digest,cases_json,created_at,updated_at FROM agent_benchmark_sets`

func scanAgentBenchmarkSet(row scanner) (domain.AgentBenchmarkSet, error) {
	var item domain.AgentBenchmarkSet
	var cases, created, updated string
	if err := row.Scan(&item.ID, &item.WorkspaceID, &item.ProjectAgentID, &item.SkillID, &item.Name,
		&item.Description, &item.Revision, &item.Digest, &cases, &created, &updated); err != nil {
		return item, err
	}
	unmarshalJSON(cases, &item.Cases)
	if item.Cases == nil {
		item.Cases = []domain.AgentBenchmarkCase{}
	}
	item.CreatedAt, item.UpdatedAt = parseTime(created), parseTime(updated)
	return item, nil
}

func (s *SQLite) SaveAgentBenchmarkEvaluation(ctx context.Context, evaluation domain.AgentBenchmarkEvaluation) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO agent_benchmark_evaluations(id,workspace_id,project_agent_id,benchmark_set_id,set_revision,set_digest,label,cases_json,metrics_json,created_at)
VALUES(?,?,?,?,?,?,?,?,?,?)`, evaluation.ID, evaluation.WorkspaceID, evaluation.ProjectAgentID,
		evaluation.BenchmarkSetID, evaluation.SetRevision, evaluation.SetDigest, evaluation.Label,
		marshalJSON(evaluation.Cases), marshalJSON(evaluation.Metrics), formatTime(evaluation.CreatedAt))
	return err
}

func (s *SQLite) GetAgentBenchmarkEvaluation(ctx context.Context, id string) (domain.AgentBenchmarkEvaluation, error) {
	return scanAgentBenchmarkEvaluation(s.db.QueryRowContext(ctx, agentBenchmarkEvaluationSelect+` WHERE id=?`, id))
}

func (s *SQLite) ListAgentBenchmarkEvaluations(ctx context.Context, workspaceID string, limit int) ([]domain.AgentBenchmarkEvaluation, error) {
	rows, err := s.db.QueryContext(ctx, agentBenchmarkEvaluationSelect+` WHERE workspace_id=? ORDER BY created_at DESC LIMIT ?`, workspaceID, boundedLearningLimit(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.AgentBenchmarkEvaluation, 0)
	for rows.Next() {
		item, scanErr := scanAgentBenchmarkEvaluation(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

const agentBenchmarkEvaluationSelect = `SELECT id,workspace_id,project_agent_id,benchmark_set_id,set_revision,set_digest,label,cases_json,metrics_json,created_at FROM agent_benchmark_evaluations`

func scanAgentBenchmarkEvaluation(row scanner) (domain.AgentBenchmarkEvaluation, error) {
	var item domain.AgentBenchmarkEvaluation
	var cases, metrics, created string
	if err := row.Scan(&item.ID, &item.WorkspaceID, &item.ProjectAgentID, &item.BenchmarkSetID,
		&item.SetRevision, &item.SetDigest, &item.Label, &cases, &metrics, &created); err != nil {
		return item, err
	}
	unmarshalJSON(cases, &item.Cases)
	unmarshalJSON(metrics, &item.Metrics)
	if item.Cases == nil {
		item.Cases = []domain.AgentBenchmarkCaseOutcome{}
	}
	item.CreatedAt = parseTime(created)
	return item, nil
}
