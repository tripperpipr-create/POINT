package tools

// Читающие инструменты git: несохранённая работа и содержимое коммита
// (git_diff), ветки, история и метки. Ни одна команда ниже репозиторий не
// меняет; ревизии проверяются до вызова, потому что git читает опции там же,
// где имена ссылок.

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/osproc"
	"local-agent-workbench/internal/security"
	"local-agent-workbench/internal/workspace"
)

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
