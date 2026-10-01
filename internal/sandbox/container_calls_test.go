package sandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// Помощник-процесс: печатает то, что велели, и выходит с нужным кодом. Так
// тест подменяет docker CLI, не требуя настоящего демона.
func TestSandboxDockerHelperProcess(t *testing.T) {
	// Окружение docker CLI бэкенд задаёт сам (dockerClientEnvironment), поэтому
	// поручение помощнику идёт аргументами после "--".
	for i, arg := range os.Args {
		if arg == "--point-docker-helper" {
			if i+1 < len(os.Args) {
				fmt.Print(os.Args[i+1])
			}
			os.Exit(0)
		}
	}
}

func helperDocker(ctx context.Context, output string) *exec.Cmd {
	return exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSandboxDockerHelperProcess$", "--", "--point-docker-helper", output)
}

// Запись песочницы хранит дайджест образа, и раньше каждая команда начиналась
// с docker image inspect. Дайджест неизменен: проверить его один раз хватает.
func TestExecutionImageDigestIsInspectedOnce(t *testing.T) {
	root := t.TempDir()
	backend := NewContainerBackend(filepath.Join(t.TempDir(), "sandboxes"))
	backend.Image = "point-agent-sandbox:release"
	backend.ImageDigest = "sha256:" + strings.Repeat("a", 64)
	pack := "sha256:" + strings.Repeat("b", 64)
	var inspects atomic.Int32
	var lastRun atomic.Value
	backend.command = func(ctx context.Context, _ string, args ...string) *exec.Cmd {
		if len(args) > 0 && args[0] == "run" {
			lastRun.Store(strings.Join(args, "\x00"))
		}
		if len(args) > 1 && args[0] == "image" && args[1] == "inspect" {
			inspects.Add(1)
			return helperDocker(ctx, pack)
		}
		return helperDocker(ctx, "")
	}
	for i := 0; i < 3; i++ {
		prepared, err := backend.PrepareProcess(context.Background(), ProcessRequest{
			WorkspaceRoot: root, WorkingDirectory: root, ShellCommand: "true", Image: pack, NetworkPolicy: "DENY",
		})
		if err != nil {
			t.Fatal(err)
		}
		run, _ := lastRun.Load().(string)
		if prepared.Command == nil || !strings.Contains(run, "\x00"+pack+"\x00") {
			t.Fatalf("process did not run on the pinned image: %q", run)
		}
	}
	if inspects.Load() != 1 {
		t.Fatalf("image digest inspected %d times, want 1", inspects.Load())
	}
	// Дайджест самого базового образа не проверяется вовсе.
	if _, err := backend.PrepareProcess(context.Background(), ProcessRequest{
		WorkspaceRoot: root, WorkingDirectory: root, ShellCommand: "true", Image: backend.ImageDigest, NetworkPolicy: "DENY",
	}); err != nil || inspects.Load() != 1 {
		t.Fatalf("base digest re-inspected: err=%v inspects=%d", err, inspects.Load())
	}
}

// docker run --rm убирает контейнер сам. rm --force нужен только тогда, когда
// клиента убили по сроку: иначе это лишний вызов docker CLI на каждую команду.
func TestCleanupSkipsRemovalAfterNormalExit(t *testing.T) {
	root := t.TempDir()
	backend := NewContainerBackend(filepath.Join(t.TempDir(), "sandboxes"))
	backend.Image = "point-agent-sandbox:release"
	backend.ImageDigest = "sha256:" + strings.Repeat("a", 64)
	var removals atomic.Int32
	backend.command = func(ctx context.Context, _ string, args ...string) *exec.Cmd {
		if len(args) > 0 && args[0] == "rm" {
			removals.Add(1)
		}
		return helperDocker(ctx, "")
	}
	ctx, cancel := context.WithCancel(context.Background())
	prepared, err := backend.PrepareProcess(ctx, ProcessRequest{WorkspaceRoot: root, WorkingDirectory: root, ShellCommand: "true", NetworkPolicy: "DENY"})
	if err != nil {
		t.Fatal(err)
	}
	if err = prepared.Command.Run(); err != nil {
		t.Fatal(err)
	}
	if err = prepared.Cleanup(context.Background()); err != nil || removals.Load() != 0 {
		t.Fatalf("normal exit still removed the container: err=%v removals=%d", err, removals.Load())
	}

	// Срок вышел: контейнер мог пережить убитого клиента — убираем.
	killed, err := backend.PrepareProcess(ctx, ProcessRequest{WorkspaceRoot: root, WorkingDirectory: root, ShellCommand: "true", NetworkPolicy: "DENY"})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	_ = killed.Cleanup(context.Background())
	if removals.Load() != 1 {
		t.Fatalf("cancelled process was not removed: removals=%d", removals.Load())
	}
}

