package osproc

import (
	"errors"
	"os/exec"
	"sync"
)

// Group — чужая программа вместе со всеми её потомками.
//
// Остановить программу мало: `npx` запускает node, node — ещё процессы, и
// убитый корень оставляет их жить без хозяина. Поэтому запуск и остановка идут
// парой: StartGroup собирает дерево в одну группу (группа процессов на Unix,
// обход дерева — на Windows), Kill гасит её целиком.
//
// Раньше это умели только пользовательские инструменты (internal/tools) — у
// каждой платформы своя копия. Долгоживущему MCP-серверу нужно то же самое и
// ещё одно: не пережить ядро. Это включается опцией DieWithParent.
type Group struct {
	cmd      *exec.Cmd
	platform groupPlatform
	release  sync.Once
}

// GroupOptions — чем группа отличается от обычного запуска.
type GroupOptions struct {
	// DieWithParent гасит группу, если ядро умерло, не успев её остановить.
	// Linux — сигнал смерти родителя; Windows — Job Object с закрытием по
	// последнему дескриптору. На прочих Unix такого механизма нет, и там
	// остаётся только закрытый stdin: MCP-сервер обязан выйти по EOF.
	//
	// Оговорка Linux: сигнал приходит, когда умирает поток, запустивший
	// процесс. Рантайм Go поток не завершает, если горутина не держала
	// LockOSThread, — поэтому запускать группу из такой горутины нельзя.
	DieWithParent bool
}

// StartGroup запускает команду отдельной группой. Команда должна быть собрана
// через Command/CommandContext этого пакета: окно на Windows прячет Hide.
func StartGroup(command *exec.Cmd, options GroupOptions) (*Group, error) {
	if command == nil {
		return nil, errors.New("osproc: nil command")
	}
	prepareGroup(command, options)
	if err := command.Start(); err != nil {
		return nil, err
	}
	return &Group{cmd: command, platform: attachGroup(command, options)}, nil
}

// Pid — номер корневого процесса группы.
func (g *Group) Pid() int {
	if g == nil || g.cmd == nil || g.cmd.Process == nil {
		return 0
	}
	return g.cmd.Process.Pid
}

// Kill гасит группу целиком. Повторный вызов безопасен.
func (g *Group) Kill() {
	if g == nil || g.cmd == nil || g.cmd.Process == nil {
		return
	}
	killGroup(g)
}

// Release освобождает ресурсы группы после Wait. На Windows закрывает Job
// Object: при DieWithParent это гасит потомков, переживших корень.
func (g *Group) Release() {
	if g == nil {
		return
	}
	g.release.Do(func() { releaseGroup(g) })
}
