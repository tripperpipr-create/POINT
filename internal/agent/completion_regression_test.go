package agent

import (
	"local-agent-workbench/internal/domain"
	"testing"
)

func TestVerificationFailureCannotBeHiddenByAnotherSuccess(t *testing.T) {
	p := domain.DefaultProfile()
	p.AllowedTools = []string{"run_command", "propose_patch"}
	tr := newCompletionTracker(p, "implement and test", nil)
	check := func(cmd string, code, rev int) {
		tr.ObserveTool("run_command", commandArguments(t, cmd), commandResult(t, code, false), rev)
	}
	check("go test ./...", 0, 1)
	check("go test ./...", 1, 1)
	check("go build ./...", 0, 1)
	if len(tr.Missing(1, []string{"main.go"})) == 0 {
		t.Fatal("failed test hidden by build")
	}
	check("go test ./...", 0, 1)
	if got := tr.Missing(1, []string{"main.go"}); len(got) != 0 {
		t.Fatal(got)
	}
	if len(tr.Missing(2, []string{"main.go"})) == 0 {
		t.Fatal("stale tests accepted")
	}
	tr.ObserveTool("run_command", commandArguments(t, "go test ./..."), commandResult(t, 0, true), 1)
	if len(tr.Missing(1, []string{"main.go"})) == 0 {
		t.Fatal("timeout accepted")
	}
}
