package app

import (
	"context"
	"fmt"
	"sort"
	"time"

	"local-agent-workbench/internal/domain"
)

const (
	legacyProfileAPIVersion    = "profile-api-v1"
	legacyProfileRunVersion    = "profile-run-v1"
	legacyWorkflowVersion      = "workflow-snapshot-v1"
	legacyCustomCommandVersion = "custom-command-v1"
)

// recordCompatibilityUsage is deliberately fail-closed for legacy mutations
// and executions. If evidence cannot be persisted, retiring that path later
// would be based on incomplete data, so the operation must not proceed.
func (a *App) recordCompatibilityUsage(ctx context.Context, workspaceID string, feature domain.CompatibilityFeature, legacyVersion string) error {
	now := time.Now().UTC()
	if err := a.store.RecordCompatibilityUsage(ctx, domain.CompatibilityUsage{
		Feature: feature, WorkspaceID: workspaceID, ApplicationVersion: Version,
		LegacyVersion: legacyVersion, FirstSeen: now, LastSeen: now,
	}); err != nil {
		return fmt.Errorf("record compatibility usage %q: %w", feature, err)
	}
	return nil
}

// publicCompatibilityUsage removes the internal workspace-scoping key and
// merges rows that become identical after that removal. Field evidence can
// therefore be exported from Statistics without disclosing even Point's random
// local workspace identifier.
func publicCompatibilityUsage(items []domain.CompatibilityUsage) []domain.CompatibilityUsage {
	type aggregateKey struct {
		feature            domain.CompatibilityFeature
		applicationVersion string
		legacyVersion      string
	}
	aggregates := make(map[aggregateKey]domain.CompatibilityUsage, len(items))
	for _, item := range items {
		key := aggregateKey{item.Feature, item.ApplicationVersion, item.LegacyVersion}
		current, ok := aggregates[key]
		if !ok {
			item.WorkspaceID = ""
			aggregates[key] = item
			continue
		}
		current.Count += item.Count
		if current.FirstSeen.IsZero() || (!item.FirstSeen.IsZero() && item.FirstSeen.Before(current.FirstSeen)) {
			current.FirstSeen = item.FirstSeen
		}
		if item.LastSeen.After(current.LastSeen) {
			current.LastSeen = item.LastSeen
		}
		aggregates[key] = current
	}
	result := make([]domain.CompatibilityUsage, 0, len(aggregates))
	for _, item := range aggregates {
		result = append(result, item)
	}
	sort.Slice(result, func(left, right int) bool {
		if !result[left].LastSeen.Equal(result[right].LastSeen) {
			return result[left].LastSeen.After(result[right].LastSeen)
		}
		if result[left].Feature != result[right].Feature {
			return result[left].Feature < result[right].Feature
		}
		if result[left].ApplicationVersion != result[right].ApplicationVersion {
			return result[left].ApplicationVersion < result[right].ApplicationVersion
		}
		return result[left].LegacyVersion < result[right].LegacyVersion
	})
	return result
}
