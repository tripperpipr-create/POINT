package sandbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"local-agent-workbench/internal/osproc"
)

type RuntimeOwner struct {
	Digest       string    `json:"digest"`
	Distribution string    `json:"distribution"`
	State        string    `json:"state"`
	LastActivity time.Time `json:"lastActivity"`
}

func RuntimeControlPath(digest string) (string, error) {
	if !imageDigestPattern.MatchString(digest) {
		return "", errors.New("invalid runtime identity")
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "Point", "runtime", strings.TrimPrefix(digest, "sha256:")), nil
}

func runtimeLock(ctx context.Context, base string) (func(), error) {
	if err := os.MkdirAll(base, 0700); err != nil {
		return nil, err
	}
	for {
		unlock, err := osproc.LockFile(filepath.Join(base, "control.lock"))
		if err == nil {
			return unlock, nil
		}
		if !osproc.IsLockBusy(err) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func runtimeOwner(base, digest, distribution string) (RuntimeOwner, error) {
	var owner RuntimeOwner
	raw, err := os.ReadFile(filepath.Join(base, "owner.json"))
	if err != nil {
		return owner, err
	}
	if json.Unmarshal(raw, &owner) != nil || owner.Digest != digest || owner.Distribution != distribution {
		return owner, errors.New("runtime ownership differs; refusing to access another WSL distribution")
	}
	return owner, nil
}

func VerifyRuntimeAsset(manifest string, asset RuntimeAsset) (string, error) {
	path, err := RuntimeAssetPath(manifest, asset)
	if err != nil {
		return "", err
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	if "sha256:"+hex.EncodeToString(h.Sum(nil)) != asset.SHA256 {
		return "", errors.New("runtime asset digest differs")
	}
	return path, nil
}

// ProvisionRuntime consumes a local, pinned release pack. It never touches
// another distribution, global WSL settings or the live project/database.
func ProvisionRuntime(ctx context.Context, manifest string) error {
	if err := runtimePlatformCheck(); err != nil {
		return err
	}
	m, digest, err := ReadRuntimeManifest(manifest)
	if err != nil {
		return err
	}
	rootfs, err := VerifyRuntimeAsset(manifest, m.RootFS)
	if err != nil {
		return err
	}
	image, err := VerifyRuntimeAsset(manifest, m.Image)
	if err != nil {
		return err
	}
	base, err := RuntimeControlPath(digest)
	if err != nil {
		return err
	}
	unlock, err := runtimeLock(ctx, base)
	if err != nil {
		return err
	}
	defer unlock()
	name := RuntimeDistribution(m, digest)
	owner, ownerErr := runtimeOwner(base, digest, name)
	list := osproc.CommandContext(ctx, "wsl.exe", "--list", "--quiet")
	list.Env = dockerClientEnvironment()
	out, err := list.CombinedOutput()
	if err != nil {
		return fmt.Errorf("WSL unavailable; enable virtualization/WSL 2, restart if requested, then retry provisioning: %w", err)
	}
	// wsl.exe may return UTF-16. Distribution names are ASCII and fixed.
	text := strings.ReplaceAll(string(out), "\x00", "")
	exists := false
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == name {
			exists = true
		}
	}
	if exists && ownerErr != nil {
		return errors.New("WSL name already exists without matching Point ownership; refusing to adopt it")
	}
	if !exists {
		owner = RuntimeOwner{Digest: digest, Distribution: name, State: "importing", LastActivity: time.Now().UTC()}
		if err = atomicJSON(filepath.Join(base, "owner.json"), owner); err != nil {
			return err
		}
		cmd := osproc.CommandContext(ctx, "wsl.exe", "--import", name, filepath.Join(base, "disk"), rootfs, "--version", "2")
		cmd.Env = dockerClientEnvironment()
		if out, err = cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("owned runtime import failed (safe to retry): %w: %s", err, strings.TrimSpace(string(out)))
		}
	}
	engine := &WSLEngine{Engine: m.Engine, Distribution: name, RuntimeDigest: digest}
	f, err := os.Open(image)
	if err != nil {
		return err
	}
	defer f.Close()
	cmd := engine.rawCommand(ctx, "load")
	// WSL binary streams require a pipe. A direct *os.File lets os/exec inherit
	// a regular Windows handle, which wsl.exe cannot reliably forward.
	cmd.Stdin = struct{ io.Reader }{f}
	cmd.Env = dockerClientEnvironment()
	if out, err = cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("load pinned runtime image: %w: %s", err, strings.TrimSpace(string(out)))
	}
	owner.State = "ready"
	owner.LastActivity = time.Now().UTC()
	return atomicJSON(filepath.Join(base, "owner.json"), owner)
}

