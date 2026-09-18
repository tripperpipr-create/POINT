package storage

import (
	"context"

	"local-agent-workbench/internal/domain"
)

func (s *SQLite) SaveLearningSignal(ctx context.Context, signal domain.LearningSignal) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO learning_signals(
  id,workspace_id,project_agent_id,blueprint_id,run_id,kind,status,summary,
  evidence_json,skill_attributions_json,created_at,updated_at
) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(run_id,kind) DO UPDATE SET
  status=excluded.status,summary=excluded.summary,evidence_json=excluded.evidence_json,
  skill_attributions_json=excluded.skill_attributions_json,updated_at=excluded.updated_at`,
		signal.ID, signal.WorkspaceID, signal.ProjectAgentID, signal.BlueprintID, signal.RunID,
		signal.Kind, signal.Status, signal.Summary, marshalJSON(signal.Evidence),
		marshalJSON(signal.SkillAttributions), formatTime(signal.CreatedAt), formatTime(signal.UpdatedAt))
	return err
}

func (s *SQLite) ListLearningSignals(ctx context.Context, workspaceID string, limit int) ([]domain.LearningSignal, error) {
	return s.listLearningSignals(ctx, ` WHERE workspace_id=? ORDER BY updated_at DESC LIMIT ?`, workspaceID, boundedLearningLimit(limit))
}

func (s *SQLite) ListLearningSignalsForRun(ctx context.Context, runID string) ([]domain.LearningSignal, error) {
	return s.listLearningSignals(ctx, ` WHERE run_id=? ORDER BY kind`, runID)
}

func (s *SQLite) ListLearningSignalsForBlueprint(ctx context.Context, blueprintID string, limit int) ([]domain.LearningSignal, error) {
	return s.listLearningSignals(ctx, ` WHERE blueprint_id=? ORDER BY updated_at DESC LIMIT ?`, blueprintID, boundedLearningLimit(limit))
}

func (s *SQLite) listLearningSignals(ctx context.Context, suffix string, args ...any) ([]domain.LearningSignal, error) {
	rows, err := s.db.QueryContext(ctx, learningSignalSelect+suffix, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.LearningSignal, 0)
	for rows.Next() {
		item, scanErr := scanLearningSignal(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

const learningSignalSelect = `SELECT id,workspace_id,project_agent_id,blueprint_id,run_id,kind,status,summary,
evidence_json,skill_attributions_json,created_at,updated_at FROM learning_signals`

func scanLearningSignal(row scanner) (domain.LearningSignal, error) {
	var item domain.LearningSignal
	var evidence, attributions, created, updated string
	if err := row.Scan(&item.ID, &item.WorkspaceID, &item.ProjectAgentID, &item.BlueprintID,
		&item.RunID, &item.Kind, &item.Status, &item.Summary, &evidence, &attributions,
		&created, &updated); err != nil {
		return item, err
	}
	unmarshalJSON(evidence, &item.Evidence)
	unmarshalJSON(attributions, &item.SkillAttributions)
	if item.Evidence == nil {
		item.Evidence = []string{}
	}
	if item.SkillAttributions == nil {
		item.SkillAttributions = []domain.SkillAttribution{}
	}
	item.CreatedAt, item.UpdatedAt = parseTime(created), parseTime(updated)
	return item, nil
}

func (s *SQLite) SaveSkillOutcome(ctx context.Context, outcome domain.SkillOutcome) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO skill_outcomes(
  id,workspace_id,project_agent_id,blueprint_id,run_id,skill_id,skill_name,skill_revision,
  skill_digest,promotion_status,run_status,health,tool_calls,tool_failures,approval_denied,
  feedback_count,completion_revisions,verification_required,verification_recorded,created_at
) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(run_id,skill_id) DO UPDATE SET
  skill_name=excluded.skill_name,skill_revision=excluded.skill_revision,skill_digest=excluded.skill_digest,
  promotion_status=excluded.promotion_status,run_status=excluded.run_status,health=excluded.health,
  tool_calls=excluded.tool_calls,tool_failures=excluded.tool_failures,approval_denied=excluded.approval_denied,
  feedback_count=excluded.feedback_count,completion_revisions=excluded.completion_revisions,
  verification_required=excluded.verification_required,verification_recorded=excluded.verification_recorded`,
		outcome.ID, outcome.WorkspaceID, outcome.ProjectAgentID, outcome.BlueprintID, outcome.RunID,
		outcome.SkillID, outcome.SkillName, outcome.SkillRevision, outcome.SkillDigest, outcome.PromotionStatus,
		outcome.RunStatus, outcome.Health, outcome.ToolCalls, outcome.ToolFailures, outcome.ApprovalDenied,
		outcome.FeedbackCount, outcome.CompletionRevisions, learningBoolInt(outcome.VerificationRequired),
		learningBoolInt(outcome.VerificationRecorded), formatTime(outcome.CreatedAt))
	return err
}

