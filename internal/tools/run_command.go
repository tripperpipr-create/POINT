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
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"local-agent-workbench/internal/diagnostics"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/egress"
	"local-agent-workbench/internal/observability"
	"local-agent-workbench/internal/osproc"
	"local-agent-workbench/internal/sandbox"
	"local-agent-workbench/internal/security"
	"local-agent-workbench/internal/verification"
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
	// Authoritative — результат команды служит доказательством (приёмка,
	// ревью, прогон без роли этапа). Такой прогон не получает кэши, в которые
	// пишут агенты и которые не сверяются с lock-файлом.
	Authoritative bool
	// NetworkPolicy is empty for user-owned terminal actions. Agent runtimes
	// set DENY by default and may provide an explicit host allowlist.
	NetworkPolicy       string
	AllowedNetworkHosts []string
	ConfirmedGitRemotes []string
	Grants              *NetworkGrantBook
	// HostOwnRemotes — локальная полоса (Fast Agent в папке человека): git
	// fetch/pull/push к remote, уже настроенным в этом репозитории, не требуют
	// подтверждённого списка и не считаются сетью агента. Push всё равно ждёт
	// кнопки человека — см. HostCommandNeedsApproval.
	HostOwnRemotes bool
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
		return finish(FailWithHint("background_process_unsupported", "interactive and background commands are not supported", "run the command in the foreground so it exits by itself (drop &, nohup, disown, start); servers are started by Point's host checks after delivery"))
	}
	if denied := deniedCommandReason(input.Command); denied != "" {
		return finish(FailWithHint("command_denied", denied, "use a non-interactive local test, build, lint, or inspection command that does not require elevated or destructive privileges"))
	}
	if t.Executor != nil {
		if unsupported := unsupportedSandboxShellSyntax(input.Command); unsupported != "" {
			return finish(FailWithHint("unsupported_shell_syntax", unsupported, "pipefail is already on: `cmd 2>&1 | tail -20` returns the exit code of cmd; or run `cmd > /tmp/out.log 2>&1; echo \"exit=$?\"; tail -20 /tmp/out.log` in the same command; use [ ] instead of [[ ]]"))
		}
	}
	var cwd string
	var err error
	if resolver, ok := t.Executor.(sandbox.VolumeDirectoryResolver); ok && resolver.UsesVolume(t.FS.Root()) {
		cwd, err = resolver.ResolveProcessDirectory(t.FS.Root(), input.CWD)
	} else {
		cwd, err = t.FS.Resolve(input.CWD, false)
	}
	if err != nil {
		return finish(FailWithHint("invalid_cwd", err.Error(), "use a workspace-relative directory such as src, or omit cwd to run at the workspace root"))
	}
	var remotes map[string]string
	if gitRemoteTrafficPattern.MatchString(input.Command) {
		remotesDir := t.FS.Root()
		if t.Executor == nil {
			remotesDir = cwd
		}
		remotes = configuredGitRemotes(ctx, remotesDir)
	}
	// 03.10 Fast Agent не смог сделать `git pull` в папке человека: список
	// подтверждённых remote на локальной полосе пуст всегда, а за ним стоял ещё
	// сетевой DENY. Свой remote человека проходит оба затвора; всё прочее сетевое
	// в той же команде — нет.
	// `git -C dir pull` сверяется с remote каталога dir, если он в рабочей области.
	remotesAt := func(dir string) map[string]string {
		if dir == "" {
			return remotes
		}
		return t.workspaceGitRemotes(ctx, cwd, dir)
	}
	ownRemote := t.HostOwnRemotes && t.Executor == nil && ownRemoteGitTraffic(input.Command, remotesAt)
	if !ownRemote {
		grantedRemotes := t.Grants.RemotesFor(t.RunID, t.QuestID)
		if denied := deniedUnconfirmedGitRemoteReason(input.Command, t.ConfirmedGitRemotes, grantedRemotes, remotes); denied != "" {
			return finish(FailWithHint("git_remote_unconfirmed", denied, "do not invent remotes; wait for Master/user confirmation of the exact repository URL"))
		}
	}
	effectiveHosts := append([]string{}, t.AllowedNetworkHosts...)
	effectiveHosts = append(effectiveHosts, t.Grants.QuestHostsFor(t.QuestID)...)
	policy, policyErr := egress.Compile(t.NetworkPolicy, effectiveHosts, egress.Quota{})
	if policyErr != nil {
		return finish(Fail("network_policy_invalid", policyErr.Error()))
	}
	var targets []string
	var targetErr error
	if !ownRemote {
		targets, targetErr = explicitNetworkTargets(input.Command)
	}
	if targetErr != nil {
		return finish(FailWithHint("network_target_invalid", targetErr.Error(), "use one explicit HTTPS FQDN and port; unknown destinations cannot be approved"))
	}
	grantScope := "none"
	if len(targets) > 0 {
		grantScope = "approved"
		for _, target := range targets {
			for _, host := range t.Grants.QuestHostsFor(t.QuestID) {
				if host == target {
					grantScope = "quest"
				}
			}
		}
	}
	var missing []string
	for _, target := range targets {
		if !hostGrantAllowed(policy, target) {
			missing = append(missing, target)
		}
	}
	if len(missing) == 1 && t.Grants.TakeHostOnce(t.RunID, missing[0]) {
		effectiveHosts = append(effectiveHosts, missing[0])
		grantScope = "once"
		policy, policyErr = egress.Compile(t.NetworkPolicy, effectiveHosts, egress.Quota{})
		if policyErr != nil {
			return finish(Fail("network_policy_invalid", policyErr.Error()))
		}
		missing = nil
	}
	if len(missing) > 0 {
		result := FailWithHint("network_denied", fmt.Sprintf("outbound network access to %q is denied by the agent network policy", missing[0]), "ask the user to allow this exact TLS destination; do not retry alternate mirrors")
		if len(missing) == 1 {
			result.Error.Target = missing[0]
		}
		result.Error.PolicyDigest = policy.Digest
		return finish(result)
	}
	if !ownRemote && !sandbox.HasControlledEgress(t.Executor) {
		if denied := deniedNetworkCommandReason(input.Command, t.NetworkPolicy, effectiveHosts); denied != "" {
			result := FailWithHint("network_denied", denied, "use an explicit allowed TLS destination inside the configured process sandbox")
			result.Error.PolicyDigest = policy.Digest
			return finish(result)
		}
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
	var volumeExecutor sandbox.DeltaProcessExecutor
	if candidate, ok := t.Executor.(sandbox.DeltaProcessExecutor); ok && candidate.UsesVolume(t.FS.Root()) {
		volumeExecutor = candidate
	}
	var readEgressDecisions func(context.Context) ([]sandbox.EgressDecision, error)
	if t.Executor != nil && volumeExecutor == nil {
		prepared, prepareErr := t.Executor.PrepareProcess(commandCtx, sandbox.ProcessRequest{
			WorkspaceRoot: t.FS.Root(), WorkingDirectory: cwd, ShellCommand: sandboxShellCommand(input.Command), Image: t.SandboxImage,
			Environment: sanitizedProcessEnv(), NetworkPolicy: t.NetworkPolicy,
			AllowedNetworkHosts: append([]string(nil), effectiveHosts...),
			RunID:               t.RunID,
			CacheScope:          t.QuestID, Authoritative: t.Authoritative,
		})
		if prepareErr != nil {
			return finish(FailWithHint("sandbox_denied", prepareErr.Error(), "use a local verifier that fits the configured sandbox network and resource policy"))
		}
		if prepared.Command == nil {
			return finish(FailWithHint("sandbox_unavailable", "sandbox backend returned no process", "the sandbox is not ready; do not repeat the call, report the blocker in your final answer"))
		}
		cmd = prepared.Command
		readEgressDecisions = prepared.EgressDecisions
		if prepared.Cleanup != nil {
			defer func() {
				cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cleanupCancel()
				_ = prepared.Cleanup(cleanupCtx)
			}()
		}
	} else if volumeExecutor == nil && runtime.GOOS == "windows" {
		cmd = osproc.Command("cmd.exe", "/d", "/s", "/c", input.Command)
	} else if volumeExecutor == nil {
		program, args := HostShellCommand(input.Command)
		cmd = osproc.Command(program, args...)
	}
	if t.Executor == nil {
		cmd.Dir = cwd
		cmd.Env = hostProcessEnv()
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
	var runErr error
	var volumeOutcome sandbox.ProcessOutcome
	if volumeExecutor != nil {
		volumeOutcome, runErr = volumeExecutor.RunVolumeProcess(commandCtx, sandbox.ProcessRequest{WorkspaceRoot: t.FS.Root(), WorkingDirectory: cwd, ShellCommand: sandboxShellCommand(input.Command), Image: t.SandboxImage, Environment: sanitizedProcessEnv(), NetworkPolicy: t.NetworkPolicy, AllowedNetworkHosts: effectiveHosts, RunID: t.RunID, CacheScope: t.QuestID, Authoritative: t.Authoritative}, stdout, stderr)
	} else {
		cmd.Stdout, cmd.Stderr = stdout, stderr
		runErr = runProcess(commandCtx, cmd)
	}
	duration := time.Since(started)
	gatewayStatus := "not_applicable"
	var gatewayDecisions []sandbox.EgressDecision
	if volumeExecutor != nil {
		gatewayDecisions = volumeOutcome.EgressDecisions
		if policy.Mode == "ALLOWLIST" {
			gatewayStatus = "recorded"
			if runErr != nil {
				gatewayStatus = "unavailable"
			}
		}
	}
	if readEgressDecisions != nil {
		readCtx, readCancel := context.WithTimeout(context.Background(), 5*time.Second)
		var readErr error
		gatewayDecisions, readErr = readEgressDecisions(readCtx)
		readCancel()
		if readErr != nil {
			gatewayStatus = "unavailable"
		} else {
			gatewayStatus = "recorded"
		}
	}
	truncated := stdout.truncated || stderr.truncated
	exitCode := 0
	if runErr != nil {
		if exit, ok := runErr.(*exec.ExitError); ok {
			exitCode = exit.ExitCode()
		} else {
			exitCode = -1
		}
	}
	timedOut := commandCtx.Err() == context.DeadlineExceeded
	if volumeExecutor != nil {
		exitCode = volumeOutcome.ExitCode
		timedOut = volumeOutcome.TimedOut || timedOut
		if runErr != nil {
			exitCode = -1
		}
	}
	output := map[string]any{"stdout": security.Redact(stdout.String()), "stderr": security.Redact(stderr.String()), "exitCode": exitCode, "durationMs": duration.Milliseconds(), "timedOut": timedOut}
	// cmd.exe не знает pipefail: код конвейера — код последней команды (Q08).
	if t.Executor == nil && volumeExecutor == nil && runtime.GOOS == "windows" && verification.TopLevelPipe(input.Command) {
		output["exitCodeOf"] = "the last command of the pipeline: cmd.exe has no pipefail, so an earlier failure is not in exitCode"
	}
	// Сетевая атрибуция — доказательство для команды, которая ходила в сеть.
	// Для `ls` она была пятью пустыми полями в каждом выводе, который читает
	// модель.
	if len(targets) > 0 || len(gatewayDecisions) > 0 {
		output["networkPolicyDigest"] = policy.Digest
		output["networkTargets"] = targets
		output["networkGrantScope"] = grantScope
		output["networkGatewayStatus"] = gatewayStatus
		output["networkGatewayDecisions"] = gatewayDecisions
	}
	switch {
	case timedOut:
		output["hint"] = fmt.Sprintf("the command was stopped after %s; raise timeoutSeconds (up to 600) for a slow build, or run a narrower command", timeout)
	case exitCode == -1 && runErr != nil:
		// Команда не запустилась вовсе: прежде причина терялась, и модель
		// видела только exitCode -1.
		output["error"] = security.Redact(runErr.Error())
		output["hint"] = "the shell could not start the command; check the program name and the cwd, then retry"
	}
	// Причина провала — сразу в выводе. 30.09 исполнитель дважды по три минуты
	// перезапускал сборку, только чтобы найти ошибку в хвосте после `| tail`.
	denied := []string{}
	for _, decision := range gatewayDecisions {
		if decision.Decision == "denied" {
			denied = append(denied, fmt.Sprintf("%s:%d", decision.FQDN, decision.Port))
		}
	}
	if failure, failed := diagnostics.DiagnoseCommand(diagnostics.CommandRun{
		Command: input.Command, ExitCode: exitCode, TimedOut: timedOut, Timeout: timeout.String(),
		Stdout: stdout.String(), Stderr: stderr.String(), DeniedHosts: denied,
		MissingDependencies: missingDeclaredPackages(t.FS.Root(), input.Command, input.CWD),
	}); failed {
		output["cause"] = security.Redact(failure.Cause)
		output["causeClass"] = failure.Class
		output["causeSignature"] = failure.Signature
		if failure.Hint != "" {
			output["causeHint"] = failure.Hint
		}
	}
	result := OK(output)
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

// limitedWriter держит начало и конец потока, а середину отбрасывает. Прежде
// он хранил только начало, и ошибка сборки — она почти всегда в последних
// строках — пропадала: модель видела «exitCode 1» и сотни строк прогресса без
// причины (Q08). Половина лимита — голове, половина — хвосту.
type limitedWriter struct {
	buffer    bytes.Buffer
	tail      []byte
	limit     int
	dropped   int
	truncated bool
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	original := len(p)
	head := w.limit - w.limit/2
	if room := head - w.buffer.Len(); room > 0 {
		take := min(room, len(p))
		w.buffer.Write(p[:take])
		p = p[take:]
	}
	if len(p) == 0 {
		return original, nil
	}
	w.truncated = true
	tailLimit := w.limit / 2
	w.tail = append(w.tail, p...)
	if over := len(w.tail) - tailLimit; over > 0 {
		w.dropped += over
		if len(w.tail) > 2*tailLimit || tailLimit == 0 {
			w.tail = append([]byte(nil), w.tail[over:]...)
		} else {
			w.tail = w.tail[over:]
		}
	}
	return original, nil
}

func (w *limitedWriter) String() string {
	if !w.truncated {
		return w.buffer.String()
	}
	tail := w.tail
	// Хвост мог начаться посреди символа UTF-8: неполные байты отбрасываются.
	for len(tail) > 0 && !utf8.RuneStart(tail[0]) {
		tail = tail[1:]
	}
	if w.dropped == 0 {
		return w.buffer.String() + string(tail)
	}
	return w.buffer.String() + fmt.Sprintf("\n…[обрезано %d байт]…\n", w.dropped+len(w.tail)-len(tail)) + string(tail)
}

var backgroundCommand = regexp.MustCompile(`(?i)(^|[;&|]\s*)(nohup|disown|start)(\s|$)`)

var networkCommandPattern = regexp.MustCompile(`(?i)\b(curl|wget|invoke-webrequest|iwr|` + gitCommand + `(clone|fetch|pull|push)|go\s+get|npm\s+(install|i)|pnpm\s+(install|add)|yarn\s+(add|install)|pip(?:3)?\s+install)\b`)
var explicitURLRequiredPattern = regexp.MustCompile(`(?i)\b(curl|wget|invoke-webrequest|iwr)\b`)

var networkURLPattern = regexp.MustCompile(`(?i)https?://[^\s"'` + "`" + `<>]+`)

// explicitNetworkTargets identifies only unambiguous, gateway-valid TLS
// destinations. The gateway still enforces every actual connection, including
// implicit registry traffic and redirects.
func explicitNetworkTargets(command string) ([]string, error) {
	if !networkCommandPattern.MatchString(command) && !networkURLPattern.MatchString(command) {
		return nil, nil
	}
	seen := map[string]bool{}
	var targets []string
	for _, raw := range networkURLPattern.FindAllString(command, -1) {
		parsed, err := url.Parse(strings.TrimRight(raw, ",);]"))
		if err != nil || parsed == nil || parsed.User != nil || parsed.Hostname() == "" || !strings.EqualFold(parsed.Scheme, "https") {
			return nil, fmt.Errorf("network command has an invalid HTTPS destination")
		}
		port := parsed.Port()
		if port == "" {
			port = "443"
		}
		target, err := CanonicalHostGrant(parsed.Hostname() + ":" + port)
		if err != nil {
			return nil, fmt.Errorf("network command has an invalid TLS destination: %w", err)
		}
		if !seen[target] {
			seen[target] = true
			targets = append(targets, target)
		}
	}
	if explicitURLRequiredPattern.MatchString(command) && len(targets) == 0 {
		return nil, fmt.Errorf("network command has no explicit HTTPS destination")
	}
	return targets, nil
}

func hostGrantAllowed(policy egress.Policy, target string) bool {
	parsed, err := egress.Compile("DENY", []string{target}, egress.Quota{})
	if err != nil || len(parsed.Rules) != 1 {
		return false
	}
	rule := parsed.Rules[0]
	return policy.Allows(rule.FQDN, rule.Port, rule.Protocol)
}

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
	// Корень, домашний каталог и системные каталоги — но не /tmp внутри
	// одноразового контейнера: `rm -rf /tmp/x` отбивался как «удаление /»
	// (квест 28.09 потерял на этом два хода).
	regexp.MustCompile(`(?i)\brm\s+(-[a-zA-Z]*f[a-zA-Z]*\s+)*(/(\*|\s|$|[;&|])|~|/home\b|/users\b|/(etc|usr|bin|sbin|var|root|opt|boot|lib|lib64|dev|proc|sys)\b)`),
	regexp.MustCompile(`(?i)\b(format|mkfs(\.\w+)?|diskpart)\b`),
	regexp.MustCompile(`(?i)\bdd\s+.*\bif=`),
	regexp.MustCompile(`(?i)\b(shutdown|reboot|poweroff)\b`),
	regexp.MustCompile(`(?i)(~|/)\.aws/credentials\b`),
	regexp.MustCompile(`(?i)\bcat\s+.*\.ssh/(id_|authorized_keys)`),
	// Ad-hoc HTTP servers burn the implement step budget; accept runs declared checks.
	regexp.MustCompile(`(?i)\bphp\s+-S\b`),
	// Git history/remote mutation that agents must never run via shell, even after approval.
	regexp.MustCompile(`(?i)` + gitCommand + `push\b[^\n;&|]*(\s(-f|--force|--force-with-lease)\b)`),
	regexp.MustCompile(`(?i)` + gitCommand + `push\s+(-f|--force|--force-with-lease)\b`),
	regexp.MustCompile(`(?i)` + gitCommand + `reset\b[^\n;&|]*--hard\b`),
	regexp.MustCompile(`(?i)` + gitCommand + `clean\b[^\n;&|]*-[a-zA-Z]*f`),
	regexp.MustCompile(`(?i)` + gitCommand + `(filter-branch|filter-repo)\b`),
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
			// Совпавший фрагмент называется, чтобы модель поправила именно его,
			// а не переписывала всю команду наугад.
			return fmt.Sprintf("command matches the soft deny-list for destructive or credential-exfiltration patterns (matched %q); prefer a narrow approved verifier (test/build/lint) instead of broad shell mutations", observability.Snippet(pattern.FindString(normalized), 80))
		}
	}
	return ""
}

