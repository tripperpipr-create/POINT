package sandbox_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/environment"
	"local-agent-workbench/internal/sandbox"
	"local-agent-workbench/internal/tools"
	"local-agent-workbench/internal/workspace"
)

func TestDockerSandboxIntegration(t *testing.T) {
	if os.Getenv("POINT_SANDBOX_DOCKER_TEST") != "1" {
		t.Skip("set POINT_SANDBOX_DOCKER_TEST=1 after building the sandbox image")
	}
	root := t.TempDir()
	outside := t.TempDir()
	outsideMarker := filepath.Join(outside, "host-secret.txt")
	if err := os.WriteFile(outsideMarker, []byte("not visible"), 0o600); err != nil {
		t.Fatal(err)
	}
	fs, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	backend := sandbox.NewContainerBackend(filepath.Join(t.TempDir(), "sandboxes"))
	if image := strings.TrimSpace(os.Getenv("POINT_SANDBOX_IMAGE")); image != "" {
		backend.Image = image
	}
	backend.MemoryLimit = "128m"
	backend.CPULimit = "0.5"
	backend.PIDsLimit = 32
	if err = backend.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POINT_HOST_SECRET", "must-not-enter-container")
	// Ожидаемый пользователь берётся у бэкенда, а не пишется числом.
	//
	// Числом здесь стояло 10001 — пользователь из образа, — и на Windows это
	// совпадало: там defaultContainerUser всегда отдаёт 10001. На Linux он
	// намеренно отдаёт uid хоста, иначе контейнер не смог бы писать в
	// bind-mount рабочей папки: владелец каталога сохраняется. Проверка с
	// литералом падала на первой же строке у любого нерутового пользователя —
	// и падала не на дефекте, а на собственном допущении.
	//
	// Гарантия при этом не ослабевает: сверяется ровно то, что бэкенд
	// запросил, и отдельно — что это не root. Остальные рубежи (пустой CapEff,
	// no-new-privs, read-only корень) проверяются ниже как прежде.
	expectedUID, expectedGID, found := strings.Cut(backend.User, ":")
	if !found || expectedUID == "" || expectedGID == "" {
		t.Fatalf("бэкенд не назвал пользователя контейнера: %q", backend.User)
	}
	command := strings.Join([]string{
		"set -eu",
		fmt.Sprintf(`test "$(id -u)" = %q`, expectedUID),
		fmt.Sprintf(`test "$(id -g)" = %q`, expectedGID),
		`test "$(id -u)" != "0"`,
		`test "$(id -g)" != "0"`,
		`test "$(hostname)" = "point-sandbox"`,
		`awk '$2 == "/" { print $4 }' /proc/mounts | tr ',' '\n' | grep -qx ro`,
		`grep -Eq '^CapEff:[[:space:]]+0+$' /proc/self/status`,
		`grep -Eq '^NoNewPrivs:[[:space:]]+1$' /proc/self/status`,
		`tr '\000' ' ' < /proc/1/cmdline | grep -q '/bin/sh'`,
		`test -z "${POINT_HOST_SECRET+x}"`,
		`for tool in node npm go python3 git rg gcc g++ make; do command -v "$tool" >/dev/null; done`,
		`test "$(node --version)" = "v24.20.0"`,
		`test "$(npm --version)" = "12.0.2"`,
		`go version | grep -Eq '^go version go1\.26\.7 linux/'`,
		`python3 --version | grep -qx 'Python 3.13.15'`,
		`if test -f /sys/fs/cgroup/memory.max; then ` +
			`test "$(cat /sys/fs/cgroup/memory.max)" = "134217728"; ` +
			`test "$(cat /sys/fs/cgroup/pids.max)" = "32"; ` +
			`test "$(cat /sys/fs/cgroup/cpu.max)" = "50000 100000"; ` +
			`else ` +
			`test "$(cat /sys/fs/cgroup/memory/memory.limit_in_bytes)" = "134217728"; ` +
			`test "$(cat /sys/fs/cgroup/pids/pids.max)" = "32"; ` +
			`test "$(cat /sys/fs/cgroup/cpu/cpu.cfs_quota_us)" = "50000"; ` +
			`test "$(cat /sys/fs/cgroup/cpu/cpu.cfs_period_us)" = "100000"; fi`,
		"test ! -e " + strconvShell(outsideMarker),
		"printf isolated > result.txt",
	}, "; ")
	payload, _ := json.Marshal(map[string]any{"command": command, "reason": "verify container filesystem boundary", "timeoutSeconds": 30})
	result := (tools.RunCommand{FS: fs, Executor: backend, NetworkPolicy: "DENY"}).Execute(context.Background(), payload)
	if !result.OK || !strings.Contains(string(result.Output), `"exitCode":0`) {
		t.Fatalf("isolated command failed: %#v", result)
	}
	written, err := os.ReadFile(filepath.Join(root, "result.txt"))
	if err != nil || string(written) != "isolated" {
		t.Fatalf("workspace bind write=%q err=%v", written, err)
	}

	networkPayload, _ := json.Marshal(map[string]any{
		"command": "python3 -c \"import socket; socket.create_connection(('1.1.1.1', 53), 1)\"",
		"reason":  "verify deny-all egress", "timeoutSeconds": 10,
	})
	networkResult := (tools.RunCommand{FS: fs, Executor: backend, NetworkPolicy: "DENY"}).Execute(context.Background(), networkPayload)
	if !networkResult.OK || strings.Contains(string(networkResult.Output), `"exitCode":0`) {
		t.Fatalf("network namespace allowed outbound connection: %#v", networkResult)
	}

	allowlist := []string{"registry.npmjs.org:443"}
	allowedPayload, _ := json.Marshal(map[string]any{
		"command": `python3 -c "import urllib.request; r=urllib.request.urlopen('https://registry.npmjs.org/-/ping', timeout=15); assert 200 <= r.status < 400"`,
		"reason":  "verify exact allowlisted TLS egress", "timeoutSeconds": 30,
	})
	allowedResult := (tools.RunCommand{
		FS: fs, Executor: backend, NetworkPolicy: "ALLOWLIST", AllowedNetworkHosts: allowlist, RunID: "integration-egress-allow",
	}).Execute(context.Background(), allowedPayload)
	if !allowedResult.OK || !strings.Contains(string(allowedResult.Output), `"exitCode":0`) {
		t.Fatalf("exact allowlisted TLS egress failed: %#v", allowedResult)
	}

	for name, blockedCommand := range map[string]string{
		"unlisted fqdn": `python3 -c "import urllib.request; urllib.request.urlopen('https://example.com/', timeout=5)"`,
		"plain http":    `python3 -c "import urllib.request; urllib.request.urlopen('http://registry.npmjs.org/', timeout=5)"`,
		"direct ip":     `python3 -c "import socket; socket.create_connection(('1.1.1.1', 443), 2)"`,
	} {
		t.Run(name, func(t *testing.T) {
			payload, _ := json.Marshal(map[string]any{"command": blockedCommand, "reason": "verify controlled egress deny", "timeoutSeconds": 15})
			result := (tools.RunCommand{
				FS: fs, Executor: backend, NetworkPolicy: "ALLOWLIST", AllowedNetworkHosts: allowlist, RunID: "integration-egress-deny",
			}).Execute(context.Background(), payload)
			if !result.OK || strings.Contains(string(result.Output), `"exitCode":0`) {
				t.Fatalf("controlled gateway allowed %s: %#v", name, result)
			}
		})
	}
}

