package app

import (
	"context"
	"local-agent-workbench/internal/forge"
	"local-agent-workbench/internal/gitflow"
	"os"
	"path/filepath"
	"testing"
)

func TestGitWorkbenchNestedRepositoriesAndAccounts(t *testing.T) {
	a := newTestApp(t)
	root := t.TempDir()
	ctx := context.Background()
	if _, e := a.OpenWorkspace(root); e != nil {
		t.Fatal(e)
	}
	ws, e := a.requireWorkspace()
	if e != nil {
		t.Fatal(e)
	}
	parent := filepath.Join(root, "parent")
	if e = os.MkdirAll(parent, 0755); e != nil {
		t.Fatal(e)
	}
	gitTest(t, parent, "init", "-b", "main")
	nested := filepath.Join(parent, "nested")
	if e = os.MkdirAll(nested, 0755); e != nil {
		t.Fatal(e)
	}
	gitTest(t, nested, "init", "-b", "main")
	for _, dir := range []string{parent, nested} {
		gitTest(t, dir, "remote", "add", "origin", "git@gitlab.example.test:group/repo.git")
	}
	repos := gitflow.Repositories(ctx, gitflow.ExecRunner{}, root)
	if len(repos) != 2 {
		t.Fatalf("nested repositories in a containing directory: %+v", repos)
	}
	// When the project itself is a repo, its nested repo must be independently addressable.
	if _, e = a.OpenWorkspace(parent); e != nil {
		t.Fatal(e)
	}
	ws, e = a.requireWorkspace()
	if e != nil {
		t.Fatal(e)
	}
	repos = gitflow.Repositories(ctx, gitflow.ExecRunner{}, parent)
	if len(repos) != 2 {
		t.Fatalf("nested repos: %+v", repos)
	}
	one, e := a.SaveForgeConnection(ctx, ForgeConnectionInput{Connection: forge.Connection{Provider: "gitlab", Name: "one", URL: "https://gitlab.example.test", Enabled: true}, Token: "token-one"})
	if e != nil {
		t.Fatal(e)
	}
	two, e := a.SaveForgeConnection(ctx, ForgeConnectionInput{Connection: forge.Connection{Provider: "gitlab", Name: "two", URL: "https://gitlab.example.test", Enabled: true}, Token: "token-two"})
	if e != nil {
		t.Fatal(e)
	}
	target := GitTarget{WorkspaceID: ws.ID, RepoRoot: nested}
	b, e := a.ForgeBindings(ctx, target)
	if e != nil || len(b.Candidates) != 2 {
		t.Fatalf("%+v %v", b, e)
	}
	_, e = a.SaveForgeBinding(ctx, forge.Binding{WorkspaceID: ws.ID, RepoRoot: nested, Remote: "origin", ConnectionID: two.ID, Project: "group/repo", Mode: "manual"})
	if e != nil {
		t.Fatal(e)
	}
	b, e = a.ForgeBindings(ctx, target)
	if e != nil || len(b.Candidates) != 1 || b.Candidates[0].ConnectionID != two.ID {
		t.Fatalf("%+v %v", b, e)
	}
	outer, e := a.ForgeBindings(ctx, GitTarget{WorkspaceID: ws.ID, RepoRoot: parent})
	if e != nil || len(outer.Candidates) != 2 {
		t.Fatalf("binding leaked: %+v %v", outer, e)
	}
	if _, e = a.GitWorkbenchStatus(ctx, GitTarget{WorkspaceID: "foreign", RepoRoot: nested}); e == nil {
		t.Fatal("foreign workspace read")
	}
	outside := t.TempDir()
	gitTest(t, outside, "init")
	if _, e = a.GitWorkbenchStatus(ctx, GitTarget{WorkspaceID: ws.ID, RepoRoot: outside}); e == nil {
		t.Fatal("outside repo read")
	}
	token, ok := a.mcp().secrets.Get(one.SecretRef)
	if !ok || token != "token-one" {
		t.Fatal("wrong token")
	}
	if e = a.UnlockForgeSecrets(ctx, map[string]string{"foreign": "oops"}); e == nil {
		t.Fatal("foreign secret unlock")
	}
	two.Enabled = false
	if _, e = a.SaveForgeConnection(ctx, ForgeConnectionInput{Connection: two}); e != nil {
		t.Fatal(e)
	}
	b, e = a.ForgeBindings(ctx, target)
	if e != nil || len(b.Candidates) != 0 {
		t.Fatalf("disabled binding: %+v %v", b, e)
	}
}
func TestGitSetupDoesNotOverwriteExisting(t *testing.T) {
	a := newTestApp(t)
	root := t.TempDir()
	if _, e := a.OpenWorkspace(root); e != nil {
		t.Fatal(e)
	}
	ws, _ := a.requireWorkspace()
	ctx := context.Background()
	if _, e := a.GitSetup(ctx, GitSetupInput{WorkspaceID: ws.ID, Action: "init", Name: "new-repo"}); e != nil {
		t.Fatal(e)
	}
	if _, e := a.GitSetup(ctx, GitSetupInput{WorkspaceID: ws.ID, Action: "init", Name: "new-repo"}); e == nil {
		t.Fatal("existing repo overwritten")
	}
	if _, e := a.GitSetup(ctx, GitSetupInput{WorkspaceID: ws.ID, Action: "init", Name: "../escape"}); e == nil {
		t.Fatal("escaped workspace")
	}
}

func TestForgeLastConnectionIsEmptyArray(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	connection, e := a.SaveForgeConnection(ctx, ForgeConnectionInput{Connection: forge.Connection{Provider: "gitlab", URL: "https://gitlab.example.test", Name: "last", Enabled: true}, Token: "secret"})
	if e != nil {
		t.Fatal(e)
	}
	if e = a.DeleteForgeConnection(ctx, connection.ID); e != nil {
		t.Fatal(e)
	}
	connections, e := a.ForgeConnections(ctx)
	if e != nil || connections == nil || len(connections) != 0 {
		t.Fatalf("empty connections: %v %v", connections, e)
	}
}
