package app

import "context"

func (a *App) startSandboxCleanup(parent context.Context) {
	a.sandboxCleanupMu.Lock()
	defer a.sandboxCleanupMu.Unlock()
	if a.sandboxCleanupCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	a.sandboxCleanupCancel = cancel
	a.sandboxCleanupWG.Add(1)
	go func() { defer a.sandboxCleanupWG.Done(); a.cleanupOrphanSandboxResources(ctx) }()
}

func (a *App) stopSandboxCleanup() {
	a.sandboxCleanupMu.Lock()
	cancel := a.sandboxCleanupCancel
	a.sandboxCleanupCancel = nil
	a.sandboxCleanupMu.Unlock()
	if cancel != nil {
		cancel()
	}
	a.sandboxCleanupWG.Wait()
}