func TestDockerRuntimePackIntegration(t *testing.T) {
	if os.Getenv("POINT_SANDBOX_DOCKER_TEST") != "1" {
		t.Skip("set POINT_SANDBOX_DOCKER_TEST=1 after building the sandbox images")
	}
	workspaceRoot := t.TempDir()
	backend := sandbox.NewContainerBackend(filepath.Join(t.TempDir(), "sandboxes"))
	if err := backend.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	runtime := environment.RuntimeRequirementsForWorkOrder(&domain.WorkOrder{
		Stack: domain.StackPresetRef{ID: "php-symfony-7", Version: "1"},
		Setup: domain.SetupPlan{Commands: []domain.SetupCommand{{Command: "composer install"}}},
	})
	// Exercise installation instead of the compatible prebuilt-image shortcut.
	runtime.CandidateImages = nil
	record, err := backend.Create(context.Background(), sandbox.CreateRequest{
		WorkspaceID: "runtime-ws", WorkspacePath: workspaceRoot, ExecutionID: "runtime-exec",
		Runtime: runtime,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close(context.Background(), record, workspaceRoot)
	if !strings.HasPrefix(record.BackendImage, "point-runtime:") || !strings.HasPrefix(record.BackendImageDigest, "sha256:") {
		t.Fatalf("runtime image attribution=%#v", record)
	}
	fs, err := workspace.Open(record.Path)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"command": "php --version && composer --version", "reason": "verify selected runtime", "timeoutSeconds": 30})
	result := (tools.RunCommand{FS: fs, Executor: backend, SandboxImage: sandbox.ExecutionImageForRecord(record), NetworkPolicy: "DENY"}).Execute(context.Background(), payload)
	if !result.OK || !strings.Contains(string(result.Output), `"exitCode":0`) {
		t.Fatalf("selected runtime did not execute required tools: %#v", result)
	}
}

