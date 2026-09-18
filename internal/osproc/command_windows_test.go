//go:build windows

package osproc

import (
	"context"
	"os/exec"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

// TestCommandHidesConsoleWindow закрепляет единственное, ради чего пакет
// существует. Проверить это глазами можно только на живой Windows, где окно
// вспыхивает и исчезает, — поэтому флаг проверяется здесь.
func TestCommandHidesConsoleWindow(t *testing.T) {
	for name, command := range map[string]*exec.Cmd{
		"Command":        Command("git", "status"),
		"CommandContext": CommandContext(context.Background(), "git", "status"),
	} {
		t.Run(name, func(t *testing.T) {
			if command.SysProcAttr == nil {
				t.Fatal("SysProcAttr is nil: the console window would pop up")
			}
			if !command.SysProcAttr.HideWindow {
				t.Error("HideWindow is not set")
			}
			if command.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
				t.Error("CREATE_NO_WINDOW is not set")
			}
		})
	}
}

// TestHideKeepsExistingSysProcAttr защищает cmd.exe: его команда собирается со
// своим CmdLine, потому что Go экранирует кавычки так, как cmd.exe не понимает.
// Подмена структуры целиком стёрла бы CmdLine, и любая утверждённая команда с
// кавычками тихо выполнилась бы как другая.
func TestHideKeepsExistingSysProcAttr(t *testing.T) {
	command := exec.Command("cmd")
	command.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd /c echo "x&y"`}
	Hide(command)
	if command.SysProcAttr.CmdLine != `cmd /c echo "x&y"` {
		t.Fatalf("CmdLine was lost: %q", command.SysProcAttr.CmdLine)
	}
	if command.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Error("CREATE_NO_WINDOW is not set alongside the existing CmdLine")
	}
}

func TestHideIgnoresNil(t *testing.T) {
	if Hide(nil) != nil {
		t.Fatal("Hide(nil) must stay nil")
	}
}
