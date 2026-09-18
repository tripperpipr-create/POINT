package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"local-agent-workbench/internal/osproc"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/egress"
	"local-agent-workbench/internal/observability"
	"local-agent-workbench/internal/sandbox"
	"local-agent-workbench/internal/security"
	"local-agent-workbench/internal/workspace"
)

func schema(value string) json.RawMessage { return json.RawMessage(value) }

type ListFiles struct{ FS *workspace.FS }

func (t ListFiles) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{Name: "list_files", Description: "List a workspace directory tree. Prefer a subdirectory path instead of listing the whole repo. Missing directories return an empty list (create files with propose_patch). Dependency roots (vendor/, node_modules/) are omitted from parent listings — pass an explicit path such as vendor/<package> to inspect them. Excludes VCS metadata, binary files and secrets.", InputSchema: schema(`{"type":"object","properties":{"path":{"type":"string","description":"Workspace-relative directory to list, for example src/internal or vendor/<package>. Empty lists the workspace root."},"maxDepth":{"type":"integer","minimum":1,"maximum":20,"description":"How many directory levels to include. Use 2-4 unless you need a deep tree."}},"additionalProperties":false}`)}
}
func (t ListFiles) Execute(ctx context.Context, raw json.RawMessage) domain.ToolResult {
	started := time.Now()
	var input struct {
		Path     string `json:"path"`
		MaxDepth int    `json:"maxDepth"`
	}
	if len(raw) > 0 {
		if bad := Decode(raw, &input); bad != nil {
			return *bad
		}
	}
	tree, err := t.FS.ListPath(ctx, input.Path, input.MaxDepth)
	if err != nil {
		return logExecute(ctx, "list_files", started, FailWithHint("list_failed", err.Error(), "use a workspace-relative directory path; confirm it with project_map or search_code"), "path", input.Path, "max_depth", input.MaxDepth)
	}
	return logExecute(ctx, "list_files", started, OK(tree), "path", input.Path, "max_depth", input.MaxDepth)
}

type ReadFile struct{ FS *workspace.FS }

func (t ReadFile) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{Name: "read_file", Description: "Read a UTF-8 text file inside the workspace with line numbers. Use startLine/endLine for large files instead of rereading the whole file.", InputSchema: schema(`{"type":"object","properties":{"path":{"type":"string","description":"Workspace-relative path with forward slashes, for example internal/app/app.go"},"startLine":{"type":"integer","minimum":1,"description":"Optional 1-based first line to return"},"endLine":{"type":"integer","minimum":1,"description":"Optional 1-based last line to return, inclusive"}},"required":["path"],"additionalProperties":false}`)}
}
func (t ReadFile) Execute(ctx context.Context, raw json.RawMessage) domain.ToolResult {
	started := time.Now()
	var input struct {
		Path      string `json:"path"`
		StartLine int    `json:"startLine"`
		EndLine   int    `json:"endLine"`
	}
	if bad := Decode(raw, &input); bad != nil {
		return *bad
	}
	content, err := t.FS.Read(input.Path, false)
	if err != nil {
		hint := "confirm the path with list_files or search_code, then retry read_file with an exact workspace-relative path"
		if strings.Contains(strings.ToLower(err.Error()), "directory") {
			hint = "this path is a directory; call list_files with that path, then read_file on a specific file"
		}
		return logExecute(ctx, "read_file", started, FailWithHint("read_failed", err.Error(), hint), "path", input.Path)
	}
	sliced, start, end, partial := sliceNumberedLines(content.Numbered, input.StartLine, input.EndLine)
	truncated := content.Truncated || partial
	digest := content.SHA256
	if partial {
		digest = ""
	}
	payload := map[string]any{
		"path": content.Path, "content": sliced, "sha256": digest, "size": content.Size,
		"truncated": truncated, "startLine": start, "endLine": end,
	}
	if truncated && !partial {
		payload["hint"] = "file was truncated; call read_file again with startLine/endLine around the region you still need"
	} else if partial {
		payload["hint"] = "partial read; this is not a complete-file inspection for propose_patch rewrites"
	}
	return logExecute(ctx, "read_file", started, OK(payload), "path", content.Path, "size", content.Size, "truncated", truncated, "start_line", start, "end_line", end)
}

