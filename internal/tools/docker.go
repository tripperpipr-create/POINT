package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"local-agent-workbench/internal/textutil"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/osproc"
	"local-agent-workbench/internal/security"
)

const (
	dockerDefaultTimeout = 45 * time.Second
	dockerMaxOutput      = 256 * 1024
	dockerLogsMaxLines   = 200
)

var dockerRefPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

// DockerCLI runs docker commands without a shell. It talks to the local Docker
// CLI / daemon and is intentionally not an OS-isolation boundary.
type DockerCLI struct {
	LookPath func(string) (string, error)
	Command  func(ctx context.Context, name string, args ...string) *exec.Cmd
	// Runner overrides LookPath/Command for unit tests.
	Runner  func(ctx context.Context, args ...string) (dockerRunResult, error)
	Timeout time.Duration
	MaxOut  int
}

func (c DockerCLI) lookPath() func(string) (string, error) {
	if c.LookPath != nil {
		return c.LookPath
	}
	return exec.LookPath
}

func (c DockerCLI) command() func(ctx context.Context, name string, args ...string) *exec.Cmd {
	if c.Command != nil {
		return c.Command
	}
	return osproc.CommandContext
}

func (c DockerCLI) timeout() time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return dockerDefaultTimeout
}

func (c DockerCLI) maxOut() int {
	if c.MaxOut > 0 {
		return c.MaxOut
	}
	return dockerMaxOutput
}

type dockerRunResult struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exitCode"`
	Duration int64  `json:"durationMs"`
	TimedOut bool   `json:"timedOut"`
}

func (c DockerCLI) ResolveBinary() (string, error) {
	path, err := c.lookPath()("docker")
	if err != nil {
		return "", fmt.Errorf("Docker CLI не найден в PATH: установите Docker Desktop или Docker Engine")
	}
	return path, nil
}

func (c DockerCLI) Run(ctx context.Context, args ...string) (dockerRunResult, error) {
	if c.Runner != nil {
		return c.Runner(ctx, args...)
	}
	binary, err := c.ResolveBinary()
	if err != nil {
		return dockerRunResult{}, err
	}
	timeout := c.timeout()
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := c.command()(runCtx, binary, args...)
	cmd.Env = dockerProcessEnv()
	stdout, stderr := &limitedWriter{limit: c.maxOut() / 2}, &limitedWriter{limit: c.maxOut() / 2}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	started := time.Now()
	runErr := runProcess(runCtx, cmd)
	result := dockerRunResult{
		Stdout:   security.Redact(stdout.String()),
		Stderr:   security.Redact(stderr.String()),
		Duration: time.Since(started).Milliseconds(),
		TimedOut: runCtx.Err() == context.DeadlineExceeded,
	}
	if runErr != nil {
		if exit, ok := runErr.(*exec.ExitError); ok {
			result.ExitCode = exit.ExitCode()
			return result, nil
		}
		return result, runErr
	}
	return result, nil
}

// dockerProcessEnv keeps PATH-like basics plus Docker Desktop connection vars,
// while still stripping credentials/tokens.
func dockerProcessEnv() []string {
	base := sanitizedProcessEnv()
	allowExact := map[string]struct{}{
		"DOCKER_HOST": {}, "DOCKER_CONTEXT": {}, "DOCKER_CONFIG": {},
		"DOCKER_CERT_PATH": {}, "DOCKER_TLS_VERIFY": {}, "DOCKER_API_VERSION": {},
		"DOCKER_DEFAULT_PLATFORM": {},
	}
	seen := make(map[string]bool, len(base))
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		seen[strings.ToUpper(key)] = true
	}
	for _, entry := range os.Environ() {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			continue
		}
		upper := strings.ToUpper(key)
		if _, ok := allowExact[upper]; !ok || seen[upper] {
			continue
		}
		if strings.Contains(upper, "KEY") || strings.Contains(upper, "TOKEN") || strings.Contains(upper, "SECRET") || strings.Contains(upper, "PASSWORD") || strings.Contains(upper, "CREDENTIAL") {
			continue
		}
		base = append(base, entry)
		seen[upper] = true
	}
	return base
}

func ValidateDockerRef(ref string) error {
	ref = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(ref), "/"))
	if ref == "" {
		return fmt.Errorf("container or image reference is required")
	}
	if !dockerRefPattern.MatchString(ref) {
		return fmt.Errorf("invalid Docker reference %q", ref)
	}
	return nil
}

// DockerInspect is a read-only agent tool for daemon status, containers, images, and logs.
type DockerInspect struct {
	CLI DockerCLI
}

