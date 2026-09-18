package environment

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/sandbox"
)

// HostController runs typed environment operations on the Hub host. Agents never
// receive a Docker socket; they only get run_command / file tools.
type HostController struct {
	Executor sandbox.ProcessExecutor
}

type CommandResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

func (c HostController) InstallDependencies(ctx context.Context, plan domain.EnvironmentPlan, workspaceRoot string) (CommandResult, error) {
	for _, cmd := range plan.Commands {
		if cmd.ID == "dependencies" || strings.Contains(strings.ToLower(cmd.Purpose), "install") {
			return c.Run(ctx, plan, workspaceRoot, cmd)
		}
	}
	return CommandResult{}, fmt.Errorf("environment plan has no dependency install command")
}

func (c HostController) Run(ctx context.Context, plan domain.EnvironmentPlan, workspaceRoot string, cmd domain.EnvironmentCommand) (CommandResult, error) {
	if c.Executor == nil {
		return CommandResult{}, fmt.Errorf("environment host controller has no process executor")
	}
	prepared, err := c.Executor.PrepareProcess(ctx, sandbox.ProcessRequest{
		WorkspaceRoot:       workspaceRoot,
		WorkingDirectory:    firstNonEmpty(cmd.WorkingDirectory, workspaceRoot),
		Program:             cmd.Program,
		Arguments:           append([]string(nil), cmd.Arguments...),
		NetworkPolicy:       "ALLOWLIST",
		AllowedNetworkHosts: withTLSPorts(plan.NetworkHosts),
	})
	if err != nil {
		return CommandResult{}, err
	}
	if prepared.Cleanup != nil {
		defer func() { _ = prepared.Cleanup(ctx) }()
	}
	if prepared.Command == nil {
		return CommandResult{}, fmt.Errorf("process executor returned no command")
	}
	var stdout, stderr bytes.Buffer
	prepared.Command.Stdout = &stdout
	prepared.Command.Stderr = &stderr
	runErr := prepared.Command.Run()
	result := CommandResult{Stdout: stdout.String(), Stderr: stderr.String()}
	if runErr != nil {
		var exitErr *exec.ExitError
		if ok := asExitError(runErr, &exitErr); ok {
			result.ExitCode = exitErr.ExitCode()
			return result, nil
		}
		return result, runErr
	}
	return result, nil
}

func (c HostController) CheckHealth(ctx context.Context, plan domain.EnvironmentPlan, workspaceRoot string) ([]CommandResult, error) {
	var out []CommandResult
	for _, check := range plan.HealthChecks {
		result, err := c.Run(ctx, plan, workspaceRoot, check)
		if err != nil {
			return out, err
		}
		out = append(out, result)
		if result.ExitCode != 0 {
			return out, fmt.Errorf("health check %q failed with exit %d", check.ID, result.ExitCode)
		}
	}
	return out, nil
}

func asExitError(err error, target **exec.ExitError) bool {
	if err == nil {
		return false
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		return false
	}
	*target = exitErr
	return true
}

func withTLSPorts(hosts []string) []string {
	result := make([]string, 0, len(hosts))
	for _, host := range hosts {
		host = strings.TrimSpace(host)
		if host == "" {
			continue
		}
		if strings.Contains(host, ":") {
			result = append(result, host)
			continue
		}
		result = append(result, host+":443")
	}
	return result
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
