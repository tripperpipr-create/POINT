package sandbox

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
)

func TestEmbeddedUnavailableNeverClaimsIsolationOrAllowsHostBind(t *testing.T) {
	t.Setenv("POINT_EMBEDDED_RUNTIME", filepath.Join(t.TempDir(), "missing-runtime.json"))
	b, err := NewEmbeddedBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	caps := b.Capabilities()
	if caps.Unavailable == "" || caps.ProcessIsolation || caps.NetworkIsolation || caps.StrongOSBoundary {
		t.Fatalf("unproven runtime claims isolation: %+v", caps)
	}
	for _, request := range []CreateRequest{{StorageMode: "bind"}, {LiveWorkspace: true}} {
		if _, err = b.Create(context.Background(), request); err == nil {
			t.Fatal("embedded host mount accepted")
		}
	}
	if err = b.RegisterWorkspace(domain.SandboxRecord{Path: t.TempDir(), Backend: "docker", StorageMode: "volume"}); err == nil {
		t.Fatal("foreign execution adopted")
	}
}

func TestEmbeddedTransportUsesOwnedGuestAndLinuxPrograms(t *testing.T) {
	e := &WSLEngine{Distribution: "point-owned-test", Engine: "podman"}
	cmd := e.rawCommand(context.Background(), "info", "--format", "{{.KernelVersion}}|{{.OperatingSystem}}")
	args := strings.Join(cmd.Args, " ")
	for _, value := range []string{"--distribution point-owned-test", "--exec /usr/bin/env -i", "/usr/bin/podman", ".Host.Kernel"} {
		if !strings.Contains(args, value) {
			t.Fatalf("missing transport invariant %q: %s", value, args)
		}
	}
	if strings.Contains(args, "/mnt/c") || strings.Contains(args, "--shutdown") {
		t.Fatal("host/global WSL access")
	}
}

func TestPodmanPreservesMountIsolationAndImageIdentity(t *testing.T) {
	e := &WSLEngine{Engine: "podman", Distribution: "point-owned-test"}
	cmd := e.rawCommand(context.Background(), "run", "--mount", "type=volume,src=point-tree,dst=/workspace,volume-nocopy", "--mount", "type=volume,src=point-tools,dst=/point-tools,readonly", "image", "printf", "volume-nocopy")
	args := strings.Join(cmd.Args, " ")
	if !strings.Contains(args, "--volume point-tree:/workspace:nocopy") || !strings.Contains(args, "dst=/point-tools,readonly") || !strings.HasSuffix(args, "printf volume-nocopy") {
		t.Fatalf("mount translation altered command/isolation: %s", args)
	}
	inspect := e.rawCommand(context.Background(), "image", "inspect", "image", "--format", "{{.Id}}")
	if inspect.Args[len(inspect.Args)-1] != "sha256:{{.Id}}" {
		t.Fatal("Podman execution ID must retain its config digest with SHA-256 prefix")
	}
}

func TestEmbeddedNeverResolvesBridgeFromProjectDirectory(t *testing.T) {
	t.Setenv("POINT_RUNTIME_BRIDGE", filepath.Join("project", "point-runtime.exe"))
	e := &WSLEngine{Engine: "moby"}
	if err := e.Command(context.Background(), "version").Run(); err == nil || !strings.Contains(err.Error(), "absolute trusted") {
		t.Fatalf("relative project bridge was not refused: %v", err)
	}
}
