package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"local-agent-workbench/internal/domain"
)

// Каталог чатов обязан работать в двух положениях, и второе важнее первого:
// без открытого мира. Ровно там он и нужен — окно Чертога поднимается раньше,
// чем к нему привяжут проект, и если метод потребует мира, левая панель будет
// пуста именно в тот момент, ради которого её писали.
func TestMasterChatDirectoryWorksWithoutOpenWorkspace(t *testing.T) {
	application := newTestApp(t)

	directory, err := application.MasterChatDirectory(context.Background())
	if err != nil {
		t.Fatalf("каталог отказал без открытого мира: %v", err)
	}
	if directory.CurrentWorkspaceID != "" || len(directory.Worlds) != 0 {
		t.Fatalf("пустая база дала непустой каталог: %+v", directory)
	}
}

func TestMasterChatDirectoryGroupsWorlds(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	data := t.TempDir()
	application, err := New(data)
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	ctx := context.Background()

	// Два настоящих мира: границу ставим общим родителем, иначе OpenWorkspace
	// откажет второму.
	parent := t.TempDir()
	alpha := filepath.Join(parent, "alpha")
	beta := filepath.Join(parent, "beta")
	for _, root := range []string{alpha, beta} {
		if err = os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err = application.SetWorkspaceBoundary(parent); err != nil {
		t.Fatal(err)
	}
	alphaView, err := application.OpenWorkspace(alpha)
	if err != nil {
		t.Fatal(err)
	}
	betaView, err := application.OpenWorkspace(beta)
	if err != nil {
		t.Fatal(err)
	}

	// Свежий разговор — в alpha, чтобы порядок «по времени» и порядок «текущий
	// мир первым» разошлись: текущим остаётся beta.
	if err = application.store.SaveMasterConversation(ctx, domain.MasterConversation{
		WorkspaceID: alphaView.Workspace.ID, ID: "chat-alpha", Title: "Разговор альфы", UpdatedAt: "2026-09-14T13:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	if err = application.store.SaveMasterConversation(ctx, domain.MasterConversation{
		WorkspaceID: betaView.Workspace.ID, ID: "chat-beta", Title: "Разговор беты", UpdatedAt: "2026-09-14T12:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}

	directory, err := application.MasterChatDirectory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(directory.Worlds) != 2 {
		t.Fatalf("ожидалось два мира, пришло %d: %+v", len(directory.Worlds), directory.Worlds)
	}
	if directory.CurrentWorkspaceID != betaView.Workspace.ID {
		t.Fatalf("текущим миром назван %q вместо беты", directory.CurrentWorkspaceID)
	}
	first := directory.Worlds[0]
	if !first.Current || first.WorkspaceID != betaView.Workspace.ID {
		t.Fatalf("текущий мир не поднят наверх: %+v", first)
	}
	if first.Path == "" {
		t.Fatal("у открытого мира нет пути — хосту нечем его опознать")
	}
	second := directory.Worlds[1]
	if second.Current {
		t.Fatal("чужой мир помечен текущим")
	}
	// Путь чужого мира наружу не отдаётся: хост берёт его из своего реестра по
	// отпечатку, и открыть можно только то, что реестр уже знает.
	if second.Path != "" {
		t.Fatalf("путь чужого мира уехал наружу: %q", second.Path)
	}
	if second.Hash == "" || second.Hash == first.Hash {
		t.Fatalf("отпечатки миров не различаются: %q и %q", first.Hash, second.Hash)
	}
	for _, world := range directory.Worlds {
		for _, chat := range world.Chats {
			if chat.WorkspaceHash != world.Hash {
				t.Fatalf("строка %s несёт чужой отпечаток мира", chat.ID)
			}
			if !world.Current && chat.WorkspacePath != "" {
				t.Fatalf("строка %s чужого мира несёт путь", chat.ID)
			}
		}
	}
	// Отпечаток берётся от нормализованного пути, и важно именно это:
	// хост сшивает строку каталога со своей записью реестра по этому числу,
	// а путь туда может прийти в любом написании. Раньше здесь стояло
	// сравнение вызова с самим собой — оно не могло упасть ни на какой
	// ошибке нормализации.
	// Именно конкатенация, а не filepath.Join: Join чистит путь сам и отдаст
	// уже нормализованную строку — проверять было бы нечего.
	detour := alpha + string(filepath.Separator) + "."
	if workspacePathHash(alpha) != workspacePathHash(detour) {
		t.Fatalf("отпечаток зависит от написания пути: %s против %s", alpha, detour)
	}
}
