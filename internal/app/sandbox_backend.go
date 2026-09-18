package app

import (
	"errors"

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
