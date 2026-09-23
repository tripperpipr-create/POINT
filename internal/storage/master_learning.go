package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"local-agent-workbench/internal/domain"
)

func migrationMasterSkillsLearningV1(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS master_skill_revisions(id TEXT PRIMARY KEY, skill_id TEXT NOT NULL, workspace_id TEXT NOT NULL DEFAULT '', status TEXT NOT NULL, created_at TEXT NOT NULL, payload TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS master_skill_scope ON master_skill_revisions(skill_id,workspace_id,status);
CREATE TABLE IF NOT EXISTS master_operations(id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, phase TEXT NOT NULL, turn_id TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, tokens INTEGER NOT NULL, eligible INTEGER NOT NULL, payload TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS master_operations_scope ON master_operations(workspace_id,phase,created_at);
CREATE TABLE IF NOT EXISTS master_learning_jobs(id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, status TEXT NOT NULL, payload TEXT NOT NULL);
CREATE UNIQUE INDEX IF NOT EXISTS master_learning_single_worker ON master_learning_jobs(status) WHERE status='running';
CREATE TABLE IF NOT EXISTS master_learning_samples(operation_id TEXT NOT NULL, skill_id TEXT NOT NULL, job_id TEXT NOT NULL, PRIMARY KEY(operation_id,skill_id));
CREATE TABLE IF NOT EXISTS master_learning_config(workspace_id TEXT PRIMARY KEY, enabled INTEGER NOT NULL DEFAULT 1);
CREATE TABLE IF NOT EXISTS master_learning_spend(id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, created_at TEXT NOT NULL, reserved INTEGER NOT NULL, spent INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS master_skill_trials(revision_id TEXT NOT NULL, workspace_id TEXT NOT NULL, status TEXT NOT NULL, PRIMARY KEY(revision_id,workspace_id));
CREATE TABLE IF NOT EXISTS master_evidence_signals(id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, proposal_id TEXT NOT NULL, quest_id TEXT NOT NULL, payload TEXT NOT NULL);
`)
	return err
}

func (s *SQLite) SeedMasterSkills(ctx context.Context, skills []domain.SkillDefinition) error {
	for _, skill := range skills {
		a := domain.SkillDefinitionAttribution(skill)
		r := domain.MasterSkillRevision{ID: skill.ID + "-" + a.Digest, Skill: skill, Digest: a.Digest, Status: "builtin", CreatedAt: time.Now().UTC()}
		if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO master_skill_revisions VALUES(?,?,?,?,?,?)`, r.ID, skill.ID, "", r.Status, formatTime(r.CreatedAt), marshalJSON(r)); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLite) MasterSkillRevisions(ctx context.Context) ([]domain.MasterSkillRevision, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT payload FROM master_skill_revisions ORDER BY created_at,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.MasterSkillRevision
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var v domain.MasterSkillRevision
		if err = json.Unmarshal([]byte(raw), &v); err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, rows.Err()
}

func (s *SQLite) SaveMasterRevision(ctx context.Context, v domain.MasterSkillRevision) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO master_skill_revisions VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET status=excluded.status,payload=json_set(master_skill_revisions.payload,'$.status',excluded.status,'$.reason',json_extract(excluded.payload,'$.reason')) WHERE master_skill_revisions.status NOT IN ('builtin','rolled_back','rejected')`, v.ID, v.Skill.ID, v.WorkspaceID, v.Status, formatTime(v.CreatedAt), marshalJSON(v))
	return err
}

func (s *SQLite) RollbackMasterRevision(ctx context.Context, id, reason string) error {
	_, err := s.db.ExecContext(ctx, `WITH RECURSIVE descendants(id) AS (SELECT id FROM master_skill_revisions WHERE id=? AND status!='builtin' UNION SELECT r.id FROM master_skill_revisions r JOIN descendants d ON json_extract(r.payload,'$.parentId')=d.id) UPDATE master_skill_revisions SET status='rolled_back',payload=json_set(payload,'$.status','rolled_back','$.reason',?) WHERE id IN (SELECT id FROM descendants)`, id, reason)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE master_learning_jobs SET status='rolled_back',payload=json_set(payload,'$.status','rolled_back','$.reason',?) WHERE json_extract(payload,'$.candidateId') IN(SELECT id FROM master_skill_revisions WHERE status='rolled_back')`, reason)
	return err
}