func (t DockerInspect) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:        "docker_inspect",
		Description: "Inspect local Docker: CLI/daemon status, containers (ps), images, or recent container logs. Read-only; does not start/stop/remove anything.",
		InputSchema: schema(`{"type":"object","properties":{"action":{"type":"string","enum":["status","ps","images","logs"]},"container":{"type":"string","description":"Required for logs: container name or ID"},"all":{"type":"boolean","description":"For ps: include stopped containers"},"tail":{"type":"integer","minimum":1,"maximum":200,"description":"For logs: number of lines"}},"required":["action"],"additionalProperties":false}`),
	}
}

func (t DockerInspect) ValidateArguments(raw json.RawMessage) *domain.ToolResult {
	var input dockerInspectInput
	if bad := Decode(raw, &input); bad != nil {
		return bad
	}
	if err := input.validate(); err != nil {
		result := Fail("invalid_input", err.Error())
		return &result
	}
	return nil
}

type dockerInspectInput struct {
	Action    string `json:"action"`
	Container string `json:"container"`
	All       bool   `json:"all"`
	Tail      int    `json:"tail"`
}

func (in dockerInspectInput) validate() error {
	switch strings.ToLower(strings.TrimSpace(in.Action)) {
	case "status", "ps", "images":
		return nil
	case "logs":
		return ValidateDockerRef(in.Container)
	default:
		return fmt.Errorf("action must be status, ps, images, or logs")
	}
}

func (t DockerInspect) Execute(ctx context.Context, raw json.RawMessage) domain.ToolResult {
	var input dockerInspectInput
	if len(raw) > 0 {
		if bad := Decode(raw, &input); bad != nil {
			return *bad
		}
	}
	if err := input.validate(); err != nil {
		return Fail("invalid_input", err.Error())
	}
	switch strings.ToLower(strings.TrimSpace(input.Action)) {
	case "status":
		return t.status(ctx)
	case "ps":
		return t.ps(ctx, input.All)
	case "images":
		return t.images(ctx)
	case "logs":
		tail := input.Tail
		if tail <= 0 {
			tail = 100
		}
		if tail > dockerLogsMaxLines {
			tail = dockerLogsMaxLines
		}
		return t.logs(ctx, strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(input.Container), "/")), tail)
	default:
		return Fail("invalid_input", "action must be status, ps, images, or logs")
	}
}

func (t DockerInspect) status(ctx context.Context) domain.ToolResult {
	binary, err := t.CLI.ResolveBinary()
	if err != nil {
		return OK(map[string]any{
			"available": false, "daemon": false, "cliPath": "", "error": err.Error(),
			"hint": "Установите Docker Desktop / Engine и убедитесь, что `docker` есть в PATH.",
		})
	}
	version, _ := t.CLI.Run(ctx, "version", "--format", "{{.Client.Version}}")
	clientVersion := strings.TrimSpace(version.Stdout)
	info, infoErr := t.CLI.Run(ctx, "info", "--format", "{{.ServerVersion}}")
	daemonOK := infoErr == nil && info.ExitCode == 0 && strings.TrimSpace(info.Stdout) != ""
	payload := map[string]any{
		"available":     true,
		"daemon":        daemonOK,
		"cliPath":       binary,
		"clientVersion": clientVersion,
		"serverVersion": strings.TrimSpace(info.Stdout),
	}
	if !daemonOK {
		payload["error"] = textutil.FirstNonEmpty(strings.TrimSpace(info.Stderr), "Docker CLI найден, но демон недоступен")
		payload["hint"] = "Запустите Docker Desktop или службу Docker Engine."
	}
	return OK(payload)
}

func (t DockerInspect) ps(ctx context.Context, all bool) domain.ToolResult {
	args := []string{"ps"}
	if all {
		args = append(args, "-a")
	}
	args = append(args, "--format", "{{json .}}")
	result, err := t.CLI.Run(ctx, args...)
	if err != nil {
		return FailWithHint("docker_failed", err.Error(), "проверьте, что Docker CLI установлен и демон запущен")
	}
	if result.ExitCode != 0 {
		return FailWithHint("docker_failed", textutil.FirstNonEmpty(result.Stderr, result.Stdout, fmt.Sprintf("exit %d", result.ExitCode)), "запустите Docker Desktop и повторите")
	}
	containers := parseDockerJSONLines(result.Stdout)
	return OK(map[string]any{"containers": containers, "count": len(containers), "all": all, "durationMs": result.Duration})
}

func (t DockerInspect) images(ctx context.Context) domain.ToolResult {
	result, err := t.CLI.Run(ctx, "images", "--format", "{{json .}}")
	if err != nil {
		return FailWithHint("docker_failed", err.Error(), "проверьте Docker CLI и демон")
	}
	if result.ExitCode != 0 {
		return FailWithHint("docker_failed", textutil.FirstNonEmpty(result.Stderr, result.Stdout, fmt.Sprintf("exit %d", result.ExitCode)), "запустите Docker Desktop и повторите")
	}
	images := parseDockerJSONLines(result.Stdout)
	return OK(map[string]any{"images": images, "count": len(images), "durationMs": result.Duration})
}

