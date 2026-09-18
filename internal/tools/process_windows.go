//go:build windows

package tools

import (
	"context"
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

func runProcess(ctx context.Context, command *exec.Cmd) error {
	if err := command.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		terminateTree(uint32(command.Process.Pid))
		<-done
		return ctx.Err()
	}
}

func terminateTree(root uint32) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return
	}
	defer windows.CloseHandle(snapshot)
	children := make(map[uint32][]uint32)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	if err = windows.Process32First(snapshot, &entry); err == nil {
		for {
			children[entry.ParentProcessID] = append(children[entry.ParentProcessID], entry.ProcessID)
			if err = windows.Process32Next(snapshot, &entry); err != nil {
				break
			}
		}
	}
	var terminate func(uint32)
	terminate = func(pid uint32) {
		for _, child := range children[pid] {
			terminate(child)
		}
		handle, openErr := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pid)
		if openErr == nil {
			_ = windows.TerminateProcess(handle, 1)
			_ = windows.CloseHandle(handle)
		}
	}
	terminate(root)
}