func (s *SQLite) ListSkillOutcomes(ctx context.Context, workspaceID string, limit int) ([]domain.SkillOutcome, error) {
	return s.listSkillOutcomes(ctx, ` WHERE workspace_id=? ORDER BY created_at DESC LIMIT ?`, workspaceID, boundedLearningLimit(limit))
}

func (s *SQLite) ListAllSkillOutcomes(ctx context.Context, limit int) ([]domain.SkillOutcome, error) {
	return s.listSkillOutcomes(ctx, ` ORDER BY created_at DESC LIMIT ?`, boundedLearningLimit(limit))
}

func (s *SQLite) ListSkillOutcomesForSkill(ctx context.Context, skillID string, limit int) ([]domain.SkillOutcome, error) {
	return s.listSkillOutcomes(ctx, ` WHERE skill_id=? ORDER BY created_at DESC LIMIT ?`, skillID, boundedLearningLimit(limit))
}

func (s *SQLite) ListSkillOutcomesForRun(ctx context.Context, runID string) ([]domain.SkillOutcome, error) {
	return s.listSkillOutcomes(ctx, ` WHERE run_id=? ORDER BY skill_id`, runID)
}

func (s *SQLite) listSkillOutcomes(ctx context.Context, suffix string, args ...any) ([]domain.SkillOutcome, error) {
	rows, err := s.db.QueryContext(ctx, skillOutcomeSelect+suffix, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.SkillOutcome, 0)
	for rows.Next() {
		item, scanErr := scanSkillOutcome(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

const skillOutcomeSelect = `SELECT id,workspace_id,project_agent_id,blueprint_id,run_id,skill_id,skill_name,
skill_revision,skill_digest,promotion_status,run_status,health,tool_calls,tool_failures,approval_denied,
feedback_count,completion_revisions,verification_required,verification_recorded,created_at FROM skill_outcomes`

func scanSkillOutcome(row scanner) (domain.SkillOutcome, error) {
	var item domain.SkillOutcome
	var required, recorded int
	var created string
	if err := row.Scan(&item.ID, &item.WorkspaceID, &item.ProjectAgentID, &item.BlueprintID,
		&item.RunID, &item.SkillID, &item.SkillName, &item.SkillRevision, &item.SkillDigest,
		&item.PromotionStatus, &item.RunStatus, &item.Health, &item.ToolCalls, &item.ToolFailures,
		&item.ApprovalDenied, &item.FeedbackCount, &item.CompletionRevisions, &required, &recorded,
		&created); err != nil {
		return item, err
	}
	item.VerificationRequired = required != 0
	item.VerificationRecorded = recorded != 0
	item.CreatedAt = parseTime(created)
	return item, nil
}

func boundedLearningLimit(limit int) int {
	if limit <= 0 || limit > 1000 {
		return 100
	}
	return limit
}

func learningBoolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