// Each bridge invocation owns an OS lease until its process has exited. The
// idle monitor takes the same control lock, so it cannot terminate a live call.
func RunRuntimeCommand(ctx context.Context, manifest, digest string, args []string, in io.Reader, out, stderr io.Writer) error {
	buildArgs, archive, buildErr := runtimeBuildContext(args)
	if buildErr != nil {
		return buildErr
	}
	args = buildArgs
	if archive != nil {
		in = archive
	}
	m, actual, err := ReadRuntimeManifest(manifest)
	if err != nil || actual != digest {
		return errors.New("pinned runtime manifest changed or unavailable")
	}
	base, err := RuntimeControlPath(digest)
	if err != nil {
		return err
	}
	unlock, err := runtimeLock(ctx, base)
	if err != nil {
		return err
	}
	owner, err := runtimeOwner(base, digest, RuntimeDistribution(m, digest))
	if err != nil || owner.State != "ready" {
		unlock()
		return errors.New("runtime not provisioned; run point-runtime install with this pack")
	}
	leases := filepath.Join(base, "leases")
	if err = os.MkdirAll(leases, 0700); err != nil {
		unlock()
		return err
	}
	id, err := containerIdentity()
	if err != nil {
		unlock()
		return err
	}
	lease := filepath.Join(leases, id+".lock")
	release, err := osproc.LockFile(lease)
	if err != nil {
		unlock()
		return err
	}
	owner.LastActivity = time.Now().UTC()
	if err = atomicJSON(filepath.Join(base, "owner.json"), owner); err != nil {
		release()
		unlock()
		return err
	}
	unlock()
	defer func() { release(); _ = os.Remove(lease) }()
	engine := &WSLEngine{Engine: m.Engine, Distribution: owner.Distribution, RuntimeDigest: digest}
	cmd := engine.rawCommand(ctx, args...)
	cmd.Env = dockerClientEnvironment()
	cmd.Stdin = in
	cmd.Stdout = out
	cmd.Stderr = stderr
	// Killing the bridge must also close its WSL transport. Otherwise an attach
	// child keeps inherited streams and the caller's Wait blocked indefinitely.
	group, startErr := osproc.StartGroup(cmd, osproc.GroupOptions{DieWithParent: true})
	if startErr != nil {
		err = startErr
	} else {
		err = cmd.Wait()
		group.Release()
	}
	finishCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if unlock, lockErr := runtimeLock(finishCtx, base); lockErr == nil {
		owner.LastActivity = time.Now().UTC()
		_ = atomicJSON(filepath.Join(base, "owner.json"), owner)
		unlock()
	}
	return err
}

func StopIdleRuntime(ctx context.Context, manifest, digest string, now time.Time) (bool, error) {
	m, actual, err := ReadRuntimeManifest(manifest)
	if err != nil || actual != digest {
		return false, errors.New("runtime identity changed")
	}
	base, err := RuntimeControlPath(digest)
	if err != nil {
		return false, err
	}
	unlock, err := runtimeLock(ctx, base)
	if err != nil {
		return false, err
	}
	defer unlock()
	owner, err := runtimeOwner(base, digest, RuntimeDistribution(m, digest))
	if err != nil {
		return false, err
	}
	if now.Sub(owner.LastActivity) < 10*time.Minute {
		return false, nil
	}
	entries, err := os.ReadDir(filepath.Join(base, "leases"))
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".lock") {
			return false, errors.New("unknown runtime lease entry")
		}
		path := filepath.Join(base, "leases", entry.Name())
		release, err := osproc.LockFile(path)
		if err != nil {
			if osproc.IsLockBusy(err) {
				return false, nil
			}
			return false, err
		}
		release()
		_ = os.Remove(path)
	}
	cmd := osproc.CommandContext(ctx, "wsl.exe", "--terminate", owner.Distribution)
	cmd.Env = dockerClientEnvironment()
	if out, err := cmd.CombinedOutput(); err != nil {
		return false, fmt.Errorf("stop owned runtime: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return true, nil
}
