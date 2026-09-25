//go:build !windows

package osproc

import (
	"os/exec"
	"syscall"
)

type groupPlatform struct{}

// Своя группа процессов: сигнал на -pid доходит до всех потомков, а сигнал
// терминала родителя до группы не доходит.
func prepareGroup(command *exec.Cmd, options GroupOptions) {
	if command.SysProcAttr == nil {
		command.SysProcAttr = &syscall.SysProcAttr{}
	}
	command.SysProcAttr.Setpgid = true
	if options.DieWithParent {
		setParentDeathSignal(command.SysProcAttr)
	}
}

func attachGroup(*exec.Cmd, GroupOptions) groupPlatform { return groupPlatform{} }

func killGroup(g *Group) {
	_ = syscall.Kill(-g.cmd.Process.Pid, syscall.SIGKILL)
}

func releaseGroup(*Group) {}
