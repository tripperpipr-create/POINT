package sandbox

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestContainerBackendPreparesLockedDownProcess(t *testing.T) {
	root := t.TempDir()
	workdir := filepath.Join(root, "nested")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatal(err)
	}
	backend := NewContainerBackend(filepath.Join(t.TempDir(), "sandboxes"))
	backend.Image = "point-agent-sandbox:test"
	backend.DockerVersion = "test-docker"
	backend.APIVersion = "1.47"
	backend.ImageDigest = "sha256:test-image"
	prepared, err := backend.PrepareProcess(context.Background(), ProcessRequest{
		WorkspaceRoot: root, WorkingDirectory: workdir, ShellCommand: "go test ./...",
		Environment:   []string{"CI=true", "OPENAI_API_KEY=must-not-leak", "PATH=/host/bin"},
		NetworkPolicy: "DENY",
	})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Command == nil || prepared.Cleanup == nil {
		t.Fatalf("incomplete prepared process: %#v", prepared)
	}
	args := strings.Join(prepared.Command.Args, "\x00")
	for _, required := range []string{
		"--network\x00none", "--read-only", "--cap-drop\x00ALL",
		"--security-opt\x00no-new-privileges=true", "--pids-limit\x00256",
		"--memory\x002g", "--cpus\x002", "--ipc\x00none",
		"--workdir\x00/workspace/nested", "CI=true",
		"/bin/sh\x00-lc\x00go test ./...",
	} {
		if !strings.Contains(args, required) {
			t.Fatalf("docker contract missing %q: %q", required, args)
		}
	}
	if !strings.Contains(args, filepath.Clean(root)+":/workspace:rw") {
		t.Fatalf("sandbox root was not the only workspace bind: %q", args)
	}
	for _, forbidden := range []string{"OPENAI_API_KEY", "must-not-leak", "PATH=/host/bin"} {
		if strings.Contains(args, forbidden) {
			t.Fatalf("container environment leaked %q: %q", forbidden, args)
		}
	}
	capabilities := backend.Capabilities()
	if !capabilities.StrongOSBoundary || !capabilities.ProcessIsolation || !capabilities.NetworkIsolation {
		t.Fatalf("container backend under-reported isolation: %#v", capabilities)
	}
	if capabilities.Version != "test-docker" || capabilities.APIVersion != "1.47" || capabilities.ImageDigest != "sha256:test-image" {
		t.Fatalf("container version attribution missing: %#v", capabilities)
	}
}

func TestContainerBackendExecutesProbedImmutableImageDigest(t *testing.T) {
	root := t.TempDir()
	backend := NewContainerBackend(filepath.Join(t.TempDir(), "sandboxes"))
	backend.Image = "point-agent-sandbox:mutable-tag"
	backend.ImageDigest = "sha256:" + strings.Repeat("a", 64)
	prepared, err := backend.PrepareProcess(context.Background(), ProcessRequest{
		WorkspaceRoot: root, WorkingDirectory: root, Program: "go", Arguments: []string{"version"}, NetworkPolicy: "DENY",
	})
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Join(prepared.Command.Args, "\x00")
	if !strings.Contains(args, backend.ImageDigest) || strings.Contains(args, backend.Image+"\x00") {
		t.Fatalf("execution was not pinned to probed digest: %q", args)
	}
}

func TestContainerBackendFailsClosedForUnenforceableEgressAndPathEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	backend := NewContainerBackend(filepath.Join(t.TempDir(), "sandboxes"))
	for name, request := range map[string]ProcessRequest{
		"unrestricted allow": {WorkspaceRoot: root, WorkingDirectory: root, Program: "go", Arguments: []string{"test", "./..."}, NetworkPolicy: "ALLOW"},
		"literal address":    {WorkspaceRoot: root, WorkingDirectory: root, Program: "go", Arguments: []string{"test", "./..."}, NetworkPolicy: "ALLOWLIST", AllowedNetworkHosts: []string{"127.0.0.1:443"}},
		"wildcard":           {WorkspaceRoot: root, WorkingDirectory: root, Program: "go", Arguments: []string{"test", "./..."}, NetworkPolicy: "ALLOWLIST", AllowedNetworkHosts: []string{"*.example.com:443"}},
		"path escape":        {WorkspaceRoot: root, WorkingDirectory: outside, Program: "go", Arguments: []string{"test", "./..."}, NetworkPolicy: "DENY"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := backend.PrepareProcess(context.Background(), request); err == nil {
				t.Fatal("unsafe container request was accepted")
			}
		})
	}
}

