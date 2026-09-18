package sandbox_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	command := strings.Join([]string{
		"set -eu",
		`test "$(id -u)" = "10001"`,
		`test "$(id -g)" = "10001"`,
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

func strconvShell(path string) string {
	return "'" + strings.ReplaceAll(filepath.ToSlash(path), "'", "'\\''") + "'"
}
