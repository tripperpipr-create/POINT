//go:build windows

package osproc

import (
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

type groupPlatform struct {
	job windows.Handle
}

func prepareGroup(*exec.Cmd, GroupOptions) {}

// Job Object с закрытием по последнему дескриптору: ядро умерло — Windows
// гасит всё, что в задании. Без DieWithParent задания нет, и поведение то же,
// что было у пользовательских инструментов: обход дерева по требованию.
//
// Процесс попадает в задание сразу после старта. Потомка, успевшего родиться
// раньше, задание не видит — его подбирает обход дерева в killGroup.
func attachGroup(command *exec.Cmd, options GroupOptions) groupPlatform {
	if !options.DieWithParent {
		return groupPlatform{}
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return groupPlatform{}
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return groupPlatform{}
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(command.Process.Pid))
	if err != nil {
		_ = windows.CloseHandle(job)
		return groupPlatform{}
	}
	defer windows.CloseHandle(process)
	if err = windows.AssignProcessToJobObject(job, process); err != nil {
		_ = windows.CloseHandle(job)
		return groupPlatform{}
	}
	return groupPlatform{job: job}
}

func killGroup(g *Group) {
	if g.platform.job != 0 {
		_ = windows.TerminateJobObject(g.platform.job, 1)
	}
	terminateTree(uint32(g.cmd.Process.Pid))
}

func releaseGroup(g *Group) {
	if g.platform.job != 0 {
		_ = windows.CloseHandle(g.platform.job)
		g.platform.job = 0
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
