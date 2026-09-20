package storage

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"local-agent-workbench/internal/domain"
)

func (s *SQLite) SaveSourceSnapshotV2(ctx context.Context, snapshot domain.SourceSnapshot) error {
	return saveSourceSnapshotV2With(ctx, s.db, snapshot)
}

func saveSourceSnapshotV2With(ctx context.Context, db sqlExecer, snapshot domain.SourceSnapshot) error {
	if err := domain.ValidateSourceSnapshot(snapshot); err != nil {
		return err
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	result, err := db.ExecContext(ctx, `INSERT OR IGNORE INTO source_snapshots_v2(id,workspace_id,kind,digest,storage_path,payload_json,created_at) VALUES(?,?,?,?,?,?,?)`,
		snapshot.ID, snapshot.WorkspaceID, snapshot.Kind, snapshot.Digest, snapshot.StoragePath, string(raw), formatTime(snapshot.CreatedAt))
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return errors.New("source snapshot id already exists")
	}
	return nil
}

func (s *SQLite) GetSourceSnapshotV2(ctx context.Context, id string) (domain.SourceSnapshot, error) {
	var raw, storagePath string
	if err := s.db.QueryRowContext(ctx, `SELECT payload_json,storage_path FROM source_snapshots_v2 WHERE id=?`, strings.TrimSpace(id)).Scan(&raw, &storagePath); err != nil {
		return domain.SourceSnapshot{}, err
	}
	var snapshot domain.SourceSnapshot
	if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
		return domain.SourceSnapshot{}, err
	}
	snapshot.StoragePath = storagePath
	return snapshot, nil
}
