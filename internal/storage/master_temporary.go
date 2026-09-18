package storage

import "context"

// Temporary conversations expire with the core session, including after a crash.
func (s *SQLite) PurgeTemporaryMasterConversations(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, table := range []string{"master_turn_events", "master_turns", "companion_messages"} {
		_, err = tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE EXISTS (SELECT 1 FROM master_conversations c WHERE c.workspace_id=`+table+`.workspace_id AND c.id=`+table+`.conversation_id AND c.temporary=1)`)
		if err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM master_conversations WHERE temporary=1`); err != nil {
		return err
	}
	return tx.Commit()
}
