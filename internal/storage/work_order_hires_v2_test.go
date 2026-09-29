package storage

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

func TestHireWorkOrderAgentV2IsAtomicAndIdempotent(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "hub-v2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	order := storageWorkOrder()
	order.State = "staffing"
	order.Roster.Permanent = []domain.AgentDraft{{ID: "draft-1", Name: "Developer", Role: "developer", Mission: "Build", RequiredTools: []string{"read_file"}, RequiresConsent: true}}
	order, err = store.SaveWorkOrderV2(ctx, order)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	agent := domain.ProjectAgent{ID: "agent-1", WorkspaceID: order.WorkspaceID, Name: "Developer", RoleDescription: "developer", Mission: "Build", Status: domain.ProjectAgentActive, AllowedTools: []string{"read_file"}, CreatedAt: now, UpdatedAt: now}
	hash := WorkOrderHireRequestHash("draft-1", "", agent)
	result, err := store.HireWorkOrderAgentV2(ctx, order.ID, "draft-1", "hire-1", order.Version, order.Digest, hash, agent, nil, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.WorkOrder.State != "ready" || result.WorkOrder.Roster.Permanent[0].ID != agent.ID {
		t.Fatalf("hire result: %#v", result.WorkOrder)
	}
	replay, err := store.HireWorkOrderAgentV2(ctx, order.ID, "draft-1", "hire-1", order.Version, order.Digest, hash, agent, nil, false, true)
	if err != nil || replay.Agent.ID != agent.ID || replay.WorkOrder.Version != result.WorkOrder.Version {
		t.Fatalf("replay: %#v %v", replay, err)
	}
	if _, err = store.HireWorkOrderAgentV2(ctx, order.ID, "draft-1", "hire-stale", order.Version, order.Digest, "different", domain.ProjectAgent{ID: "agent-2", WorkspaceID: order.WorkspaceID}, nil, false, true); err == nil {
		t.Fatal("stale revision accepted")
	}
	if _, found, replayErr := store.WorkOrderHireReplayV2(ctx, order.ID, "draft-1", "hire-1", order.Version, order.Digest, "different"); replayErr == nil || found {
		t.Fatal("idempotency key accepted a different payload")
	}
	agents, err := store.ListProjectAgents(ctx, order.WorkspaceID)
	if err != nil || len(agents) != 1 {
		t.Fatalf("agents after stale request: %#v %v", agents, err)
	}
}

func TestHireWorkOrderAgentV2RollsBackAndCanReuseExisting(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "hub-v2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	order := storageWorkOrder()
	order.State = "staffing"
	order.Roster.Permanent = []domain.AgentDraft{{ID: "draft-lost", Name: "Developer", Role: "developer", Mission: "Build", RequiresConsent: true}}
	order, err = store.SaveWorkOrderV2(ctx, order)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	agent := domain.ProjectAgent{ID: "agent-orphan", WorkspaceID: order.WorkspaceID, Name: "Developer", RoleDescription: "developer", Mission: "Build", Status: domain.ProjectAgentActive, CreatedAt: now, UpdatedAt: now}
	hash := WorkOrderHireRequestHash("draft-lost", "", agent)
	if _, err = store.HireWorkOrderAgentV2(ctx, order.ID, "missing-draft", "missing", order.Version, order.Digest, hash, agent, nil, false, true); err == nil || !strings.Contains(err.Error(), "черновик") {
		t.Fatalf("missing draft: %v", err)
	}
	if _, err = store.GetProjectAgent(ctx, agent.ID); err == nil {
		t.Fatal("missing draft created an agent")
	}
	if _, err = store.db.ExecContext(ctx, `CREATE TRIGGER hire_abort BEFORE INSERT ON work_order_revisions_v2 BEGIN SELECT RAISE(ABORT,'revision failed'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = store.HireWorkOrderAgentV2(ctx, order.ID, "draft-lost", "retry-after-failure", order.Version, order.Digest, hash, agent, nil, false, true); err == nil || !strings.Contains(err.Error(), "revision failed") {
		t.Fatalf("revision failure: %v", err)
	}
	if _, err = store.GetProjectAgent(ctx, agent.ID); err == nil {
		t.Fatal("agent committed before roster revision")
	}
	if _, err = store.db.ExecContext(ctx, `DROP TRIGGER hire_abort`); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveProjectAgent(ctx, agent); err != nil {
		t.Fatal(err)
	}
	existingHash := WorkOrderHireRequestHash("draft-lost", agent.ID, domain.ProjectAgent{})
	result, err := store.HireWorkOrderAgentV2(ctx, order.ID, "draft-lost", "reuse", order.Version, order.Digest, existingHash, agent, nil, true, true)
	if err != nil || result.WorkOrder.Roster.Permanent[0].ID != agent.ID {
		t.Fatalf("reuse: %#v %v", result, err)
	}
}
