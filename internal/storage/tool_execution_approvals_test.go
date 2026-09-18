package storage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

func TestToolExecutionApprovalResolveAndAtomicConsume(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "workbench.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	approval := domain.ToolExecutionApproval{
		ID: "tool-approval-1", WorkspaceID: "workspace-a", ToolID: "tool-a",
		ToolDigest: "tool-digest", ArgumentsDigest: "arguments-digest", Arguments: []byte(`{"reason":"redacted"}`),
		Reason: "review", Status: domain.ToolExecutionApprovalPending, CreatedAt: now, ExpiresAt: now.Add(time.Minute),
	}
	if err = store.SaveToolExecutionApproval(ctx, approval); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ResolveToolExecutionApproval(ctx, approval.ID, "workspace-b", true, now); !errors.Is(err, ErrToolExecutionApprovalNotPending) {
		t.Fatalf("cross-workspace resolve err=%v", err)
	}
	resolved, err := store.ResolveToolExecutionApproval(ctx, approval.ID, approval.WorkspaceID, true, now)
	if err != nil || resolved.Status != domain.ToolExecutionApprovalAllowed || resolved.ResolvedAt == nil {
		t.Fatalf("resolved=%#v err=%v", resolved, err)
	}
	if _, err = store.ConsumeToolExecutionApproval(ctx, approval.ID, approval.WorkspaceID, approval.ToolID, approval.ToolDigest, "substituted", now); !errors.Is(err, ErrToolExecutionApprovalNotUsable) {
		t.Fatalf("substituted arguments err=%v", err)
	}
	consumed, err := store.ConsumeToolExecutionApproval(ctx, approval.ID, approval.WorkspaceID, approval.ToolID, approval.ToolDigest, approval.ArgumentsDigest, now)
	if err != nil || consumed.Status != domain.ToolExecutionApprovalConsumed || consumed.ConsumedAt == nil {
		t.Fatalf("consumed=%#v err=%v", consumed, err)
	}
	if _, err = store.ConsumeToolExecutionApproval(ctx, approval.ID, approval.WorkspaceID, approval.ToolID, approval.ToolDigest, approval.ArgumentsDigest, now); !errors.Is(err, ErrToolExecutionApprovalNotUsable) {
		t.Fatalf("replay err=%v", err)
	}
}

func TestExpiredToolExecutionApprovalFailsClosed(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "workbench.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	approval := domain.ToolExecutionApproval{
		ID: "expired", WorkspaceID: "workspace-a", ToolID: "tool-a", ToolDigest: "tool-digest",
		ArgumentsDigest: "arguments-digest", Arguments: []byte(`{}`), Reason: "review",
		Status: domain.ToolExecutionApprovalPending, CreatedAt: now.Add(-time.Hour), ExpiresAt: now.Add(-time.Second),
	}
	if err = store.SaveToolExecutionApproval(ctx, approval); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ResolveToolExecutionApproval(ctx, approval.ID, approval.WorkspaceID, true, now); !errors.Is(err, ErrToolExecutionApprovalNotPending) {
		t.Fatalf("expired resolve err=%v", err)
	}
	stored, err := store.ToolExecutionApproval(ctx, approval.ID)
	if err != nil || stored.Status != domain.ToolExecutionApprovalPending {
		t.Fatalf("expired approval mutated: %#v err=%v", stored, err)
	}
}