func sliceNumberedLines(numbered string, startLine, endLine int) (string, int, int, bool) {
	lines := strings.Split(strings.TrimSuffix(numbered, "\n"), "\n")
	total := len(lines)
	if total == 1 && lines[0] == "" {
		total = 0
	}
	if startLine <= 0 && endLine <= 0 {
		return numbered, 1, total, false
	}
	if startLine <= 0 {
		startLine = 1
	}
	if endLine <= 0 || endLine > total {
		endLine = total
	}
	if startLine > total {
		startLine = total
	}
	if startLine < 1 {
		startLine = 1
	}
	if endLine < startLine {
		endLine = startLine
	}
	if total == 0 {
		return "", 1, 0, false
	}
	selected := lines[startLine-1 : endLine]
	partial := startLine > 1 || endLine < total
	return strings.Join(selected, "\n") + "\n", startLine, endLine, partial
}

type SearchText struct{ FS *workspace.FS }

func (t SearchText) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{Name: "search_text", Description: "Search case-insensitively through safe UTF-8 workspace files. Prefer search_code when you need ranked implementation context.", InputSchema: schema(`{"type":"object","properties":{"query":{"type":"string","description":"Literal substring to find. Not a regular expression."},"maxResults":{"type":"integer","minimum":1,"maximum":500}},"required":["query"],"additionalProperties":false}`)}
}
func (t SearchText) Execute(ctx context.Context, raw json.RawMessage) domain.ToolResult {
	started := time.Now()
	var input struct {
		Query      string `json:"query"`
		MaxResults int    `json:"maxResults"`
	}
	if bad := Decode(raw, &input); bad != nil {
		return *bad
	}
	if len(input.Query) > 500 {
		return logExecute(ctx, "search_text", started, FailWithHint("invalid_input", "search query exceeds 500 characters", "shorten the query to a distinctive symbol, string, or path fragment"))
	}
	if strings.TrimSpace(input.Query) == "" {
		return logExecute(ctx, "search_text", started, FailWithHint("invalid_input", "search query is empty", "provide a literal substring such as a function name or error text"))
	}
	matches, err := t.FS.Search(ctx, input.Query, input.MaxResults)
	if err != nil {
		return logExecute(ctx, "search_text", started, Fail("search_failed", err.Error()), "query", observability.Snippet(input.Query, 80))
	}
	count := 0
	if matches != nil {
		count = len(matches)
	}
	return logExecute(ctx, "search_text", started, OK(matches), "query", observability.Snippet(input.Query, 80), "matches", count)
}

type GitDiff struct {
	FS        *workspace.FS
	MaxOutput int
}

func (t GitDiff) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:        "git_diff",
		Description: "Show Git changes. Without arguments: uncommitted changes versus HEAD (staged and unstaged) plus a short status summary. With commit: the changes introduced by that revision, with its author, date and subject. Read-only.",
		InputSchema: schema(`{"type":"object","properties":{"path":{"type":"string","description":"Optional workspace-relative path to limit the diff"},"commit":{"type":"string","description":"Optional revision (commit hash, tag, branch or HEAD~1) to show the changes introduced by that commit instead of the working tree"}},"additionalProperties":false}`),
	}
}

