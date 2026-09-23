package storage

import (
	"context"
	"database/sql"
	"errors"
	"local-agent-workbench/internal/domain"
)

// The predicate is an internal constant, never user-supplied SQL. Keep usage
// accounting while erasing inputs, provenance and eligibility on explicit purge.
func forgetMasterExamplesTx(ctx context.Context, tx *sql.Tx, predicate string, args ...any) error {
	selected := `SELECT id FROM master_operations WHERE ` + predicate
	if _, err := tx.ExecContext(ctx, `UPDATE master_learning_jobs SET status='rejected',payload=json_set(payload,'$.status','rejected','$.reason','Исходные примеры удалены пользователем') WHERE status IN ('queued','deferred','running') AND id IN (SELECT job_id FROM master_learning_samples WHERE operation_id IN (`+selected+`))`, args...); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM master_evidence_signals WHERE proposal_id!='' AND EXISTS(SELECT 1 FROM master_operations WHERE `+predicate+` AND json_extract(payload,'$.proposalId')=master_evidence_signals.proposal_id AND workspace_id=master_evidence_signals.workspace_id)`, args...); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE master_operations SET eligible=0,turn_id='',payload=json_remove(payload,'$.replay','$.skills','$.turnId','$.proposalId','$.questId','$.flowId') WHERE `+predicate, args...)
	return err
}

// Compare-and-activate keeps rollback, deleting source examples and a finishing
// background worker atomic. A stale worker can never resurrect a withdrawn job.
func (s *SQLite) ActivateMasterSkillTrial(ctx context.Context, job domain.MasterLearningJob, r domain.MasterSkillRevision) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var allowed int
	err = tx.QueryRowContext(ctx, `SELECT count(*) FROM master_learning_jobs j JOIN master_skill_revisions r ON r.id=? JOIN master_skill_revisions b ON b.id=? WHERE j.id=? AND j.workspace_id=? AND j.status='running' AND r.status NOT IN ('rolled_back','rejected') AND b.status NOT IN ('rolled_back','rejected') AND NOT EXISTS(SELECT 1 FROM master_learning_config WHERE workspace_id=? AND enabled=0)`, r.ID, job.BaselineID, job.ID, job.WorkspaceID, job.WorkspaceID).Scan(&allowed)
	if err != nil {
		return err
	}
	if allowed != 1 {
		return errors.New("проверка отозвана или обучение выключено")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE master_skill_revisions SET status=?,payload=? WHERE id=?`, r.Status, marshalJSON(r), r.ID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO master_skill_trials VALUES(?,?,'canary') ON CONFLICT(revision_id,workspace_id) DO UPDATE SET status='canary'`, r.ID, job.WorkspaceID); err != nil {
		return err
	}
	return tx.Commit()
}
