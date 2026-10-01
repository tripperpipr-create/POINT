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
	// CacheScope — область постоянных кэшей пакетных менеджеров (квест).
	// Пусто — кэши живут в tmpfs одной команды, как раньше.
	CacheScope string
	// Authoritative — прогон, чей результат Point принимает как доказательство
	// (приёмка). Он получает только кэши, которые менеджер пакетов сверяет с
	// lock-файлом: подложить в них «зелёный» результат нельзя.
	Authoritative bool
}

// PreparedProcess owns one executable command and an idempotent cleanup hook.
// Cleanup must be called even when the command fails to start or is cancelled.
type PreparedProcess struct {
	Command *exec.Cmd
	Cleanup func(context.Context) error
	// EgressDecisions reads bounded, sanitized gateway decisions before Cleanup
	// removes the gateway container. Nil means no gateway was started.
	EgressDecisions func(context.Context) ([]EgressDecision, error)
}

type EgressDecision struct {
	PolicyDigest string `json:"policyDigest"`
	FQDN         string `json:"fqdn"`
	Port         uint16 `json:"port"`
	Decision     string `json:"decision"`
	Reason       string `json:"reason,omitempty"`
	Bytes        int64  `json:"bytes"`
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
