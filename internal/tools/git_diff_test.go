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

func TestGitDiffIncludesStagedAndStatus(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("staged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitIn("add", "tracked.txt")
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("staged\nunstaged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "fresh.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fs, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	result := (GitDiff{FS: fs, MaxOutput: 64 * 1024}).Execute(context.Background(), json.RawMessage(`{}`))
	if !result.OK {
		t.Fatalf("git_diff failed: %#v", result)
	}
	text := string(result.Output)
	if !strings.Contains(text, `"base":"HEAD"`) {
		t.Fatalf("expected HEAD base: %s", text)
	}
	if !strings.Contains(text, "staged") || !strings.Contains(text, "unstaged") {
		t.Fatalf("expected staged+unstaged content in diff: %s", text)
	}
	if !strings.Contains(text, "fresh.txt") {
		t.Fatalf("expected untracked file in status: %s", text)
	}
}

func TestGitDiffFailsClearlyWithoutGitMetadata(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "readme.txt"), []byte("copy sandbox\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fs, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	result := (GitDiff{FS: fs}).Execute(context.Background(), json.RawMessage(`{}`))
	if result.OK || result.Error == nil || result.Error.Code != "git_unavailable" {
		t.Fatalf("expected git_unavailable, got %#v", result)
	}
}

func TestRunCommandDeniesDestructiveGit(t *testing.T) {
	fs, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tool := RunCommand{FS: fs}
	for _, command := range []string{
		"git push --force origin main",
		"git push -f",
		"git push --force-with-lease",
		"git reset --hard HEAD~1",
		"git clean -fd",
		"git clean -fxd",
		"git filter-branch --all",
	} {
		raw, _ := json.Marshal(map[string]any{"command": command, "reason": "denied git"})
		result := tool.Execute(context.Background(), raw)
		if result.OK || result.Error == nil || result.Error.Code != "command_denied" {
			t.Fatalf("destructive git accepted: %q => %#v", command, result)
		}
	}
}

func TestGitPushIsNetworkDeniedByDefault(t *testing.T) {
	if reason := deniedNetworkCommandReason("git push origin main", "DENY", nil); reason == "" {
		t.Fatal("git push must be treated as a network command under DENY")
	}
	if reason := deniedNetworkCommandReason("git status", "DENY", nil); reason != "" {
		t.Fatalf("local git status incorrectly denied: %s", reason)
	}
	if reason := deniedCommandReason("git commit -m ok"); reason != "" {
		t.Fatalf("local commit must remain approval-gated, not soft-denied: %s", reason)
	}
}

// Содержимое коммита, а не рабочего дерева. Помощник видит историю заголовками
// через git_log; без этого пути на вопрос «что изменилось в этих коммитах» он
// звал git_diff по кругу и получал один и тот же ответ про рабочую копию.
func TestGitDiffShowsCommitChanges(t *testing.T) {
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
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runGitIn("init")
	write("tracked.txt", "base\n")
	runGitIn("add", "tracked.txt")
	runGitIn("commit", "-m", "init")
	write("tracked.txt", "base\ncommitted line\n")
	runGitIn("add", "tracked.txt")
	runGitIn("commit", "-m", "add committed line")
	// Несохранённая правка обязана остаться за кадром: спрашивали про коммит.
	write("tracked.txt", "base\ncommitted line\nworktree line\n")

	fs, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	tool := GitDiff{FS: fs, MaxOutput: 64 * 1024}
	result := tool.Execute(context.Background(), json.RawMessage(`{"commit":"HEAD"}`))
	if !result.OK {
		t.Fatalf("git_diff по коммиту не сработал: %#v", result)
	}
	text := string(result.Output)
	if !strings.Contains(text, `"scope":"commit"`) {
		t.Fatalf("вывод не назвался коммитным: %s", text)
	}
	if !strings.Contains(text, "committed line") {
		t.Fatalf("в diff нет изменения коммита: %s", text)
	}
	if strings.Contains(text, "worktree line") {
		t.Fatalf("в diff коммита попала несохранённая правка: %s", text)
	}
	if !strings.Contains(text, "add committed line") {
		t.Fatalf("в ответе нет заголовка коммита: %s", text)
	}
	// Предыдущий коммит доступен по тому же пути: модель просит их по одному.
	previous := tool.Execute(context.Background(), json.RawMessage(`{"commit":"HEAD~1"}`))
	if !previous.OK || !strings.Contains(string(previous.Output), "\"subject\":\"init\"") {
		t.Fatalf("HEAD~1 не показан: %#v", previous)
	}
}

func TestGitDiffRefusesUnsafeOrUnknownRevision(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	root := t.TempDir()
	cmd := exec.Command("git", "-C", root, "init")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	fs, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	tool := GitDiff{FS: fs, MaxOutput: 64 * 1024}
	// Опция под видом ссылки: git читает их в одном и том же месте.
	unsafe := tool.Execute(context.Background(), json.RawMessage(`{"commit":"--upload-pack=touch"}`))
	if unsafe.OK || unsafe.Error == nil || unsafe.Error.Code != "invalid_revision" {
		t.Fatalf("опция принята за ревизию: %#v", unsafe)
	}
	// Диапазон — не один коммит, и молча разбирать его нельзя.
	span := tool.Execute(context.Background(), json.RawMessage(`{"commit":"HEAD~2..HEAD"}`))
	if span.OK || span.Error == nil || span.Error.Code != "invalid_revision" {
		t.Fatalf("диапазон принят за один коммит: %#v", span)
	}
	// Несуществующая ссылка отвечает отказом, а не пустым diff: пустой diff
	// модель прочитает как «изменений нет».
	missing := tool.Execute(context.Background(), json.RawMessage(`{"commit":"0123456789abcdef0123456789abcdef01234567"}`))
	if missing.OK || missing.Error == nil || missing.Error.Code != "unknown_revision" {
		t.Fatalf("несуществующая ревизия не названа: %#v", missing)
	}
}
