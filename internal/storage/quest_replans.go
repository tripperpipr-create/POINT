package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
)

func migrationQuestReplansV1(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS quest_replans (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  quest_id TEXT NOT NULL,
  flow_id TEXT NOT NULL,
  flow_run_id TEXT NOT NULL DEFAULT '',
  seq INTEGER NOT NULL,
  reason TEXT NOT NULL,
  criterion_ids TEXT NOT NULL DEFAULT '[]',
  stage_digests TEXT NOT NULL DEFAULT '[]',
  stopped_runs TEXT NOT NULL DEFAULT '[]',
  updated_nodes TEXT NOT NULL DEFAULT '[]',
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_quest_replans_quest ON quest_replans(quest_id, seq);
`)
	return err
}

func (s *SQLite) SaveQuestReplan(ctx context.Context, replan domain.QuestReplan) error {
	criteria, _ := json.Marshal(replan.CriterionIDs)
	digests, _ := json.Marshal(replan.StageDigests)
	stopped, _ := json.Marshal(replan.StoppedRuns)
	nodes, _ := json.Marshal(replan.UpdatedNodes)
	_, err := s.db.ExecContext(ctx, `
INSERT INTO quest_replans(id, workspace_id, quest_id, flow_id, flow_run_id, seq, reason, criterion_ids, stage_digests, stopped_runs, updated_nodes, created_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		replan.ID, replan.WorkspaceID, replan.QuestID, replan.FlowID, replan.FlowRunID, replan.Seq, replan.Reason,
		string(criteria), string(digests), string(stopped), string(nodes), replan.CreatedAt.UTC().Format(time.RFC3339Nano))
	return err
}

func (s *SQLite) CountQuestReplans(ctx context.Context, questID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM quest_replans WHERE quest_id=?`, questID).Scan(&count)
	return count, err
}

func (s *SQLite) ListQuestReplans(ctx context.Context, questID string) ([]domain.QuestReplan, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, workspace_id, quest_id, flow_id, flow_run_id, seq, reason, criterion_ids, stage_digests, stopped_runs, updated_nodes, created_at
FROM quest_replans WHERE quest_id=? ORDER BY seq ASC`, questID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.QuestReplan
	for rows.Next() {
		var item domain.QuestReplan
		var created string
		var criteria, digests, stopped, nodes string
		if err = rows.Scan(&item.ID, &item.WorkspaceID, &item.QuestID, &item.FlowID, &item.FlowRunID, &item.Seq, &item.Reason, &criteria, &digests, &stopped, &nodes, &created); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(criteria), &item.CriterionIDs)
		_ = json.Unmarshal([]byte(digests), &item.StageDigests)
		_ = json.Unmarshal([]byte(stopped), &item.StoppedRuns)
		_ = json.Unmarshal([]byte(nodes), &item.UpdatedNodes)
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *SQLite) LatestQuestReplanDigests(ctx context.Context, questID string) ([]string, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `
SELECT stage_digests FROM quest_replans WHERE quest_id=? ORDER BY seq DESC LIMIT 1`, questID).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var digests []string
	_ = json.Unmarshal([]byte(strings.TrimSpace(raw)), &digests)
	return digests, nil
}
