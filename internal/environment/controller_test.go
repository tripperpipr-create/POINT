package environment

import (
	"context"
	"os/exec"
	"testing"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/sandbox"
)

type fakeProcessExecutor struct{}

func (fakeProcessExecutor) PrepareProcess(_ context.Context, req sandbox.ProcessRequest) (sandbox.PreparedProcess, error) {
	cmd := exec.Command("go", "version")
	if req.Program != "" {
		cmd = exec.Command(req.Program, req.Arguments...)
	}
	return sandbox.PreparedProcess{Command: cmd, Cleanup: func(context.Context) error { return nil }}, nil
}

func TestHostControllerRunsTypedCommand(t *testing.T) {
	ctrl := HostController{Executor: fakeProcessExecutor{}}
	plan := domain.EnvironmentPlan{
		NetworkHosts: []string{"proxy.golang.org"},
		Commands: []domain.EnvironmentCommand{
			{ID: "tests", Purpose: "Run Go version", Program: "go", Arguments: []string{"version"}},
		},
	}
	result, err := ctrl.Run(context.Background(), plan, t.TempDir(), plan.Commands[0])
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%s", result.ExitCode, result.Stderr)
	}
	if result.Stdout == "" {
		t.Fatal("expected go version stdout")
	}
}
