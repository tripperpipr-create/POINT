//go:build windows

package app

import (
	"context"
	"os/exec"
	"syscall"

	"local-agent-workbench/internal/osproc"
)

// completionShellCommand hands cmd.exe the approved command line verbatim.
// Go's default argv escaping quotes with backslashes, which cmd.exe does not
// understand: `echo "x&y"` would reach the shell as `\"x&y\"`, so every
// approved command containing quotes would silently run as something else.
func completionShellCommand(ctx context.Context, directory, command string) *exec.Cmd {
	process := exec.CommandContext(ctx, "cmd")
	process.SysProcAttr = &syscall.SysProcAttr{CmdLine: "cmd /c " + command}
	// Hide, а не osproc.CommandContext: своя SysProcAttr уже собрана выше, и
	// конструктор её бы перезаписал вместе с CmdLine. Hide дополняет готовую.
	osproc.Hide(process)
	process.Dir = directory
	return process
}
