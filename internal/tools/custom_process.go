package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/osproc"
	"local-agent-workbench/internal/sandbox"
	"local-agent-workbench/internal/security"
	"local-agent-workbench/internal/workspace"
)

var customToolPlaceholderPattern = regexp.MustCompile(`\{\{[a-z][a-z0-9_]{0,63}\}\}`)
var windowsDriveProgramPattern = regexp.MustCompile(`^[A-Za-z]:`)

type CustomProcess struct {
	FS                  *workspace.FS
	Config              domain.CustomTool
	NetworkPolicy       string
	AllowedNetworkHosts []string
	Executor            sandbox.ProcessExecutor
	RunID               string
}

type effectiveProcess struct {
	Program string
	Args    []string
	Reason  string
	Values  map[string]any
}

// CustomProcessPreview is the fully validated operation a process tool would
// request. Producing it never starts a process.
type CustomProcessPreview struct {
	Definition     domain.ToolDefinition `json:"definition"`
	Program        string                `json:"program"`
	Arguments      []string              `json:"arguments"`
	Command        string                `json:"command"`
	Parameters     map[string]any        `json:"parameters"`
	CWD            string                `json:"cwd"`
	ResolvedCWD    string                `json:"resolvedCwd"`
	Reason         string                `json:"reason"`
	TimeoutSeconds int                   `json:"timeoutSeconds"`
}

func (t CustomProcess) Definition() domain.ToolDefinition {
	properties := map[string]any{
		"reason": map[string]any{"type": "string", "description": "Почему этот запуск нужен для текущей задачи", "maxLength": 4096},
	}
	required := []string{"reason"}
	for _, parameter := range t.Config.Parameters {
		property := map[string]any{"description": parameter.Description}
		switch parameter.Type {
		case domain.CustomToolParameterInteger:
			property["type"] = "integer"
		default:
			property["type"] = "string"
			maximum := parameter.MaxLength
			if maximum <= 0 {
				maximum = 1024
			}
			property["maxLength"] = maximum
		}
		if parameter.Type == domain.CustomToolParameterEnum {
			property["enum"] = parameter.EnumValues
		}
		properties[parameter.Name] = property
		if parameter.Required {
			required = append(required, parameter.Name)
		}
	}
	encoded, _ := json.Marshal(map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false})
	return domain.ToolDefinition{Name: t.Config.ID, Description: t.Config.Description, InputSchema: encoded}
}

func (t CustomProcess) ValidateArguments(raw json.RawMessage) *domain.ToolResult {
	if _, err := t.prepare(raw); err != nil {
		result := Fail("invalid_input", err.Error())
		return &result
	}
	return nil
}