// Действия, которые на локальной полосе теряют работу человека или уходят на
// сервер. Запрещать их нельзя — человек сам просит удалить ветку или
// отправить коммит, — но решает он, а не модель: 03.10 Fast Agent удалил ветку
// с уникальными коммитами вопреки условию остановки из задачи.
var hostApprovalPatterns = []struct {
	pattern *regexp.Regexp
	reason  string
}{
	// Без (?i): `-d` безопасен — git сам откажет удалять несмерженное.
	{regexp.MustCompile(gitCommand + `branch\b[^\n;&|]*(\s-[a-zA-Z]*D|\s--delete\b[^\n;&|]*\s(-f|--force)\b|\s(-f|--force)\b[^\n;&|]*\s(-d|--delete)\b)`), "Удаление ветки без проверки слияния: несмерженные коммиты потеряются"},
	{regexp.MustCompile(`(?i)` + gitCommand + `stash\s+(drop|clear)\b`), "Удаление отложенных изменений (stash)"},
	{regexp.MustCompile(`(?i)` + gitCommand + `(checkout|switch)\b[^\n;&|]*\s(-f|--force|--discard-changes)\b`), "Переключение с потерей незакоммиченных правок"},
	{regexp.MustCompile(`(?i)` + gitCommand + `checkout\s+(\S+\s+)?--\s+\.`), "Откат незакоммиченных правок во всей папке"},
	{regexp.MustCompile(`(?i)` + gitCommand + `restore\b[^\n;&|]*\s\.(\s|$)`), "Откат незакоммиченных правок во всей папке"},
	{regexp.MustCompile(`(?i)` + gitCommand + `push\b`), "Отправка коммитов на сервер"},
}

