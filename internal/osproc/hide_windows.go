//go:build windows

package osproc

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// hideWindow ставит два флага, а не один, потому что они закрывают разные щели.
//
// CREATE_NO_WINDOW не даёт Windows выдать консоль консольному потомку процесса,
// у которого консоли нет, — это и есть источник мигающих чёрных окон. HideWindow
// добавляет STARTF_USESHOWWINDOW с SW_HIDE и прячет окно у программ, которые
// создают его сами, не полагаясь на консоль.
//
// Поля дописываются в существующий SysProcAttr: у cmd.exe там лежит свой
// CmdLine, и подменять структуру целиком значило бы потерять его.
func hideWindow(command *exec.Cmd) {
	if command.SysProcAttr == nil {
		command.SysProcAttr = &syscall.SysProcAttr{}
	}
	command.SysProcAttr.HideWindow = true
	command.SysProcAttr.CreationFlags |= windows.CREATE_NO_WINDOW
}
