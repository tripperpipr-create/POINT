package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/orchestrator"
	"local-agent-workbench/internal/sandbox"
)

func (a *App) sandboxCapabilities() sandbox.Capabilities {
	if a == nil || a.sandboxBackend == nil {
		return (&sandbox.Manager{}).Capabilities()
	}
	return a.sandboxBackend.Capabilities()
}

func (a *App) sandboxMergeBackend() (sandbox.MergeBackend, error) {
	if a == nil || a.sandboxBackend == nil {
		return nil, errors.New("execution sandbox backend is unavailable")
	}
	backend, ok := a.sandboxBackend.(sandbox.MergeBackend)
	if !ok {
		return nil, errors.New("execution sandbox backend does not support deterministic branch merges")
	}
	return backend, nil
}

func (a *App) sandboxProcessExecutor() sandbox.ProcessExecutor {
	if a == nil || a.sandboxBackend == nil {
		return nil
	}
	executor, _ := a.sandboxBackend.(sandbox.ProcessExecutor)
	return executor
}

// plannerExecutionEnvironment tells the planner what executors can do: the
// container sandbox has no Docker, Compose criteria are checked on the host
// after delivery, and the approved network already carries the stack's
// package registries.
func (a *App) plannerExecutionEnvironment(brief *domain.TaskBrief) *orchestrator.ExecutionEnvironment {
	capabilities := a.sandboxCapabilities()
	environment := &orchestrator.ExecutionEnvironment{SandboxBackend: capabilities.Backend, NetworkHosts: []string{}}
	// A host executable proves nothing about the project execution boundary.
	// Keep this unavailable unless a future executor proves in-sandbox support.
	if brief == nil {
		return environment
	}
	environment.NetworkHosts = append(environment.NetworkHosts, brief.Permissions.NetworkHosts...)
	order := domain.WorkOrder{Criteria: brief.Criteria}
	for _, criterion := range brief.Criteria {
		if deferredHostCriterionV2(order, criterion) {
			environment.HostVerifiedCriteria = append(environment.HostVerifiedCriteria, criterion.ID)
		}
	}
	return environment
}

// sandboxRoot is shared by every core on the machine. Tests point
// POINT_SANDBOX_ROOT at their own temporary directory: their data directories
// are removed after the run, the shared root is not, and test copies piled up
// there by the thousand.
func sandboxRoot() string {
	if root := strings.TrimSpace(os.Getenv("POINT_SANDBOX_ROOT")); root != "" {
		return root
	}
	return filepath.Join(os.TempDir(), "point-sandboxes")
}
