package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/filepolicy"
)

func TestSandboxPinnedStorageAndImmutableAuditReview(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "volume.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	r := domain.SandboxRecord{ID: "volume", WorkspaceID: "ws", ExecutionID: "exec", Path: t.TempDir(), Kind: "copy", StorageMode: "volume", WorkspaceVolume: "owned-volume", FileRulesVersion: filepolicy.Current, SandboxdDigest: "sha256:helper", CreatedAt: time.Now().UTC()}
	if err = s.SaveSandbox(ctx, r); err != nil {
		t.Fatal(err)
	}
	changed := r
	changed.StorageMode = "bind"
	changed.WorkspaceVolume = "other-volume"
	changed.FileRulesVersion = filepolicy.Legacy
	if err = s.SaveSandbox(ctx, changed); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.GetSandbox(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.StorageMode != r.StorageMode || loaded.WorkspaceVolume != r.WorkspaceVolume || loaded.FileRulesVersion != r.FileRulesVersion || loaded.SandboxdDigest != r.SandboxdDigest {
		t.Fatal("changed pinned storage")
	}
	old := domain.SandboxRecord{ID: "old", Path: t.TempDir(), CreatedAt: time.Now().UTC()}
	if err = s.SaveSandbox(ctx, old); err != nil {
		t.Fatal(err)
	}
	loaded, err = s.GetSandbox(ctx, old.ID)
	if err != nil || loaded.StorageMode != "bind" || loaded.FileRulesVersion != filepolicy.Legacy {
		t.Fatalf("legacy %+v %v", loaded, err)
	}
	review := SandboxAuditReview{ID: "review", SandboxID: r.ID, TreeDigest: "sha256:tree", Decision: "accepted", CreatedAt: time.Now().UTC()}
	if err = s.SaveSandboxAuditReview(ctx, review); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`UPDATE sandbox_audit_reviews SET decision='rejected' WHERE id='review'`, `DELETE FROM sandbox_audit_reviews WHERE id='review'`} {
		if _, err = s.db.ExecContext(ctx, query); err == nil {
			t.Fatal("mutated historical human decision")
		}
	}
	got, found, err := s.SandboxAuditReview(ctx, r.ID, review.TreeDigest)
	if err != nil || !found || got.Decision != "accepted" {
		t.Fatalf("review %+v %v", got, err)
	}
	_, found, err = s.SandboxAuditReview(ctx, r.ID, "sha256:other")
	if err != nil || found {
		t.Fatal("decision leaked to another revision")
	}
	if _, err = s.db.ExecContext(ctx, `DELETE FROM sandboxes WHERE id=?`, r.ID); err != nil {
		t.Fatalf("immutable review prevented sandbox retention: %v", err)
	}
	if _, found, err = s.SandboxAuditReview(ctx, r.ID, review.TreeDigest); err != nil || !found {
		t.Fatal("retention erased historical human decision", err)
	}
}
