package sandbox

import (
	"context"
	"os/exec"
)

// ProcessRequest is the complete host-to-sandbox process contract. Paths are
// host paths beneath WorkspaceRoot; a strong backend maps only that root into
// its execution boundary and translates WorkingDirectory itself.
type ProcessRequest struct {
	WorkspaceRoot       string
	WorkingDirectory    string
	Image               string
	Program             string
	Arguments           []string
	ShellCommand        string
	Environment         []string
	NetworkPolicy       string
	AllowedNetworkHosts []string
	RunID               string
}

// PreparedProcess owns one executable command and an idempotent cleanup hook.
// Cleanup must be called even when the command fails to start or is cancelled.
type PreparedProcess struct {
	Command *exec.Cmd
	Cleanup func(context.Context) error
}

// ProcessExecutor prepares commands inside the same isolation boundary that
// owns the execution workspace. Implementations must fail closed when they
// cannot enforce the requested filesystem, environment, resource or network
// policy.
type ProcessExecutor interface {
	PrepareProcess(context.Context, ProcessRequest) (PreparedProcess, error)
}

// ControlledEgressExecutor marks a process boundary where allowlisted traffic
// is enforced outside the child process. Command-string filtering may then
// remain defense in depth instead of blocking package managers with implicit
// registry destinations.
type ControlledEgressExecutor interface {
	ProcessExecutor
	EnforcesControlledEgress() bool
}

func HasControlledEgress(executor ProcessExecutor) bool {
	controlled, ok := executor.(ControlledEgressExecutor)
	return ok && controlled.EnforcesControlledEgress()
}

// MergeBackend is implemented by sandbox backends that can seed an execution
// from multiple immutable branch heads.
type MergeBackend interface {
	Backend
	Merge(context.Context, MergeRequest) (MergeResult, error)
}