func TestDockerComposerDistributionIntegration(t *testing.T) {
	if os.Getenv("POINT_SANDBOX_DOCKER_TEST") != "1" {
		t.Skip("set POINT_SANDBOX_DOCKER_TEST=1 with the PHP sandbox image available")
	}
	root := t.TempDir()
	fs, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	backend := sandbox.NewContainerBackend(filepath.Join(t.TempDir(), "sandboxes"))
	if err := backend.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{
		"command": `composer create-project symfony/skeleton:"7.*" . --no-interaction --prefer-dist`,
		"reason": "verify Composer archive hosts through controlled egress", "timeoutSeconds": 180,
	})
	result := (tools.RunCommand{
		FS: fs, Executor: backend, SandboxImage: "point-agent-sandbox-php:1.3.1",
		NetworkPolicy: "ALLOWLIST", AllowedNetworkHosts: []string{
			"repo.packagist.org", "packagist.org", "github.com", "api.github.com", "codeload.github.com", "raw.githubusercontent.com",
		}, RunID: "integration-composer-dist",
	}).Execute(context.Background(), payload)
	if !result.OK || !strings.Contains(string(result.Output), `"exitCode":0`) {
		var output struct{ Stderr string `json:"stderr"` }
		_ = json.Unmarshal(result.Output, &output)
		t.Fatalf("Composer distribution through controlled egress failed: %s (tool error: %v)", output.Stderr, result.Error)
	}
	if _, err := os.Stat(filepath.Join(root, "composer.json")); err != nil {
		t.Fatalf("Composer did not create the project: %v", err)
	}
	requirePayload, _ := json.Marshal(map[string]any{
		"command": "composer require symfony/orm-pack --no-interaction --prefer-dist",
		"reason": "verify approved Symfony ORM dependencies through controlled egress", "timeoutSeconds": 240,
	})
	result = (tools.RunCommand{
		FS: fs, Executor: backend, SandboxImage: "point-agent-sandbox-php:1.3.1",
		NetworkPolicy: "ALLOWLIST", AllowedNetworkHosts: []string{
			"repo.packagist.org", "packagist.org", "github.com", "api.github.com", "codeload.github.com", "raw.githubusercontent.com",
		}, RunID: "integration-composer-orm",
	}).Execute(context.Background(), requirePayload)
	if !result.OK || !strings.Contains(string(result.Output), `"exitCode":0`) {
		var output struct{ Stderr string `json:"stderr"` }
		_ = json.Unmarshal(result.Output, &output)
		t.Fatalf("Composer ORM dependencies through controlled egress failed: %s (tool error: %v)", output.Stderr, result.Error)
	}
}

func TestDockerPythonPackageManagerProvisioningIntegration(t *testing.T) {
	if os.Getenv("POINT_SANDBOX_DOCKER_TEST") != "1" {
		t.Skip("set POINT_SANDBOX_DOCKER_TEST=1 after building the sandbox images")
	}
	workspaceRoot := t.TempDir()
	backend := sandbox.NewContainerBackend(filepath.Join(t.TempDir(), "sandboxes"))
	if err := backend.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	record, err := backend.Create(context.Background(), sandbox.CreateRequest{
		WorkspaceID: "python-runtime-ws", WorkspacePath: workspaceRoot, ExecutionID: "python-runtime-exec",
		Runtime: sandbox.RuntimeRequirements{
			ID: "python-service", Version: "1", RequiredCommands: []string{"python3", "pip"},
			Packages: []string{"py3.13-pip=26.2.1-r1"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close(context.Background(), record, workspaceRoot)
	fs, err := workspace.Open(record.Path)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"command": "python3 --version && pip --version", "reason": "verify provisioned package manager", "timeoutSeconds": 30})
	result := (tools.RunCommand{FS: fs, Executor: backend, SandboxImage: sandbox.ExecutionImageForRecord(record), NetworkPolicy: "DENY"}).Execute(context.Background(), payload)
	if !result.OK || !strings.Contains(string(result.Output), `"exitCode":0`) {
		t.Fatalf("provisioned Python runtime did not execute required tools: %#v", result)
	}
}

func strconvShell(path string) string {
	return "'" + strings.ReplaceAll(filepath.ToSlash(path), "'", "'\\''") + "'"
}