func (s *SQLite) MasterLearningConfig(ctx context.Context, ws string) (domain.MasterLearningConfig, error) {
	v := domain.MasterLearningConfig{Enabled: true}
	err := s.db.QueryRowContext(ctx, `SELECT enabled FROM master_learning_config WHERE workspace_id=?`, ws).Scan(&v.Enabled)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return v, err
}
func (s *SQLite) SetMasterLearningConfig(ctx context.Context, ws string, v domain.MasterLearningConfig) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO master_learning_config VALUES(?,?) ON CONFLICT(workspace_id) DO UPDATE SET enabled=excluded.enabled`, ws, v.Enabled)
	return err
}

func (s *SQLite) SaveMasterOperation(ctx context.Context, v domain.MasterOperation) error {
	eligible := !v.ProviderError && len(v.Skills) > 0 && v.Replay != ""
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO master_operations VALUES(?,?,?,?,?,?,?,?)`, v.ID, v.WorkspaceID, v.Phase, v.TurnID, formatTime(v.CreatedAt), v.InputTokens+v.OutputTokens, eligible, marshalJSON(v))
	return err
}
func (s *SQLite) MasterOperations(ctx context.Context, ws string) ([]domain.MasterOperation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT payload FROM master_operations WHERE workspace_id=? ORDER BY created_at DESC LIMIT 300`, ws)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.MasterOperation
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var v domain.MasterOperation
		if err = json.Unmarshal([]byte(raw), &v); err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, rows.Err()
}
func (s *SQLite) MasterLearningJobs(ctx context.Context, ws string) ([]domain.MasterLearningJob, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT payload FROM master_learning_jobs WHERE workspace_id=? ORDER BY rowid DESC LIMIT 100`, ws)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.MasterLearningJob
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var v domain.MasterLearningJob
		if err = json.Unmarshal([]byte(raw), &v); err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, rows.Err()
}
func (s *SQLite) SaveMasterLearningJob(ctx context.Context, v domain.MasterLearningJob) error {
	v.UpdatedAt = time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `UPDATE master_learning_jobs SET status=?,payload=? WHERE id=? AND workspace_id=? AND status NOT IN ('rolled_back','rejected')`, v.Status, marshalJSON(v), v.ID, v.WorkspaceID)
	return err
}

// Queue and claim are transactions: concurrent turns cannot consume the same
// evidence twice or start two evaluations. No model calls inside transactions.
func (s *SQLite) QueueMasterLearning(ctx context.Context, ws, phase, skillID, baselineID string, candidates ...string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var active int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM master_learning_jobs WHERE workspace_id=? AND status IN ('queued','deferred','running','canary') AND json_extract(payload,'$.skillId')=?`, ws, skillID).Scan(&active); err != nil {
		return err
	}
	if active > 0 {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM master_operations o WHERE workspace_id=? AND phase=? AND eligible=1 AND EXISTS(SELECT 1 FROM json_each(o.payload,'$.skills') sk WHERE json_extract(sk.value,'$.skillId')=?) AND NOT EXISTS(SELECT 1 FROM master_learning_samples s WHERE s.operation_id=o.id AND s.skill_id=?) ORDER BY created_at LIMIT 3`, ws, phase, skillID, skillID)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(ids) < 3 {
		return nil
	}
	v := domain.MasterLearningJob{ID: domain.NewID("master-learning"), WorkspaceID: ws, Phase: phase, SkillID: skillID, BaselineID: baselineID, ExampleIDs: ids, Status: "queued", UpdatedAt: time.Now().UTC()}
	if len(candidates) > 0 {
		v.CandidateID = candidates[0]
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO master_learning_jobs VALUES(?,?,?,?)`, v.ID, ws, v.Status, marshalJSON(v)); err != nil {
		return err
	}
	for _, id := range ids {
		if _, err = tx.ExecContext(ctx, `INSERT INTO master_learning_samples VALUES(?,?,?)`, id, skillID, v.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *SQLite) ClaimMasterLearning(ctx context.Context, ws string) (domain.MasterLearningJob, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.MasterLearningJob{}, err
	}
	defer tx.Rollback()
	var raw string
	err = tx.QueryRowContext(ctx, `SELECT payload FROM master_learning_jobs WHERE workspace_id=? AND status IN ('queued','deferred') AND NOT EXISTS(SELECT 1 FROM master_learning_jobs WHERE status='running') ORDER BY rowid LIMIT 1`, ws).Scan(&raw)
	if err != nil {
		return domain.MasterLearningJob{}, err
	}
	var v domain.MasterLearningJob
	if err = json.Unmarshal([]byte(raw), &v); err != nil {
		return v, err
	}
	v.Status = "running"
	if _, err = tx.ExecContext(ctx, `UPDATE master_learning_jobs SET status='running',payload=? WHERE id=?`, marshalJSON(v), v.ID); err != nil {
		return v, err
	}
	return v, tx.Commit()
}

func (s *SQLite) RecoverMasterLearning(ctx context.Context) error {
	// A crash may occur after provider billing but before usage arrives. Charge
	// the full reservation; releasing it would allow spending the budget twice.
	_, err := s.db.ExecContext(ctx, `UPDATE master_learning_spend SET spent=spent+reserved,reserved=0 WHERE reserved>0;
UPDATE master_learning_jobs SET status='deferred',payload=json_set(payload,'$.status','deferred','$.reason','Возобновление после перезапуска; ожидается авторизация модели') WHERE status='running';`)
	return err
}

type masterBudgetQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func masterBudget(ctx context.Context, q masterBudgetQuery, ws string) (domain.MasterLearningBudget, error) {
	var v domain.MasterLearningBudget
	since := formatTime(time.Now().UTC().AddDate(0, 0, -30))
	err := q.QueryRowContext(ctx, `SELECT COALESCE(SUM(tokens),0) FROM master_operations WHERE workspace_id=? AND created_at>=?`, ws, since).Scan(&v.MainTokens)
	if err != nil {
		return v, err
	}
	v.LimitTokens = v.MainTokens / 10
	err = q.QueryRowContext(ctx, `SELECT COALESCE(SUM(spent),0),COALESCE(SUM(reserved),0) FROM master_learning_spend WHERE workspace_id=? AND (created_at>=? OR reserved>0)`, ws, since).Scan(&v.SpentTokens, &v.ReservedTokens)
	return v, err
}
func (s *SQLite) MasterLearningBudget(ctx context.Context, ws string) (domain.MasterLearningBudget, error) {
	return masterBudget(ctx, s.db, ws)
}
func (s *SQLite) ReserveMasterLearning(ctx context.Context, ws string, tokens int64) (string, error) {
	if tokens <= 0 {
		return "", fmt.Errorf("invalid learning reservation")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	v, err := masterBudget(ctx, tx, ws)
	if err != nil {
		return "", err
	}
	if v.SpentTokens+v.ReservedTokens+tokens > v.LimitTokens {
		return "", fmt.Errorf("недостаточно бюджета обучения: доступно %d, нужно %d токенов", max(0, v.LimitTokens-v.SpentTokens-v.ReservedTokens), tokens)
	}
	id := domain.NewID("master-reserve")
	_, err = tx.ExecContext(ctx, `INSERT INTO master_learning_spend VALUES(?,?,?,?,0)`, id, ws, formatTime(time.Now().UTC()), tokens)
	if err != nil {
		return "", err
	}
	return id, tx.Commit()
}
func (s *SQLite) SettleMasterLearning(ctx context.Context, id string, actual int64) error {
	// Unknown usage is charged conservatively. Reservations are never negative.
	_, err := s.db.ExecContext(ctx, `UPDATE master_learning_spend SET spent=CASE WHEN ?>0 THEN MAX(?,spent) ELSE spent+reserved END,reserved=0 WHERE id=? AND reserved>0`, actual, actual, id)
	return err
}

func (s *SQLite) SetMasterOperationFeedback(ctx context.Context, ws, messageID, value string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE master_operations SET payload=json_set(payload,'$.feedback',?) WHERE workspace_id=? AND turn_id IN(SELECT turn_id FROM companion_messages WHERE id=? AND workspace_id=? AND speaker='master')`, value, ws, messageID, ws)
	return err
}

