package tools

import (
	"context"
	"encoding/json"
	"strings"

	"local-agent-workbench/internal/domain"
)

// PermissionPrompt is the Claude CLI --permission-prompt-tool callback.
// Native write, shell, web and subagent tools stay denied; Point MCP tools
// remain the only mutation path and are still enforced by executeTool.
type PermissionPrompt struct{}

func (PermissionPrompt) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:        "permission_prompt",
		Description: "Decide whether a native CLI tool may run. Mutations, shell, web and subagents must use Point MCP instead.",
		InputSchema: schema(`{"type":"object","properties":{"tool_name":{"type":"string"},"input":{"type":"object"},"tool_use_id":{"type":"string"}},"additionalProperties":true}`),
	}
}

func (PermissionPrompt) Execute(_ context.Context, raw json.RawMessage) domain.ToolResult {
	var input struct {
		ToolName string          `json:"tool_name"`
		Input    json.RawMessage `json:"input"`
	}
	if invalid := Decode(raw, &input); invalid != nil {
		return *invalid
	}
	name := strings.TrimSpace(input.ToolName)
	switch strings.ToLower(name) {
	case "write", "edit", "notebookedit", "bash", "webfetch", "websearch", "task":
		return OK(map[string]any{
			"behavior": "deny",
			"message":  "Native " + name + " is outside the Point contract. Use Point MCP tools.",
		})
	default:
		updated := json.RawMessage(`{}`)
		if len(input.Input) > 0 {
			updated = input.Input
		}
		return OK(map[string]any{"behavior": "allow", "updatedInput": updated})
	}
}