func (t GitDiff) Execute(ctx context.Context, raw json.RawMessage) domain.ToolResult {
	var input struct {
		Path   string `json:"path"`
		Commit string `json:"commit"`
	}
	if len(raw) > 0 {
		if bad := Decode(raw, &input); bad != nil {
			return *bad
		}
	}
	root := t.FS.Root()
	if !gitWorkTreeAvailable(ctx, root) {
		return FailWithHint(
			"git_unavailable",
			"workspace is not a usable Git repository",
			"filtered-copy sandboxes omit .git; use a PreferWorktree sandbox, or inspect Change Sets instead of git_diff",
		)
	}
	if revision := strings.TrimSpace(input.Commit); revision != "" {
		return t.showCommit(ctx, root, revision, input.Path)
	}

	statusCmd := osproc.CommandContext(ctx, "git", "status", "--porcelain=v1", "--branch")
	statusCmd.Dir = root
	statusOut, statusErr := statusCmd.CombinedOutput()
	statusText := strings.TrimSpace(string(statusOut))
	if statusErr != nil && statusText == "" {
		return Fail("git_diff_failed", security.Redact(statusErr.Error()))
	}

	diffArgs := []string{"diff", "--no-ext-diff", "HEAD", "--"}
	pathFilter := ""
	if rel := strings.TrimSpace(input.Path); rel != "" {
		resolved, err := t.FS.Resolve(rel, false)
		if err != nil {
			return Fail("invalid_path", err.Error())
		}
		relPath, err := filepath.Rel(root, resolved)
		if err != nil {
			return Fail("invalid_path", err.Error())
		}
		pathFilter = filepath.ToSlash(relPath)
		diffArgs = append(diffArgs, pathFilter)
	}

	diffOut, diffErr := runGitDiff(ctx, root, diffArgs)
	if diffErr != nil && isMissingHEAD(string(diffOut)+diffErr.Error()) {
		// Empty / unborn branch: fall back to the index/workdir diff.
		fallback := []string{"diff", "--no-ext-diff", "--"}
		if pathFilter != "" {
			fallback = append(fallback, pathFilter)
		}
		diffOut, diffErr = runGitDiff(ctx, root, fallback)
	}
	if diffErr != nil {
		return Fail("git_diff_failed", security.Redact(strings.TrimSpace(string(diffOut)+": "+diffErr.Error())))
	}

	max := t.MaxOutput
	if max <= 0 {
		max = 256 * 1024
	}
	statusBudget := max / 4
	if statusBudget < 4*1024 {
		statusBudget = 4 * 1024
	}
	if statusBudget > 64*1024 {
		statusBudget = 64 * 1024
	}
	statusTruncated := len(statusText) > statusBudget
	if statusTruncated {
		statusText = statusText[:statusBudget]
	}
	diffBudget := max - len(statusText)
	if diffBudget < 8*1024 {
		diffBudget = 8 * 1024
	}
	diffText := string(diffOut)
	diffTruncated := len(diffText) > diffBudget
	if diffTruncated {
		diffText = diffText[:diffBudget]
	}
	result := OK(map[string]any{
		"scope":  "worktree",
		"status": statusText,
		"diff":   diffText,
		"base":   "HEAD",
	})
	result.Truncated = statusTruncated || diffTruncated
	return result
}

// Содержимое одного коммита. История приходит из git_log одними заголовками, и
// на вопрос «что изменилось в этих коммитах» ответить было нечем: git_diff знал
// только рабочее дерево. Модель звала его снова и снова с теми же аргументами и
// получала тот же ответ.
//
// Ревизия уходит аргументом exec, а не в строку оболочки, но одного этого мало:
// git принимает опции там же, где ссылки, поэтому имя проверяется до вызова, а
// затем разрешается в хеш — несуществующая ссылка обязана отвечать отказом, а
// не пустым diff, который модель прочитает как «изменений нет».
func (t GitDiff) showCommit(ctx context.Context, root, revision, path string) domain.ToolResult {
	if !safeGitRevision(revision) {
		return FailWithHint(
			"invalid_revision",
			"revision must be a single commit reference without spaces, ranges or leading dashes",
			"pass one commit: a hash, tag, branch or HEAD~1",
		)
	}
	resolvedOut, resolveErr := runGitDiff(ctx, root, []string{"rev-parse", "--verify", "--quiet", revision + "^{commit}"})
	hash := strings.TrimSpace(string(resolvedOut))
	if resolveErr != nil || hash == "" {
		return Fail("unknown_revision", fmt.Sprintf("revision %q is not a commit in this repository", revision))
	}
	headerOut, headerErr := runGitDiff(ctx, root, []string{"show", "--no-patch", "--format=%H%n%an%n%aI%n%s", hash})
	if headerErr != nil {
		return Fail("git_diff_failed", security.Redact(strings.TrimSpace(string(headerOut)+": "+headerErr.Error())))
	}
	header := strings.SplitN(strings.TrimSpace(string(headerOut)), "\n", 4)
	for len(header) < 4 {
		header = append(header, "")
	}
	diffArgs := []string{"show", "--no-ext-diff", "--format=", hash}
	if rel := strings.TrimSpace(path); rel != "" {
		resolved, err := t.FS.Resolve(rel, false)
		if err != nil {
			return Fail("invalid_path", err.Error())
		}
		relPath, err := filepath.Rel(root, resolved)
		if err != nil {
			return Fail("invalid_path", err.Error())
		}
		diffArgs = append(diffArgs, "--", filepath.ToSlash(relPath))
	}
	diffOut, diffErr := runGitDiff(ctx, root, diffArgs)
	if diffErr != nil {
		return Fail("git_diff_failed", security.Redact(strings.TrimSpace(string(diffOut)+": "+diffErr.Error())))
	}
	max := t.MaxOutput
	if max <= 0 {
		max = 256 * 1024
	}
	diffText := string(diffOut)
	truncated := len(diffText) > max
	if truncated {
		diffText = diffText[:max]
	}
	result := OK(map[string]any{
		"scope":  "commit",
		"base":   hash,
		"commit": map[string]any{"hash": header[0], "author": header[1], "date": header[2], "subject": header[3]},
		"diff":   diffText,
	})
	result.Truncated = truncated
	return result
}

