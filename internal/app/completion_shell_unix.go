//go:build !windows

package app

import (
	"context"
	"os/exec"

	"local-agent-workbench/internal/osproc"
	workbenchtools "local-agent-workbench/internal/tools"
)

// completionShellCommand — проверки хоста идут через ту же оболочку, что и
// run_command без песочницы: с pipefail, где он есть (Q08).
func completionShellCommand(ctx context.Context, directory, command string) *exec.Cmd {
	program, args := workbenchtools.HostShellCommand(command)
	process := osproc.CommandContext(ctx, program, args...)
	process.Dir = directory
	return process
}
