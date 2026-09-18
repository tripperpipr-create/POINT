package storage

import (
	"context"
	"errors"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
)

// RecordCompatibilityUsage increments one version-attributed aggregate. The
// store intentionally accepts no free-form details so paths, prompts, tool
// arguments, agent identifiers, and secrets cannot leak into this telemetry.
func (s *SQLite) RecordCompatibilityUsage(ctx context.Context, usage domain.CompatibilityUsage) error {
	usage.Feature = domain.CompatibilityFeature(strings.TrimSpace(string(usage.Feature)))
	usage.WorkspaceID = strings.TrimSpace(usage.WorkspaceID)
	usage.ApplicationVersion = strings.TrimSpace(usage.ApplicationVersion)
	usage.LegacyVersion = strings.TrimSpace(usage.LegacyVersion)
	if usage.Feature == "" || usage.ApplicationVersion == "" || usage.LegacyVersion == "" {
		return errors.New("compatibility feature, application version, and legacy version are required")
	}
	if len(usage.Feature) > 100 || len(usage.WorkspaceID) > 200 || len(usage.ApplicationVersion) > 50 || len(usage.LegacyVersion) > 100 {
		return errors.New("compatibility usage field exceeds its limit")
	}
	now := usage.LastSeen.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	first := usage.FirstSeen.UTC()
	if first.IsZero() {
		first = now
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO compatibility_usage(feature,workspace_id,application_version,legacy_version,count,first_seen,last_seen)
VALUES(?,?,?,?,1,?,?)
ON CONFLICT(feature,workspace_id,application_version,legacy_version) DO UPDATE SET
  count=compatibility_usage.count+1,
  first_seen=CASE WHEN excluded.first_seen < compatibility_usage.first_seen THEN excluded.first_seen ELSE compatibility_usage.first_seen END,
  last_seen=CASE WHEN excluded.last_seen > compatibility_usage.last_seen THEN excluded.last_seen ELSE compatibility_usage.last_seen END`,
		usage.Feature, usage.WorkspaceID, usage.ApplicationVersion, usage.LegacyVersion, formatTime(first), formatTime(now))
	return err
}

// ListCompatibilityUsage returns current-workspace and application-global
// aggregates. A workspace can never observe another workspace's evidence.
func (s *SQLite) ListCompatibilityUsage(ctx context.Context, workspaceID string) ([]domain.CompatibilityUsage, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	query := `
SELECT feature,workspace_id,application_version,legacy_version,count,first_seen,last_seen
FROM compatibility_usage WHERE workspace_id='' ORDER BY last_seen DESC, feature, application_version`
	args := []any{}
	if workspaceID != "" {
		query = `
SELECT feature,workspace_id,application_version,legacy_version,count,first_seen,last_seen
FROM compatibility_usage WHERE workspace_id IN ('', ?) ORDER BY last_seen DESC, feature, application_version`
		args = append(args, workspaceID)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.CompatibilityUsage{}
	for rows.Next() {
		var usage domain.CompatibilityUsage
		var first, last string
		if err = rows.Scan(&usage.Feature, &usage.WorkspaceID, &usage.ApplicationVersion, &usage.LegacyVersion, &usage.Count, &first, &last); err != nil {
			return nil, err
		}
		usage.FirstSeen, usage.LastSeen = parseTime(first), parseTime(last)
		result = append(result, usage)
	}
	return result, rows.Err()
}
