package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type HealthReport struct {
	Integrity                string    `json:"integrity"`
	ForeignKeyViolations     int       `json:"foreignKeyViolations"`
	MigrationVersion         int       `json:"migrationVersion"`
	ExpectedMigrationVersion int       `json:"expectedMigrationVersion"`
	MissingMigrationVersions []int     `json:"missingMigrationVersions"`
	LastMigrationAt          time.Time `json:"lastMigrationAt,omitempty"`
}

func LatestMigrationVersion() int {
	migrations := hubMigrations()
	if len(migrations) == 0 {
		return 0
	}
	return migrations[len(migrations)-1].version
}

// Health runs integrity, foreign-key and exact migration checks against the
// already-open connection. It does not copy the active SQLite file.
func (s *SQLite) Health(ctx context.Context) (HealthReport, error) {
	report := HealthReport{ExpectedMigrationVersion: LatestMigrationVersion(), MissingMigrationVersions: []int{}}
	rows, err := s.db.QueryContext(ctx, `PRAGMA integrity_check`)
	if err != nil {
		return report, err
	}
	lines := []string{}
	for rows.Next() {
		var line string
		if err = rows.Scan(&line); err != nil {
			_ = rows.Close()
			return report, err
		}
		lines = append(lines, strings.TrimSpace(line))
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return report, err
	}
	if err = rows.Close(); err != nil {
		return report, err
	}
	report.Integrity = strings.Join(lines, "; ")
	if len(lines) != 1 || !strings.EqualFold(lines[0], "ok") {
		return report, fmt.Errorf("sqlite integrity_check: %s", report.Integrity)
	}

	foreignKeys, err := s.db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return report, err
	}
	for foreignKeys.Next() {
		report.ForeignKeyViolations++
	}
	if err = foreignKeys.Err(); err != nil {
		_ = foreignKeys.Close()
		return report, err
	}
	if err = foreignKeys.Close(); err != nil {
		return report, err
	}

	versions, err := s.MigrationVersions(ctx)
	if err != nil {
		return report, err
	}
	applied := make(map[int]bool, len(versions))
	for _, version := range versions {
		applied[version] = true
		if version > report.MigrationVersion {
			report.MigrationVersion = version
		}
	}
	for _, migration := range hubMigrations() {
		if !applied[migration.version] {
			report.MissingMigrationVersions = append(report.MissingMigrationVersions, migration.version)
		}
	}
	var appliedAt sql.NullString
	if err = s.db.QueryRowContext(ctx, `SELECT applied_at FROM schema_migrations ORDER BY version DESC LIMIT 1`).Scan(&appliedAt); err != nil && err != sql.ErrNoRows {
		return report, err
	}
	if appliedAt.Valid {
		report.LastMigrationAt, _ = time.Parse(time.RFC3339Nano, appliedAt.String)
	}
	return report, nil
}
