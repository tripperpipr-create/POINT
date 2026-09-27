package tools

// run_command: команда оболочки в рабочей области под политикой сущности.
// Здесь же отказы до запуска — фоновые процессы, разрушительные команды git и
// сеть мимо разрешённых узлов — и ограниченный по размеру сбор вывода.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/egress"
	"local-agent-workbench/internal/observability"
	"local-agent-workbench/internal/osproc"
	"local-agent-workbench/internal/sandbox"
	"local-agent-workbench/internal/security"
	"local-agent-workbench/internal/workspace"
)

type RunCommand struct {
	FS             *workspace.FS
	MaxOutput      int
	DefaultTimeout time.Duration
	Executor       sandbox.ProcessExecutor
	SandboxImage   string
	RunID          string
	QuestID        string
	// NetworkPolicy is empty for user-owned terminal actions. Agent runtimes
	// set DENY by default and may provide an explicit host allowlist.
	NetworkPolicy       string
	AllowedNetworkHosts []string
	ConfirmedGitRemotes []string
	Grants              *NetworkGrantBook
}

func (t RunCommand) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{Name: "run_command", Description: "Run one non-interactive command in a workspace directory after user approval." + t.runCommandShellNote(), InputSchema: schema(`{"type":"object","properties":{"command":{"type":"string","description":"One local shell command. Do not background or request elevation."},"cwd":{"type":"string","description":"Workspace-relative directory, for example src. Empty uses the workspace root."},"reason":{"type":"string","description":"Why this command is needed for the current task"},"timeoutSeconds":{"type":"integer","minimum":1,"maximum":600}},"required":["command","reason"],"additionalProperties":false}`)}
}

func (t RunCommand) Execute(ctx context.Context, raw json.RawMessage) domain.ToolResult {
	started := time.Now()
	var input struct {
		Command, CWD, Reason string
		TimeoutSeconds       int `json:"timeoutSeconds"`
	}
	if bad := Decode(raw, &input); bad != nil {
		return *bad
	}
	finish := func(result domain.ToolResult) domain.ToolResult {
		return logExecute(ctx, "run_command", started, result,
			"command", observability.Snippet(security.Redact(input.Command), 180),
			"cwd", input.CWD,
			"reason", observability.Snippet(input.Reason, 80),
		)
	}
	if strings.TrimSpace(input.Command) == "" {
		return finish(Fail("invalid_input", "command is empty"))
	}
	if len(input.Command) > 32*1024 || len(input.Reason) > 4*1024 {
		return finish(Fail("invalid_input", "command or reason exceeds its size limit"))
	}
	if isBackgroundShellCommand(input.Command) {
		return finish(Fail("background_process_unsupported", "interactive and background commands are not supported"))
	}
	if denied := deniedCommandReason(input.Command); denied != "" {
		return finish(FailWithHint("command_denied", denied, "use a non-interactive local test, build, lint, or inspection command that does not require elevated or destructive privileges"))
	}
	grantedRemotes := t.Grants.RemotesFor(t.RunID, t.QuestID)
	if denied := deniedUnconfirmedGitRemoteReason(input.Command, t.ConfirmedGitRemotes, grantedRemotes); denied != "" {
		return finish(FailWithHint("git_remote_unconfirmed", denied, "do not invent remotes; wait for Master/user confirmation of the exact repository URL"))
	}
	effectiveHosts := append([]string{}, t.AllowedNetworkHosts...)
	effectiveHosts = append(effectiveHosts, t.Grants.HostsFor(t.RunID, t.QuestID)...)
	if len(effectiveHosts) == 0 || !sandbox.HasControlledEgress(t.Executor) {
		if denied := deniedNetworkCommandReason(input.Command, t.NetworkPolicy, effectiveHosts); denied != "" {
			return finish(FailWithHint("network_denied", denied, "do not retry alternate mirrors; escalate the exact host to the Master so the user can allow or deny it"))
		}
	}
	cwd, err := t.FS.Resolve(input.CWD, false)
	if err != nil {
		return finish(FailWithHint("invalid_cwd", err.Error(), "use a workspace-relative directory such as src, or omit cwd to run at the workspace root"))
	}
	timeout := t.DefaultTimeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	if input.TimeoutSeconds > 0 {
		timeout = time.Duration(input.TimeoutSeconds) * time.Second
	}
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var cmd *exec.Cmd
	if t.Executor != nil {
		prepared, prepareErr := t.Executor.PrepareProcess(commandCtx, sandbox.ProcessRequest{
			WorkspaceRoot: t.FS.Root(), WorkingDirectory: cwd, ShellCommand: input.Command, Image: t.SandboxImage,
			Environment: sanitizedProcessEnv(), NetworkPolicy: t.NetworkPolicy,
			AllowedNetworkHosts: append([]string(nil), effectiveHosts...),
			RunID:               t.RunID,
		})
		if prepareErr != nil {
			return finish(FailWithHint("sandbox_denied", prepareErr.Error(), "use a local verifier that fits the configured sandbox network and resource policy"))
		}
		if prepared.Command == nil {
			return finish(Fail("sandbox_unavailable", "sandbox backend returned no process"))
		}
		cmd = prepared.Command
		if prepared.Cleanup != nil {
			defer func() {
				cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cleanupCancel()
				_ = prepared.Cleanup(cleanupCtx)
			}()
		}
	} else if runtime.GOOS == "windows" {
		cmd = osproc.Command("cmd.exe", "/d", "/s", "/c", input.Command)
	} else {
		cmd = osproc.Command("/bin/sh", "-c", input.Command)
	}
	if t.Executor == nil {
		cmd.Dir = cwd
		cmd.Env = sanitizedProcessEnv()
	}
	max := t.MaxOutput
	if max <= 0 {
		max = 128 * 1024
	}
	streamLimit := max / 2
	if streamLimit < 1 {
		streamLimit = 1
	}
	stdout, stderr := &limitedWriter{limit: streamLimit}, &limitedWriter{limit: streamLimit}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	runErr := runProcess(commandCtx, cmd)
	duration := time.Since(started)
	truncated := stdout.truncated || stderr.truncated
	exitCode := 0
	if runErr != nil {
		if exit, ok := runErr.(*exec.ExitError); ok {
			exitCode = exit.ExitCode()
		} else {
			exitCode = -1
		}
	}
	result := OK(map[string]any{"stdout": security.Redact(stdout.String()), "stderr": security.Redact(stderr.String()), "exitCode": exitCode, "durationMs": duration.Milliseconds(), "timedOut": commandCtx.Err() == context.DeadlineExceeded})
	result.Truncated = truncated
	return logExecute(ctx, "run_command", started, result,
		"command", observability.Snippet(security.Redact(input.Command), 180),
		"cwd", input.CWD,
		"exit_code", exitCode,
		"timed_out", commandCtx.Err() == context.DeadlineExceeded,
		"stdout_preview", observability.Snippet(security.Redact(stdout.String()), 200),
		"stderr_preview", observability.Snippet(security.Redact(stderr.String()), 200),
	)
}

