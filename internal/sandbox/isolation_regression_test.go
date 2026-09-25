package sandbox_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"local-agent-workbench/internal/changesets"
	"local-agent-workbench/internal/sandbox"
	"local-agent-workbench/internal/storage"
)

func gitRepository(t *testing.T, files map[string]string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	workspace := t.TempDir()
	for name, content := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(workspace, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(workspace, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", workspace}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Point", "GIT_AUTHOR_EMAIL=point@local", "GIT_COMMITTER_NAME=Point", "GIT_COMMITTER_EMAIL=point@local")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
	}
	run("init")
	run("add", "-A")
	run("commit", "-m", "init")
	return workspace
}

func junction(t *testing.T, link, target string) {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("junction is a Windows reparse point")
	}
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Skipf("mklink /J unavailable: %v (%s)", err, out)
	}
}

// Закрытие worktree не уходит по junction наружу. `git worktree remove
// --force` проходил по ссылке и удалял содержимое её цели — файлы человека
// вне проекта; закрытие идёт и молча, при уборке через 30 дней.
func TestClosingWorktreeDoesNotFollowJunctionOutside(t *testing.T) {
	workspace := gitRepository(t, map[string]string{"hello.txt": "hello"})
	outside := t.TempDir()
	victim := filepath.Join(outside, "victim.txt")
	if err := os.WriteFile(victim, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager := &sandbox.Manager{Root: filepath.Join(t.TempDir(), "sandboxes")}
	record, err := manager.Create(context.Background(), sandbox.CreateRequest{WorkspaceID: "ws", WorkspacePath: workspace, ExecutionID: "exec-junction", PreferWorktree: true})
	if err != nil || record.Kind != "worktree" {
		t.Fatalf("worktree: %#v err=%v", record, err)
	}
	junction(t, filepath.Join(record.Path, "out"), outside)
	if err = manager.Close(context.Background(), record, workspace); err != nil {
		t.Fatal(err)
	}
	if data, readErr := os.ReadFile(victim); readErr != nil || string(data) != "keep me" {
		t.Fatalf("закрытие песочницы удалило файл вне проекта: %q err=%v", data, readErr)
	}
	out, err := exec.Command("git", "-C", workspace, "worktree", "list").CombinedOutput()
	if err != nil || strings.Count(strings.TrimSpace(string(out)), "\n") != 0 {
		t.Fatalf("worktree остался зарегистрирован: %s err=%v", out, err)
	}
}

// Секреты не попадают в рабочие копии — ни в копию, ни в worktree, куда
// Git приносит отслеживаемые .env и ключи вместе с checkout. И их удаление из
// копии не становится удалением в проекте.
func TestSecretsStayOutOfSandboxes(t *testing.T) {
	secrets := map[string]string{".env": "A=1", ".npmrc": "//registry/:_authToken=x", "credentials": "k", "id_rsa": "key", "server.key": "k", "cert.pfx": "p", "id_ed25519_sk": "fido", "deploy_id_rsa": "key", "id_rsa.txt": "key", "build/certs/server.key": "k", ".vscode/.env": "B=2"}
	files := map[string]string{"main.go": "package main"}
	for name, content := range secrets {
		files[name] = content
	}
	workspace := gitRepository(t, files)
	manager := &sandbox.Manager{Root: filepath.Join(t.TempDir(), "sandboxes")}
	for _, worktree := range []bool{false, true} {
		record, err := manager.Create(context.Background(), sandbox.CreateRequest{WorkspaceID: "ws", WorkspacePath: workspace, ExecutionID: "exec-secrets-" + map[bool]string{false: "copy", true: "worktree"}[worktree], PreferWorktree: worktree})
		if err != nil {
			t.Fatal(err)
		}
		for name := range secrets {
			if _, statErr := os.Stat(filepath.Join(record.Path, name)); statErr == nil {
				t.Fatalf("%s: секрет %s скопирован в песочницу", record.Kind, name)
			}
		}
		diff, err := manager.Diff(context.Background(), record.BaselinePath, record.Path)
		if err != nil || len(diff) != 0 {
			t.Fatalf("%s: удаление секретов из копии стало изменением: %#v err=%v", record.Kind, diff, err)
		}
		_ = manager.Close(context.Background(), record, workspace)
	}
	for name, content := range secrets {
		if data, err := os.ReadFile(filepath.Join(workspace, name)); err != nil || string(data) != content {
			t.Fatalf("секрет %s в проекте тронут: %q err=%v", name, data, err)
		}
	}
}

// Файл, который агент переписал в UTF-16 (так пишет `>` в PowerShell 5.1),
// перестаёт быть текстом. Раньше Diff считал его удалённым, и доставка
// стирала файл человека; теперь перенос останавливается.
func TestDiffRefusesTextFileThatBecameBinary(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "settings.txt"), []byte("x=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager := &sandbox.Manager{Root: filepath.Join(t.TempDir(), "sandboxes")}
	record, err := manager.Create(context.Background(), sandbox.CreateRequest{WorkspaceID: "ws", WorkspacePath: workspace, ExecutionID: "exec-utf16"})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(record.Path, "settings.txt"), []byte{0xFF, 0xFE, 'x', 0, '=', 0, '2', 0}, 0o644); err != nil {
		t.Fatal(err)
	}
	if diff, err := manager.Diff(context.Background(), record.BaselinePath, record.Path); err == nil {
		t.Fatalf("нетекстовый файл стал удалением: %#v", diff)
	}
}