func TestContainerBackendPreparesControlledTLSAllowlist(t *testing.T) {
	root := t.TempDir()
	backend := NewContainerBackend(filepath.Join(t.TempDir(), "sandboxes"))
	backend.Image = "point-agent-sandbox:test"
	var infrastructureCalls [][]string
	backend.infrastructure = func(_ context.Context, args ...string) error {
		infrastructureCalls = append(infrastructureCalls, append([]string(nil), args...))
		return nil
	}
	backend.infrastructureOutput = func(_ context.Context, args ...string) (string, error) {
		infrastructureCalls = append(infrastructureCalls, append([]string(nil), args...))
		return "172.30.0.2", nil
	}

	prepared, err := backend.PrepareProcess(context.Background(), ProcessRequest{
		WorkspaceRoot: root, WorkingDirectory: root, Program: "npm", Arguments: []string{"view", "typescript", "version"},
		NetworkPolicy: "ALLOWLIST", AllowedNetworkHosts: []string{"registry.npmjs.org:443"}, RunID: "run-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Join(prepared.Command.Args, "\x00")
	for _, required := range []string{
		"--network\x00point-egress-net-", "HTTP_PROXY=http://point-egress-gateway:8080",
		"HTTPS_PROXY=http://point-egress-gateway:8080", "POINT_EGRESS_POLICY_DIGEST=sha256:",
		"--add-host\x00point-egress-gateway:172.30.0.2",
		"--add-host\x00registry.npmjs.org:",
	} {
		if !strings.Contains(args, required) {
			t.Fatalf("controlled egress command missing %q: %q", required, args)
		}
	}
	if strings.Contains(args, "--dns\x00127.0.0.1") {
		t.Fatalf("tool container must not use broken localhost DNS: %q", args)
	}
	if strings.Contains(args, "--network\x00none") {
		t.Fatalf("allowlisted process was left on deny-all network: %q", args)
	}
	joinedCalls := make([]string, 0, len(infrastructureCalls))
	for _, call := range infrastructureCalls {
		joinedCalls = append(joinedCalls, strings.Join(call, "\x00"))
	}
	joined := strings.Join(joinedCalls, "\n")
	for _, required := range []string{
		"network\x00create\x00--internal\x00--driver\x00bridge\x00point-egress-net-",
		"run\x00--detach\x00--rm\x00--pull\x00never",
		"--network-alias\x00point-egress-gateway", "--read-only\x00--cap-drop\x00ALL",
		"--memory\x00128m\x00--cpus\x000.25", "point-egress-gateway\x00serve",
		"--run-id\x00run-test", "network\x00connect\x00bridge\x00point-egress-gateway-",
		"inspect\x00--format",
		"exec\x00point-egress-gateway-",
	} {
		if !strings.Contains(joined, required) {
			t.Fatalf("gateway infrastructure missing %q:\n%s", required, joined)
		}
	}

	backend.command = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, os.Args[0], "-test.run=^$")
	}
	if err = prepared.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	joined = ""
	for _, call := range infrastructureCalls {
		joined += strings.Join(call, "\x00") + "\n"
	}
	if !strings.Contains(joined, "rm\x00--force\x00point-egress-gateway-") || !strings.Contains(joined, "network\x00rm\x00point-egress-net-") {
		t.Fatalf("controlled egress infrastructure was not cleaned up:\n%s", joined)
	}
}

func TestContainerBackendRollsBackPartialEgressInfrastructure(t *testing.T) {
	root := t.TempDir()
	backend := NewContainerBackend(filepath.Join(t.TempDir(), "sandboxes"))
	var calls []string
	backend.infrastructure = func(_ context.Context, args ...string) error {
		joined := strings.Join(args, "\x00")
		calls = append(calls, joined)
		if len(args) > 0 && args[0] == "run" {
			return errors.New("gateway start failed")
		}
		return nil
	}
	_, err := backend.PrepareProcess(context.Background(), ProcessRequest{
		WorkspaceRoot: root, WorkingDirectory: root, Program: "npm", Arguments: []string{"view", "typescript"},
		NetworkPolicy: "ALLOWLIST", AllowedNetworkHosts: []string{"registry.npmjs.org:443"},
	})
	if err == nil || !strings.Contains(err.Error(), "gateway start failed") {
		t.Fatalf("gateway setup failure=%v", err)
	}
	joined := strings.Join(calls, "\n")
	if !strings.Contains(joined, "rm\x00--force\x00point-egress-gateway-") || !strings.Contains(joined, "network\x00rm\x00point-egress-net-") {
		t.Fatalf("partial gateway infrastructure was not rolled back:\n%s", joined)
	}
}

func TestContainerBackendAttributesCreatedSandbox(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "go.mod"), []byte("module fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	backend := NewContainerBackend(filepath.Join(t.TempDir(), "sandboxes"))
	backend.DockerVersion = "27.1.0"
	backend.Image = "point-agent-sandbox:release"
	backend.ImageDigest = "sha256:immutable"
	record, err := backend.Create(context.Background(), CreateRequest{WorkspaceID: "ws", WorkspacePath: workspace, ExecutionID: "exec"})
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close(context.Background(), record, workspace)
	if record.Backend != "docker" || record.BackendVersion != "27.1.0" || record.BackendImageDigest != "sha256:immutable" {
		t.Fatalf("sandbox attribution=%#v", record)
	}
}

func TestBackendSelectionCannotDowngradeRequiredIsolation(t *testing.T) {
	t.Setenv("POINT_SANDBOX_BACKEND", "local")
	t.Setenv("POINT_SANDBOX_REQUIRE_STRONG", "true")
	if _, err := BackendFromEnvironment(filepath.Join(t.TempDir(), "sandboxes")); err == nil {
		t.Fatal("required strong isolation silently downgraded to the host backend")
	}
	t.Setenv("POINT_SANDBOX_REQUIRE_STRONG", "false")
	backend, err := BackendFromEnvironment(filepath.Join(t.TempDir(), "sandboxes"))
	if err != nil {
		t.Fatal(err)
	}
	if backend.Capabilities().StrongOSBoundary {
		t.Fatal("local compatibility backend claimed a strong OS boundary")
	}
}
