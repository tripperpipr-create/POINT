package sandbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type RuntimeAsset struct {
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
}

// RuntimeManifest is a release/operator-supplied pack, outside project trees.
// Assets are local and digest-pinned; provisioning never executes a download URL.
type RuntimeManifest struct {
	Schema         int          `json:"schema"`
	Engine         string       `json:"engine"`
	EngineVersion  string       `json:"engineVersion"`
	License        string       `json:"license"`
	RootFS         RuntimeAsset `json:"rootfs"`
	Image          RuntimeAsset `json:"image"`
	ImageReference string       `json:"imageReference"`
	ImageDigest    string       `json:"imageDigest"`
}

var runtimeVersionPatternStrict = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:[-+][a-zA-Z0-9._-]+)?$`)
var runtimeAssetName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)

func ReadRuntimeManifest(path string) (RuntimeManifest, string, error) {
	var m RuntimeManifest
	raw, err := os.ReadFile(path)
	if err != nil {
		return m, "", err
	}
	if len(raw) > 64*1024 {
		return m, "", errors.New("runtime manifest exceeds 64 KiB")
	}
	if err = json.Unmarshal(raw, &m); err != nil {
		return m, "", err
	}
	if m.Schema != 1 || (m.Engine != "moby" && m.Engine != "podman") || !runtimeVersionPatternStrict.MatchString(m.EngineVersion) {
		return m, "", errors.New("unsupported runtime schema, engine or unpinned version")
	}
	if m.License != "Apache-2.0" || !imageReferencePattern.MatchString(m.ImageReference) || !imageDigestPattern.MatchString(m.ImageDigest) {
		return m, "", errors.New("runtime license/image attribution is invalid")
	}
	for _, asset := range []RuntimeAsset{m.RootFS, m.Image} {
		if !runtimeAssetName.MatchString(asset.File) || !imageDigestPattern.MatchString(asset.SHA256) {
			return m, "", errors.New("runtime asset must be a local basename with a SHA-256 digest")
		}
	}
	sum := sha256.Sum256(raw)
	return m, "sha256:" + hex.EncodeToString(sum[:]), nil
}

func RuntimeDistribution(m RuntimeManifest, digest string) string {
	if !imageDigestPattern.MatchString(digest) {
		return ""
	}
	return "point-runtime-" + m.Engine + "-" + strings.TrimPrefix(digest, "sha256:")[:24]
}

func NewEmbeddedBackend(root string) (*ContainerBackend, error) {
	b := NewContainerBackend(root)
	m, digest, err := ReadRuntimeManifest(os.Getenv("POINT_EMBEDDED_RUNTIME"))
	if err != nil {
		b.Engine = &WSLEngine{Engine: "unconfigured"}
		b.deferProbe(fmt.Errorf("embedded runtime unavailable: supply a verified POINT_EMBEDDED_RUNTIME pack and provision it with scripts/setup-point-runtime.ps1: %w", err))
		return b, nil
	}
	b.Engine = &WSLEngine{Engine: m.Engine, Distribution: RuntimeDistribution(m, digest), RuntimeDigest: digest}
	b.Image = m.ImageReference
	if platformErr := runtimePlatformCheck(); platformErr != nil {
		b.deferProbe(platformErr)
		return b, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err = b.Probe(ctx); err != nil {
		b.deferProbe(err)
	}
	return b, nil
}

// Called by Probe, including retries: a manifest claim is never a capability.
func (b *ContainerBackend) probeEmbeddedSecurity(ctx context.Context) error {
	e, ok := b.Engine.(*WSLEngine)
	if !ok {
		return nil
	}
	m, digest, err := ReadRuntimeManifest(os.Getenv("POINT_EMBEDDED_RUNTIME"))
	if err != nil || digest != e.RuntimeDigest || m.Engine != e.Engine {
		return errors.New("pinned embedded runtime pack unavailable or changed")
	}
	if b.DockerVersion != m.EngineVersion || b.ImageDigest != m.ImageDigest {
		return errors.New("embedded engine/image version differs from its pinned pack")
	}
	// No DrvFS automount or Windows executable interop in the Point-owned guest.
	cmd := e.rawCommand(ctx, "version")
	cmd.Args = []string{"wsl.exe", "--distribution", e.Distribution, "--user", "root", "--exec", "/bin/cat", "/etc/wsl.conf"}
	cmd.Env = dockerClientEnvironment()
	conf, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("inspect owned WSL boundary: %w", err)
	}
	section, automount, interop := "", false, false
	for _, line := range strings.Split(strings.ToLower(string(conf)), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			section = line
			continue
		}
		k, v, found := strings.Cut(line, "=")
		if found && strings.TrimSpace(k) == "enabled" && strings.TrimSpace(v) == "false" {
			if section == "[automount]" {
				automount = true
			}
			if section == "[interop]" {
				interop = true
			}
		}
	}
	if !automount || !interop {
		return errors.New("Point WSL guest must disable Windows automount and interop")
	}
	linux, err := b.output(ctx, "info", "--format", "{{.KernelVersion}}|{{.OperatingSystem}}")
	if err != nil || strings.TrimSpace(linux) == "" {
		return errors.New("embedded Linux environment version is unavailable")
	}
	b.LinuxVersion = strings.TrimSpace(linux)
	probe := `set -eu; test "$(id -u)" != 0; grep -Eq '^CapEff:[[:space:]]+0+$' /proc/self/status; grep -Eq '^NoNewPrivs:[[:space:]]+1$' /proc/self/status; test ! -e /mnt/c; test ! -S /var/run/docker.sock; test "$(ls /sys/class/net | wc -l)" -eq 1; if test -f /sys/fs/cgroup/memory.max; then test "$(cat /sys/fs/cgroup/memory.max)" = 134217728; test "$(cat /sys/fs/cgroup/pids.max)" = 32; test "$(cat /sys/fs/cgroup/cpu.max)" = '25000 100000'; else test "$(cat /sys/fs/cgroup/memory/memory.limit_in_bytes)" = 134217728; test "$(cat /sys/fs/cgroup/pids/pids.max)" = 32; test "$(cat /sys/fs/cgroup/cpu/cpu.cfs_quota_us)" = 25000; test "$(cat /sys/fs/cgroup/cpu/cpu.cfs_period_us)" = 100000; fi; if touch /etc/point-security-probe 2>/dev/null; then exit 1; fi; printf 'point-security-ok'`
	out, err := b.output(ctx, "run", "--rm", "--pull", "never", "--network", "none", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges=true", "--user", b.User, "--memory", "128m", "--pids-limit", "32", "--cpus", "0.25", "--ipc", "none", m.ImageDigest, "sh", "-c", probe)
	if err != nil {
		return fmt.Errorf("embedded security profile probe failed: %w", err)
	}
	if strings.TrimSpace(out) != "point-security-ok" {
		return errors.New("embedded security profile probe returned incomplete evidence")
	}
	return nil
}

// AssetPath rejects link substitution in provisioning callers.
func RuntimeAssetPath(manifest string, asset RuntimeAsset) (string, error) {
	if !runtimeAssetName.MatchString(asset.File) {
		return "", errors.New("invalid asset name")
	}
	base, err := filepath.EvalSymlinks(filepath.Dir(manifest))
	if err != nil {
		return "", err
	}
	path := filepath.Join(base, asset.File)
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("runtime assets must be regular files")
	}
	return path, nil
}
