// Package osproc — единственная дверь ядра наружу, к чужим программам.
//
// Point запускается без консоли: расширение поднимает point-core.exe с
// CREATE_NO_WINDOW и detached. У процесса без консоли Windows выдаёт новую
// консоль каждому консольному потомку — и git, docker, cmd, ssh, CLI-исполнители
// вспыхивали чёрными окнами поверх рабочего места. Пользователь видел мигание
// терминалов, которых он не открывал и закрыть не мог.
//
// Поэтому запуск чужой программы идёт только отсюда: здесь на команду ставится
// флаг «без окна». Прямой exec.Command в остальном коде запрещён проверкой
// scripts/check-release-contracts.mjs — иначе дыра открывалась бы заново с
// каждым новым вызовом, а увидеть её можно только на живой Windows.
package osproc

import (
	"context"
	"os/exec"
)

// Command — замена exec.Command.
func Command(name string, args ...string) *exec.Cmd {
	return Hide(exec.Command(name, args...))
}

// CommandContext — замена exec.CommandContext. Совпадает по сигнатуре, поэтому
// её можно класть в поля-фабрики вместо exec.CommandContext.
func CommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	return Hide(exec.CommandContext(ctx, name, args...))
}

// Hide помечает уже собранную команду. Нужна там, где команду создаёт чужой
// код или где SysProcAttr заполняется своими полями: Hide дополняет структуру,
// а не подменяет её.
func Hide(command *exec.Cmd) *exec.Cmd {
	if command == nil {
		return nil
	}
	hideWindow(command)
	return command
}
