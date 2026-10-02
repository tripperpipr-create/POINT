package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/filepolicy"
	"local-agent-workbench/internal/sandbox"
)

type auditReviewFixtureBackend struct {
	sandbox.Backend
	status sandbox.WorkspaceIntegrity
}

func (b *auditReviewFixtureBackend) CheckWorkspace(context.Context, domain.SandboxRecord) (sandbox.WorkspaceIntegrity, error) {
	return b.status, nil
}
func (b *auditReviewFixtureBackend) AuditStatus(domain.SandboxRecord) (sandbox.WorkspaceIntegrity, error) {
	return b.status, nil
}

func TestSandboxAuditReviewBindsCleanCheckRevisionAndInterruption(t *testing.T) {
	f := newVerificationFixture(t)
	r := f.record
	r.ID, r.ExecutionID = "sandbox_audit", "execution_audit"
	r.StorageMode, r.FileRulesVersion = "volume", filepolicy.Current
	if err := f.app.store.SaveSandbox(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	b := &auditReviewFixtureBackend{Backend: f.backend, status: sandbox.WorkspaceIntegrity{Digest: "sha256:" + strings.Repeat("a", 64), Incomplete: true, IncompleteAt: time.Now().UTC()}}
	f.app.sandboxBackend = b
	set := domain.ChangeSet{ExecutionID: r.ExecutionID, WorkspaceID: r.WorkspaceID}
	input := SandboxAuditReviewRequest{}
	input.Review.Decision, input.Review.Digest = "accepted", b.status.Digest
	if err := f.app.guardSandboxDelivery(context.Background(), set); err == nil {
		t.Fatal("delivered incomplete unreviewed audit")
	}
	if _, err := f.app.ReviewSandboxAudit(context.Background(), r.ID, input); err == nil {
		t.Fatal("accepted without a clean check")
	}
	b.status.FreshCheck = true
	input.Review.Digest = "sha256:" + strings.Repeat("b", 64)
	if _, err := f.app.ReviewSandboxAudit(context.Background(), r.ID, input); err == nil {
		t.Fatal("accepted stale revision")
	}
	input.Review.Digest = b.status.Digest
	if _, err := f.app.ReviewSandboxAudit(context.Background(), r.ID, input); err != nil {
		t.Fatal(err)
	}
	if err := f.app.guardSandboxDelivery(context.Background(), set); err != nil {
		t.Fatal(err)
	}
	if !f.app.canReuseSandboxVerification(context.Background(), r, b.status) {
		t.Fatal("reviewed clean revision cannot reuse verification")
	}
	b.status.IncompleteAt = time.Now().UTC().Add(time.Second)
	if err := f.app.guardSandboxDelivery(context.Background(), set); err == nil {
		t.Fatal("old decision survived a new interruption")
	}
	if f.app.canReuseSandboxVerification(context.Background(), r, b.status) {
		t.Fatal("reused verification after a new interruption")
	}
	b.status.Digest = "sha256:" + strings.Repeat("b", 64)
	if err := f.app.guardSandboxDelivery(context.Background(), set); err == nil {
		t.Fatal("decision leaked to a new revision")
	}
}
