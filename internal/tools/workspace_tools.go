package tools

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/observability"
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
