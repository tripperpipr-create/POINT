package app

import (
	"context"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/orchestrator"
	"testing"
	"time"
)

func TestMasterSessionArchiveAndPinAreReversible(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	defer a.Shutdown(ctx)
	openTestWorld(t, a)
	for _, action := range []string{"archive", "pin"} {
		value, err := a.UpdateMasterSession(ctx, MasterSessionUpdate{Action: action, ID: "legacy"})
		if err != nil {
			t.Fatal(err)
		}
		if action == "archive" && !value.Items[0].Archived || action == "pin" && !value.Items[0].Pinned {
			t.Fatalf("action was not saved: %+v", value)
		}
		value, err = a.UpdateMasterSession(ctx, MasterSessionUpdate{Action: action, ID: "legacy"})
		if err != nil || value.Items[0].Archived || value.Items[0].Pinned || len(value.Items) != 1 {
			t.Fatalf("toggle lost conversation: %+v %v", value, err)
		}
	}
}

func TestMasterSessionsPersistAndIsolateHistory(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	dir := t.TempDir()
	a, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	world := openTestWorld(t, a)
	initial, err := a.MasterSessions(ctx)
	if err != nil || initial.Active != "legacy" {
		t.Fatalf("initial: %+v %v", initial, err)
	}
	svc := orchestrator.ChatService{Store: a.store}
	old, _, err := a.sessionMasterService(ctx, svc, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	if err = old.Store.SaveCompanionMessage(ctx, domain.CompanionMessage{ID: "legacy-turn", WorkspaceID: world.ID, Speaker: "master", Role: "user", Content: "old private context", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	next, err := a.UpdateMasterSession(ctx, MasterSessionUpdate{Action: "new"})
	if err != nil {
		t.Fatal(err)
	}
	fresh, _, err := a.sessionMasterService(ctx, svc, next.Active)
	if err != nil {
		t.Fatal(err)
	}
	history, err := fresh.History(ctx, world.ID, 60)
	if err != nil || len(history) != 0 {
		t.Fatalf("leaked old context: %+v %v", history, err)
	}
	if err = fresh.Store.SaveCompanionMessage(ctx, domain.CompanionMessage{ID: "new-turn", WorkspaceID: world.ID, Speaker: "master", Role: "user", Content: "new private context", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	history, err = old.History(ctx, world.ID, 60)
	if err != nil || len(history) != 1 || history[0].ID != "legacy-turn" {
		t.Fatalf("legacy history changed: %+v %v", history, err)
	}
	for _, req := range []MasterSessionUpdate{{Action: "rename", ID: next.Active, Value: "Design"}, {Action: "memory", Value: "Examples in Go"}, {Action: "mode", Value: "detailed"}} {
		if _, err = a.UpdateMasterSession(ctx, req); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err = a.sessionMasterService(ctx, svc, "foreign"); err == nil {
		t.Fatal("accepted unknown conversation")
	}
	// Reopening the same durable store must preserve metadata and message partition.
	a.Shutdown(ctx)
	reopened, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Shutdown(ctx)
	if _, err = reopened.OpenWorkspace(world.Path); err != nil {
		t.Fatal(err)
	}
	saved, err := reopened.MasterSessions(ctx)
	if err != nil || saved.Active != next.Active || saved.Memory != "Examples in Go" || saved.Mode != "detailed" || saved.Items[0].Title != "Design" {
		t.Fatalf("lost metadata: %+v %v", saved, err)
	}
	read, _, err := reopened.sessionMasterService(ctx, orchestrator.ChatService{Store: reopened.store}, next.Active)
	if err != nil {
		t.Fatal(err)
	}
	history, err = read.History(ctx, world.ID, 60)
	if err != nil || len(history) != 1 || history[0].ID != "new-turn" {
		t.Fatalf("lost history: %+v %v", history, err)
	}
}
