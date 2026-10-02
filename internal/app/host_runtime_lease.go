package app

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/osproc"
)

func (a *App) hostWriterLock(ctx context.Context, w domain.Workspace) (func(), error) {
	unlock, err := scopeFileLock(w, "writers")
	if err != nil {
		return nil, fmt.Errorf("workspace writer is already active: %w", err)
	}
	lease, exists, err := a.store.WriterLeaseV2(ctx, w.ID)
	if err != nil {
		unlock()
		return nil, err
	}
	if exists && lease.State == "active" {
		if err = a.recoverHostWriter(ctx, w, lease); err != nil {
			unlock()
			return nil, err
		}
	}
	return unlock, nil
}

func scopeFileLock(w domain.Workspace, kind string) (func(), error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	locks := filepath.Join(home, "POINT", "Runtime", kind)
	if err = os.MkdirAll(locks, 0700); err != nil {
		return nil, err
	}
	root := filepath.Clean(w.Path)
	// workspace.Open already resolves symlinks. Windows paths are case insensitive.
	if filepath.VolumeName(root) != "" {
		root = strings.ToLower(root)
	}
	hash := sha256.Sum256([]byte(root))
	return osproc.LockFile(filepath.Join(locks, fmt.Sprintf("%x.lock", hash)))
}

func (a *App) recoverHostWriter(ctx context.Context, w domain.Workspace, lease domain.WriterLease) error {
	approval, e := a.store.WorkOrderApprovalByQuestV2(ctx, lease.QuestID)
	if e != nil || approval.WorkOrder.Workspace.Isolation != "host_live" {
		return fmt.Errorf("workspace writer is owned by another Point execution")
	}
	// Acquiring the OS lease proves no host worker remains. Preserve every file
	// and journal entry; never repeat a command whose outcome may be unknown.
	executions, e := a.store.ListExecutions(ctx, w.ID, 10000)
	if e != nil {
		return e
	}
	for _, execution := range executions {
		if execution.QuestID == lease.QuestID && execution.RunID != "" {
			run, e := a.store.GetRun(ctx, execution.RunID)
			if e != nil {
				return e
			}
			if run.Status != domain.RunCompleted && run.Status != domain.RunFailed && run.Status != domain.RunCancelled && run.Status != domain.RunInterrupted {
				run.Status = domain.RunInterrupted
				run.Error = "The host worker stopped; local files were preserved. Inspect them before starting another task."
				now := time.Now().UTC()
				run.FinishedAt = &now
				if e = a.store.SaveRun(ctx, run); e != nil {
					return e
				}
			}
			a.finishHostFastAgent(ctx, approval, execution, run, approval.WorkOrder.ConversationID)
		}
	}
	return nil
}
