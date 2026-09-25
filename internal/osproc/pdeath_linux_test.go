//go:build linux

package osproc

import (
	"syscall"
	"testing"
)

func TestDieWithParentSetsDeathSignalOnLinux(t *testing.T) {
	command := Command("true")
	prepareGroup(command, GroupOptions{DieWithParent: true})
	if command.SysProcAttr.Pdeathsig != syscall.SIGKILL {
		t.Fatalf("Pdeathsig = %v, ждали SIGKILL", command.SysProcAttr.Pdeathsig)
	}
}
