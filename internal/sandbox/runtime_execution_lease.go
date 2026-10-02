package sandbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"local-agent-workbench/internal/osproc"
)

// A live execution keeps the guest awake during model requests and manual
// pauses too. OS locks coordinate windows and disappear when the core crashes.
func (b *ContainerBackend) retainRuntimeExecution(root string) error {
	e, ok := b.Engine.(*WSLEngine)
	if !ok {
		return nil
	}
	b.runtimeLeasesMu.Lock()
	defer b.runtimeLeasesMu.Unlock()
	if b.runtimeLeases[root] != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	release, err := AcquireRuntimeLease(ctx, os.Getenv("POINT_EMBEDDED_RUNTIME"), e.RuntimeDigest)
	if err != nil {
		return err
	}
	if b.runtimeLeases == nil {
		b.runtimeLeases = map[string]func(){}
	}
	b.runtimeLeases[root] = release
	return nil
}

func (b *ContainerBackend) releaseRuntimeExecution(root string) {
	b.runtimeLeasesMu.Lock()
	defer b.runtimeLeasesMu.Unlock()
	if release := b.runtimeLeases[root]; release != nil {
		release()
		delete(b.runtimeLeases, root)
	}
}

func AcquireRuntimeLease(ctx context.Context, manifest, digest string) (func(), error) {
	m, actual, err := ReadRuntimeManifest(manifest)
	if err != nil || actual != digest {
		return nil, errors.New("runtime identity changed")
	}
	base, err := RuntimeControlPath(digest)
	if err != nil {
		return nil, err
	}
	unlock, err := runtimeLock(ctx, base)
	if err != nil {
		return nil, err
	}
	defer unlock()
	owner, err := runtimeOwner(base, digest, RuntimeDistribution(m, digest))
	if err != nil || owner.State != "ready" {
		return nil, errors.New("owned runtime is not ready")
	}
	dir := filepath.Join(base, "leases")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	id, err := containerIdentity()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, id+".lock")
	release, err := osproc.LockFile(path)
	if err != nil {
		return nil, err
	}
	owner.LastActivity = time.Now().UTC()
	if err = atomicJSON(filepath.Join(base, "owner.json"), owner); err != nil {
		release()
		_ = os.Remove(path)
		return nil, err
	}
	return func() {
		finishCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if unlock, lockErr := runtimeLock(finishCtx, base); lockErr == nil {
			if current, readErr := runtimeOwner(base, digest, owner.Distribution); readErr == nil {
				current.LastActivity = time.Now().UTC()
				_ = atomicJSON(filepath.Join(base, "owner.json"), current)
			}
			unlock()
		}
		release()
		_ = os.Remove(path)
	}, nil
}
