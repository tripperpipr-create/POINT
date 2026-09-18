package tools

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/sandbox"
	"local-agent-workbench/internal/workspace"
)

type CustomCommand struct {
	FS                  *workspace.FS
	Config              domain.CustomTool
	NetworkPolicy       string
	AllowedNetworkHosts []string
	Executor            sandbox.ProcessExecutor
	RunID               string
}

func (t CustomCommand) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name: t.Config.ID, Description: t.Config.Description,
		InputSchema: schema(`{"type":"object","properties":{"reason":{"type":"string"}},"required":["reason"],"additionalProperties":false}`),
	}
}

func (t CustomCommand) ValidateArguments(raw json.RawMessage) *domain.ToolResult {
	var input struct {
		Reason string `json:"reason"`
	}
	if bad := Decode(raw, &input); bad != nil {
		return bad
	}
	if strings.TrimSpace(input.Reason) == "" {
		result := Fail("invalid_input", "reason is required")
		return &result
	}
	return nil
}

func (t CustomCommand) Execute(ctx context.Context, raw json.RawMessage) domain.ToolResult {
	if invalid := t.ValidateArguments(raw); invalid != nil {
		return *invalid
	}
	var input struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(raw, &input)
	payload, err := json.Marshal(map[string]any{
		"command": t.Config.Command, "cwd": t.Config.CWD, "reason": input.Reason,
		"timeoutSeconds": t.Config.TimeoutSeconds,
	})
	if err != nil {
		return Fail("encode_error", err.Error())
	}
	return (RunCommand{FS: t.FS, MaxOutput: 128 * 1024, DefaultTimeout: time.Duration(t.Config.TimeoutSeconds) * time.Second, NetworkPolicy: t.NetworkPolicy, AllowedNetworkHosts: t.AllowedNetworkHosts, Executor: t.Executor, RunID: t.RunID}).Execute(ctx, payload)
}

func (t CustomCommand) ApprovalArguments(raw json.RawMessage) json.RawMessage {
	var input struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(raw, &input)
	payload, _ := json.Marshal(map[string]any{
		"toolId": t.Config.ID, "displayName": t.Config.DisplayName,
		"command": t.Config.Command, "cwd": t.Config.CWD,
		"reason": input.Reason, "timeoutSeconds": t.Config.TimeoutSeconds,
	})
	return payload
}
