package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/servers"
)

// ServerProfileSource loads durable SSH profiles for agent tools.
type ServerProfileSource interface {
	GetServerProfile(ctx context.Context, id string) (servers.Profile, error)
}

// SSHToolConfig is shared by SSH agent tools.
type SSHToolConfig struct {
	Profiles            ServerProfileSource
	Runner              servers.Runner
	NetworkPolicy       string
	AllowedNetworkHosts []string
}

func (c SSHToolConfig) runner() servers.Runner {
	if c.Runner != nil {
		return c.Runner
	}
	return servers.OpenSSHRunner{}
}

func (c SSHToolConfig) loadProfile(ctx context.Context, id string) (servers.Profile, *domain.ToolResult) {
	if c.Profiles == nil {
		result := FailWithHint("servers_unavailable", "каталог SSH-серверов недоступен", "сохраните профиль сервера в Гильдии → Связи")
		return servers.Profile{}, &result
	}
	id = strings.TrimSpace(id)
	if id == "" {
		result := Fail("invalid_input", "нужен profileId сохранённого SSH-сервера")
		return servers.Profile{}, &result
	}
	profile, err := c.Profiles.GetServerProfile(ctx, id)
	if err != nil {
		result := Fail("not_found", err.Error())
		return servers.Profile{}, &result
	}
	if denied := deniedSSHNetwork(profile.TargetHost(), c.NetworkPolicy, c.AllowedNetworkHosts); denied != "" {
		result := FailWithHint("network_denied", denied, "разрешите хост через toolPolicies network:<host>=ALLOW или отключите network DENY")
		return servers.Profile{}, &result
	}
	return profile, nil
}

func deniedSSHNetwork(host, policy string, allowedHosts []string) string {
	normalized := strings.ToUpper(strings.TrimSpace(policy))
	if normalized == "" || normalized == "ALLOW" || normalized == "ASK" {
		return ""
	}
	if networkHostAllowed(host, allowedHosts) {
		return ""
	}
	return fmt.Sprintf("outbound SSH to %q is denied by the agent network policy", host)
}

// SSHTestConnection verifies a saved SSH profile (key/agent; password unsupported for agents).
type SSHTestConnection struct{ Config SSHToolConfig }

func (t SSHTestConnection) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:        "ssh_test_connection",
		Description: "Проверяет сохранённый SSH-профиль (ключ или ssh-agent). Не изменяет удалённую систему.",
		InputSchema: schema(`{"type":"object","properties":{"profileId":{"type":"string"},"reason":{"type":"string"}},"required":["profileId","reason"],"additionalProperties":false}`),
	}
}

func (t SSHTestConnection) Execute(ctx context.Context, raw json.RawMessage) domain.ToolResult {
	var input struct {
		ProfileID, Reason string
	}
	if bad := Decode(raw, &input); bad != nil {
		return *bad
	}
	if len(input.Reason) > 4*1024 {
		return Fail("invalid_input", "reason exceeds its size limit")
	}
	profile, bad := t.Config.loadProfile(ctx, input.ProfileID)
	if bad != nil {
		return *bad
	}
	if profile.AuthMethod == servers.AuthPassword {
		return FailWithHint("auth_unsupported", "агент не использует сохранённые пароли", "переключите профиль на ключ или ssh-agent")
	}
	result, err := t.Config.runner().Probe(ctx, profile, "")
	if err != nil {
		return Fail("ssh_error", err.Error())
	}
	return OK(map[string]any{"ok": true, "message": result.Message, "host": profile.Host, "user": profile.User, "port": profile.Port})
}

// SSHListRemote lists a remote path over SSH (read-only).
type SSHListRemote struct{ Config SSHToolConfig }

func (t SSHListRemote) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:        "ssh_list_remote",
		Description: "Показывает содержимое удалённого каталога через SSH (ls). Только чтение.",
		InputSchema: schema(`{"type":"object","properties":{"profileId":{"type":"string"},"path":{"type":"string"},"reason":{"type":"string"}},"required":["profileId","reason"],"additionalProperties":false}`),
	}
}

func (t SSHListRemote) Execute(ctx context.Context, raw json.RawMessage) domain.ToolResult {
	var input struct {
		ProfileID, Path, Reason string
	}
	if bad := Decode(raw, &input); bad != nil {
		return *bad
	}
	profile, bad := t.Config.loadProfile(ctx, input.ProfileID)
	if bad != nil {
		return *bad
	}
	if profile.AuthMethod == servers.AuthPassword {
		return FailWithHint("auth_unsupported", "агент не использует сохранённые пароли", "переключите профиль на ключ или ssh-agent")
	}
	path := strings.TrimSpace(input.Path)
	if path == "" {
		path = profile.DefaultRemotePath
	}
	entries, rawOut, err := t.Config.runner().List(ctx, profile, path, "")
	if err != nil {
		return Fail("ssh_error", err.Error())
	}
	if len(entries) > 200 {
		entries = entries[:200]
	}
	return OK(map[string]any{"path": path, "entries": entries, "raw": truncateToolText(rawOut, 32*1024), "host": profile.Host})
}

