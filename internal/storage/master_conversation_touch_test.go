package storage_test

import (
	"context"
	"testing"

	"local-agent-workbench/internal/domain"
)

// Ход, начавшийся до фонового пересказа, сохранял разговор снимком из своего
// начала и стирал свежее резюме, а заодно переименование, сделанное человеком
// за время хода. След хода пишет только своё.
func TestTouchMasterConversationKeepsConcurrentSummaryAndRename(t *testing.T) {
	s := masterLearningStore(t)
	ctx := context.Background()
	if err := s.SaveMasterConversation(ctx, domain.MasterConversation{ID: "c1", WorkspaceID: "w", Title: "Новый разговор", Summary: "S0"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveMasterConversationSummary(ctx, "w", "c1", "S1"); err != nil {
		t.Fatal(err)
	}
	if err := s.TouchMasterConversation(ctx, "w", "c1", "Как устроен поток", ""); err != nil {
		t.Fatal(err)
	}
	got := findConversation(t, s, "c1")
	if got.Summary != "S1" {
		t.Fatalf("свежее резюме стёрто ходом: %q", got.Summary)
	}
	if got.Title != "Как устроен поток" {
		t.Fatalf("новый разговор не получил заголовок: %q", got.Title)
	}
	if err := s.SaveMasterConversation(ctx, domain.MasterConversation{ID: "c1", WorkspaceID: "w", Title: "Переименовал человек", Summary: "S1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.TouchMasterConversation(ctx, "w", "c1", "Заголовок из начала хода", "S2"); err != nil {
		t.Fatal(err)
	}
	got = findConversation(t, s, "c1")
	if got.Title != "Переименовал человек" || got.Summary != "S2" {
		t.Fatalf("след хода переписал чужое или потерял своё: %+v", got)
	}
}

func findConversation(t *testing.T, s interface {
	MasterConversations(context.Context, string) ([]domain.MasterConversation, error)
}, id string) domain.MasterConversation {
	t.Helper()
	items, err := s.MasterConversations(context.Background(), "w")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.ID == id {
			return item
		}
	}
	t.Fatalf("разговор %s не найден", id)
	return domain.MasterConversation{}
}
