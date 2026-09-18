package storage

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

func TestConcurrentBudgetReservationsCannotExceedLimit(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "budget.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	limits := domain.BudgetReserveLimits{DailyCents: 10, MonthlyCents: 10, HardStop: true, DayStart: now.Add(-time.Hour), MonthStart: now.Add(-24 * time.Hour)}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, id := range []string{"one", "two"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_, reserveErr := store.ReserveBudget(context.Background(), domain.BudgetReservation{
				ID: id, WorkspaceID: "workspace", RunID: "run-" + id, Provider: "openai", Model: "model",
				ReservedTokens: 100, ReservedCents: 6, CreatedAt: now,
			}, limits)
			results <- reserveErr
		}(id)
	}
	wg.Wait()
	close(results)
	succeeded, blocked := 0, 0
	for reserveErr := range results {
		if reserveErr == nil {
			succeeded++
		} else if errors.Is(reserveErr, ErrBudgetLimitExceeded) {
			blocked++
		} else {
			t.Fatalf("unexpected reserve error: %v", reserveErr)
		}
	}
	if succeeded != 1 || blocked != 1 {
		t.Fatalf("succeeded=%d blocked=%d", succeeded, blocked)
	}
}

func TestMissingProviderUsageKeepsConservativeReservation(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "budget.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	created, err := store.ReserveBudget(context.Background(), domain.BudgetReservation{
		ID: "reservation", WorkspaceID: "workspace", RunID: "run", Provider: "openai", Model: "model",
		ReservedTokens: 100, ReservedCents: 5, CreatedAt: now,
	}, domain.BudgetReserveLimits{DayStart: now.Add(-time.Hour), MonthStart: now.Add(-time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.ReconcileBudget(context.Background(), created.ID, 0, 0, 0, false, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	stored, err := store.BudgetReservation(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != domain.BudgetConservative || stored.ReservedCents != 5 {
		t.Fatalf("reservation=%#v", stored)
	}
}
