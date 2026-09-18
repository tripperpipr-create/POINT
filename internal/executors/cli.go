package executors

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"local-agent-workbench/internal/osproc"
	"local-agent-workbench/internal/mcp"
)

type CLI struct {
	Runtime    Kind
	Executable string
	mu         sync.Mutex
	active     map[string]*exec.Cmd
}

func NewCLI(kind Kind) *CLI {
	name := map[Kind]string{KindCursor: "agent", KindCodex: "codex", KindClaude: "claude"}[kind]
	return &CLI{Runtime: kind, Executable: name, active: map[string]*exec.Cmd{}}
}

func (c *CLI) Kind() Kind { return c.Runtime }

func (c *CLI) Capabilities() Capabilities {
	base := Capabilities{Headless: true, NativeRead: true, NativeWrite: true, NativeShell: true, MCP: true, StructuredEvents: true}
	switch c.Runtime {
	case KindClaude:
		base.Resume, base.ApprovalDelegation, base.Images, base.Web = true, true, true, true
	case KindCodex:
		base.Resume, base.ProcessIsolation, base.NetworkIsolation = true, true, true
	case KindCursor:
		base.Resume, base.ProcessIsolation, base.NetworkIsolation, base.Images = true, true, true, true
	}
	return base
}

func (c *CLI) Probe(ctx context.Context) error {
	path, err := exec.LookPath(c.Executable)
	if err != nil {
		return err
	}
	return osproc.CommandContext(ctx, path, "--version").Run()
}

func (c *CLI) Start(ctx context.Context, request Request, onEvent func(Event) error) (Result, error) {
	return c.Run(ctx, request, onEvent)
}

func (c *CLI) Run(ctx context.Context, request Request, onEvent func(Event) error) (Result, error) {
	return c.run(ctx, request, false, onEvent)
}

func (c *CLI) Resume(ctx context.Context, request Request, onEvent func(Event) error) (Result, error) {
	if strings.TrimSpace(request.SessionID) == "" {
		return Result{}, errors.New("resume requires a session id")
	}
	return c.run(ctx, request, true, onEvent)
}

func (c *CLI) run(ctx context.Context, request Request, resume bool, onEvent func(Event) error) (Result, error) {
	if strings.TrimSpace(request.WorkspacePath) == "" {
		return Result{}, errors.New("CLI execution requires a workspace")
	}
	if onEvent == nil {
		onEvent = func(Event) error { return nil }
	}
	path, err := exec.LookPath(c.Executable)
	if err != nil {
		return Result{}, err
	}
	args, stdin, cleanup, env, err := c.command(request, resume)
	if err != nil {
		return Result{}, err
	}
	defer cleanup()
	command := osproc.CommandContext(ctx, path, args...)
	command.Dir = request.WorkspacePath
	command.Stdin = strings.NewReader(stdin)
	command.Env = cleanEnvironment(request.Environment, env)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return Result{}, err
	}
	var stderr strings.Builder
	command.Stderr = &stderr
	if err = command.Start(); err != nil {
		return Result{}, err
	}
	key := request.SessionID
	if key == "" {
		key = fmt.Sprintf("%s-%d", c.Runtime, command.Process.Pid)
	}
	c.mu.Lock()
	c.active[key] = command
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.active, key)
		c.mu.Unlock()
	}()
	result, decodeErr := decodeJSONL(stdout, onEvent)
	waitErr := command.Wait()
	if result.SessionID == "" {
		result.SessionID = key
	}
	if waitErr != nil {
		if exit, ok := waitErr.(*exec.ExitError); ok {
			result.ExitCode = exit.ExitCode()
		} else {
			result.ExitCode = -1
		}
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = waitErr.Error()
		}
		return result, fmt.Errorf("%s CLI failed: %s", c.Runtime, detail)
	}
	if decodeErr != nil {
		return result, decodeErr
	}
	return result, nil
}

func (c *CLI) Stop(sessionID string) error {
	c.mu.Lock()
	command := c.active[sessionID]
	c.mu.Unlock()
	if command == nil || command.Process == nil {
		return errors.New("CLI session is not active")
	}
	return command.Process.Kill()
}

