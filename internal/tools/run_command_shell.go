package tools

import "runtime"

// runCommandShellNote names the shell the command really runs in. Agents
// wrote bash-isms (${PIPESTATUS[0]}) for the sandbox's busybox sh and read
// "bad substitution" as a failure of the project.
func (t RunCommand) runCommandShellNote() string {
	switch {
	case t.Executor != nil:
		return " It runs in the sandbox container through POSIX /bin/sh (busybox), not bash: no ${PIPESTATUS}, arrays, [[ ]] or <(...)."
	case runtime.GOOS == "windows":
		return " It runs through cmd.exe on Windows."
	default:
		return " It runs through POSIX /bin/sh."
	}
}