func (t DockerInspect) logs(ctx context.Context, container string, tail int) domain.ToolResult {
	result, err := t.CLI.Run(ctx, "logs", "--tail", fmt.Sprintf("%d", tail), container)
	if err != nil {
		return FailWithHint("docker_failed", err.Error(), "укажите существующий контейнер через docker_inspect action=ps")
	}
	if result.ExitCode != 0 {
		return FailWithHint("docker_failed", textutil.FirstNonEmpty(result.Stderr, result.Stdout, fmt.Sprintf("exit %d", result.ExitCode)), "проверьте имя/ID контейнера")
	}
	combined := result.Stdout
	if result.Stderr != "" {
		if combined != "" {
			combined += "\n"
		}
		combined += result.Stderr
	}
	return OK(map[string]any{
		"container": container, "tail": tail, "logs": combined,
		"durationMs": result.Duration, "truncated": len(combined) >= t.CLI.maxOut()/2,
	})
}

// DockerControl starts or stops a container after approval. Destructive remove
// operations are intentionally unsupported.
type DockerControl struct {
	CLI DockerCLI
}

func (t DockerControl) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:        "docker_control",
		Description: "Start or stop a local Docker container after user approval. Does not remove containers/images and does not support force delete.",
		InputSchema: schema(`{"type":"object","properties":{"action":{"type":"string","enum":["start","stop"]},"container":{"type":"string","description":"Container name or ID"},"reason":{"type":"string","description":"Why this mutation is needed"}},"required":["action","container","reason"],"additionalProperties":false}`),
	}
}

func (t DockerControl) ValidateArguments(raw json.RawMessage) *domain.ToolResult {
	var input dockerControlInput
	if bad := Decode(raw, &input); bad != nil {
		return bad
	}
	if err := input.validate(); err != nil {
		result := Fail("invalid_input", err.Error())
		return &result
	}
	return nil
}

func (t DockerControl) ApprovalArguments(raw json.RawMessage) json.RawMessage {
	var input dockerControlInput
	_ = json.Unmarshal(raw, &input)
	preview, _ := json.Marshal(map[string]any{
		"action":    strings.ToLower(strings.TrimSpace(input.Action)),
		"container": strings.TrimSpace(input.Container),
		"reason":    strings.TrimSpace(input.Reason),
		"command":   fmt.Sprintf("docker %s %s", strings.ToLower(strings.TrimSpace(input.Action)), strings.TrimSpace(input.Container)),
		"note":      "Удаление (rm/rmi/prune) этим инструментом недоступно",
	})
	return preview
}

type dockerControlInput struct {
	Action    string `json:"action"`
	Container string `json:"container"`
	Reason    string `json:"reason"`
}

func (in dockerControlInput) validate() error {
	action := strings.ToLower(strings.TrimSpace(in.Action))
	if action != "start" && action != "stop" {
		return fmt.Errorf("action must be start or stop")
	}
	if err := ValidateDockerRef(in.Container); err != nil {
		return err
	}
	if strings.TrimSpace(in.Reason) == "" {
		return fmt.Errorf("reason is required")
	}
	if len(in.Reason) > 4*1024 {
		return fmt.Errorf("reason exceeds its size limit")
	}
	return nil
}

func (t DockerControl) Execute(ctx context.Context, raw json.RawMessage) domain.ToolResult {
	var input dockerControlInput
	if bad := Decode(raw, &input); bad != nil {
		return *bad
	}
	if err := input.validate(); err != nil {
		return Fail("invalid_input", err.Error())
	}
	action := strings.ToLower(strings.TrimSpace(input.Action))
	container := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(input.Container), "/"))
	result, err := t.CLI.Run(ctx, action, container)
	if err != nil {
		return FailWithHint("docker_failed", err.Error(), "проверьте Docker CLI/демон и имя контейнера")
	}
	if result.ExitCode != 0 {
		return FailWithHint("docker_failed", textutil.FirstNonEmpty(result.Stderr, result.Stdout, fmt.Sprintf("exit %d", result.ExitCode)), "убедитесь, что контейнер существует и демон запущен")
	}
	return OK(map[string]any{
		"action": action, "container": container, "stdout": result.Stdout, "stderr": result.Stderr,
		"exitCode": result.ExitCode, "durationMs": result.Duration, "reason": strings.TrimSpace(input.Reason),
	})
}

func parseDockerJSONLines(raw string) []map[string]any {
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	out := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var item map[string]any
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			out = append(out, map[string]any{"raw": line})
			continue
		}
		out = append(out, item)
	}
	return out
}