func (c *CLI) command(request Request, resume bool) (args []string, stdin string, cleanup func(), extraEnv map[string]string, err error) {
	cleanup = func() {}
	extraEnv = map[string]string{}
	model := strings.TrimSpace(request.Model)
	switch c.Runtime {
	case KindClaude:
		args = []string{"-p", "--output-format", "stream-json", "--verbose", "--include-partial-messages", "--permission-mode", "dontAsk"}
		allowed := []string{"Read", "Grep", "Glob"}
		disallowed := []string{"WebFetch", "WebSearch", "Task"}
		if request.MCPOnly || !request.WriteFiles {
			disallowed = append(disallowed, "Write", "Edit", "NotebookEdit")
		} else {
			allowed = append(allowed, "Write", "Edit", "NotebookEdit")
		}
		if request.MCPOnly || !request.ExecuteCommands {
			disallowed = append(disallowed, "Bash")
		} else {
			allowed = append(allowed, "Bash")
		}
		args = append(args, "--allowed-tools", strings.Join(allowed, ","), "--disallowed-tools", strings.Join(disallowed, ","))
		if resume {
			args = append(args, "--resume", request.SessionID)
		}
		if model != "" && model != "auto" {
			args = append(args, "--model", model)
		}
		if request.SystemPrompt != "" {
			args = append(args, "--append-system-prompt", request.SystemPrompt)
		}
		if request.MCPConfigJSON != "" {
			var configPath string
			configPath, cleanup, err = writeRuntimeFile("point-claude-mcp-*.json", request.MCPConfigJSON)
			if err != nil {
				return nil, "", cleanup, nil, err
			}
			args = append(args, "--strict-mcp-config", "--mcp-config", configPath, "--permission-prompt-tool", mcp.ToolPattern("permission_prompt"))
		}
		stdin = request.Prompt
	case KindCodex:
		sandbox := "read-only"
		if request.WriteFiles && !request.MCPOnly {
			sandbox = "workspace-write"
		}
		args = []string{"exec", "--json", "--sandbox", sandbox}
		if resume {
			args = append(args, "resume", request.SessionID)
		}
		if model != "" && model != "auto" {
			args = append(args, "--model", model)
		}
		if request.MCPConfigJSON != "" {
			url, token := pointMCP(request.MCPConfigJSON)
			if url != "" {
				args = append(args, "-c", "mcp_servers.point.url="+strconv.Quote(url), "-c", "mcp_servers.point.bearer_token_env_var=\"POINT_MCP_TOKEN\"")
				extraEnv["POINT_MCP_TOKEN"] = token
			}
		}
		args = append(args, "-")
		stdin = request.SystemPrompt + "\n\n" + request.Prompt
	case KindCursor:
		args = []string{"-p", "--output-format", "stream-json", "--stream-partial-output", "--approve-mcps", "--trust", "--workspace", request.WorkspacePath, "--sandbox", "enabled"}
		if request.WriteFiles && !request.MCPOnly {
			args = append(args, "--force")
		} else {
			args = append(args, "--mode", "ask")
		}
		if model != "" && model != "auto" {
			args = append(args, "--model", model)
		}
		if request.MCPConfigJSON != "" {
			config := strings.ReplaceAll(request.MCPConfigJSON, pointMCPToken(request.MCPConfigJSON), "${env:POINT_MCP_TOKEN}")
			var configPath string
			configPath, cleanup, err = writeRuntimeFile("point-cursor-mcp-*.json", config)
			if err != nil {
				return nil, "", cleanup, nil, err
			}
			args = append(args, "--mcp-config", configPath)
			extraEnv["POINT_MCP_TOKEN"] = pointMCPToken(request.MCPConfigJSON)
		}
		prompt := request.SystemPrompt + "\n\n" + request.Prompt
		if len(prompt) > 24000 {
			return nil, "", cleanup, nil, errors.New("Cursor CLI prompt exceeds safe Windows argv limit")
		}
		args = append(args, prompt)
	default:
		return nil, "", cleanup, nil, fmt.Errorf("unsupported CLI runtime %q", c.Runtime)
	}
	return args, stdin, cleanup, extraEnv, nil
}

func cleanEnvironment(request, extra map[string]string) []string {
	allowed := map[string]bool{"PATH": true, "PATHEXT": true, "SYSTEMROOT": true, "WINDIR": true, "HOME": true, "USERPROFILE": true, "TEMP": true, "TMP": true, "APPDATA": true, "LOCALAPPDATA": true}
	out := make([]string, 0, len(os.Environ())+len(request)+len(extra))
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if allowed[strings.ToUpper(key)] {
			out = append(out, item)
		}
	}
	for key, value := range request {
		out = append(out, key+"="+value)
	}
	for key, value := range extra {
		out = append(out, key+"="+value)
	}
	return out
}

func writeRuntimeFile(pattern, content string) (string, func(), error) {
	file, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", func() {}, err
	}
	path := file.Name()
	cleanup := func() { _ = os.Remove(path) }
	if err = file.Chmod(0o600); err == nil {
		_, err = io.WriteString(file, content)
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		cleanup()
		return "", func() {}, err
	}
	return filepath.Clean(path), cleanup, nil
}

func pointMCP(raw string) (string, string) {
	var config struct {
		Servers map[string]struct {
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if json.Unmarshal([]byte(raw), &config) != nil {
		return "", ""
	}
	server := config.Servers["point"]
	return server.URL, strings.TrimPrefix(server.Headers["Authorization"], "Bearer ")
}

func pointMCPToken(raw string) string {
	_, token := pointMCP(raw)
	return token
}

func decodeJSONL(reader io.Reader, onEvent func(Event) error) (Result, error) {
	result := Result{}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		var raw map[string]any
		if json.Unmarshal(line, &raw) != nil {
			continue
		}
		event := Event{Kind: stringValue(raw, "type"), SessionID: firstString(raw, "session_id", "thread_id")}
		event.Text = firstString(raw, "result", "text", "message")
		if item, ok := raw["item"].(map[string]any); ok {
			if event.Kind == "" {
				event.Kind = stringValue(item, "type")
			}
			if event.Text == "" {
				event.Text = firstString(item, "text", "message")
			}
		}
		event.Data = line
		if event.SessionID != "" {
			result.SessionID = event.SessionID
		}
		if event.Text != "" {
			result.Text = event.Text
		}
		if err := onEvent(event); err != nil {
			return result, err
		}
	}
	return result, scanner.Err()
}

func stringValue(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return value
}

func firstString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := stringValue(values, key); value != "" {
			return value
		}
	}
	return ""
}
