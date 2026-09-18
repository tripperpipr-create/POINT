//go:build !windows

package app

import (
	"context"
	"os/exec"

	"local-agent-workbench/internal/osproc"
)

func completionShellCommand(ctx context.Context, directory, command string) *exec.Cmd {
	process := osproc.CommandContext(ctx, "sh", "-c", command)
	process.Dir = directory
	return process
}