func (t CustomProcess) Execute(ctx context.Context, raw json.RawMessage) domain.ToolResult {
	preview, err := t.Preview(raw)
	if err != nil {
		return Fail("invalid_input", err.Error())
	}
	policyCommand := strings.TrimSpace(strings.Join(append([]string{preview.Program}, preview.Arguments...), " "))
	if denied := deniedCommandReason(policyCommand); denied != "" {
		return FailWithHint("command_denied", denied, "use a non-interactive local test, build, lint, or inspection command that does not require elevated or destructive privileges")
	}
	if len(t.AllowedNetworkHosts) == 0 || !sandbox.HasControlledEgress(t.Executor) {
		if denied := deniedNetworkCommandReason(policyCommand, t.NetworkPolicy, t.AllowedNetworkHosts); denied != "" {
			return FailWithHint("network_denied", denied, "remove the outbound network/package/git remote action, or ask the user to allow a specific host in the agent network policy")
		}
	}
	commandCtx, cancel := context.WithTimeout(ctx, time.Duration(t.Config.TimeoutSeconds)*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	if t.Executor != nil {
		prepared, prepareErr := t.Executor.PrepareProcess(commandCtx, sandbox.ProcessRequest{
			WorkspaceRoot: t.FS.Root(), WorkingDirectory: preview.ResolvedCWD,
			Program: preview.Program, Arguments: append([]string(nil), preview.Arguments...),
			Environment: sanitizedProcessEnv(), NetworkPolicy: t.NetworkPolicy,
			AllowedNetworkHosts: append([]string(nil), t.AllowedNetworkHosts...),
			RunID:               t.RunID,
		})
		if prepareErr != nil {
			return FailWithHint("sandbox_denied", prepareErr.Error(), "use a local verifier that fits the configured sandbox network and resource policy")
		}
		if prepared.Command == nil {
			return Fail("sandbox_unavailable", "sandbox backend returned no process")
		}
		cmd = prepared.Command
		if prepared.Cleanup != nil {
			defer func() {
				cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cleanupCancel()
				_ = prepared.Cleanup(cleanupCtx)
			}()
		}
	} else {
		cmd = osproc.Command(preview.Program, preview.Arguments...)
		cmd.Dir = preview.ResolvedCWD
		cmd.Env = sanitizedProcessEnv()
	}
	stdout, stderr := &limitedWriter{limit: 64 * 1024}, &limitedWriter{limit: 64 * 1024}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	started := time.Now()
	runErr := runProcess(commandCtx, cmd)
	exitCode := 0
	if runErr != nil {
		if exit, ok := runErr.(*exec.ExitError); ok {
			exitCode = exit.ExitCode()
		} else {
			exitCode = -1
		}
	}
	result := OK(map[string]any{
		"stdout": security.Redact(stdout.String()), "stderr": security.Redact(stderr.String()),
		"exitCode": exitCode, "durationMs": time.Since(started).Milliseconds(),
		"timedOut": commandCtx.Err() == context.DeadlineExceeded,
	})
	result.Truncated = stdout.truncated || stderr.truncated
	return result
}

func (t CustomProcess) ApprovalArguments(raw json.RawMessage) json.RawMessage {
	preview, err := t.Preview(raw)
	if err != nil {
		payload, _ := json.Marshal(map[string]any{"toolId": t.Config.ID, "displayName": t.Config.DisplayName, "error": err.Error()})
		return payload
	}
	payload, _ := json.Marshal(map[string]any{
		"toolId": t.Config.ID, "displayName": t.Config.DisplayName, "kind": t.Config.Kind,
		"program": preview.Program, "arguments": preview.Arguments, "command": preview.Command,
		"parameters": preview.Parameters, "cwd": preview.CWD, "reason": preview.Reason,
		"timeoutSeconds": preview.TimeoutSeconds,
	})
	return payload
}

func (t CustomProcess) Preview(raw json.RawMessage) (CustomProcessPreview, error) {
	effective, err := t.prepare(raw)
	if err != nil {
		return CustomProcessPreview{}, err
	}
	resolvedCWD, err := t.FS.Resolve(t.Config.CWD, false)
	if err != nil {
		return CustomProcessPreview{}, fmt.Errorf("resolve process working directory: %w", err)
	}
	info, err := os.Stat(resolvedCWD)
	if err != nil {
		return CustomProcessPreview{}, fmt.Errorf("inspect process working directory: %w", err)
	}
	if !info.IsDir() {
		return CustomProcessPreview{}, fmt.Errorf("process working directory is not a directory")
	}
	quoted := make([]string, 0, len(effective.Args)+1)
	quoted = append(quoted, strconv.Quote(effective.Program))
	for _, argument := range effective.Args {
		quoted = append(quoted, strconv.Quote(argument))
	}
	return CustomProcessPreview{
		Definition: t.Definition(), Program: effective.Program, Arguments: effective.Args,
		Command: strings.Join(quoted, " "), Parameters: effective.Values,
		CWD: t.Config.CWD, ResolvedCWD: resolvedCWD, Reason: effective.Reason,
		TimeoutSeconds: t.Config.TimeoutSeconds,
	}, nil
}

func (t CustomProcess) prepare(raw json.RawMessage) (effectiveProcess, error) {
	var object map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &object) != nil || object == nil {
		return effectiveProcess{}, fmt.Errorf("tool arguments must be a JSON object")
	}
	var reason string
	if value, ok := object["reason"]; !ok || json.Unmarshal(value, &reason) != nil || strings.TrimSpace(reason) == "" {
		return effectiveProcess{}, fmt.Errorf("reason is required")
	}
	if len(reason) > 4*1024 || !utf8.ValidString(reason) || strings.IndexByte(reason, 0) >= 0 {
		return effectiveProcess{}, fmt.Errorf("reason exceeds its limit")
	}
	delete(object, "reason")
	values := make(map[string]string, len(t.Config.Parameters))
	publicValues := make(map[string]any, len(t.Config.Parameters))
	missing := make(map[string]bool)
	for _, parameter := range t.Config.Parameters {
		rawValue, exists := object[parameter.Name]
		if !exists || string(rawValue) == "null" {
			if parameter.Required {
				return effectiveProcess{}, fmt.Errorf("parameter %q is required", parameter.Name)
			}
			missing[parameter.Name] = true
			continue
		}
		delete(object, parameter.Name)
		var rendered string
		switch parameter.Type {
		case domain.CustomToolParameterInteger:
			var value int64
			if json.Unmarshal(rawValue, &value) != nil {
				return effectiveProcess{}, fmt.Errorf("parameter %q must be an integer", parameter.Name)
			}
			rendered = strconv.FormatInt(value, 10)
			publicValues[parameter.Name] = value
		case domain.CustomToolParameterString, domain.CustomToolParameterEnum, domain.CustomToolParameterWorkspacePath:
			var value string
			if json.Unmarshal(rawValue, &value) != nil || !utf8.ValidString(value) || strings.IndexByte(value, 0) >= 0 {
				return effectiveProcess{}, fmt.Errorf("parameter %q must be valid text", parameter.Name)
			}
			maximum := parameter.MaxLength
			if maximum <= 0 {
				maximum = 1024
			}
			if len(value) > maximum {
				return effectiveProcess{}, fmt.Errorf("parameter %q exceeds %d bytes", parameter.Name, maximum)
			}
			if parameter.Type == domain.CustomToolParameterEnum && !slices.Contains(parameter.EnumValues, value) {
				return effectiveProcess{}, fmt.Errorf("parameter %q is not an allowed value", parameter.Name)
			}
			if parameter.Type == domain.CustomToolParameterWorkspacePath {
				if workspace.IsSensitive(value) {
					return effectiveProcess{}, fmt.Errorf("parameter %q points to a sensitive file", parameter.Name)
				}
				absolute, resolveErr := t.FS.Resolve(filepath.ToSlash(value), false)
				if resolveErr != nil {
					return effectiveProcess{}, fmt.Errorf("parameter %q: %w", parameter.Name, resolveErr)
				}
				relative, relativeErr := filepath.Rel(t.FS.Root(), absolute)
				if relativeErr != nil {
					return effectiveProcess{}, fmt.Errorf("parameter %q: %w", parameter.Name, relativeErr)
				}
				value = filepath.ToSlash(relative)
			}
			rendered = value
			publicValues[parameter.Name] = value
		default:
			return effectiveProcess{}, fmt.Errorf("parameter %q has unsupported type", parameter.Name)
		}
		values[parameter.Name] = rendered
	}
	if len(object) > 0 {
		for name := range object {
			return effectiveProcess{}, fmt.Errorf("unknown parameter %q", name)
		}
	}
	args := make([]string, 0, len(t.Config.Arguments))
	expandedBytes := 0
	for _, template := range t.Config.Arguments {
		omit := false
		expanded := customToolPlaceholderPattern.ReplaceAllStringFunc(template, func(placeholder string) string {
			name := strings.TrimSuffix(strings.TrimPrefix(placeholder, "{{"), "}}")
			if missing[name] {
				omit = true
				return ""
			}
			return values[name]
		})
		if omit {
			continue
		}
		if len(expanded) > 16*1024 {
			return effectiveProcess{}, fmt.Errorf("expanded process argument exceeds 16 KiB")
		}
		expandedBytes += len(expanded)
		if expandedBytes > 64*1024 {
			return effectiveProcess{}, fmt.Errorf("expanded process argv exceeds 64 KiB")
		}
		args = append(args, expanded)
	}
	program := t.Config.Program
	if filepath.IsAbs(program) || strings.HasPrefix(program, "/") || windowsDriveProgramPattern.MatchString(program) || strings.HasPrefix(program, `\\`) {
		return effectiveProcess{}, fmt.Errorf("absolute process programs are not allowed")
	}
	if strings.ContainsAny(program, `/\`) {
		resolved, resolveErr := t.FS.Resolve(filepath.ToSlash(program), false)
		if resolveErr != nil {
			return effectiveProcess{}, fmt.Errorf("resolve process program: %w", resolveErr)
		}
		info, statErr := os.Stat(resolved)
		if statErr != nil {
			return effectiveProcess{}, fmt.Errorf("inspect process program: %w", statErr)
		}
		if info.IsDir() {
			return effectiveProcess{}, fmt.Errorf("process program points to a directory")
		}
		program = resolved
	}
	return effectiveProcess{Program: program, Args: args, Reason: reason, Values: publicValues}, nil
}
