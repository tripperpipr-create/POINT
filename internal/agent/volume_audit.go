package agent

import (
	"context"
	"local-agent-workbench/internal/sandbox"
)

func (e *Engine) beginVolumeToolAudit(ctx context.Context, active *activeRun, root string) error {
	if journal, ok := active.processExecutor.(sandbox.VolumeAuditCommit); ok && journal.UsesVolume(root) {
		return journal.BeginVolumeAudit(ctx, root)
	}
	return nil
}
func (e *Engine) finishVolumeToolAudit(ctx context.Context, active *activeRun, root string) error {
	if journal, ok := active.processExecutor.(sandbox.VolumeAuditCommit); ok && journal.UsesVolume(root) {
		return journal.FinishVolumeAudit(ctx, root)
	}
	return nil
}
