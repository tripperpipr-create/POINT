package app

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"local-agent-workbench/internal/backup"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/security"
)

func (a *App) CreateBackup(ctx context.Context, reason string) (backup.Snapshot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	a.backupMu.Lock()
	if a.backupStopping || a.backupManager == nil {
		a.backupMu.Unlock()
		return backup.Snapshot{}, errors.New("backup service is stopping")
	}
	a.backupWG.Add(1)
	a.backupMu.Unlock()
	defer a.backupWG.Done()

	snapshot, err := a.backupManager.Create(ctx, reason)
	if err == nil {
		a.backupMu.Lock()
		a.lastBackupAt = snapshot.CreatedAt
		a.backupMu.Unlock()
	}
	return snapshot, err
}

func (a *App) ListBackups(ctx context.Context) ([]backup.Snapshot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	a.backupMu.Lock()
	manager := a.backupManager
	stopping := a.backupStopping
	a.backupMu.Unlock()
	if stopping || manager == nil {
		return nil, errors.New("backup service is stopping")
	}
	return manager.List(ctx)
}

func (a *App) scheduleEventBackup(eventType domain.EventType) {
	switch eventType {
	case domain.EventRunCompleted, domain.EventRunFailed, domain.EventRunCancelled,
		domain.EventPatchApplied, domain.EventWorkspaceChanged:
	default:
		return
	}
	a.backupMu.Lock()
	defer a.backupMu.Unlock()
	if a.backupStopping || a.backupManager == nil || a.backupTimer != nil {
		return
	}
	if !a.lastBackupAt.IsZero() && time.Since(a.lastBackupAt) < a.backupInterval {
		return
	}
	a.backupTimer = time.AfterFunc(a.backupDebounce, func() {
		a.backupMu.Lock()
		a.backupTimer = nil
		ctx := a.backupCtx
		stopping := a.backupStopping
		a.backupMu.Unlock()
		if stopping {
			return
		}
		backupCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		snapshot, err := a.CreateBackup(backupCtx, "event")
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				slog.Warn("background backup failed", "error", security.Redact(err.Error()))
			}
			return
		}
		slog.Info("background backup created", "backup_id", snapshot.ID, "size_bytes", snapshot.Database.SizeBytes)
	})
}