// Одна ссылка, а не диапазон и не опция. `git` читает опции там же, где имена
// ревизий, поэтому ведущий дефис запрещён; `..` отсечён, чтобы «показать один
// коммит» не превратилось в разбор диапазона молча.
func safeGitRevision(value string) bool {
	if value == "" || len(value) > 200 || strings.HasPrefix(value, "-") || strings.Contains(value, "..") {
		return false
	}
	for _, symbol := range value {
		switch {
		case symbol >= 'a' && symbol <= 'z', symbol >= 'A' && symbol <= 'Z', symbol >= '0' && symbol <= '9':
		case symbol == '.' || symbol == '_' || symbol == '-' || symbol == '/' || symbol == '^' || symbol == '~' || symbol == '@':
		default:
			return false
		}
	}
	return true
}

func runGitDiff(ctx context.Context, root string, args []string) ([]byte, error) {
	cmd := osproc.CommandContext(ctx, "git", args...)
	cmd.Dir = root
	return cmd.CombinedOutput()
}

func gitWorkTreeAvailable(ctx context.Context, root string) bool {
	cmd := osproc.CommandContext(ctx, "git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	return err == nil && strings.EqualFold(strings.TrimSpace(string(out)), "true")
}

func isMissingHEAD(message string) bool {
	lower := strings.ToLower(message)
	return strings.Contains(lower, "bad revision 'head'") ||
		strings.Contains(lower, "unknown revision or path not in the working tree") ||
		strings.Contains(lower, "ambiguous argument 'head'") ||
		strings.Contains(lower, "needed a single revision") ||
		strings.Contains(lower, "does not have any commits yet")
}

type RunCommand struct {
	FS             *workspace.FS
	MaxOutput      int
	DefaultTimeout time.Duration
	Executor       sandbox.ProcessExecutor
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
	return domain.ToolDefinition{Name: "run_command", Description: "Run one non-interactive command in a workspace directory after user approval.", InputSchema: schema(`{"type":"object","properties":{"command":{"type":"string","description":"One local shell command. Do not background or request elevation."},"cwd":{"type":"string","description":"Workspace-relative directory, for example src. Empty uses the workspace root."},"reason":{"type":"string","description":"Why this command is needed for the current task"},"timeoutSeconds":{"type":"integer","minimum":1,"maximum":600}},"required":["command","reason"],"additionalProperties":false}`)}
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
			WorkspaceRoot: t.FS.Root(), WorkingDirectory: cwd, ShellCommand: input.Command,
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

// GitBranches — ветки репозитория списком: текущая, локальные, удалённые.
//
// Отдельный инструмент, а не поле в `git_diff`: тот показывает несохранённую
// работу, и подмешивать в его выдачу справочник веток значило бы отдавать
// список тому, кто спросил про diff. Читающий: ни одна из команд ниже ничего
// не меняет.
type GitBranches struct {
	FS *workspace.FS
	// Потолок строк на раздел. Репозитории с сотнями веток встречаются чаще,
	// чем кажется, и без границы выдача вытеснит из окна сам вопрос.
	MaxRefs int
}

func (t GitBranches) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:        "git_branches",
		Description: "List Git branches of the workspace: the current branch, local branches, and remote-tracking branches with their upstream and ahead/behind counts. Read-only. Call this when asked which branches exist — the supplied context carries only the current one.",
		InputSchema: schema(`{"type":"object","properties":{"remote":{"type":"boolean","description":"Include remote-tracking branches. Default true."},"contains":{"type":"string","description":"Optional case-insensitive substring to filter branch names."}},"additionalProperties":false}`),
	}
}

