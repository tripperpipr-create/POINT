package app

import (
	"context"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/observability"
)

func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
	// Брошенную работу ядро разбирает до приёма запросов, но только в своём
	// мире: живое в соседних мирах ведут их ядра (world_recovery.go).
	a.recoverAbandonedWorld(context.Background(), a.startupWorldID(context.Background()))
	go a.cleanupExpiredQuestSandboxes(context.Background(), time.Now().UTC())
	go a.cleanupFinishedQuestCaches(context.Background())
	a.startSandboxCleanup(ctx)
}

func (a *App) cleanupOrphanSandboxResources(ctx context.Context) {
	backend, ok := a.sandboxBackend.(interface {
		CleanupOrphans(context.Context, []domain.SandboxRecord) error
	})
	if !ok || a.store == nil {
		return
	}
	for attempt := 0; attempt < 3; attempt++ {
		records, err := a.store.ListOpenSandboxes(ctx, "")
		if err != nil {
			observability.From(ctx).Error("sandbox orphan database scan failed", "error", err)
			return
		}
		if err = backend.CleanupOrphans(ctx, records); err == nil {
			return
		} else {
			observability.From(ctx).Error("sandbox resource cleanup will retry", "error", err)
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 10 * time.Second)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return
		}
	}
}

func (a *App) cleanupExpiredQuestSandboxes(ctx context.Context, now time.Time) {
	candidates, err := a.store.ListExpiredQuestSandboxes(ctx, now.Add(-30*24*time.Hour))
	if err != nil {
		observability.From(ctx).Error("sandbox retention scan failed", "error", err)
		return
	}
	for _, candidate := range candidates {
		if err = a.sandboxBackend.Close(ctx, candidate.Sandbox, candidate.WorkspacePath); err != nil {
			observability.From(ctx).Error("sandbox retention cleanup failed", "sandbox_id", candidate.Sandbox.ID, "error", err)
			continue
		}
		if err = a.store.MarkSandboxClosed(ctx, candidate.Sandbox.ID, now); err != nil {
			observability.From(ctx).Error("sandbox retention journal failed", "sandbox_id", candidate.Sandbox.ID, "error", err)
		}
	}
}
