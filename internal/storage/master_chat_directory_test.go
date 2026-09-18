package storage

import (
	"context"
	"local-agent-workbench/internal/domain"
	"path/filepath"
	"testing"
	"time"
)

// Каталог чатов — единственный запрос мастера, смотрящий за пределы мира.
// Проверяем ровно то, во что он может соврать: порядок по времени, отсев
// архивного и временного, и признак «идёт ответ» — он берётся подзапросом и
// обязан гореть только у того разговора, у которого ход действительно живой.
func TestMasterConversationDirectory(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "directory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()

	worlds := []domain.Workspace{
		{ID: "ws-a", Path: "c:/worlds/alpha", Name: "alpha", OpenedAt: time.Now().UTC()},
		{ID: "ws-b", Path: "c:/worlds/beta", Name: "beta", OpenedAt: time.Now().UTC()},
	}
	for _, world := range worlds {
		if err = store.SaveWorkspace(ctx, world); err != nil {
			t.Fatal(err)
		}
	}

	stamp := func(minutes int) string {
		return time.Date(2026, 9, 14, 12, minutes, 0, 0, time.UTC).Format(time.RFC3339Nano)
	}
	chats := []domain.MasterConversation{
		{WorkspaceID: "ws-a", ID: "chat-old", Title: "Старый разговор", UpdatedAt: stamp(10)},
		{WorkspaceID: "ws-b", ID: "chat-fresh", Title: "Свежий разговор", UpdatedAt: stamp(40)},
		{WorkspaceID: "ws-a", ID: "chat-middle", Title: "Средний разговор", UpdatedAt: stamp(20)},
		{WorkspaceID: "ws-a", ID: "chat-archived", Title: "В архиве", Archived: true, UpdatedAt: stamp(50)},
		{WorkspaceID: "ws-b", ID: "chat-temporary", Title: "Временный", Temporary: true, UpdatedAt: stamp(55)},
	}
	for _, chat := range chats {
		if err = store.SaveMasterConversation(ctx, chat); err != nil {
			t.Fatal(err)
		}
	}
	// Один живой ход и один завершённый: подзапрос обязан различить их.
	if err = store.SaveMasterTurn(ctx, domain.MasterTurn{ID: "turn-live", ConversationID: "chat-middle", WorkspaceID: "ws-a", Status: "streaming"}); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveMasterTurn(ctx, domain.MasterTurn{ID: "turn-done", ConversationID: "chat-fresh", WorkspaceID: "ws-b", Status: "completed"}); err != nil {
		t.Fatal(err)
	}

	rows, err := store.MasterConversationDirectory(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	order := []string{}
	for _, row := range rows {
		order = append(order, row.ID)
	}
	want := []string{"chat-fresh", "chat-middle", "chat-old"}
	if len(order) != len(want) {
		t.Fatalf("каталог отдал %v, ожидалось %v — архивный и временный не должны приходить", order, want)
	}
	for index, id := range want {
		if order[index] != id {
			t.Fatalf("порядок каталога %v, ожидался %v", order, want)
		}
	}
	for _, row := range rows {
		if row.ID == "chat-middle" && !row.Running {
			t.Fatal("живой ход не отмечен: строка выглядит спящей, хотя мастер отвечает")
		}
		if row.ID != "chat-middle" && row.Running {
			t.Fatalf("разговор %s помечен идущим без живого хода", row.ID)
		}
		if row.WorkspaceName == "" || row.WorkspacePath == "" {
			t.Fatalf("строка %s пришла без имени или пути мира", row.ID)
		}
	}

	limited, err := store.MasterConversationDirectory(ctx, 2)
	if err != nil || len(limited) != 2 {
		t.Fatalf("потолок каталога не сработал: %d %v", len(limited), err)
	}
}
