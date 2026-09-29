package tools

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"local-agent-workbench/internal/workspace"
)

// Папка «фронт cf»: в корне Git нет, в cf-pages и cf-vue-apps — есть.
func nestedGitWorkspace(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	root := t.TempDir()
	for _, name := range []string{"cf-pages", "cf-vue-apps"} {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		run := func(args ...string) {
			t.Helper()
			cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
			cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Point", "GIT_AUTHOR_EMAIL=point@local", "GIT_COMMITTER_NAME=Point", "GIT_COMMITTER_EMAIL=point@local")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v (%s)", args, err, out)
			}
		}
		run("init")
		if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(name+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		run("add", "README.md")
		run("commit", "-m", "init "+name)
	}
	// Зависимости не считаются проектами.
	if err := os.MkdirAll(filepath.Join(root, "cf-vue-apps", "node_modules", "dep", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestDiscoverGitReposFindsNestedProjects(t *testing.T) {
	root := nestedGitWorkspace(t)
	if got := strings.Join(DiscoverGitRepos(root), ","); got != "cf-pages,cf-vue-apps" {
		t.Fatalf("repositories=%q", got)
	}
	if gitWorkTreeAvailable(context.Background(), root) {
		t.Skip("temporary directory is inside a Git work tree")
	}
	if !GitAvailable(context.Background(), root) {
		t.Fatal("nested repositories not seen as available Git")
	}
}

func TestGitLogChoosesNestedRepository(t *testing.T) {
	root := nestedGitWorkspace(t)
	if gitWorkTreeAvailable(context.Background(), root) {
		t.Skip("temporary directory is inside a Git work tree")
	}
	fs, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	tool := GitLog{FS: fs}
	if description := tool.Definition().Description; !strings.Contains(description, "cf-pages, cf-vue-apps") {
		t.Fatalf("description does not name the repositories: %s", description)
	}
	// Без подсказки — отказ со списком, а не «не репозиторий».
	result := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if result.OK || result.Error == nil || result.Error.Code != "git_repo_required" || !strings.Contains(result.Error.Message, "cf-vue-apps") {
		t.Fatalf("ambiguous call: %#v", result.Error)
	}
	// Путь внутри проекта выбирает его репозиторий.
	result = tool.Execute(context.Background(), json.RawMessage(`{"path":"cf-vue-apps/README.md"}`))
	if !result.OK || !strings.Contains(string(result.Output), `"repo":"cf-vue-apps"`) || !strings.Contains(string(result.Output), "init cf-vue-apps") {
		t.Fatalf("path did not select its repository: %s %#v", result.Output, result.Error)
	}
	// Явный repo.
	result = tool.Execute(context.Background(), json.RawMessage(`{"repo":"cf-pages"}`))
	if !result.OK || !strings.Contains(string(result.Output), "init cf-pages") {
		t.Fatalf("explicit repo: %s %#v", result.Output, result.Error)
	}
	// Путь вне выбранного репозитория — понятный отказ.
	result = tool.Execute(context.Background(), json.RawMessage(`{"repo":"cf-pages","path":"cf-vue-apps/README.md"}`))
	if result.OK || result.Error == nil || result.Error.Code != "invalid_path" {
		t.Fatalf("path outside repo accepted: %#v", result)
	}
	for _, name := range []string{"git_diff", "git_branches", "git_tags"} {
		var raw json.RawMessage
		switch name {
		case "git_diff":
			raw = GitDiff{FS: fs}.Execute(context.Background(), json.RawMessage(`{"repo":"cf-pages"}`)).Output
		case "git_branches":
			raw = GitBranches{FS: fs}.Execute(context.Background(), json.RawMessage(`{"repo":"cf-pages"}`)).Output
		case "git_tags":
			raw = GitTags{FS: fs}.Execute(context.Background(), json.RawMessage(`{"repo":"cf-pages"}`)).Output
		}
		if !strings.Contains(string(raw), `"repo":"cf-pages"`) {
			t.Fatalf("%s did not work in the nested repository: %s", name, raw)
		}
	}
}
