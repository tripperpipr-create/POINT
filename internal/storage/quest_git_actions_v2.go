package storage

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
)

// Журнал git-действий квеста: переключение ветки при утверждении, коммит
// после вердикта, отправка, MR, откат. Только дописывается: started без
// пары — оборванное действие. Пакет доказательств (неизменяемый) не трогает:
// git-действия вердикт квеста не меняют.
func migrationQuestGitActionsV2(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
CREATE TABLE quest_git_actions_v2 (
  id TEXT PRIMARY KEY,
  op_id TEXT NOT NULL,
  quest_id TEXT NOT NULL,
  work_order_id TEXT NOT NULL DEFAULT '',
  repo TEXT NOT NULL,
  action TEXT NOT NULL CHECK(action IN ('checkout','commit','push','merge_request','revert','cleanup')),
  phase TEXT NOT NULL CHECK(phase IN ('started','succeeded','failed')),
  branch TEXT NOT NULL DEFAULT '',
  base TEXT NOT NULL DEFAULT '',
  commit_id TEXT NOT NULL DEFAULT '',
  remote TEXT NOT NULL DEFAULT '',
  mr_url TEXT NOT NULL DEFAULT '',
  message TEXT NOT NULL DEFAULT '',
  error TEXT NOT NULL DEFAULT '',
  trigger TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE INDEX quest_git_actions_v2_quest ON quest_git_actions_v2(quest_id, created_at);
CREATE TRIGGER quest_git_actions_v2_no_update BEFORE UPDATE ON quest_git_actions_v2 BEGIN SELECT RAISE(ABORT, 'quest git actions are append-only'); END;
CREATE TRIGGER quest_git_actions_v2_no_delete BEFORE DELETE ON quest_git_actions_v2 BEGIN SELECT RAISE(ABORT, 'quest git actions are append-only'); END;
`)
	return err
}

func (s *SQLite) AppendQuestGitAction(ctx context.Context, action domain.QuestGitAction) (domain.QuestGitAction, error) {
	if strings.TrimSpace(action.ID) == "" {
		action.ID = domain.NewID("gitaction")
	}
	if strings.TrimSpace(action.OpID) == "" {
		action.OpID = action.ID
	}
	if action.CreatedAt.IsZero() {
		action.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO quest_git_actions_v2(id,op_id,quest_id,work_order_id,repo,action,phase,branch,base,commit_id,remote,mr_url,message,error,trigger,created_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		action.ID, action.OpID, action.QuestID, action.WorkOrderID, action.Repo, action.Action, action.Phase, action.Branch, action.Base,
		action.CommitID, action.Remote, action.MRURL, clipStorageText(action.Message, 8000), clipStorageText(action.Error, 2000), action.Trigger, formatTime(action.CreatedAt))
	return action, err
}

func (s *SQLite) ListQuestGitActions(ctx context.Context, questID string) ([]domain.QuestGitAction, error) {
	return listQuestGitActionsV2(ctx, s.db, questID)
}

func listQuestGitActionsV2(ctx context.Context, q storageQueryer, questID string) ([]domain.QuestGitAction, error) {
	rows, err := q.QueryContext(ctx, `SELECT id,op_id,quest_id,work_order_id,repo,action,phase,branch,base,commit_id,remote,mr_url,message,error,trigger,created_at
FROM quest_git_actions_v2 WHERE quest_id=? ORDER BY created_at, rowid`, questID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.QuestGitAction
	for rows.Next() {
		var item domain.QuestGitAction
		var created string
		if err = rows.Scan(&item.ID, &item.OpID, &item.QuestID, &item.WorkOrderID, &item.Repo, &item.Action, &item.Phase, &item.Branch, &item.Base,
			&item.CommitID, &item.Remote, &item.MRURL, &item.Message, &item.Error, &item.Trigger, &created); err != nil {
			return nil, err
		}
		item.CreatedAt = parseTime(created)
		out = append(out, item)
	}
	return out, rows.Err()
}

func clipStorageText(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
