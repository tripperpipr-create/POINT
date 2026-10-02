package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"local-agent-workbench/internal/osproc"
)

// ContainerEngine is the infrastructure transport. Project commands remain
// inside the existing container/sandboxd boundary, with identical audit rules.
type ContainerEngine interface {
	Name() string
	Command(context.Context, ...string) *exec.Cmd
}

type WSLEngine struct {
	Distribution  string
	Engine        string
	RuntimeDigest string
}

func (e *WSLEngine) Name() string { return "embedded-" + e.Engine }
func (e *WSLEngine) Command(ctx context.Context, args ...string) *exec.Cmd {
	exe, _ := os.Executable()
	bridge := filepath.Join(filepath.Dir(exe), "point-runtime.exe")
	if configured := os.Getenv("POINT_RUNTIME_BRIDGE"); configured != "" {
		if !filepath.IsAbs(configured) || filepath.Base(configured) != "point-runtime.exe" {
			cmd := osproc.CommandContext(ctx, bridge)
			cmd.Err = errors.New("POINT_RUNTIME_BRIDGE must be an absolute trusted point-runtime.exe path")
			return cmd
		}
		bridge = configured
	}
	manifest := os.Getenv("POINT_EMBEDDED_RUNTIME")
	cmd := osproc.CommandContext(ctx, bridge, append([]string{"exec", "-manifest", manifest, "-digest", e.RuntimeDigest, "--"}, args...)...)
	// Bound stream draining if the transport exits while a descendant retains
	// a pipe. ErrWaitDelay is an infrastructure failure, never command success.
	cmd.WaitDelay = 5 * time.Second
	info, err := os.Lstat(bridge)
	if err != nil {
		cmd.Err = err
	} else if !info.Mode().IsRegular() {
		cmd.Err = errors.New("runtime bridge must be a regular trusted binary")
	}
	return cmd
}

func (e *WSLEngine) rawCommand(ctx context.Context, args ...string) *exec.Cmd {
	args = append([]string(nil), args...)
	if e.Engine == "podman" {
		if len(args) >= 3 && args[0] == "network" && args[1] == "connect" && args[2] == "bridge" {
			args[2] = "podman"
		}
		for i := 1; i < len(args); i++ {
			if args[i-1] == "--network" && args[i] == "bridge" {
				args[i] = "podman"
			}
			if args[i-1] == "--mount" && strings.HasSuffix(args[i], ",volume-nocopy") {
				// Podman 4.9 accepts nocopy only in the -v syntax, not --mount.
				parts := strings.Split(args[i], ",")
				if len(parts) == 4 && parts[0] == "type=volume" && strings.HasPrefix(parts[1], "src=") && strings.HasPrefix(parts[2], "dst=") {
					args[i-1] = "--volume"
					args[i] = strings.TrimPrefix(parts[1], "src=") + ":" + strings.TrimPrefix(parts[2], "dst=") + ":nocopy"
				}
			}
		}
	}
	if e.Engine == "podman" && len(args) == 3 && args[0] == "version" {
		args[2] = strings.ReplaceAll(args[2], ".Server.", ".Client.")
	}
	if e.Engine == "podman" && len(args) == 3 && args[0] == "info" && args[2] == "{{.KernelVersion}}|{{.OperatingSystem}}" {
		args[2] = "{{.Host.Kernel}}|{{.Host.Distribution.Distribution}} {{.Host.Distribution.Version}}"
	}
	if e.Engine == "podman" && len(args) >= 4 && args[0] == "image" && args[1] == "inspect" {
		for i := 2; i < len(args)-1; i++ {
			if args[i] == "--format" && args[i+1] == "{{.Id}}" {
				args[i+1] = "sha256:{{.Id}}"
			}
		}
	}
	program := "/usr/bin/docker"
	if e.Engine == "podman" {
		program = "/usr/bin/podman"
	}
	base := []string{"--distribution", e.Distribution, "--user", "root", "--exec", "/usr/bin/env", "-i",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "HOME=/root", program}
	return osproc.CommandContext(ctx, "wsl.exe", append(base, args...)...)
}

func (b *ContainerBackend) engineName() string {
	if b.Engine != nil {
		return b.Engine.Name()
	}
	return "docker"
}

func (b *ContainerBackend) engineVersion() string {
	version := b.DockerVersion
	if e, ok := b.Engine.(*WSLEngine); ok {
		version += "|" + e.RuntimeDigest + "|" + b.LinuxVersion + "|" + SecurityProfileVersion
	}
	if b.coldContainers || b.disableDownloadCache {
		version += fmt.Sprintf("|execution:cold=%t,cache-off=%t", b.coldContainers, b.disableDownloadCache)
	}
	return version
}
