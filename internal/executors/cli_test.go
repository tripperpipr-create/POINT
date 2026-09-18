package executors

import (
	"strings"
	"testing"
)

func TestKindForProviderAndCapabilities(t *testing.T) {
	for _, kind := range []Kind{KindCursor, KindCodex, KindClaude} {
		cli := NewCLI(kind)
		caps := cli.Capabilities()
		if !caps.Headless || !caps.NativeRead || !caps.NativeWrite || !caps.MCP || !caps.StructuredEvents {
			t.Fatalf("%s capabilities=%#v", kind, caps)
		}
		names := CapabilityNames(caps)
		if len(names) == 0 {
			t.Fatalf("%s capability names empty", kind)
		}
	}
}

func TestPointMCPDoesNotPutTokenInCodexArguments(t *testing.T) {
	config := `{"mcpServers":{"point":{"type":"http","url":"http://127.0.0.1:123/mcp","headers":{"Authorization":"Bearer secret-token"}}}}`
	args, stdin, cleanup, env, err := NewCLI(KindCodex).command(Request{
		Model: "auto", Prompt: "task", SystemPrompt: "system", WorkspacePath: t.TempDir(), MCPConfigJSON: config,
	}, false)
	defer cleanup()
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "secret-token") {
		t.Fatalf("secret leaked into argv: %s", joined)
	}
	if env["POINT_MCP_TOKEN"] != "secret-token" || stdin != "system\n\ntask" {
		t.Fatalf("env=%#v stdin=%q", env, stdin)
	}
}

func TestClaudePermissionPromptToolUsesPointMCPName(t *testing.T) {
	config := `{"mcpServers":{"point":{"type":"http","url":"http://127.0.0.1:123/mcp","headers":{"Authorization":"Bearer secret-token"}}}}`
	args, _, cleanup, _, err := NewCLI(KindClaude).command(Request{
		Model: "auto", Prompt: "task", WorkspacePath: t.TempDir(), MCPConfigJSON: config, MCPOnly: true,
	}, false)
	defer cleanup()
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "secret-token") {
		t.Fatalf("secret leaked into argv: %s", joined)
	}
	if !strings.Contains(joined, "--permission-prompt-tool") || !strings.Contains(joined, "mcp__point__permission_prompt") {
		t.Fatalf("claude MCP config missing Point permission tool: %s", joined)
	}
}

func TestMCPOnlyDisablesNativeMutationTools(t *testing.T) {
	for _, kind := range []Kind{KindClaude, KindCodex, KindCursor} {
		args, _, cleanup, _, err := NewCLI(kind).command(Request{
			Model: "auto", Prompt: "task", SystemPrompt: "system", WorkspacePath: t.TempDir(),
			WriteFiles: true, ExecuteCommands: true, MCPOnly: true,
		}, false)
		defer cleanup()
		if err != nil {
			t.Fatal(err)
		}
		joined := strings.Join(args, " ")
		switch kind {
		case KindClaude:
			if strings.Contains(joined, "Write") && !strings.Contains(joined, "disallowed") {
				t.Fatalf("claude MCP-only still allows writes: %s", joined)
			}
			if !strings.Contains(joined, "Bash") {
				t.Fatalf("claude MCP-only should list Bash as disallowed: %s", joined)
			}
		case KindCodex:
			if !strings.Contains(joined, "read-only") {
				t.Fatalf("codex MCP-only must stay read-only: %s", joined)
			}
		case KindCursor:
			if strings.Contains(joined, "--force") || !strings.Contains(joined, "--mode") {
				t.Fatalf("cursor MCP-only must not force writes: %s", joined)
			}
		}
	}
}

func TestDecodeJSONLNormalizesSessionAndResult(t *testing.T) {
	var events []Event
	result, err := decodeJSONL(strings.NewReader(
		"{\"type\":\"thread.started\",\"thread_id\":\"thread-1\"}\n"+
			"{\"type\":\"result\",\"result\":\"done\"}\n",
	), func(event Event) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.SessionID != "thread-1" || result.Text != "done" || len(events) != 2 {
		t.Fatalf("result=%#v events=%#v", result, events)
	}
}