func (s *SQLite) SetMasterSkillTrial(ctx context.Context, id, ws, status string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO master_skill_trials VALUES(?,?,?) ON CONFLICT(revision_id,workspace_id) DO UPDATE SET status=excluded.status`, id, ws, status)
	return err
}
func (s *SQLite) MasterSkillTrials(ctx context.Context, ws string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT revision_id,status FROM master_skill_trials WHERE workspace_id=?`, ws)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]string{}
	for rows.Next() {
		var id, status string
		if err = rows.Scan(&id, &status); err != nil {
			return nil, err
		}
		result[id] = status
	}
	return result, rows.Err()
}
func (s *SQLite) MasterSkillConfirmations(ctx context.Context, id string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT workspace_id) FROM master_skill_trials WHERE revision_id=? AND status='passed'`, id).Scan(&n)
	return n, err
}

func (s *SQLite) MasterLearningExamples(ctx context.Context, ws string, ids []string) ([]domain.MasterOperation, error) {
	var result []domain.MasterOperation
	for _, id := range ids {
		var raw string
		if err := s.db.QueryRowContext(ctx, `SELECT payload FROM master_operations WHERE workspace_id=? AND id=?`, ws, id).Scan(&raw); err != nil {
			return nil, err
		}
		var op domain.MasterOperation
		if err := json.Unmarshal([]byte(raw), &op); err != nil {
			return nil, err
		}
		result = append(result, op)
	}
	return result, nil
}

func (s *SQLite) MasterTurnSkills(ctx context.Context, ws, turn string) ([]domain.SkillAttribution, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT payload FROM master_operations WHERE workspace_id=? AND turn_id=?`, ws, turn)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.SkillAttribution
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var op domain.MasterOperation
		if err = json.Unmarshal([]byte(raw), &op); err != nil {
			return nil, err
		}
		result = append(result, op.Skills...)
	}
	return result, rows.Err()
}

func (s *SQLite) UpdateMasterCandidateJobs(ctx context.Context, id, status, reason string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE master_learning_jobs SET status=?,payload=json_set(payload,'$.status',?,'$.reason',?) WHERE json_extract(payload,'$.candidateId')=? AND status NOT IN ('rejected','rolled_back')`, status, status, reason, id)
	return err
}
