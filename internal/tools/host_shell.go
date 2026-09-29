package tools

import (
	"os/exec"
	"sync"

	"local-agent-workbench/internal/osproc"
)

// Оболочка хоста для команд вне песочницы (Q08).
//
// В песочнице pipefail включён давно, а на хосте команды шли через голый
// `/bin/sh -c`: на Debian и Ubuntu это dash, где `set -o pipefail` — ошибка,
// и `npm run verify | tail` возвращал код tail. Проверки на хосте (профиль
// завершения, compose-критерии) и run_command без песочницы теперь получают
// pipefail там, где оболочка его понимает: сама /bin/sh (busybox, bash как
// sh, zsh), иначе bash, если он есть. Где нет ни того, ни другого, команда
// идёт как прежде, и описание инструмента говорит об этом прямо.

type hostShellChoice struct {
	program  string
	pipefail bool
}

var hostShell = sync.OnceValue(func() hostShellChoice {
	if osproc.Command("/bin/sh", "-c", "set -o pipefail").Run() == nil {
		return hostShellChoice{program: "/bin/sh", pipefail: true}
	}
	if bash, err := exec.LookPath("bash"); err == nil && osproc.Command(bash, "-c", "set -o pipefail").Run() == nil {
		return hostShellChoice{program: bash, pipefail: true}
	}
	return hostShellChoice{program: "/bin/sh"}
})

// HostShellCommand — программа и аргументы для команды на хосте (не Windows).
func HostShellCommand(command string) (string, []string) {
	choice := hostShell()
	if choice.pipefail {
		return choice.program, []string{"-c", "set -o pipefail\n" + command}
	}
	return choice.program, []string{"-c", command}
}

// hostShellNote — какая оболочка и включён ли pipefail, словами для модели.
func hostShellNote() string {
	choice := hostShell()
	if choice.pipefail {
		return " It runs through " + choice.program + " with pipefail on, so `cmd 2>&1 | tail -20` returns the exit code of cmd."
	}
	return " It runs through POSIX /bin/sh without pipefail: `cmd | tail` returns the exit code of tail, so do not pipe a check whose exit code matters."
}
