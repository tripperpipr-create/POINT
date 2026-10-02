package agent

import (
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/sandbox"
)

// A local run must never inherit the engine's Docker executor.
func (e *Engine) executorForProfile(profile domain.AgentProfile, brief *domain.TaskBrief) sandbox.ProcessExecutor {
	if domain.HostLiveFastAgent(profile, brief) {
		return nil
	}
	return e.processExecutor
}