func (t GitBranches) Execute(ctx context.Context, raw json.RawMessage) domain.ToolResult {
	input := struct {
		Remote   *bool  `json:"remote"`
		Contains string `json:"contains"`
	}{}
	if len(raw) > 0 {
		if bad := Decode(raw, &input); bad != nil {
			return *bad
		}
	}
	root := t.FS.Root()
	if !gitWorkTreeAvailable(ctx, root) {
		return FailWithHint(
			"git_unavailable",
			"workspace is not a usable Git repository",
			"filtered-copy sandboxes omit .git; branches are only visible in a worktree sandbox",
		)
	}
	max := t.MaxRefs
	if max <= 0 {
		max = 200
	}
	// Формат задан явно, а не разбирается из человекочитаемого `git branch -v`:
	// тот выравнивает колонки пробелами и метит текущую ветку звёздочкой, и
	// разбор поехал бы на первом же имени с пробелом.
	format := "%(refname:short)\t%(upstream:short)\t%(upstream:track)\t%(objectname:short)\t%(HEAD)"
	local, err := gitRefLines(ctx, root, format, "refs/heads")
	if err != nil {
		return Fail("git_branches_failed", security.Redact(err.Error()))
	}
	refs := make([]map[string]any, 0, len(local))
	current := ""
	needle := strings.ToLower(strings.TrimSpace(input.Contains))
	add := func(lines []string, kind string) bool {
		truncated := false
		for _, line := range lines {
			parts := strings.Split(line, "\t")
			if len(parts) < 5 || strings.TrimSpace(parts[0]) == "" {
				continue
			}
			name := parts[0]
			if parts[4] == "*" {
				current = name
			}
			if needle != "" && !strings.Contains(strings.ToLower(name), needle) {
				continue
			}
			if len(refs) >= max {
				truncated = true
				break
			}
			ref := map[string]any{"name": name, "kind": kind, "revision": parts[3]}
			if parts[1] != "" {
				ref["upstream"] = parts[1]
			}
			if parts[2] != "" {
				ref["track"] = strings.Trim(parts[2], "[]")
			}
			if parts[4] == "*" {
				ref["current"] = true
			}
			refs = append(refs, ref)
		}
		return truncated
	}
	truncated := add(local, "local")
	if input.Remote == nil || *input.Remote {
		remote, remoteErr := gitRefLines(ctx, root, format, "refs/remotes")
		if remoteErr != nil {
			return Fail("git_branches_failed", security.Redact(remoteErr.Error()))
		}
		truncated = add(remote, "remote") || truncated
	}
	result := OK(map[string]any{
		"current":  current,
		"branches": refs,
		// Оговорка та же, что у жетона ветки: git на сервер сам не ходит, и
		// «отстаём на 3» верно на момент последнего `git fetch`.
		"note": "ahead/behind in track are as of the last fetch; git does not contact the server on its own",
	})
	result.Truncated = truncated
	return result
}

func gitRefLines(ctx context.Context, root, format string, args ...string) ([]string, error) {
	cmd := osproc.CommandContext(ctx, "git", append([]string{"for-each-ref", "--format=" + format}, args...)...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), err)
	}
	return strings.Split(strings.TrimRight(string(out), "\n"), "\n"), nil
}

