package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/observability"
	"local-agent-workbench/internal/workspace"
)

func schema(value string) json.RawMessage { return json.RawMessage(value) }

type ListFiles struct{ FS *workspace.FS }

func (t ListFiles) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{Name: "list_files", Description: "List a workspace directory tree (default depth 3). Prefer a subdirectory path instead of listing the whole repo. Missing directories return an empty list (create files with propose_patch). Dependency roots (vendor/, node_modules/) are omitted from parent listings — pass an explicit path such as vendor/<package> to inspect them. Excludes VCS metadata, binary files and secrets. truncated=true means the listing stopped at 5000 entries: list a narrower path.", InputSchema: schema(`{"type":"object","properties":{"path":{"type":"string","description":"Workspace-relative directory to list, for example src/internal or vendor/<package>. Empty lists the workspace root."},"maxDepth":{"type":"integer","minimum":1,"maximum":20,"description":"How many directory levels to include (default 3). Raise it only for a narrow path."}},"additionalProperties":false}`)}
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
	// Глубина по умолчанию 3, а не 8, как у дерева IDE: описание всегда
	// советовало 2–4, а модель без числа получала всё дерево проекта.
	if input.MaxDepth <= 0 {
		input.MaxDepth = defaultListDepth
	}
	tree, err := t.FS.ListPath(ctx, input.Path, input.MaxDepth)
	if err != nil {
		return logExecute(ctx, "list_files", started, FailWithHint("list_failed", err.Error(), "use a workspace-relative directory path; confirm it with project_map or search_code"), "path", input.Path, "max_depth", input.MaxDepth)
	}
	result := OK(tree)
	result.Truncated = countFileNodes(tree) >= workspace.MaxListedNodes
	return logExecute(ctx, "list_files", started, result, "path", input.Path, "max_depth", input.MaxDepth)
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
	// Большой файл без диапазона отдаётся первыми readFileDefaultLines
	// строками: lock-файл в 3500 строк целиком превращал каждый следующий
	// запрос к модели в сотню тысяч токенов (квест cba8).
	capped := false
	if input.StartLine <= 0 && input.EndLine <= 0 && content.Size > readFileCapBytes && strings.Count(content.Numbered, "\n") > readFileDefaultLines {
		input.StartLine, input.EndLine, capped = 1, readFileDefaultLines, true
	}
	sliced, start, end, partial := sliceNumberedLines(content.Numbered, input.StartLine, input.EndLine)
	truncated := content.Truncated || partial
	// sha256 — всего файла и при частичном чтении: по нему осмотр фрагмента
	// сверяется с текущим файлом перед патчем.
	payload := map[string]any{
		"path": content.Path, "content": sliced, "sha256": content.SHA256, "size": content.Size,
		"truncated": truncated, "partial": partial, "startLine": start, "endLine": end,
	}
	switch {
	case capped:
		payload["hint"] = fmt.Sprintf("large file: only lines %d-%d are shown; continue with startLine=%d, or use search_text to find the region you need", start, end, end+1)
	case truncated && !partial:
		payload["hint"] = "file was truncated; call read_file again with startLine/endLine around the region you still need"
	case partial:
		payload["hint"] = "partial read: exact propose_patch edits whose oldText lies inside these lines are allowed; a complete-content rewrite needs a full read_file"
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

const (
	defaultListDepth     = 3
	readFileDefaultLines = 2000
	// Предел строк действует только для файлов крупнее этого: обычный
	// исходник в пару тысяч коротких строк читается целиком, как прежде.
	readFileCapBytes = 64 * 1024
)

func countFileNodes(nodes []domain.FileNode) int {
	count := 0
	for _, node := range nodes {
		count += 1 + countFileNodes(node.Children)
	}
	return count
}

type SearchText struct{ FS *workspace.FS }

func (t SearchText) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{Name: "search_text", Description: "Search workspace text files line by line. Literal substring by default (case-insensitive); set regex=true for a Go RE2 pattern. Narrow with path (directory or file) and glob (*.go, src/**/*.ts). context adds up to 5 neighbouring lines. truncated=true means more matches exist: narrow the query, path or glob. Prefer search_code for ranked implementation context.", InputSchema: schema(`{"type":"object","properties":{"query":{"type":"string","description":"Substring, or an RE2 regular expression when regex=true."},"regex":{"type":"boolean","description":"Treat query as a regular expression."},"caseSensitive":{"type":"boolean"},"path":{"type":"string","description":"Workspace-relative directory or file to search in."},"glob":{"type":"string","description":"File name or path pattern, for example *.yml or cf-vue-apps/**/*.js"},"context":{"type":"integer","minimum":0,"maximum":5,"description":"Lines of context before and after each match."},"maxResults":{"type":"integer","minimum":1,"maximum":500}},"required":["query"],"additionalProperties":false}`)}
}
func (t SearchText) Execute(ctx context.Context, raw json.RawMessage) domain.ToolResult {
	started := time.Now()
	var input struct {
		Query         string `json:"query"`
		Regex         bool   `json:"regex"`
		CaseSensitive bool   `json:"caseSensitive"`
		Path          string `json:"path"`
		Glob          string `json:"glob"`
		Context       int    `json:"context"`
		MaxResults    int    `json:"maxResults"`
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
	matches, truncated, err := t.FS.SearchWith(ctx, workspace.SearchOptions{
		Query: input.Query, Regex: input.Regex, CaseSensitive: input.CaseSensitive,
		Path: input.Path, Glob: input.Glob, Context: input.Context, MaxResults: input.MaxResults,
	})
	if err != nil {
		hint := "check that path is a workspace-relative directory or file; drop path and glob to search the whole workspace"
		if input.Regex {
			hint = "the pattern is not valid RE2; escape special characters or retry with regex=false for a literal search"
		}
		return logExecute(ctx, "search_text", started, FailWithHint("search_failed", err.Error(), hint), "query", observability.Snippet(input.Query, 80))
	}
	result := OK(matches)
	result.Truncated = truncated
	return logExecute(ctx, "search_text", started, result, "query", observability.Snippet(input.Query, 80), "matches", len(matches), "truncated", truncated)
}
