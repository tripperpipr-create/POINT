package sandbox

import (
	"context"
	"errors"
	"os/exec"

	"local-agent-workbench/internal/osproc"
)

// Systemd services alone do not keep a WSL guest alive. The idle monitor owns
// this bounded infrastructure process; project commands still use sandboxd.
func StartRuntimeKeeper(ctx context.Context, manifest, digest string) (*exec.Cmd, error) {
	m, actual, err := ReadRuntimeManifest(manifest)
	if err != nil || actual != digest {
		return nil, errors.New("runtime keeper identity changed")
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
		return nil, errors.New("runtime keeper requires matching Point ownership")
	}
	cmd := osproc.CommandContext(ctx, "wsl.exe", "--distribution", owner.Distribution, "--user", "root", "--exec", "/bin/sleep", "660")
	cmd.Env = dockerClientEnvironment()
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}
