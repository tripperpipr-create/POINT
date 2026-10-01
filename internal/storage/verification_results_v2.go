package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
)

// Результаты проверок Point лежат неизменяемым журналом: доказательство
// приёмки может сослаться на проверку («переиспользовано из …»), и такая
// ссылка обязана вести к записи, которую никто не переписал. База общая для
// миров, поэтому запись несёт workspace_id, а поиск идёт по квесту.
func migrationVerificationResultsV2(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
CREATE TABLE verification_results_v2 (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL DEFAULT '',
  quest_id TEXT NOT NULL,
  flow_run_id TEXT NOT NULL DEFAULT '',
  execution_id TEXT NOT NULL DEFAULT '',
  run_id TEXT NOT NULL DEFAULT '',
  batch_key TEXT NOT NULL,
  tree_digest TEXT NOT NULL,
  image_digest TEXT NOT NULL DEFAULT '',
  source TEXT NOT NULL DEFAULT '',
  all_passed INTEGER NOT NULL DEFAULT 0,
  evidence_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL
);
CREATE INDEX verification_results_v2_lookup ON verification_results_v2(quest_id, batch_key, created_at DESC);
CREATE TRIGGER verification_results_v2_no_update BEFORE UPDATE ON verification_results_v2 BEGIN SELECT RAISE(ABORT, 'verification results are immutable'); END;
CREATE TRIGGER verification_results_v2_no_delete BEFORE DELETE ON verification_results_v2 BEGIN SELECT RAISE(ABORT, 'verification results are immutable'); END;
`)
	return err
}

// SaveVerificationResultV2 записывает исход прогона критериев.
func (s *SQLite) SaveVerificationResultV2(ctx context.Context, result domain.VerificationResult) error {
	if strings.TrimSpace(result.ID) == "" || strings.TrimSpace(result.QuestID) == "" || strings.TrimSpace(result.BatchKey) == "" || strings.TrimSpace(result.TreeDigest) == "" {
		return errors.New("результату проверки нужны id, квест, ключ и отпечаток дерева")
	}
	if result.CreatedAt.IsZero() {
		result.CreatedAt = time.Now().UTC()
	}
	evidence := string(result.Evidence)
	if evidence == "" {
		evidence = "{}"
	}
	passed := 0
	if result.AllPassed {
		passed = 1
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO verification_results_v2(id,workspace_id,quest_id,flow_run_id,execution_id,run_id,batch_key,tree_digest,image_digest,source,all_passed,evidence_json,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		result.ID, result.WorkspaceID, result.QuestID, result.FlowRunID, result.ExecutionID, result.RunID, result.BatchKey,
		result.TreeDigest, result.ImageDigest, result.Source, passed, evidence, formatTime(result.CreatedAt))
	return err
}

// PassedVerificationResultV2 — последний целиком прошедший прогон квеста с
// этим ключом. Провалы не возвращаются: провал всегда перепроверяется.
func (s *SQLite) PassedVerificationResultV2(ctx context.Context, questID, batchKey string) (domain.VerificationResult, bool, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id,workspace_id,flow_run_id,execution_id,run_id,tree_digest,image_digest,source,evidence_json,created_at FROM verification_results_v2 WHERE quest_id=? AND batch_key=? AND all_passed=1 ORDER BY created_at DESC LIMIT 1`, questID, batchKey)
	result := domain.VerificationResult{QuestID: questID, BatchKey: batchKey, AllPassed: true}
	var evidence, created string
	if err := row.Scan(&result.ID, &result.WorkspaceID, &result.FlowRunID, &result.ExecutionID, &result.RunID, &result.TreeDigest, &result.ImageDigest, &result.Source, &evidence, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.VerificationResult{}, false, nil
		}
		return domain.VerificationResult{}, false, err
	}
	result.Evidence = []byte(evidence)
	result.CreatedAt = parseTime(created)
	return result, true, nil
}

// VerificationResultV2 — запись по id: затвор доказательств сверяет по ней
// переиспользованную проверку.
func (s *SQLite) VerificationResultV2(ctx context.Context, id string) (domain.VerificationResult, bool, error) {
	row := s.db.QueryRowContext(ctx, `SELECT workspace_id,quest_id,flow_run_id,execution_id,run_id,batch_key,tree_digest,image_digest,source,all_passed,evidence_json,created_at FROM verification_results_v2 WHERE id=?`, id)
	result := domain.VerificationResult{ID: id}
	var evidence, created string
	var passed int
	if err := row.Scan(&result.WorkspaceID, &result.QuestID, &result.FlowRunID, &result.ExecutionID, &result.RunID, &result.BatchKey, &result.TreeDigest, &result.ImageDigest, &result.Source, &passed, &evidence, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.VerificationResult{}, false, nil
		}
		return domain.VerificationResult{}, false, err
	}
	result.AllPassed = passed == 1
	result.Evidence = []byte(evidence)
	result.CreatedAt = parseTime(created)
	return result, true, nil
}

// latestPreAcceptCheckV2 — сводка последней проверки Point перед приёмкой в
// этом прогоне Flow. Считаются только проверки, которые запускались: ручные и
// отложенные критерии команды не имеют.
func (s *SQLite) latestPreAcceptCheckV2(ctx context.Context, flowRunID string) *domain.WorkOrderPreAcceptCheck {
	if strings.TrimSpace(flowRunID) == "" {
		return nil
	}
	row := s.db.QueryRowContext(ctx, `SELECT all_passed,evidence_json,created_at FROM verification_results_v2 WHERE flow_run_id=? AND source='pre_accept' ORDER BY created_at DESC LIMIT 1`, flowRunID)
	var passed int
	var evidence, created string
	if err := row.Scan(&passed, &evidence, &created); err != nil {
		return nil
	}
	var payload struct {
		Criteria []struct {
			Status string           `json:"status"`
			Check  *json.RawMessage `json:"check"`
		} `json:"criteria"`
	}
	_ = json.Unmarshal([]byte(evidence), &payload)
	check := &domain.WorkOrderPreAcceptCheck{AllPassed: passed == 1, At: parseTime(created)}
	for _, criterion := range payload.Criteria {
		if criterion.Check == nil || (criterion.Status != "satisfied" && criterion.Status != "failed") {
			continue
		}
		check.Total++
		if criterion.Status == "satisfied" {
			check.Passed++
		}
	}
	return check
}