type limitedWriter struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	original := len(p)
	remaining := w.limit - w.buffer.Len()
	if remaining <= 0 {
		w.truncated = true
		return original, nil
	}
	if len(p) > remaining {
		p = p[:remaining]
		w.truncated = true
	}
	_, err := w.buffer.Write(p)
	return original, err
}

func (w *limitedWriter) String() string { return w.buffer.String() }

var backgroundCommand = regexp.MustCompile(`(?i)(^|[;&|]\s*)(nohup|disown|start)(\s|$)`)

var networkCommandPattern = regexp.MustCompile(`(?i)\b(curl|wget|invoke-webrequest|iwr|git\s+(clone|fetch|pull|push)|go\s+get|npm\s+(install|i)|pnpm\s+(install|add)|yarn\s+(add|install)|pip(?:3)?\s+install)\b`)

var networkURLPattern = regexp.MustCompile(`(?i)https?://[^\s"'` + "`" + `<>]+`)

func isBackgroundShellCommand(command string) bool {
	if backgroundCommand.MatchString(command) {
		return true
	}
	// Detect shell job control `&` without treating `&&`, `||`, or `2>&1` as background.
	for i := 0; i < len(command); i++ {
		if command[i] != '&' {
			continue
		}
		prev := byte(0)
		if i > 0 {
			prev = command[i-1]
		}
		next := byte(0)
		if i+1 < len(command) {
			next = command[i+1]
		}
		if prev == '>' || prev == '<' || prev == '&' || next == '&' {
			continue
		}
		j := i + 1
		for j < len(command) && (command[j] == ' ' || command[j] == '\t') {
			j++
		}
		if j >= len(command) || command[j] == '\n' || command[j] == '\r' || command[j] == ';' {
			return true
		}
	}
	return false
}

var deniedCommandPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(curl|wget)\b.+\|\s*(ba)?sh\b`),
	regexp.MustCompile(`(?i)\b(curl|wget)\b.+\|\s*python(?:3)?\b`),
	regexp.MustCompile(`(?i)\binvoke-webrequest\b.+\|\s*iex\b`),
	regexp.MustCompile(`(?i)\biex\s*\(`),
	regexp.MustCompile(`(?i)\brm\s+(-[a-zA-Z]*f[a-zA-Z]*\s+)*(/|~|/home\b|/users\b)`),
	regexp.MustCompile(`(?i)\b(format|mkfs(\.\w+)?|diskpart)\b`),
	regexp.MustCompile(`(?i)\bdd\s+.*\bif=`),
	regexp.MustCompile(`(?i)\b(shutdown|reboot|poweroff)\b`),
	regexp.MustCompile(`(?i)(~|/)\.aws/credentials\b`),
	regexp.MustCompile(`(?i)\bcat\s+.*\.ssh/(id_|authorized_keys)`),
	// Ad-hoc HTTP servers burn the implement step budget; accept runs declared checks.
	regexp.MustCompile(`(?i)\bphp\s+-S\b`),
	// Git history/remote mutation that agents must never run via shell, even after approval.
	regexp.MustCompile(`(?i)\bgit\s+push\b[^\n;&|]*(\s(-f|--force|--force-with-lease)\b)`),
	regexp.MustCompile(`(?i)\bgit\s+push\s+(-f|--force|--force-with-lease)\b`),
	regexp.MustCompile(`(?i)\bgit\s+reset\b[^\n;&|]*--hard\b`),
	regexp.MustCompile(`(?i)\bgit\s+clean\b[^\n;&|]*-[a-zA-Z]*f`),
	regexp.MustCompile(`(?i)\bgit\s+(filter-branch|filter-repo)\b`),
}

func deniedCommandReason(command string) string {
	normalized := strings.TrimSpace(command)
	if normalized == "" {
		return ""
	}
	for _, pattern := range deniedCommandPatterns {
		if pattern.MatchString(normalized) {
			if phpBuiltInServerPattern.MatchString(normalized) {
				return "starting php -S or curling localhost is blocked during agent runs; put the feature in src/config with propose_patch and leave verification to the accept stage"
			}
			if gitDestructivePattern.MatchString(normalized) {
				return "destructive Git history/remote mutation is blocked; use the IDE SCM / Chronicle UI for commits and never force-push from an agent shell"
			}
			return "command matches the soft deny-list for destructive or credential-exfiltration patterns; prefer a narrow approved verifier (test/build/lint) instead of broad shell mutations"
		}
	}
	return ""
}

var gitDestructivePattern = regexp.MustCompile(`(?i)\bgit\s+(push|reset|clean|filter-branch|filter-repo)\b`)

var phpBuiltInServerPattern = regexp.MustCompile(`(?i)\bphp\s+-S\b`)

func deniedNetworkCommandReason(command, policy string, allowedHosts []string) string {
	normalizedPolicy := strings.ToUpper(strings.TrimSpace(policy))
	if normalizedPolicy == "" || normalizedPolicy == "ALLOW" || normalizedPolicy == "ASK" {
		return ""
	}
	if !networkCommandPattern.MatchString(command) {
		return ""
	}
	targets := networkURLPattern.FindAllString(command, -1)
	if len(targets) == 0 {
		return "outbound network commands are denied by the agent network policy; use an explicitly allowed host"
	}
	compiled, compileErr := egress.Compile(normalizedPolicy, allowedHosts, egress.Quota{})
	if compileErr != nil {
		return "outbound network policy is invalid and fails closed"
	}
	for _, target := range targets {
		parsed, err := url.Parse(target)
		port := uint64(443)
		if parsed != nil && parsed.Port() != "" {
			port, err = strconv.ParseUint(parsed.Port(), 10, 16)
		}
		if err != nil || parsed.Hostname() == "" || !strings.EqualFold(parsed.Scheme, "https") || port == 0 || !compiled.Allows(parsed.Hostname(), uint16(port), "tls") {
			host := target
			if err == nil && parsed.Hostname() != "" {
				host = parsed.Hostname()
			}
			return fmt.Sprintf("outbound network access to %q is denied by the agent network policy", host)
		}
	}
	return ""
}

func networkHostAllowed(host string, allowedHosts []string) bool {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	for _, allowed := range allowedHosts {
		allowed = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(allowed), "."))
		if allowed == "" {
			continue
		}
		if host == allowed || strings.HasSuffix(host, "."+allowed) {
			return true
		}
	}
	return false
}
