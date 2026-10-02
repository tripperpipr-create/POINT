package sandbox

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strconv"
	"strings"

	"local-agent-workbench/internal/domain"
)

// Hash the mounted bytes through Docker's archive API before executing them.
// An executable cannot be trusted to attest to its own identity.
func (b *ContainerBackend) verifyHelperVolume(ctx context.Context, r domain.SandboxRecord, name, expected string) error {
	labels, err := b.output(ctx, "volume", "inspect", name, "--format", `{{index .Labels "point.owner"}}|{{index .Labels "point.resource"}}`)
	if err != nil {
		return err
	}
	if strings.TrimSpace(labels) != ownerIdentity(b.Root)+"|tools" {
		return fmt.Errorf("sandboxd volume %s has foreign ownership labels", name)
	}
	reader := domain.NewID("point-tools-verify")
	args := []string{"create", "--name", reader, "--pull", "never", "--network", "none", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges=true", "--user", b.User, "--pids-limit", strconv.Itoa(b.PIDsLimit), "--memory", b.MemoryLimit, "--cpus", b.CPULimit, "--entrypoint", helperMount, "--mount", "type=volume,src=" + name + ",dst=/point-tools,readonly"}
	args = append(args, b.labels(r, "tools-verify")...)
	args = append(args, r.BackendImageDigest, "digest")
	if err = b.runInfrastructure(ctx, args...); err != nil {
		return err
	}
	defer b.runInfrastructure(context.Background(), "rm", "--force", reader)
	copy := b.commandFor(ctx, "cp", reader+":"+helperMount, "-")
	copy.Env = dockerClientEnvironment()
	data, err := copy.Output()
	if err != nil {
		return fmt.Errorf("read sandboxd volume bytes: %w", err)
	}
	actual, err := helperArchiveDigest(data)
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("sandboxd volume hash differs from pinned binary: got %s, want %s", actual, expected)
	}
	return nil
}

func helperArchiveDigest(data []byte) (string, error) {
	reader := tar.NewReader(bytes.NewReader(data))
	header, err := reader.Next()
	if err != nil {
		return "", fmt.Errorf("read sandboxd archive: %w", err)
	}
	if header.Typeflag != tar.TypeReg || header.Size < 1 || header.Size > 64<<20 {
		return "", fmt.Errorf("sandboxd archive must contain one regular binary")
	}
	hash := sha256.New()
	if _, err = io.Copy(hash, reader); err != nil {
		return "", err
	}
	if _, err = reader.Next(); err != io.EOF {
		return "", fmt.Errorf("sandboxd archive contains unexpected entries")
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}
