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

func TestGitBranchesListsLocalBranchesAndMarksCurrent(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	root := t.TempDir()
	runGitIn := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Point", "GIT_AUTHOR_EMAIL=point@local",
			"GIT_COMMITTER_NAME=Point", "GIT_COMMITTER_EMAIL=point@local",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, strings.TrimSpace(string(out)))
		}
	}
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitIn("init")
	runGitIn("add", "tracked.txt")
	runGitIn("commit", "-m", "init")
	runGitIn("branch", "feature/webhook-retry")
	runGitIn("branch", "hotfix/timeout")

	fs, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	result := (GitBranches{FS: fs}).Execute(context.Background(), json.RawMessage(`{}`))
	if !result.OK {
		t.Fatalf("git_branches failed: %#v", result)
	}
	var payload struct {
		Current  string `json:"current"`
		Branches []struct {
			Name    string `json:"name"`
			Kind    string `json:"kind"`
			Current bool   `json:"current"`
		} `json:"branches"`
		Note string `json:"note"`
	}
	if err := json.Unmarshal(result.Output, &payload); err != nil {
		t.Fatalf("выдача не разбирается: %v (%s)", err, result.Output)
	}
	if payload.Current == "" {
		t.Fatalf("текущая ветка не названа: %s", result.Output)
	}
	names := make([]string, 0, len(payload.Branches))
	marked := 0
	for _, branch := range payload.Branches {
		names = append(names, branch.Name)
		if branch.Current {
			marked++
		}
		if branch.Kind != "local" {
			t.Fatalf("в пустом репозитории без remote появилась ветка %q вида %q", branch.Name, branch.Kind)
		}
	}
	for _, want := range []string{"feature/webhook-retry", "hotfix/timeout", payload.Current} {
		if !strings.Contains(strings.Join(names, ","), want) {
			t.Fatalf("ветка %q не попала в список: %v", want, names)
		}
	}
	if marked != 1 {
		t.Fatalf("текущей помечено %d веток вместо одной: %v", marked, names)
	}
	// Оговорка про давность обязана ехать вместе с числами: без неё «отстаём
	// на 0» читается как «на сервере ничего нет», а git туда не ходил.
	if !strings.Contains(payload.Note, "last fetch") {
		t.Fatalf("оговорка о давности потеряна: %q", payload.Note)
	}
}

func TestGitBranchesFiltersByContains(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	root := t.TempDir()
	runGitIn := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Point", "GIT_AUTHOR_EMAIL=point@local",
			"GIT_COMMITTER_NAME=Point", "GIT_COMMITTER_EMAIL=point@local",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, strings.TrimSpace(string(out)))
		}
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitIn("init")
	runGitIn("add", "a.txt")
	runGitIn("commit", "-m", "init")
	runGitIn("branch", "feature/one")
	runGitIn("branch", "release/two")

	fs, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	result := (GitBranches{FS: fs}).Execute(context.Background(), json.RawMessage(`{"contains":"FEATURE"}`))
	if !result.OK {
		t.Fatalf("git_branches failed: %#v", result)
	}
	text := string(result.Output)
	if !strings.Contains(text, "feature/one") {
		t.Fatalf("отбор регистронезависимый, ветка потеряна: %s", text)
	}
	if strings.Contains(text, "release/two") {
		t.Fatalf("отбор не применился: %s", text)
	}
}
