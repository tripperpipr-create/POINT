package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

// Память проекта растёт без предела, а промпт — нет: в контекст идут свежие
// принятые записи, пока помещаются, в прежнем порядке.
func TestMasterMemoryInContextKeepsNewestWithinBudget(t *testing.T) {
	big := strings.Repeat("а", masterMemoryPromptRunes/2)
	entries := []domain.MasterMemoryEntry{
		{ID: "oldest", Content: big, Status: "accepted"},
		{ID: "proposed", Content: "предложение", Status: "proposed"},
		{ID: "middle", Content: big, Status: "accepted"},
		{ID: "newest", Content: "короткое правило", Status: "accepted"},
	}
	got := masterMemoryInContext(entries)
	ids := make([]string, 0, len(got))
	for _, entry := range got {
		ids = append(ids, entry.ID)
	}
	if strings.Join(ids, ",") != "middle,newest" {
		t.Fatalf("in context: %v", ids)
	}
}

func TestMasterMemorySignatureIgnoresCaseSpacingAndFinalPunctuation(t *testing.T) {
	if masterMemorySignature("Пиши  тесты перед правкой.") != masterMemorySignature("пиши тесты перед правкой") {
		t.Fatal("equivalent memories got different signatures")
	}
	if masterMemorySignature("Пиши тесты") == masterMemorySignature("Не пиши тесты") {
		t.Fatal("different memories collided")
	}
}

// «Не запоминать» оставляет подпись отказа, а не пустое место.
func TestDeclinedMasterMemoryLeavesSignature(t *testing.T) {
	application := newTestApp(t)
	ctx := context.Background()
	if _, err := application.OpenWorkspace(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	sessions, err := application.UpdateMasterSession(ctx, MasterSessionUpdate{Action: "memory-propose", Value: "Всегда пиши ответ по-русски."})
	if err != nil || len(sessions.MemoryEntries) != 1 {
		t.Fatalf("propose: %#v err=%v", sessions.MemoryEntries, err)
	}
	if _, err = application.UpdateMasterSession(ctx, MasterSessionUpdate{Action: "memory-delete", ID: sessions.MemoryEntries[0].ID}); err != nil {
		t.Fatal(err)
	}
	declined, err := application.store.MasterMemoryDeclined(ctx, application.currentWorldID())
	if err != nil || !declined[masterMemorySignature("всегда пиши ответ по-русски")] {
		t.Fatalf("declined=%v err=%v", declined, err)
	}
}

// Неразобранное предложение снимается по сроку; принятая память — никогда.
func TestStaleMasterMemoryProposalsExpireButAcceptedStays(t *testing.T) {
	application := newTestApp(t)
	ctx := context.Background()
	for _, entry := range []domain.MasterMemoryEntry{
		{ID: "proposal", Content: "Предложение без решения", Status: "proposed"},
		{ID: "accepted", Content: "Принятое правило", Status: "accepted"},
	} {
		if err := application.store.SaveMasterMemory(ctx, "w", entry); err != nil {
			t.Fatal(err)
		}
	}
	if removed, err := application.store.DeleteStaleMasterMemoryProposals(ctx, "w", time.Now().Add(-masterMemoryProposalTTL)); err != nil || removed != 0 {
		t.Fatalf("fresh proposal removed: %d err=%v", removed, err)
	}
	if removed, err := application.store.DeleteStaleMasterMemoryProposals(ctx, "w", time.Now().Add(time.Hour)); err != nil || removed != 1 {
		t.Fatalf("stale proposal kept: %d err=%v", removed, err)
	}
	left, _ := application.store.MasterMemory(ctx, "w")
	if len(left) != 1 || left[0].ID != "accepted" {
		t.Fatalf("left=%#v", left)
	}
}
