package tools

import (
	"regexp"
	"runtime"
)

// runCommandShellNote names the shell the command really runs in. Agents
// wrote bash-isms (${PIPESTATUS[0]}) for the sandbox's busybox sh and read
// "bad substitution" as a failure of the project.
//
// Живые квесты 28–29.09 показали ещё две ловушки: каждая команда идёт в новом
// контейнере, и установленное в /tmp к следующей команде исчезает; а код
// `npm run verify | tail` без pipefail — код tail.
func (t RunCommand) runCommandShellNote() string {
	switch {
	case t.Executor != nil:
		return " It runs in the sandbox container through POSIX /bin/sh (busybox), not bash: no ${PIPESTATUS}, arrays, [[ ]] or <(...)." +
			" pipefail is on, so `cmd 2>&1 | tail -20` returns the exit code of cmd." +
			" Every call starts a fresh container: only the workspace persists, /tmp and anything installed outside the workspace are gone by the next call, so chain dependent steps in one command."
	case runtime.GOOS == "windows":
		return " It runs through cmd.exe on Windows."
	default:
		return " It runs through POSIX /bin/sh."
	}
}

// sandboxShellCommand включает pipefail: busybox ash его поддерживает во всех
// образах песочницы, и падение сборки больше не прячется за `| tail`.
func sandboxShellCommand(command string) string {
	return "set -o pipefail\n" + command
}

var bashOnlySyntax = []struct {
	pattern *regexp.Regexp
	name    string
}{
	{regexp.MustCompile(`\$\{PIPESTATUS`), "${PIPESTATUS}"},
	{regexp.MustCompile(`(^|[\s;&|(])\[\[\s`), "[[ ]]"},
	{regexp.MustCompile(`<\(`), "<(...)"},
}

// unsupportedSandboxShellSyntax отказывает до запуска контейнера: busybox
// ответит «bad substitution» только после того, как команда уже отработала,
// — квест 29.09 так потерял код шестиминутной сборки.
func unsupportedSandboxShellSyntax(command string) string {
	for _, item := range bashOnlySyntax {
		if item.pattern.MatchString(command) {
			return "the sandbox shell is busybox sh, not bash: " + item.name + " is not supported"
		}
	}
	return ""
}