// GitLog — история коммитов: кто, когда и с какой формулировкой.
//
// Отдельно от git_diff по той же причине, что и ветки: diff показывает
// несохранённую работу, а история — уже сохранённую, и смешивать их в одной
// выдаче значит отдавать спросившему про одно ответ про другое. Без этого
// инструмента модель тянулась читать .git/logs/HEAD — путь исключённый, и
// разговор упирался в отказ вместо истории.
type GitLog struct {
	FS *workspace.FS
	// Потолок коммитов на вызов. История длиннее окна модели у любого живого
	// репозитория, и граница здесь не украшение.
	MaxCommits int
}

func (t GitLog) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:        "git_log",
		Description: "List recent Git commits of the workspace: hash, author, ISO date, ref names and subject, newest first. Read-only. Call this when asked about commit history, recent changes, or who changed something — the supplied context carries no history.",
		InputSchema: schema(`{"type":"object","properties":{"limit":{"type":"integer","minimum":1,"maximum":200,"description":"How many commits to return, newest first. Default 20."},"path":{"type":"string","description":"Optional workspace-relative path; only commits touching it are returned."},"contains":{"type":"string","description":"Optional case-insensitive substring the commit message must contain."}},"additionalProperties":false}`),
	}
}

func (t GitLog) Execute(ctx context.Context, raw json.RawMessage) domain.ToolResult {
	started := time.Now()
	var input struct {
		Limit    int    `json:"limit"`
		Path     string `json:"path"`
		Contains string `json:"contains"`
	}
	if len(raw) > 0 {
		if bad := Decode(raw, &input); bad != nil {
			return *bad
		}
	}
	root := t.FS.Root()
	if !gitWorkTreeAvailable(ctx, root) {
		return logExecute(ctx, "git_log", started, FailWithHint(
			"git_unavailable",
			"workspace is not a usable Git repository",
			"filtered-copy sandboxes omit .git; history is only visible in a worktree sandbox",
		))
	}
	limit := t.MaxCommits
	if limit <= 0 {
		limit = 20
	}
	if input.Limit > 0 {
		limit = input.Limit
	}
	if limit > 200 {
		limit = 200
	}
	// Формат задан по полям, а не берётся из oneline: сообщение коммита несёт
	// что угодно, включая табуляции, поэтому subject идёт последним и режется
	// с ограничением на число частей.
	format := "%H%x09%h%x09%an%x09%aI%x09%D%x09%s"
	args := []string{"log", "--no-color", "--format=" + format, "-n", strconv.Itoa(limit)}
	if needle := strings.TrimSpace(input.Contains); needle != "" {
		args = append(args, "--fixed-strings", "--regexp-ignore-case", "--grep="+needle)
	}
	pathFilter := ""
	if rel := strings.TrimSpace(input.Path); rel != "" {
		resolved, err := t.FS.Resolve(rel, false)
		if err != nil {
			return logExecute(ctx, "git_log", started, Fail("invalid_path", err.Error()))
		}
		relPath, err := filepath.Rel(root, resolved)
		if err != nil {
			return logExecute(ctx, "git_log", started, Fail("invalid_path", err.Error()))
		}
		pathFilter = filepath.ToSlash(relPath)
		args = append(args, "--", pathFilter)
	}
	cmd := osproc.CommandContext(ctx, "git", args...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Репозиторий без единого коммита — не поломка, а пустая история:
		// отказ здесь заставил бы модель гадать, что именно недоступно.
		if isMissingHEAD(string(out) + err.Error()) {
			return logExecute(ctx, "git_log", started, OK(map[string]any{
				"commits": []any{}, "count": 0,
				"note": "repository has no commits yet",
			}), "commits", 0)
		}
		return logExecute(ctx, "git_log", started, Fail("git_log_failed", security.Redact(strings.TrimSpace(string(out)+": "+err.Error()))))
	}
	commits := make([]map[string]any, 0, limit)
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		parts := strings.SplitN(line, "\t", 6)
		if len(parts) < 6 || strings.TrimSpace(parts[0]) == "" {
			continue
		}
		commit := map[string]any{
			"hash": parts[0], "shortHash": parts[1], "author": parts[2],
			"date": parts[3], "subject": parts[5],
		}
		if parts[4] != "" {
			commit["refs"] = parts[4]
		}
		commits = append(commits, commit)
	}
	payload := map[string]any{
		"commits": commits, "count": len(commits),
		// Оговорка та же, что у веток: git на сервер сам не ходит, и «последний
		// коммит» верен на момент последнего `git fetch`.
		"note": "local history only; commits pushed by others appear after a fetch",
	}
	if pathFilter != "" {
		payload["path"] = pathFilter
	}
	result := OK(payload)
	result.Truncated = len(commits) >= limit
	return logExecute(ctx, "git_log", started, result, "commits", len(commits), "limit", limit, "path", pathFilter)
}

