package storage

import (
	"context"
	"local-agent-workbench/internal/domain"
	"strings"
	"time"
)

func (s *SQLite) ForkMasterConversation(ctx context.Context, w, id, messageID string) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var anchor int64
	if err = tx.QueryRowContext(ctx, `SELECT rowid FROM companion_messages WHERE workspace_id=? AND conversation_id=? AND id=?`, w, id, messageID).Scan(&anchor); err != nil {
		return "", err
	}
	branch := domain.NewID("chat")
	_, err = tx.ExecContext(ctx, `INSERT INTO master_conversations(workspace_id,id,title,mode,work_mode,model,parent_id,updated_at) SELECT workspace_id,?,substr(title,1,85)||' · ветка',mode,work_mode,model,id,? FROM master_conversations WHERE workspace_id=? AND id=?`, branch, time.Now().UTC().Format(time.RFC3339Nano), w, id)
	if err != nil {
		return "", err
	}
	columns := strings.Split(strings.TrimPrefix(masterMessageColumns, "rowid,")+",search_text", ",")
	expressions := append([]string{}, columns...)
	for i, c := range columns {
		if c == "id" {
			expressions[i] = "? || id"
		}
		if c == "conversation_id" {
			expressions[i] = "?"
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO companion_messages(`+strings.Join(columns, ",")+`) SELECT `+strings.Join(expressions, ",")+` FROM companion_messages WHERE workspace_id=? AND conversation_id=? AND rowid<? ORDER BY rowid`, branch+"-", branch, w, id, anchor)
	if err != nil {
		return "", err
	}
	return branch, tx.Commit()
}
