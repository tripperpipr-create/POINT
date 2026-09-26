package tools

import (
	"runtime"
	"strings"
	"testing"
)

// Agents wrote bash-isms for the sandbox's busybox sh and read "bad
// substitution" as a project failure; the tool now names its real shell.
func TestRunCommandNamesItsShell(t *testing.T) {
	description := RunCommand{}.Definition().Description
	want := "POSIX /bin/sh"
	if runtime.GOOS == "windows" {
		want = "cmd.exe"
	}
	if !strings.Contains(description, want) {
		t.Fatalf("host run_command does not name %s: %q", want, description)
	}
}
