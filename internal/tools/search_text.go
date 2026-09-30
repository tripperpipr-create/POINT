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