// SSHReadRemote reads a bounded UTF-8 preview without turning a harmless
// inspection into an arbitrary remote-command approval.
type SSHReadRemote struct{ Config SSHToolConfig }

func (t SSHReadRemote) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:        "ssh_read_remote",
		Description: "Читает до 64 КиБ одного UTF-8 файла через сохранённый SSH-профиль. Не изменяет сервер.",
		InputSchema: schema(`{"type":"object","properties":{"profileId":{"type":"string"},"path":{"type":"string","minLength":1,"maxLength":1024},"reason":{"type":"string"}},"required":["profileId","path","reason"],"additionalProperties":false}`),
	}
}

func (t SSHReadRemote) Execute(ctx context.Context, raw json.RawMessage) domain.ToolResult {
	var input struct {
		ProfileID, Path, Reason string
	}
	if bad := Decode(raw, &input); bad != nil {
		return *bad
	}
	if len(input.Reason) > 4*1024 {
		return Fail("invalid_input", "reason exceeds its size limit")
	}
	profile, bad := t.Config.loadProfile(ctx, input.ProfileID)
	if bad != nil {
		return *bad
	}
	if profile.AuthMethod == servers.AuthPassword {
		return FailWithHint("auth_unsupported", "агент не использует сохранённые пароли", "переключите профиль на ключ или ssh-agent")
	}
	path := strings.TrimSpace(input.Path)
	if path == "" {
		return Fail("invalid_input", "нужен путь к удалённому файлу")
	}
	content, truncated, err := t.Config.runner().Read(ctx, profile, path, "")
	if err != nil {
		return Fail("ssh_error", err.Error())
	}
	return OK(map[string]any{"path": path, "content": content, "truncated": truncated, "host": profile.Host})
}

// SSHExecRemote runs one remote command after explicit user approval.
type SSHExecRemote struct{ Config SSHToolConfig }

func (t SSHExecRemote) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:        "ssh_exec_remote",
		Description: "Выполняет одну удалённую команду по SSH после явного подтверждения. Мутирующие операции требуют ревью.",
		InputSchema: schema(`{"type":"object","properties":{"profileId":{"type":"string"},"command":{"type":"string"},"reason":{"type":"string"},"timeoutSeconds":{"type":"integer","minimum":1,"maximum":300}},"required":["profileId","command","reason"],"additionalProperties":false}`),
	}
}

func (t SSHExecRemote) ApprovalArguments(raw json.RawMessage) json.RawMessage {
	var input map[string]any
	if err := json.Unmarshal(raw, &input); err != nil {
		return raw
	}
	input["mutatesRemote"] = true
	encoded, err := json.Marshal(input)
	if err != nil {
		return raw
	}
	return encoded
}

func (t SSHExecRemote) Execute(ctx context.Context, raw json.RawMessage) domain.ToolResult {
	var input struct {
		ProfileID, Command, Reason string
		TimeoutSeconds             int `json:"timeoutSeconds"`
	}
	if bad := Decode(raw, &input); bad != nil {
		return *bad
	}
	profile, bad := t.Config.loadProfile(ctx, input.ProfileID)
	if bad != nil {
		return *bad
	}
	if profile.AuthMethod == servers.AuthPassword {
		return FailWithHint("auth_unsupported", "агент не использует сохранённые пароли", "переключите профиль на ключ или ssh-agent")
	}
	timeout := 60 * time.Second
	if input.TimeoutSeconds > 0 {
		timeout = time.Duration(input.TimeoutSeconds) * time.Second
	}
	stdout, stderr, exitCode, err := t.Config.runner().Exec(ctx, profile, input.Command, "", timeout)
	if err != nil {
		return Fail("ssh_error", err.Error())
	}
	return OK(map[string]any{
		"stdout": truncateToolText(stdout, 64*1024), "stderr": truncateToolText(stderr, 32*1024),
		"exitCode": exitCode, "host": profile.Host, "command": input.Command,
	})
}

func truncateToolText(value string, max int) string {
	if max <= 0 || len(value) <= max {
		return value
	}
	return value[:max]
}
