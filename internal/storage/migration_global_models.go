package storage

import (
	"context"
	"database/sql"
)

func migrationOrchestratorProjectModelOverrideV1(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `ALTER TABLE orchestrator_config ADD COLUMN project_model_override INTEGER NOT NULL DEFAULT 0`)
	return err
}
