//go:build linux

package osproc

import "syscall"

func setParentDeathSignal(attributes *syscall.SysProcAttr) {
	attributes.Pdeathsig = syscall.SIGKILL
}

func deathSignal(attributes *syscall.SysProcAttr) syscall.Signal { return attributes.Pdeathsig }
