package executors

import (
	"context"
	"encoding/json"
	"io"
	"time"

	"local-agent-workbench/internal/domain"
)

type Kind string

const (
	KindPoint  Kind = "point"
	KindCursor Kind = "cursor"
	KindCodex  Kind = "codex"
	KindClaude Kind = "claude"
)

type Capabilities struct {
	Headless           bool `json:"headless"`
	NativeRead         bool `json:"nativeRead"`
	NativeWrite        bool `json:"nativeWrite"`
	NativeShell        bool `json:"nativeShell"`
	MCP                bool `json:"mcp"`
	Resume             bool `json:"resume"`
	StructuredEvents   bool `json:"structuredEvents"`
	ApprovalDelegation bool `json:"approvalDelegation"`
	ProcessIsolation   bool `json:"processIsolation"`
	NetworkIsolation   bool `json:"networkIsolation"`
	Images             bool `json:"images"`
	Web                bool `json:"web"`
}

type Request struct {
	Provider        domain.ProviderKind
	Model           string
	Prompt          string
	SystemPrompt    string
	WorkspacePath   string
	MCPConfigJSON   string
	MCPTools        []string
	SessionID       string
	MaxDuration     time.Duration
	Environment     map[string]string
	WriteFiles      bool
	ExecuteCommands bool
	MCPOnly         bool
}

type Event struct {
	Kind      string          `json:"kind"`
	SessionID string          `json:"sessionId,omitempty"`
	Text      string          `json:"text,omitempty"`
	Tool      string          `json:"tool,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
}

type Result struct {
	SessionID string `json:"sessionId,omitempty"`
	Text      string `json:"text,omitempty"`
	ExitCode  int    `json:"exitCode"`
}

type Executor interface {
	Kind() Kind
	Capabilities() Capabilities
	Probe(context.Context) error
	Start(context.Context, Request, func(Event) error) (Result, error)
	Run(context.Context, Request, func(Event) error) (Result, error)
	Resume(context.Context, Request, func(Event) error) (Result, error)
	Stop(string) error
}

// EventDecoder lets adapters share process lifecycle while retaining their
// provider-specific JSONL formats.
type EventDecoder interface {
	Decode(io.Reader, func(Event) error) (Result, error)
}

func KindForProvider(provider domain.ProviderKind) Kind {
	switch provider {
	case domain.ProviderCursor:
		return KindCursor
	case domain.ProviderCodexCLI:
		return KindCodex
	case domain.ProviderClaudeCLI:
		return KindClaude
	default:
		return KindPoint
	}
}

// CapabilityNames is the planner/catalog matrix for a runtime. Native write
// and shell are listed because the CLI binary can do them; Engine still runs
// MCP-only and disables those native tools for project executions.
func CapabilityNames(caps Capabilities) []string {
	var names []string
	if caps.Headless {
		names = append(names, "headless")
	}
	if caps.NativeRead {
		names = append(names, "native_read")
	}
	if caps.NativeWrite {
		names = append(names, "native_write")
	}
	if caps.NativeShell {
		names = append(names, "native_shell")
	}
	if caps.MCP {
		names = append(names, "mcp")
	}
	if caps.Resume {
		names = append(names, "resume")
	}
	if caps.StructuredEvents {
		names = append(names, "structured_events")
	}
	if caps.ApprovalDelegation {
		names = append(names, "approval_delegation")
	}
	if caps.ProcessIsolation {
		names = append(names, "process_isolation")
	}
	if caps.NetworkIsolation {
		names = append(names, "network_isolation")
	}
	if caps.Images {
		names = append(names, "images")
	}
	if caps.Web {
		names = append(names, "web")
	}
	return names
}