// GitTags — метки репозитория: имя, коммит, дата, подпись аннотации.
//
// Ветки и метки живут в разных пространствах имён, и git_branches про
// refs/tags не знает: на вопрос про теги он отвечал списком веток либо
// признанием, что инструмента нет.
type GitTags struct {
	FS *workspace.FS
	// Потолок строк, как у веток: у релизного репозитория меток больше, чем
	// поместится в ответ.
	MaxRefs int
}

func (t GitTags) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:        "git_tags",
		Description: "List Git tags of the workspace with their commit, creation date and annotation subject, newest first. Read-only. Call this when asked about tags, releases or versions — git_branches covers branches only and never returns tags.",
		InputSchema: schema(`{"type":"object","properties":{"contains":{"type":"string","description":"Optional case-insensitive substring to filter tag names."}},"additionalProperties":false}`),
	}
}

func (t GitTags) Execute(ctx context.Context, raw json.RawMessage) domain.ToolResult {
	started := time.Now()
	var input struct {
		Contains string `json:"contains"`
	}
	if len(raw) > 0 {
		if bad := Decode(raw, &input); bad != nil {
			return *bad
		}
	}
	root := t.FS.Root()
	if !gitWorkTreeAvailable(ctx, root) {
		return logExecute(ctx, "git_tags", started, FailWithHint(
			"git_unavailable",
			"workspace is not a usable Git repository",
			"filtered-copy sandboxes omit .git; tags are only visible in a worktree sandbox",
		))
	}
	max := t.MaxRefs
	if max <= 0 {
		max = 200
	}
	// У аннотированной метки objectname — сам объект метки, а коммит лежит в
	// `*objectname`; у лёгкой метки второго поля нет вовсе. Спрашиваем оба и
	// отдаём коммит, потому что спрашивают всегда про него.
	format := "%(refname:short)%09%(objecttype)%09%(objectname:short)%09%(*objectname:short)%09%(creatordate:iso-strict)%09%(contents:subject)"
	lines, err := gitRefLines(ctx, root, format, "--sort=-creatordate", "refs/tags")
	if err != nil {
		return logExecute(ctx, "git_tags", started, Fail("git_tags_failed", security.Redact(err.Error())))
	}
	needle := strings.ToLower(strings.TrimSpace(input.Contains))
	tags := make([]map[string]any, 0, len(lines))
	truncated := false
	for _, line := range lines {
		parts := strings.SplitN(line, "\t", 6)
		if len(parts) < 6 || strings.TrimSpace(parts[0]) == "" {
			continue
		}
		name := parts[0]
		if needle != "" && !strings.Contains(strings.ToLower(name), needle) {
			continue
		}
		if len(tags) >= max {
			truncated = true
			break
		}
		revision := parts[3]
		if revision == "" {
			revision = parts[2]
		}
		tag := map[string]any{"name": name, "revision": revision, "annotated": parts[1] == "tag", "date": parts[4]}
		if parts[5] != "" {
			tag["subject"] = parts[5]
		}
		tags = append(tags, tag)
	}
	result := OK(map[string]any{
		"tags": tags, "count": len(tags),
		"note": "local tags only; tags pushed by others appear after a fetch",
	})
	result.Truncated = truncated
	return logExecute(ctx, "git_tags", started, result, "tags", len(tags))
}
