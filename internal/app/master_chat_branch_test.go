package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Папка без своего Git с cf-pages/ и cf-vue-apps/ внутри: ветка плана прежде
// падала на `rev-parse --show-toplevel` в корне. Теперь рабочая копия чата —
// обычный каталог, в котором у каждого вложенного репозитория свой worktree
// на общей ветке, и ядро сверяет каждый.
func TestBindMasterChatBranchAcceptsNestedRepositories(t *testing.T) {
	root, heads := nestedReposForTest(t)
	application := newTestApp(t)
	view, err := application.OpenWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err = application.store.SaveMasterConversation(ctx, MasterSession{
		ID: "chat_nested", WorkspaceID: view.Workspace.ID, Title: "План", Mode: "auto", WorkMode: "plan", BranchOffer: "pending",
	}); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(application.dataDir, "managed-workspaces", "chat-chat_nested")
	if err = os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	const branch = "point/plan-nested"
	repos := []MasterChatBranchRepository{}
	for _, repo := range []string{"cf-pages", "cf-vue-apps"} {
		if output, gitErr := exec.Command("git", "-C", filepath.Join(root, repo), "worktree", "add", "-q", "-b", branch, filepath.Join(target, repo), heads[repo]).CombinedOutput(); gitErr != nil {
			t.Fatalf("worktree %s: %v: %s", repo, gitErr, output)
		}
		repos = append(repos, MasterChatBranchRepository{Path: repo, Base: "master", Commit: heads[repo]})
	}

	wrong := append([]MasterChatBranchRepository(nil), repos...)
	wrong[1].Commit = heads["cf-pages"]
	if _, err = application.BindMasterChatBranch(ctx, "chat_nested", MasterChatBranchBindRequest{Path: target, Name: branch, Repositories: wrong}); err == nil || !strings.Contains(err.Error(), "cf-vue-apps") {
		t.Fatalf("чужой коммит во вложенном репозитории принят: %v", err)
	}
	escape := append([]MasterChatBranchRepository(nil), repos...)
	escape[0].Path = "../cf-pages"
	if _, err = application.BindMasterChatBranch(ctx, "chat_nested", MasterChatBranchBindRequest{Path: target, Name: branch, Repositories: escape}); err == nil {
		t.Fatal("путь репозитория вне рабочей копии принят")
	}

	moved, err := application.BindMasterChatBranch(ctx, "chat_nested", MasterChatBranchBindRequest{Path: target, Name: branch, Repositories: repos})
	if err != nil {
		t.Fatal(err)
	}
	items, err := application.store.MasterConversations(ctx, moved.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("чат не переехал: %#v %v", items, err)
	}
	chat := items[0]
	if chat.BranchOffer != "bound" || chat.BranchName != branch || chat.BranchBase != "cf-pages@master, cf-vue-apps@master" ||
		chat.BranchCommit != "cf-pages@"+heads["cf-pages"]+", cf-vue-apps@"+heads["cf-vue-apps"] {
		t.Fatalf("ветка записана не так: %#v", chat)
	}
}

// Рабочая копия, которая сама репозиторий, с перечнем вложенных — путаница
// хоста: доставка по вложенным её бы не поняла.
func TestBindMasterChatBranchRefusesNestedListInsideRepository(t *testing.T) {
	root, heads := nestedReposForTest(t)
	application := newTestApp(t)
	if _, err := application.OpenWorkspace(root); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(application.dataDir, "managed-workspaces", "chat-x")
	if err := os.MkdirAll(filepath.Join(target, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := application.BindMasterChatBranch(context.Background(), "chat-x", MasterChatBranchBindRequest{
		Path: target, Name: "point/x", Repositories: []MasterChatBranchRepository{{Path: "cf-pages", Base: "master", Commit: heads["cf-pages"]}},
	})
	if err == nil || !strings.Contains(err.Error(), "не должна быть репозиторием") {
		t.Fatalf("вложенный перечень внутри репозитория принят: %v", err)
	}
}
