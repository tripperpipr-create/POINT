package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
)

// DatabaseNeedsMigration inspects an existing database without mutating it.
// A missing or empty file is a fresh installation and does not need a recovery
// point. Any difference from the exact compiled migration set does.
func DatabaseNeedsMigration(ctx context.Context, path string) (bool, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("database is not a regular file: %s", path)
	}
	if info.Size() == 0 {
		return false, nil
	}

	db, err := sql.Open("sqlite", readOnlyDSN(path))
	if err != nil {
		return false, err
	}
	db.SetMaxOpenConns(1)
	defer db.Close()

	var tableCount int
	if err = db.QueryRowContext(ctx, `SELECT COUNT(1) FROM sqlite_master WHERE type='table' AND name='schema_migrations'`).Scan(&tableCount); err != nil {
		return false, fmt.Errorf("inspect migration table: %w", err)
	}
	if tableCount == 0 {
		return true, nil
	}
	rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		return false, fmt.Errorf("read migration versions: %w", err)
	}
	defer rows.Close()
	applied := map[int]bool{}
	for rows.Next() {
		var version int
		if err = rows.Scan(&version); err != nil {
			return false, err
		}
		applied[version] = true
	}
	if err = rows.Err(); err != nil {
		return false, err
	}
	expected := hubMigrations()
	if len(applied) != len(expected) {
		return true, nil
	}
	for _, migration := range expected {
		if !applied[migration.version] {
			return true, nil
		}
	}
	return false, nil
}