// Каждый этап квеста заново пробовал образ отдельным docker run. Образ-дайджест
// неизменен: удачная проба повторяться не должна.
func TestRuntimeProbesRunOncePerImageDigest(t *testing.T) {
	backend := NewContainerBackend(filepath.Join(t.TempDir(), "sandboxes"))
	backend.Image = "point-agent-sandbox:release"
	backend.ImageDigest = "sha256:" + strings.Repeat("a", 64)
	var probes atomic.Int32
	backend.command = func(ctx context.Context, _ string, args ...string) *exec.Cmd {
		if len(args) > 0 && args[0] == "run" {
			probes.Add(1)
		}
		return helperDocker(ctx, "node v20.19.1")
	}
	runtime := RuntimeRequirements{RequiredCommands: []string{"node", "npm"}, ToolVersions: map[string]string{"node": "20"}}
	for i := 0; i < 3; i++ {
		if _, _, err := backend.resolveRuntimeImage(context.Background(), backend.ImageDigest, runtime); err != nil {
			t.Fatal(err)
		}
	}
	if probes.Load() != 2 {
		t.Fatalf("runtime probes ran %d times over three stages, want 2 (commands + version once)", probes.Load())
	}
}

// Кэши квеста переживают команды; приёмке достаются только те, что менеджер
// пакетов сверяет с lock-файлом.
func TestQuestCachesMountOnceAndStayOutOfAuthoritativeRuns(t *testing.T) {
	root := t.TempDir()
	backend := NewContainerBackend(filepath.Join(t.TempDir(), "sandboxes"))
	backend.Image = "point-agent-sandbox:release"
	backend.ImageDigest = "sha256:" + strings.Repeat("a", 64)
	var infra []string
	backend.infrastructure = func(_ context.Context, args ...string) error {
		infra = append(infra, strings.Join(args, " "))
		return nil
	}
	prepare := func(authoritative bool) string {
		prepared, err := backend.PrepareProcess(context.Background(), ProcessRequest{
			WorkspaceRoot: root, WorkingDirectory: root, ShellCommand: "go build ./...", NetworkPolicy: "DENY",
			CacheScope: "quest-1", Authoritative: authoritative,
		})
		if err != nil {
			t.Fatal(err)
		}
		return strings.Join(prepared.Command.Args, " ")
	}
	writer := prepare(false)
	_ = prepare(false)
	for _, want := range []string{"/cache/npm:rw", "/cache/gomod:rw", "/cache/gobuild:rw", "/cache/pip:rw", "GOCACHE=/cache/gobuild", "NPM_CONFIG_CACHE=/cache/npm"} {
		if !strings.Contains(writer, want) {
			t.Fatalf("writer run misses %q: %s", want, writer)
		}
	}
	if strings.Contains(writer, "GOCACHE=/tmp/go-cache") {
		t.Fatalf("tmpfs cache left alongside the quest cache: %s", writer)
	}
	creates := 0
	for _, call := range infra {
		if strings.HasPrefix(call, "volume create") {
			creates++
		}
	}
	if creates != 4 {
		t.Fatalf("cache volumes created %d times over two commands, want 4:\n%s", creates, strings.Join(infra, "\n"))
	}
	judge := prepare(true)
	if strings.Contains(judge, "/cache/gobuild") || strings.Contains(judge, "/cache/pip") || !strings.Contains(judge, "GOCACHE=/tmp/go-cache") {
		t.Fatalf("authoritative run got a writable unverified cache: %s", judge)
	}
	if !strings.Contains(judge, "/cache/npm:rw") {
		t.Fatalf("authoritative run lost the verified npm cache: %s", judge)
	}
	plain, err := backend.PrepareProcess(context.Background(), ProcessRequest{WorkspaceRoot: root, WorkingDirectory: root, ShellCommand: "true", NetworkPolicy: "DENY"})
	if err != nil || strings.Contains(strings.Join(plain.Command.Args, " "), "/cache/") {
		t.Fatalf("run outside a quest mounted a quest cache: %v", err)
	}
}
