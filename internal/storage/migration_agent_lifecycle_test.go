package storage

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

func TestLifecycleMigrationMaterializesOnlyOpenLegacyDraftsWithStableIDs(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "legacy-agent-draft.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	if err = store.SaveConnection(ctx, domain.Connection{ID: "c", Provider: domain.ProviderOpenAI, PresetID: "openai", DisplayName: "OpenAI", BaseURL: "https://api.openai.com/v1", Status: domain.ConnectionConnected, DefaultModel: "m", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	open := storageWorkOrder()
	open.ID, open.ConversationID = "legacy-open-order", "legacy-conversation"
	open.Roster = domain.AgentRosterPlan{Permanent: []domain.AgentDraft{{ID: "stable-legacy-draft", Name: "Symfony developer", Role: "Symfony specialist", Mission: "Implement API", RequiredTools: []string{"read_file"}, RequiresConsent: true}}}
	open.Budget.MaxProjectAgents = 1
	open, err = store.SaveWorkOrderV2(ctx, open)
	if err != nil {
		t.Fatal(err)
	}
	approved := storageWorkOrder()
	approved.ID = "legacy-approved-order"
	approved.Roster = domain.AgentRosterPlan{Permanent: []domain.AgentDraft{{ID: "historical-draft", Name: "Historical", Role: "developer", Mission: "Past work", RequiredTools: []string{"read_file"}, RequiresConsent: true}}}
	approved.Budget.MaxProjectAgents = 1
	approved, err = store.SaveWorkOrderV2(ctx, approved)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.ExecContext(ctx, `UPDATE work_order_current_v2 SET status='approved' WHERE id=?`, approved.ID); err != nil {
		t.Fatal(err)
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = migrateOpenLegacyWorkOrderAgents(ctx, tx); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	migrated, err := store.GetWorkOrderV2(ctx, open.ID)
	if err != nil {
		t.Fatal(err)
	}
	if migrated.Version != open.Version+1 || len(migrated.Roster.AgentIDs) != 1 || migrated.Roster.AgentIDs[0] != "stable-legacy-draft" || !migrated.Roster.Permanent[0].Existing {
		t.Fatalf("open legacy order was not converted: %#v", migrated.Roster)
	}
	agent, err := store.GetProjectAgent(ctx, "stable-legacy-draft")
	if err != nil || agent.Status != domain.ProjectAgentDraft || agent.RoleFamily != "developer" || agent.BlueprintID != "" {
		t.Fatalf("migrated draft=%#v err=%v", agent, err)
	}
	if _, err = store.GetProjectAgent(ctx, "historical-draft"); err == nil {
		t.Fatal("approved historical WorkOrder was rewritten")
	}
}

// A card recreated under the same deterministic ID keeps the immutable
// revisions of its earlier life, so current.version + 1 is already taken.
func TestLifecycleMigrationSkipsRevisionVersionsLeftByAnEarlierCard(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "legacy-revision-gap.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	if err = store.SaveConnection(ctx, domain.Connection{ID: "c", Provider: domain.ProviderOpenAI, PresetID: "openai", DisplayName: "OpenAI", BaseURL: "https://api.openai.com/v1", Status: domain.ConnectionConnected, DefaultModel: "m", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	order := storageWorkOrder()
	order.ID, order.ConversationID = "workorder-qp_recreated", "legacy-conversation"
	order.Budget.MaxProjectAgents = 1
	if order, err = store.SaveWorkOrderV2(ctx, order); err != nil {
		t.Fatal(err)
	}
	second := order
	second.Version, second.Goal = order.Version+1, order.Goal+" (revised)"
	if _, err = store.SaveWorkOrderV2(ctx, second); err != nil {
		t.Fatal(err)
	}
	// The proposal is decided again: the card is rewritten from version 1
	// while revision 2 stays behind in the immutable history.
	recreated := order
	recreated.Roster = domain.AgentRosterPlan{Permanent: []domain.AgentDraft{{ID: "stable-legacy-draft", Name: "Symfony developer", Role: "Symfony specialist", Mission: "Implement API", RequiredTools: []string{"read_file"}, RequiresConsent: true}}}
	raw, marshalErr := json.Marshal(recreated)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if _, err = store.db.ExecContext(ctx, `UPDATE work_order_current_v2 SET version=1,digest=?,payload_json=?,status='ready' WHERE id=?`, domain.WorkOrderDigest(recreated), string(raw), recreated.ID); err != nil {
		t.Fatal(err)
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = migrateOpenLegacyWorkOrderAgents(ctx, tx); err != nil {
		tx.Rollback()
		t.Fatalf("migration collided with an orphaned revision: %v", err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	migrated, err := store.GetWorkOrderV2(ctx, recreated.ID)
	if err != nil {
		t.Fatal(err)
	}
	if migrated.Version != 3 || len(migrated.Roster.AgentIDs) != 1 || migrated.Roster.AgentIDs[0] != "stable-legacy-draft" {
		t.Fatalf("recreated card was not lifted above the old revisions: version=%d roster=%#v", migrated.Version, migrated.Roster)
	}
	if _, err = store.GetProjectAgent(ctx, "stable-legacy-draft"); err != nil {
		t.Fatalf("draft was not materialised: %v", err)
	}
}