// Junction внутри песочницы не роняет Diff и не копируется.
func TestJunctionInsideSandboxIsSkipped(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager := &sandbox.Manager{Root: filepath.Join(t.TempDir(), "sandboxes")}
	record, err := manager.Create(context.Background(), sandbox.CreateRequest{WorkspaceID: "ws", WorkspacePath: workspace, ExecutionID: "exec-junction-diff"})
	if err != nil {
		t.Fatal(err)
	}
	junction(t, filepath.Join(record.Path, "link"), t.TempDir())
	if err = os.WriteFile(filepath.Join(record.Path, "a.txt"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	diff, err := manager.Diff(context.Background(), record.BaselinePath, record.Path)
	if err != nil || len(diff) != 1 || diff[0].Path != "a.txt" {
		t.Fatalf("junction сломал Diff: %#v err=%v", diff, err)
	}
}

// «Взять sandbox» поверх файла, который человек создал сам, и затем откат:
// раньше откат удалял его файл, потому что считал, что до применения файла
// не было.
func TestRevertAfterKeepTheirsRestoresUserFile(t *testing.T) {
	workspace := t.TempDir()
	db, err := storage.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	manager := &sandbox.Manager{Root: filepath.Join(t.TempDir(), "sandboxes")}
	record, err := manager.Create(context.Background(), sandbox.CreateRequest{WorkspaceID: "ws", WorkspacePath: workspace, ExecutionID: "exec-keep-theirs"})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(record.Path, "notes.md"), []byte("agent notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	applier := changesets.Applier{Store: db}
	set, err := applier.BuildFromSandbox(context.Background(), changesets.BuildRequest{WorkspaceID: "ws", ExecutionID: "exec-keep-theirs", WorkspacePath: workspace, SandboxPath: record.Path})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(workspace, "notes.md"), []byte("my notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if result, applyErr := applier.Apply(context.Background(), workspace, set.ID); applyErr != nil || len(result.Conflicts) != 1 {
		t.Fatalf("ожидался конфликт: %#v err=%v", result, applyErr)
	}
	if _, err = applier.ResolveConflict(context.Background(), set.ID, changesets.ResolveRequest{Strategy: "keep_theirs", Path: "notes.md"}); err != nil {
		t.Fatal(err)
	}
	if _, err = applier.Apply(context.Background(), workspace, set.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = applier.Revert(context.Background(), workspace, set.ID); err != nil {
		t.Fatal(err)
	}
	if data, readErr := os.ReadFile(filepath.Join(workspace, "notes.md")); readErr != nil || string(data) != "my notes" {
		t.Fatalf("откат уничтожил файл человека: %q err=%v", data, readErr)
	}
}

// Удалённые из worktree секреты не видны Git: иначе `git_diff` показал бы
// модели их содержимое строками удаления.
func TestStrippedWorktreeSecretsStayInvisibleToGit(t *testing.T) {
	workspace := gitRepository(t, map[string]string{"main.go": "package main", ".env": "DB_PASS=hunter2", "id_rsa_parser.go": "package main"})
	manager := &sandbox.Manager{Root: filepath.Join(t.TempDir(), "sandboxes")}
	record, err := manager.Create(context.Background(), sandbox.CreateRequest{WorkspaceID: "ws", WorkspacePath: workspace, ExecutionID: "exec-skip-worktree", PreferWorktree: true})
	if err != nil || record.Kind != "worktree" {
		t.Fatalf("worktree: %#v err=%v", record, err)
	}
	defer manager.Close(context.Background(), record, workspace)
	out, err := exec.Command("git", "-C", record.Path, "diff", "HEAD").CombinedOutput()
	if err != nil || strings.Contains(string(out), "hunter2") || strings.TrimSpace(string(out)) != "" {
		t.Fatalf("удалённый секрет виден в git diff: %s err=%v", out, err)
	}
	if _, err = os.Stat(filepath.Join(record.Path, "id_rsa_parser.go")); err != nil {
		t.Fatalf("исходник с id_rsa в имени принят за ключ и удалён: %v", err)
	}
}

// Закрытие песочницы снимает запись только своего worktree: чужой worktree
// человека на отключённом носителе раньше исчезал вместе с ней через prune.
func TestClosingWorktreeKeepsUsersOtherWorktrees(t *testing.T) {
	workspace := gitRepository(t, map[string]string{"main.go": "package main"})
	other := filepath.Join(t.TempDir(), "detached-drive")
	if out, err := exec.Command("git", "-C", workspace, "worktree", "add", "--detach", other).CombinedOutput(); err != nil {
		t.Fatalf("worktree add: %v (%s)", err, out)
	}
	if err := os.RemoveAll(other); err != nil {
		t.Fatal(err)
	}
	manager := &sandbox.Manager{Root: filepath.Join(t.TempDir(), "sandboxes")}
	record, err := manager.Create(context.Background(), sandbox.CreateRequest{WorkspaceID: "ws", WorkspacePath: workspace, ExecutionID: "exec-other-worktree", PreferWorktree: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = manager.Close(context.Background(), record, workspace); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("git", "-C", workspace, "worktree", "list").CombinedOutput()
	if err != nil || !strings.Contains(strings.ToLower(filepath.ToSlash(string(out))), "detached-drive") {
		t.Fatalf("чужой worktree человека забыт: %s err=%v", out, err)
	}
}

// Переименование только регистром на Windows — тот же файл, а пустой файл,
// ставший бинарным, терять нечего: ни то, ни другое не валит перенос.
func TestDiffToleratesCaseRenameAndEmptyBinary(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("case-insensitive file system")
	}
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "Readme.md"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "app.db"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	manager := &sandbox.Manager{Root: filepath.Join(t.TempDir(), "sandboxes")}
	record, err := manager.Create(context.Background(), sandbox.CreateRequest{WorkspaceID: "ws", WorkspacePath: workspace, ExecutionID: "exec-case"})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(filepath.Join(record.Path, "Readme.md"), filepath.Join(record.Path, "README.md")); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(record.Path, "app.db"), []byte{0, 1, 2}, 0o644); err != nil {
		t.Fatal(err)
	}
	diff, err := manager.Diff(context.Background(), record.BaselinePath, record.Path)
	if err != nil {
		t.Fatalf("перенос остановлен ложно: %v", err)
	}
	for _, entry := range diff {
		if entry.Kind == "delete" {
			t.Fatalf("переименование регистром стало удалением: %#v", diff)
		}
	}
}

// Хук post-checkout проекта создаёт неотслеживаемый секрет, а фикстур-
// сертификатов в репозитории больше, чем помещается в командную строку.
// Ни то, ни другое не срывает создание рабочей копии.
func TestWorktreeSecretStrippingSurvivesHooksAndManyFiles(t *testing.T) {
	files := map[string]string{"main.go": "package main"}
	for i := 0; i < 1400; i++ {
		files[filepath.Join("testdata", "certs", strings.Repeat("c", 30)+string(rune('a'+i%26))+strings.Repeat("x", i/26)+".pem")] = "cert"
	}
	workspace := gitRepository(t, files)
	hook := filepath.Join(workspace, ".git", "hooks", "post-checkout")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho SECRET=1 > .env\necho ran > hook-ran.txt\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	manager := &sandbox.Manager{Root: filepath.Join(t.TempDir(), "sandboxes")}
	record, err := manager.Create(context.Background(), sandbox.CreateRequest{WorkspaceID: "ws", WorkspacePath: workspace, ExecutionID: "exec-hook", PreferWorktree: true})
	if err != nil {
		t.Fatalf("создание рабочей копии сорвано: %v", err)
	}
	defer manager.Close(context.Background(), record, workspace)
	if _, err = os.Stat(filepath.Join(record.Path, ".env")); err == nil {
		t.Fatal("секрет из хука остался в рабочей копии")
	}
	if record.Kind != "worktree" {
		t.Fatalf("worktree не создан, сработал запасной путь: %s", record.Kind)
	}
	out, diffErr := exec.Command("git", "-C", record.Path, "diff", "HEAD", "--stat").CombinedOutput()
	if diffErr != nil || strings.TrimSpace(string(out)) != "" {
		t.Fatalf("удалённые сертификаты видны git: %s err=%v", out, diffErr)
	}
}

// В индексе, пришедшем с Linux, `Keys/a.txt` и `keys/id_rsa` лежат в
// каталогах, различающихся регистром; Windows выкладывает их в один. Путь с
// диска сопоставляется с индексом без учёта регистра, и удалённый ключ не
// всплывает в `git diff`.
func TestStrippedSecretInCaseDifferingDirectoryStaysInvisible(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("case-insensitive checkout")
	}
	workspace := gitRepository(t, map[string]string{"main.go": "package main"})
	git := func(input string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", workspace}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Point", "GIT_AUTHOR_EMAIL=point@local", "GIT_COMMITTER_NAME=Point", "GIT_COMMITTER_EMAIL=point@local")
		if input != "" {
			cmd.Stdin = strings.NewReader(input)
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v (%s)", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	plain := git("plain", "hash-object", "-w", "--stdin")
	secret := git("SECRETKEY", "hash-object", "-w", "--stdin")
	git("", "update-index", "--add", "--cacheinfo", "100644,"+plain+",Keys/a.txt")
	git("", "update-index", "--add", "--cacheinfo", "100644,"+secret+",keys/id_rsa")
	git("", "commit", "-m", "linux layout")
	git("", "checkout", "--", ".")
	if status := git("", "status", "--porcelain"); status != "" {
		t.Skipf("проект не чист после checkout: %s", status)
	}
	manager := &sandbox.Manager{Root: filepath.Join(t.TempDir(), "sandboxes")}
	record, err := manager.Create(context.Background(), sandbox.CreateRequest{WorkspaceID: "ws", WorkspacePath: workspace, ExecutionID: "exec-case-dir", PreferWorktree: true})
	if err != nil || record.Kind != "worktree" {
		t.Fatalf("worktree: %#v err=%v", record, err)
	}
	defer manager.Close(context.Background(), record, workspace)
	out, err := exec.Command("git", "-C", record.Path, "diff", "HEAD").CombinedOutput()
	if err != nil || strings.Contains(string(out), "SECRETKEY") {
		t.Fatalf("ключ из каталога другого регистра виден в git diff: %s err=%v", out, err)
	}
}
