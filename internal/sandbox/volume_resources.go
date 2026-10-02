package sandbox

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/filepolicy"
	"local-agent-workbench/internal/sandboxsync"
)

const helperMount = "/point-tools/point-sandboxd"

// Native compilation executes generated binaries inside the same unprivileged
// container. Pin exec instead of inheriting differing Docker/Podman defaults;
// resource limits, nosuid/nodev, root read-only and namespace reset still apply.
const commandTemporaryMount = "/tmp:rw,exec,nosuid,nodev,size=512m"

func (b *ContainerBackend) helperVolume(ctx context.Context, r *domain.SandboxRecord) (string, error) {
	b.helperMu.Lock()
	defer b.helperMu.Unlock()
	if b.helperVolumes == nil {
		b.helperVolumes = map[string]string{}
	}
	if name, ok := b.helperVolumes[r.SandboxdDigest]; ok && r.SandboxdDigest != "" {
		if _, err := b.output(ctx, "volume", "inspect", name); err == nil {
			return name, nil
		} else if !missingDockerResource(err) {
			return "", err
		}
		delete(b.helperVolumes, r.SandboxdDigest)
	}
	if strings.HasPrefix(r.SandboxdDigest, "sha256:") && len(r.SandboxdDigest) == 71 {
		name := "point-tools-" + ownerIdentity(b.Root) + "-" + r.SandboxdDigest[7:31]
		if _, err := b.output(ctx, "volume", "inspect", name); err == nil {
			if err := b.verifyHelperVolume(ctx, *r, name, r.SandboxdDigest); err != nil {
				return "", err
			}
			b.helperVolumes[r.SandboxdDigest] = name
			return name, nil
		} else if !missingDockerResource(err) {
			return "", err
		}
	}
	arch, err := b.output(ctx, "image", "inspect", r.BackendImageDigest, "--format", "{{.Architecture}}")
	if err != nil {
		return "", err
	}
	arch = strings.TrimSpace(arch)
	if arch != "amd64" && arch != "arm64" {
		return "", fmt.Errorf("unsupported Linux sandbox architecture %q", arch)
	}
	path := strings.TrimSpace(os.Getenv("POINT_SANDBOXD_BINARY"))
	if path == "" {
		exe, _ := os.Executable()
		candidates := []string{filepath.Join(filepath.Dir(exe), "point-sandboxd-linux-"+arch), filepath.Join("vscode-extension", "bin", "point-sandboxd-linux-"+arch)}
		for _, candidate := range candidates {
			if _, err = os.Stat(candidate); err == nil {
				path = candidate
				break
			}
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("static point-sandboxd-linux-%s is unavailable; build the core bundle: %w", arch, err)
	}
	sum := sha256.Sum256(data)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	if r.SandboxdDigest != "" && r.SandboxdDigest != digest {
		return "", errors.New("sandboxd changed for an existing sandbox; its pinned binary must remain available")
	}
	r.SandboxdDigest = digest
	name := "point-tools-" + ownerIdentity(b.Root) + "-" + hex.EncodeToString(sum[:12])
	if _, err = b.output(ctx, "volume", "inspect", name); err != nil {
		if !missingDockerResource(err) {
			return "", err
		}
		args := append([]string{"volume", "create"}, b.labels(*r, "tools")...)
		args = append(args, name)
		if err = b.runInfrastructure(ctx, args...); err != nil {
			return "", err
		}
		setup := "point-tools-upload-" + r.ID
		args = []string{"create", "--name", setup, "--pull", "never", "--network", "none", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges=true", "--mount", "type=volume,src=" + name + ",dst=/point-tools,volume-nocopy", "--entrypoint", helperMount}
		args = append(args, b.labels(*r, "tools-upload")...)
		args = append(args, r.BackendImageDigest, "digest")
		if err = b.runInfrastructure(ctx, args...); err != nil {
			return "", err
		}
		defer b.runInfrastructure(context.Background(), "rm", "--force", setup)
		var archive bytes.Buffer
		tw := tar.NewWriter(&archive)
		if err = tw.WriteHeader(&tar.Header{Name: "point-sandboxd", Mode: 0555, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			return "", err
		}
		if _, err = tw.Write(data); err != nil {
			return "", err
		}
		if err = tw.Close(); err != nil {
			return "", err
		}
		upload := b.commandFor(ctx, "cp", "-", setup+":/point-tools")
		upload.Env = dockerClientEnvironment()
		upload.Stdin = &archive
		if out, copyErr := upload.CombinedOutput(); copyErr != nil {
			return "", fmt.Errorf("upload sandboxd: %w (%s)", copyErr, out)
		}
	}
	if err := b.verifyHelperVolume(ctx, *r, name, digest); err != nil {
		return "", err
	}
	b.helperVolumes[digest] = name
	return name, nil
}
func (b *ContainerBackend) initializeWorkspace(ctx context.Context, r *domain.SandboxRecord, req CreateRequest) error {
	if req.SeedVolume != "" && !req.PortableOnly {
		parent, err := b.stateFor(req.SeedPath)
		if err != nil {
			return fmt.Errorf("load volume clone parent: %w", err)
		}
		if parent.Record.WorkspaceVolume != req.SeedVolume || parent.Record.ID != req.ParentSandboxID || parent.Record.WorkspaceID != r.WorkspaceID || parent.Record.BackendImageDigest != req.SeedImageDigest {
			return errors.New("volume clone source differs from its parent sandbox")
		}
		if exists, err := b.ownedResource(ctx, "volume", req.SeedVolume, parent.Record.ID, "workspace"); err != nil {
			return err
		} else if !exists {
			return errors.New("parent volume was lost; synchronize its mirror before cloning")
		}
	}
	if err := writeWorkspaceRecord(*r); err != nil {
		return err
	}
	helper, err := b.helperVolume(ctx, r)
	if err != nil {
		return err
	}
	r.WorkspaceVolume = "point-work-" + r.ID
	if err = b.createWorkspaceVolume(ctx, *r, helper); err != nil {
		return err
	}
	if req.SeedVolume != "" && !req.PortableOnly && req.SeedImageDigest == r.BackendImageDigest {
		args := []string{"run", "--rm", "--pull", "never", "--network", "none", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges=true", "--pids-limit", strconv.Itoa(b.PIDsLimit), "--memory", b.MemoryLimit, "--cpus", b.CPULimit, "--user", b.User, "--entrypoint", helperMount, "--mount", "type=volume,src=" + helper + ",dst=/point-tools,readonly", "--mount", "type=volume,src=" + req.SeedVolume + ",dst=/source,readonly", "--mount", "type=volume,src=" + r.WorkspaceVolume + ",dst=/workspace,volume-nocopy"}
		args = append(args, b.labels(*r, "clone")...)
		args = append(args, r.BackendImageDigest, "clone", "--full")
		if err = b.runInfrastructure(ctx, args...); err != nil {
			return err
		}
	} else if req.SeedVolume != "" && !req.PortableOnly && req.SeedImageDigest != r.BackendImageDigest {
		if req.Runtime.Progress != nil {
			req.Runtime.Progress("runtime_warning", "Image or architecture changed; dependencies were not inherited and require installation")
		}
	}
	if err = writeWorkspaceRecord(*r); err != nil {
		return err
	}
	s := &volumeState{Record: *r, Manifest: sandboxsync.Manifest{Rules: filepolicy.Current}, Phase: "idle", Watcher: newMirrorWatcher(r.Path)}
	if req.SeedPath != "" {
		if parent, err := b.stateFor(req.SeedPath); err == nil && parent.Incomplete {
			s.taint("Inherited a sandbox with incomplete audit")
		}
	}
	b.workspaceStates.Store(r.Path, s)
	if err = b.ensureWarmContainer(ctx, s, ProcessRequest{WorkspaceRoot: r.Path, Image: r.BackendImageDigest, NetworkPolicy: "DENY"}); err != nil {
		return err
	}
	m, err := sandboxsync.Scan(ctx, r.Path, r.FileRulesVersion)
	if err != nil {
		return err
	}
	before, err := b.volumeManifest(ctx, s)
	if err != nil {
		return err
	}
	changes := sandboxsync.Changes(before, m)
	for i := range changes {
		changes[i], err = sandboxsync.ReadChange(r.Path, changes[i])
		if err != nil {
			return err
		}
	}
	if err = b.applyVolume(ctx, s, m, changes); err != nil {
		return err
	}
	s.Manifest = m
	if out, err := b.output(ctx, "exec", s.Container, helperMount, "digest", "--space"); err == nil {
		var space sandboxsync.Space
		if json.Unmarshal([]byte(out), &space) == nil && space.Low() {
			warning := fmt.Sprintf("Docker workspace disk is low: %.1f GiB available; remove completed sandboxes or expand Docker Desktop storage", float64(space.Available)/(1024*1024*1024))
			b.diskWarnings.Store(r.WorkspaceVolume, warning)
			slog.Warn(warning)
		}
	}
	return saveVolumeState(s)
}
func (b *ContainerBackend) createWorkspaceVolume(ctx context.Context, r domain.SandboxRecord, helper string) error {
	if exists, err := b.ownedResource(ctx, "volume", r.WorkspaceVolume, r.ID, "workspace"); err != nil {
		return err
	} else if exists {
		return nil
	}
	args := append([]string{"volume", "create"}, b.labels(r, "workspace")...)
	args = append(args, r.WorkspaceVolume)
	if err := b.runInfrastructure(ctx, args...); err != nil {
		return err
	}
	owner := b.User
	if !strings.Contains(owner, ":") {
		owner += ":" + owner
	}
	args = []string{"run", "--rm", "--pull", "never", "--network", "none", "--read-only", "--cap-drop", "ALL", "--cap-add", "CHOWN", "--security-opt", "no-new-privileges=true", "--pids-limit", "32", "--memory", "128m", "--cpus", "0.25", "--user", "0:0", "--entrypoint", helperMount, "--mount", "type=volume,src=" + helper + ",dst=/point-tools,readonly", "--mount", "type=volume,src=" + r.WorkspaceVolume + ",dst=/workspace,volume-nocopy"}
	args = append(args, b.labels(r, "ownership")...)
	args = append(args, r.BackendImageDigest, "apply", "--owner", owner)
	return b.runInfrastructure(ctx, args...)
}
func (b *ContainerBackend) ensureWarmContainer(ctx context.Context, s *volumeState, req ProcessRequest) error {
	signature := req.CacheScope + "|" + strconv.FormatBool(req.Authoritative)
	if s.Container != "" && s.CacheSignature == signature {
		return nil
	}
	helper, err := b.helperVolume(ctx, &s.Record)
	if err != nil {
		return err
	}
	if _, err = b.output(ctx, "volume", "inspect", s.Record.WorkspaceVolume); err != nil {
		if !missingDockerResource(err) {
			return err
		}
		if err = b.createWorkspaceVolume(ctx, s.Record, helper); err != nil {
			return err
		}
		s.taint("Docker workspace volume was lost; dependencies must be installed again")
		s.Manifest = sandboxsync.Manifest{Rules: s.Record.FileRulesVersion}
		s.Phase = "restored"
		if err = saveVolumeState(s); err != nil {
			return err
		}
	}
	name := "point-stage-" + s.Record.ID
	if exists, err := b.ownedResource(ctx, "container", name, s.Record.ID, "stage"); err != nil {
		return err
	} else if exists {
		if err := b.runInfrastructure(ctx, "rm", "--force", name); err != nil {
			return err
		}
	}
	network := "none"
	if s.Gateway != nil {
		network = s.Gateway.Network
	}
	args := []string{"run", "--detach", "--pull", "never", "--name", name, "--network", network, "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges=true", "--pids-limit", strconv.Itoa(b.PIDsLimit), "--memory", b.MemoryLimit, "--cpus", b.CPULimit, "--ipc", "none", "--hostname", "point-sandbox", "--user", b.User, "--tmpfs", commandTemporaryMount, "--entrypoint", helperMount, "--mount", "type=volume,src=" + helper + ",dst=/point-tools,readonly", "--mount", "type=volume,src=" + s.Record.WorkspaceVolume + ",dst=/workspace,volume-nocopy"}
	args = append(args, b.labels(s.Record, "stage")...)
	cacheArgs, _, err := b.cacheMounts(ctx, req.CacheScope, req.Authoritative, s.Record.BackendImageDigest)
	if err != nil {
		return err
	}
	args = append(args, cacheArgs...)
	if s.Gateway != nil {
		args = append(args, "--add-host", "point-egress-gateway:"+s.Gateway.Address, "--dns", "127.0.0.1")
	}
	args = append(args, s.Record.BackendImageDigest, "run", "--init")
	if err = b.runInfrastructure(ctx, args...); err != nil {
		return err
	}
	s.Container = name
	s.CacheSignature = signature
	if s.Phase == "restored" {
		m, err := sandboxsync.Scan(ctx, s.Record.Path, s.Record.FileRulesVersion)
		if err != nil {
			return err
		}
		changes := sandboxsync.Changes(sandboxsync.Manifest{}, m)
		for i := range changes {
			changes[i], err = sandboxsync.ReadChange(s.Record.Path, changes[i])
			if err != nil {
				return err
			}
		}
		if err = b.applyVolume(ctx, s, m, changes); err != nil {
			return err
		}
		s.Manifest = m
		s.Before = sandboxsync.Manifest{}
		s.Pending = nil
		s.PendingManifest = nil
		s.Operation = ""
		s.Phase = "idle"
		if err = saveVolumeState(s); err != nil {
			return err
		}
	}
	return nil
}
func (b *ContainerBackend) applyVolume(ctx context.Context, s *volumeState, m sandboxsync.Manifest, changes []sandboxsync.Change) error {
	r := sandboxsync.Request{Version: 1, Operation: domain.NewID("sync"), Rules: s.Record.FileRulesVersion, Before: m, Changes: changes}
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	command := b.commandFor(ctx, "exec", "-i", s.Container, helperMount, "apply")
	command.Env = dockerClientEnvironment()
	command.Stdin = bytes.NewReader(data)
	out, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("apply volume: %w (%s)", err, out)
	}
	return nil
}
func (b *ContainerBackend) volumeManifest(ctx context.Context, s *volumeState) (sandboxsync.Manifest, error) {
	out, err := b.output(ctx, "exec", s.Container, helperMount, "digest", "--rules", s.Record.FileRulesVersion)
	if err != nil {
		return sandboxsync.Manifest{}, err
	}
	var m sandboxsync.Manifest
	err = json.Unmarshal([]byte(out), &m)
	if err == nil {
		err = sandboxsync.ValidateManifest(m)
	}
	return m, err
}
