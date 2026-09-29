package tools

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"testing"

	"local-agent-workbench/internal/workspace"
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

// Квест 29.09 дважды запускал шестиминутный `npm run verify | tail` с
// ${PIPESTATUS[0]}: busybox отвечал «bad substitution», и код сборки терялся.
func TestSandboxRunCommandRejectsBashOnlySyntaxBeforeStart(t *testing.T) {
	fs, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	executor := &capturingProcessExecutor{}
	tool := RunCommand{FS: fs, Executor: executor}
	for _, command := range []string{
		`npm run verify 2>&1 | tail -25; echo "verify_exit=${PIPESTATUS[0]}"`,
		`if [[ -f package.json ]]; then echo yes; fi`,
		`diff <(sort a) <(sort b)`,
	} {
		raw, _ := json.Marshal(map[string]any{"command": command, "reason": "bash-ism"})
		result := tool.Execute(context.Background(), raw)
		if result.OK || result.Error == nil || result.Error.Code != "unsupported_shell_syntax" {
			t.Fatalf("bash-only syntax accepted: %q => %#v", command, result)
		}
	}
	if len(executor.requests) != 0 {
		t.Fatalf("container started for rejected command: %d", len(executor.requests))
	}
}

func TestSandboxRunCommandEnablesPipefail(t *testing.T) {
	fs, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	executor := &capturingProcessExecutor{}
	tool := RunCommand{FS: fs, Executor: executor}
	raw, _ := json.Marshal(map[string]any{"command": "npm run verify 2>&1 | tail -25", "reason": "verify"})
	tool.Execute(context.Background(), raw)
	if len(executor.requests) != 1 {
		t.Fatalf("expected one sandbox process, got %d", len(executor.requests))
	}
	if got := executor.requests[0].ShellCommand; !strings.HasPrefix(got, "set -o pipefail\n") || !strings.HasSuffix(got, "npm run verify 2>&1 | tail -25") {
		t.Fatalf("pipefail not enabled: %q", got)
	}
	description := tool.Definition().Description
	for _, want := range []string{"pipefail is on", "fresh container", "/tmp"} {
		if !strings.Contains(description, want) {
			t.Fatalf("sandbox description misses %q: %s", want, description)
		}
	}
}
