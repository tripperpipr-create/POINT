package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

// Q14: запуск приложения, прерванный остановкой ядра, навсегда оставался
// «executing». Восстановление своего мира называет исход неизвестным, а
// действие соседнего мира — его ядро ещё может делать — не трогает.
func TestRecoveryMarksInterruptedAppActionUnknown(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "app-control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	for _, quest := range []domain.Quest{{ID: "quest-own", WorkspaceID: "w", Title: "own", Status: domain.QuestCompleted, CreatedAt: now, UpdatedAt: now}, {ID: "quest-other", WorkspaceID: "other", Title: "other", Status: domain.QuestCompleted, CreatedAt: now, UpdatedAt: now}} {
		if err = s.SaveQuest(ctx, quest); err != nil {
			t.Fatal(err)
		}
		if _, _, err = s.BeginDeliveredAppControlV2(ctx, "key-"+quest.ID, domain.DeliveredApplicationControl{QuestID: quest.ID, DeliveryReceiptID: "receipt", WorkOrderDigest: "digest", Action: "start"}); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.RecoverAbandonedWork(ctx, "w"); err != nil {
		t.Fatal(err)
	}
	own, found, err := s.LatestDeliveredAppControlV2(ctx, "quest-own")
	if err != nil || !found || own.Status != "unknown_outcome" || own.Summary == "" {
		t.Fatalf("interrupted app action still looks live: %+v found=%v err=%v", own, found, err)
	}
	other, _, err := s.LatestDeliveredAppControlV2(ctx, "quest-other")
	if err != nil || other.Status != "executing" {
		t.Fatalf("neighbour world's action was touched: %+v err=%v", other, err)
	}
}