// HostCommandNeedsApproval — причина спросить человека перед командой на его
// устройстве, или "". Разрушительное без возврата (force-push, reset --hard)
// сюда не попадает: его отбивает deniedCommandReason раньше.
func HostCommandNeedsApproval(command string) string {
	for _, item := range hostApprovalPatterns {
		if item.pattern.MatchString(command) {
			return item.reason
		}
	}
	return ""
}

var gitDestructivePattern = regexp.MustCompile(`(?i)` + gitCommand + `(push|reset|clean|filter-branch|filter-repo)\b`)

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

// Classify only packages both declared in a manifest and absent in this workspace.
// An application import or a TypeScript diagnostic alone is not environment evidence.
func missingDeclaredPackages(root, command, cwd string) []string {
	dirs := []string{cwd}
	for _, match := range regexp.MustCompile(`(?:^|&&\s*)cd\s+(?:"([^"]+)"|'([^']+)'|([^\s;&|]+))\s*&&`).FindAllStringSubmatch(command, -1) {
		for _, dir := range match[1:] {
			if dir != "" && domain.DependencyPathValid(dir, true) {
				dirs = append(dirs, dir)
				break
			}
		}
	}
	var result []string
	for _, dir := range dirs {
		if !domain.DependencyPathValid(dir, true) {
			continue
		}
		base := filepath.Join(root, filepath.FromSlash(dir))
		data, err := os.ReadFile(filepath.Join(base, "package.json"))
		if err != nil {
			continue
		}
		var manifest struct {
			Dependencies    map[string]any
			DevDependencies map[string]any
		}
		if json.Unmarshal(data, &manifest) != nil {
			continue
		}
		for _, group := range []map[string]any{manifest.Dependencies, manifest.DevDependencies} {
			for name := range group {
				if !domain.DependencyPathValid(name, false) {
					continue
				}
				if _, err := os.Stat(filepath.Join(base, "node_modules", filepath.FromSlash(name))); os.IsNotExist(err) {
					result = append(result, name)
				}
			}
		}
	}
	return result
}
