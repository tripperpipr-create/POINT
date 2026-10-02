package storage

import (
	"context"
	"strings"
)

// Copy only conversation context; executable proposals, turns and memory identities stay in the source scope.
func (s *SQLite) CopyMasterContext(ctx context.Context, source, target, from, to, anchorID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	anchor := int64(9223372036854775807)
	if anchorID != "" {
		if err = tx.QueryRowContext(ctx, `SELECT rowid FROM companion_messages WHERE workspace_id=? AND conversation_id=? AND id=?`, source, from, anchorID).Scan(&anchor); err != nil {
			return err
		}
	}
	columns := strings.Split(strings.TrimPrefix(masterMessageColumns, "rowid,")+",search_text", ",")
	expressions := append([]string(nil), columns...)
	args := []any{}
	for i, c := range columns {
		switch c {
		case "id":
			expressions[i] = "? || id"
			args = append(args, to+"-")
		case "workspace_id":
			expressions[i] = "?"
			args = append(args, target)
		case "conversation_id":
			expressions[i] = "?"
			args = append(args, to)
		case "turn_id", "proposal_id", "action_proposal_id", "usage_record_id":
			expressions[i] = "''"
		case "memory_ids_json", "questions_json", "clarifications_json":
			expressions[i] = "'[]'"
		}
	}
	args = append(args, source, from, anchor)
	_, err = tx.ExecContext(ctx, `INSERT INTO companion_messages(`+strings.Join(columns, ",")+`) SELECT `+strings.Join(expressions, ",")+` FROM companion_messages WHERE workspace_id=? AND conversation_id=? AND rowid<? ORDER BY rowid`, args...)
	if err != nil {
		return err
	}
	return tx.Commit()
}
