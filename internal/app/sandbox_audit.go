package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/sandbox"
	"local-agent-workbench/internal/storage"
)

type SandboxAuditReviewRequest struct {
	Review struct {
		Decision string `json:"decision"`
		Digest   string `json:"digest"`
	} `json:"review"`
}

func (a *App) sandboxIntegrity(ctx context.Context, r domain.SandboxRecord) (sandbox.WorkspaceIntegrity, error) {
	if backend, ok := a.sandboxBackend.(sandbox.IntegrityBackend); ok {
		return backend.CheckWorkspace(ctx, r)
	}
	digest, err := sandbox.TreeDigestWithRules(r.Path, r.FileRulesVersion)
	return sandbox.WorkspaceIntegrity{Digest: digest}, err
}
func (a *App) ReviewSandboxAudit(ctx context.Context, id string, input SandboxAuditReviewRequest) (storage.SandboxAuditReview, error) {
	r, err := a.store.GetSandbox(ctx, id)
	if err != nil {
		return storage.SandboxAuditReview{}, err
	}
	if err = a.guardWorld(r.WorkspaceID); err != nil {
		return storage.SandboxAuditReview{}, err
	}
	status, err := a.sandboxIntegrity(ctx, r)
	if err != nil {
		return storage.SandboxAuditReview{}, err
	}
	if !status.Incomplete || status.Digest != input.Review.Digest {
		return storage.SandboxAuditReview{}, fmt.Errorf("audit review must match the current incomplete sandbox revision")
	}
	if input.Review.Decision == "accepted" && !status.FreshCheck {
		return storage.SandboxAuditReview{}, fmt.Errorf("a new clean verification is required before accepting incomplete audit")
	}
	review := storage.SandboxAuditReview{ID: domain.NewID("audit-review"), SandboxID: id, TreeDigest: status.Digest, Decision: input.Review.Decision, CreatedAt: time.Now().UTC()}
	if err = a.store.SaveSandboxAuditReview(ctx, review); err != nil {
		return storage.SandboxAuditReview{}, err
	}
	return review, nil
}
func (a *App) guardSandboxDelivery(ctx context.Context, set domain.ChangeSet) error {
	r, err := a.store.GetSandboxByExecution(ctx, set.ExecutionID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	status, err := a.sandboxIntegrity(ctx, r)
	if err != nil {
		return err
	}
	if !status.Incomplete {
		return nil
	}
	review, found, err := a.store.SandboxAuditReview(ctx, r.ID, status.Digest)
	if err != nil {
		return err
	}
	if !status.FreshCheck || !found || review.Decision != "accepted" || review.CreatedAt.Before(status.IncompleteAt) {
		return fmt.Errorf("sandbox %s has incomplete audit: new clean verification and a human decision for %s are required", r.ID, status.Digest)
	}
	return nil
}
func (a *App) recordCleanVerification(ctx context.Context, r domain.SandboxRecord, digest string) error {
	if backend, ok := a.sandboxBackend.(interface {
		RecordCleanVerification(context.Context, domain.SandboxRecord, string) error
	}); ok {
		return backend.RecordCleanVerification(ctx, r, digest)
	}
	return nil
}

func (a *App) canReuseSandboxVerification(ctx context.Context, r domain.SandboxRecord, status sandbox.WorkspaceIntegrity) bool {
	if !status.Incomplete {
		return true
	}
	if !status.FreshCheck {
		return false
	}
	review, found, err := a.store.SandboxAuditReview(ctx, r.ID, status.Digest)
	return err == nil && found && review.Decision == "accepted" && !review.CreatedAt.Before(status.IncompleteAt)
}

func (a *App) appendSandboxAuditLimitations(ctx context.Context, bundle *domain.EvidenceBundle, workspaceID string, quest domain.Quest) {
	backend, ok := a.sandboxBackend.(interface {
		AuditStatus(domain.SandboxRecord) (sandbox.WorkspaceIntegrity, error)
	})
	if !ok {
		return
	}
	executions, err := a.store.ListExecutions(ctx, workspaceID, 500)
	if err != nil {
		return
	}
	for _, execution := range executions {
		if execution.QuestID != quest.ID && (quest.FlowRunID == "" || execution.FlowRunID != quest.FlowRunID) {
			continue
		}
		r, err := a.store.GetSandboxByExecution(ctx, execution.ID)
		if err != nil {
			continue
		}
		status, err := backend.AuditStatus(r)
		if err == nil && status.Incomplete {
			bundle.KnownLimitations = append(bundle.KnownLimitations, "Аудит песочницы "+r.ID+" неполный; ревизия "+status.Digest)
		}
	}
}
func (a *App) sandboxAuditDecisions(ctx context.Context, workspaceID string, now time.Time) ([]Decision, error) {
	backend, ok := a.sandboxBackend.(interface {
		AuditStatus(domain.SandboxRecord) (sandbox.WorkspaceIntegrity, error)
	})
	if !ok {
		return nil, nil
	}
	records, err := a.store.ListOpenSandboxes(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	var items []Decision
	for _, r := range records {
		if r.StorageMode != "volume" {
			continue
		}
		status, err := backend.AuditStatus(r)
		if err != nil {
			return nil, err
		}
		if !status.Incomplete {
			continue
		}
		review, found, err := a.store.SandboxAuditReview(ctx, r.ID, status.Digest)
		if err != nil {
			return nil, err
		}
		if found && !review.CreatedAt.Before(status.IncompleteAt) {
			continue
		}
		items = append(items, Decision{ID: "sandbox-audit-" + r.ID, Kind: "sandbox-audit", Label: "Аудит", Title: "История команд песочницы неполная", Detail: status.Reason + "; " + status.Digest, Risk: "HIGH", Blocking: true, CreatedAt: r.CreatedAt, WaitingMs: waitingMs(r.CreatedAt, now), Resolve: DecisionResolve{Path: "/api/sandboxes/" + r.ID + "/audit-review", Field: "review", Accept: "accepted", Reject: "rejected", AcceptValue: map[string]string{"decision": "accepted", "digest": status.Digest}, RejectValue: map[string]string{"decision": "rejected", "digest": status.Digest}}})
	}
	return items, nil
}
