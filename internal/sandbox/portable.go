package sandbox

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/filepolicy"
	"local-agent-workbench/internal/sandboxsync"
)

func CopyPortable(ctx context.Context, src, dst, rules string) error {
	m, err := sandboxsync.Scan(ctx, src, rules)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(dst, 0755); err != nil {
		return err
	}
	if entries, err := os.ReadDir(dst); err != nil {
		return err
	} else if len(entries) != 0 {
		return fmt.Errorf("portable copy destination must be empty: %s", dst)
	}
	for _, e := range m.Entries {
		c, err := sandboxsync.ReadChange(src, sandboxsync.Change{Entry: e})
		if err != nil {
			return err
		}
		if err = sandboxsync.Seed(dst, []sandboxsync.Change{c}); err != nil {
			return err
		}
	}
	return nil
}
func (m *Manager) createPortable(ctx context.Context, req CreateRequest, id string) (domain.SandboxRecord, error) {
	target := filepath.Join(m.Root, id)
	r := domain.SandboxRecord{ID: id, WorkspaceID: req.WorkspaceID, ExecutionID: req.ExecutionID, Kind: "copy", Backend: "filtered-copy", BackendVersion: "1", StorageMode: req.StorageMode, FileRulesVersion: filepolicy.Current, Path: target, BaselinePath: target + "-baseline", ParentSandboxID: req.ParentSandboxID, ParentExecutionID: req.ParentExecutionID, BaselineChangeSetIDs: append([]string(nil), req.BaselineChangeSetIDs...), CreatedAt: time.Now().UTC()}
	if r.ParentSandboxID != "" {
		r.ParentSandboxIDs = []string{r.ParentSandboxID}
	}
	if r.ParentExecutionID != "" {
		r.ParentExecutionIDs = []string{r.ParentExecutionID}
	}
	source := req.WorkspacePath
	if req.SeedPath != "" {
		source = req.SeedPath
	}
	if err := CopyPortable(ctx, source, r.BaselinePath, filepolicy.Current); err != nil {
		_ = os.RemoveAll(r.BaselinePath)
		return domain.SandboxRecord{}, err
	}
	if err := CopyPortable(ctx, r.BaselinePath, target, filepolicy.Current); err != nil {
		_ = os.RemoveAll(r.BaselinePath)
		_ = os.RemoveAll(target)
		return domain.SandboxRecord{}, err
	}
	return r, nil
}

func TreeDigestWithRules(root, rules string) (string, error) {
	if rules == "" || rules == filepolicy.Legacy {
		return TreeDigest(root)
	}
	m, err := sandboxsync.Scan(context.Background(), root, rules)
	return m.Digest, err
}
