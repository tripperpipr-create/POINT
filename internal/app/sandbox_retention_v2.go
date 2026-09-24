package app

import (
	"context"
	"time"

	"local-agent-workbench/internal/observability"
)

func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
	// This one-shot idempotent repair runs after migrations and before generic
	// interrupted-work recovery can reinterpret the historic cancelled root.
	a.reconcileBrokenWorkOrderFinalizationsV2(context.Background())
	a.reconcileNoopWorkOrderResumesV2(context.Background())
	// A quest left in a live state belongs to a process that no longer exists;
	// resolve that before anything else can read it as progress.
	a.pauseInterruptedWorkOrderQuestsV2(context.Background())
	go a.cleanupExpiredQuestSandboxes(context.Background(), time.Now().UTC())
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
