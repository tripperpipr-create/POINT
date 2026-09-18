package storage

import (
	"context"
	"errors"
	"local-agent-workbench/internal/domain"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestRootBudgetIncludesParallelChildrenWithTheirOwnCaps(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "root.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	for _, q := range []domain.Quest{{ID: "root", BudgetTokens: 100}, {ID: "left", ParentID: "root", BudgetTokens: 100}, {ID: "right", ParentID: "root", BudgetTokens: 100}} {
		q.WorkspaceID = "ws"
		q.CreatedAt = now
		q.UpdatedAt = now
		if err = s.SaveQuest(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, id := range []string{"left", "right"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			r, e := s.ReserveBudget(ctx, domain.BudgetReservation{ID: "r-" + id, WorkspaceID: "ws", QuestID: id, BudgetScopeQuestID: id, ReservedTokens: 60, CreatedAt: now}, domain.BudgetReserveLimits{})
			if e == nil && r.BudgetScopeQuestID != "root" {
				e = errors.New("reservation charged to child only")
			}
			results <- e
		}(id)
	}
	wg.Wait()
	close(results)
	passed, blocked := 0, 0
	for e := range results {
		if e == nil {
			passed++
		} else if errors.Is(e, ErrBudgetLimitExceeded) {
			blocked++
		} else {
			t.Fatal(e)
		}
	}
	if passed != 1 || blocked != 1 {
		t.Fatalf("passed=%d blocked=%d", passed, blocked)
	}
}
func TestChildCapAndLegacyReservationsRemainInRootBudget(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "root.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	for _, q := range []domain.Quest{{ID: "root", BudgetTokens: 100}, {ID: "child", ParentID: "root", BudgetTokens: 30}, {ID: "sibling", ParentID: "root"}} {
		q.WorkspaceID = "ws"
		q.CreatedAt = now
		q.UpdatedAt = now
		if err = s.SaveQuest(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.ReserveBudget(ctx, domain.BudgetReservation{ID: "too-big", WorkspaceID: "ws", QuestID: "child", ReservedTokens: 31, CreatedAt: now}, domain.BudgetReserveLimits{}); !errors.Is(err, ErrBudgetLimitExceeded) {
		t.Fatalf("child cap ignored: %v", err)
	}
	r, err := s.ReserveBudget(ctx, domain.BudgetReservation{ID: "legacy", WorkspaceID: "ws", QuestID: "child", ReservedTokens: 30, CreatedAt: now}, domain.BudgetReserveLimits{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, "UPDATE budget_reservations SET budget_scope_quest_id='child' WHERE id=?", r.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.ReconcileBudget(ctx, r.ID, 10, 20, 0, true, now); err != nil {
		t.Fatal(err)
	}
	if err = s.InsertUsageRecord(ctx, domain.UsageRecord{ID: "usage_budget_legacy", WorkspaceID: "ws", QuestID: "child", TotalTokens: 30, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err = s.CheckQuestBudget(ctx, "ws", "sibling", 70); err != nil {
		t.Fatalf("mirrored usage double counted: %v", err)
	}
	if err = s.CheckQuestBudget(ctx, "ws", "sibling", 71); !errors.Is(err, ErrBudgetLimitExceeded) {
		t.Fatalf("legacy child escaped root cap: %v", err)
	}
	if err = s.CheckQuestBudget(ctx, "other", "sibling", 1); err == nil {
		t.Fatal("foreign quest accepted")
	}
	if _, err = s.db.ExecContext(ctx, "UPDATE quests SET parent_id='child' WHERE id='root'"); err != nil {
		t.Fatal(err)
	}
	if err = s.CheckQuestBudget(ctx, "ws", "child", 1); err == nil {
		t.Fatal("ancestry cycle ignored")
	}
}
