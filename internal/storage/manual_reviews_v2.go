package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
)

// Human decisions on manual criteria live beside the immutable evidence
// bundle: one decision per criterion, never rewritten, so the audit shows
// exactly what the human accepted and when.
func migrationWorkOrderManualReviewsV2(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
CREATE TABLE work_order_manual_reviews_v2 (
  quest_id TEXT NOT NULL,
  criterion_id TEXT NOT NULL,
  decision TEXT NOT NULL CHECK(decision IN ('accepted','rejected')),
  note TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  PRIMARY KEY(quest_id, criterion_id)
);
CREATE TRIGGER work_order_manual_reviews_v2_no_update BEFORE UPDATE ON work_order_manual_reviews_v2 BEGIN SELECT RAISE(ABORT, 'manual reviews are immutable'); END;
CREATE TRIGGER work_order_manual_reviews_v2_no_delete BEFORE DELETE ON work_order_manual_reviews_v2 BEGIN SELECT RAISE(ABORT, 'manual reviews are immutable'); END;
`)
	return err
}

type storageQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func listManualReviewsV2(ctx context.Context, q storageQueryer, questID string) ([]domain.ManualCriterionReview, error) {
	rows, err := q.QueryContext(ctx, `SELECT criterion_id,decision,note,created_at FROM work_order_manual_reviews_v2 WHERE quest_id=? ORDER BY created_at, criterion_id`, questID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var reviews []domain.ManualCriterionReview
	for rows.Next() {
		review := domain.ManualCriterionReview{QuestID: questID}
		var created string
		if err = rows.Scan(&review.CriterionID, &review.Decision, &review.Note, &created); err != nil {
			return nil, err
		}
		review.CreatedAt = parseTime(created)
		reviews = append(reviews, review)
	}
	return reviews, rows.Err()
}

// gatedWorkOrderV2 loads the approved order a quest's evidence gate judged.
func gatedWorkOrderV2(ctx context.Context, q storageQueryer, questID string) (domain.WorkOrder, string, error) {
	var workOrderID, digest, evidenceID, raw string
	var version int
	if err := q.QueryRowContext(ctx, `SELECT work_order_id,version,digest,evidence_id FROM work_order_completion_gates_v2 WHERE quest_id=?`, questID).Scan(&workOrderID, &version, &digest, &evidenceID); err != nil {
		return domain.WorkOrder{}, "", err
	}
	if err := q.QueryRowContext(ctx, `SELECT payload_json FROM work_order_revisions_v2 WHERE id=? AND version=? AND digest=?`, workOrderID, version, digest).Scan(&raw); err != nil {
		return domain.WorkOrder{}, "", err
	}
	var order domain.WorkOrder
	if err := json.Unmarshal([]byte(raw), &order); err != nil {
		return domain.WorkOrder{}, "", err
	}
	return order, evidenceID, nil
}

// overlayManualReviewsV2 turns a stored bundle into its effective view. A
// quest without human decisions is returned untouched.
func (s *SQLite) overlayManualReviewsV2(ctx context.Context, bundle domain.EvidenceBundle) domain.EvidenceBundle {
	reviews, err := listManualReviewsV2(ctx, s.db, bundle.QuestID)
	if err != nil || len(reviews) == 0 {
		return bundle
	}
	order, _, err := gatedWorkOrderV2(ctx, s.db, bundle.QuestID)
	if err != nil {
		return bundle
	}
	effective, verdict := domain.ApplyManualReviews(order, bundle, reviews)
	if verdict.Err != nil {
		return bundle
	}
	return effective
}

// ReviewManualCriterionV2 records one human decision on a manual criterion
// and re-derives the quest verdict from the same evidence gate. The stored
// bundle stays immutable; the gate row and the quest status follow the new
// verdict, so "completed with limitations" becomes "completed" once the
// human accepts, and a rejection becomes "blocked".
func (s *SQLite) ReviewManualCriterionV2(ctx context.Context, review domain.ManualCriterionReview) (domain.QuestStatus, error) {
	if review.Decision != domain.ManualReviewAccepted && review.Decision != domain.ManualReviewRejected {
		return "", fmt.Errorf("неизвестное решение %q: нужно accepted или rejected", review.Decision)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	order, evidenceID, err := gatedWorkOrderV2(ctx, tx, review.QuestID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errors.New("квест ещё не прошёл шлюз доказательств: ручная приёмка возможна после итога")
	}
	if err != nil {
		return "", err
	}
	manual := false
	for _, criterion := range order.Criteria {
		if criterion.ID == review.CriterionID {
			manual = criterion.Kind == "manual"
			break
		}
	}
	if !manual {
		return "", fmt.Errorf("критерий %q не ручной: его доказывает проверка, а не решение человека", review.CriterionID)
	}
	if review.CreatedAt.IsZero() {
		review.CreatedAt = time.Now().UTC()
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO work_order_manual_reviews_v2(quest_id,criterion_id,decision,note,created_at) VALUES(?,?,?,?,?)`,
		review.QuestID, review.CriterionID, review.Decision, strings.TrimSpace(review.Note), formatTime(review.CreatedAt)); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return "", fmt.Errorf("решение по критерию %q уже записано", review.CriterionID)
		}
		return "", err
	}
	var raw string
	if err = tx.QueryRowContext(ctx, `SELECT payload_json FROM evidence_bundles WHERE id=?`, evidenceID).Scan(&raw); err != nil {
		return "", err
	}
	var bundle domain.EvidenceBundle
	if err = json.Unmarshal([]byte(raw), &bundle); err != nil {
		return "", err
	}
	reviews, err := listManualReviewsV2(ctx, tx, review.QuestID)
	if err != nil {
		return "", err
	}
	_, verdict := domain.ApplyManualReviews(order, bundle, reviews)
	if verdict.Err != nil {
		return "", verdict.Err
	}
	now := time.Now().UTC()
	if _, err = tx.ExecContext(ctx, `UPDATE work_order_completion_gates_v2 SET status=?,updated_at=? WHERE quest_id=?`, verdict.Status, formatTime(now), review.QuestID); err != nil {
		return "", err
	}
	if err = reconcileCompletedWorkOrderGateV2Tx(ctx, tx, review.QuestID, verdict.Status, now); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return verdict.Status, nil
}
