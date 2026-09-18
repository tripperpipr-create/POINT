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

// ProjectMap gives an agent a compact, locally generated overview before it
// spends tokens reading individual files.
type ProjectMap struct{ FS *workspace.FS }

func (t ProjectMap) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{Name: "project_map", Description: "Return a compact local index of languages, important directories, and code symbols. Call this before reading many files or searching blindly.", InputSchema: schema(`{"type":"object","properties":{"max_symbols":{"type":"integer","minimum":10,"maximum":200,"description":"Maximum symbol names to include in the overview"}},"additionalProperties":false}`)}
}

func (t ProjectMap) Execute(ctx context.Context, raw json.RawMessage) domain.ToolResult {
	started := time.Now()
	var input struct {
		MaxSymbols int `json:"max_symbols"`
	}
	if len(raw) > 0 {
		if bad := Decode(raw, &input); bad != nil {
			return *bad
		}
	}
	result, err := t.FS.ProjectMap(ctx, input.MaxSymbols)
	if err != nil {
		return logExecute(ctx, "project_map", started, Fail("index_failed", err.Error()))
	}
	return logExecute(ctx, "project_map", started, OK(result), "files", result.Status.Files, "symbols", result.Status.Symbols)
}

// SearchCode returns only the most relevant indexed chunks and enforces a
// strict character budget so context growth is visible and predictable.
type SearchCode struct{ FS *workspace.FS }

func (t SearchCode) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{Name: "search_code", Description: "Search the local project index and return diverse ranked code chunks under a strict context budget. Set include_related to receive dependency, importer, and test paths without loading their code. The result reports candidateChunks and truncated; refine the query when important matches were omitted. Prefer this over full-file reads when locating implementation details.", InputSchema: schema(`{"type":"object","properties":{"query":{"type":"string","minLength":1,"maxLength":4096,"description":"Symbol, path fragment, or short keyword query. Avoid whole-file dumps."},"max_chunks":{"type":"integer","minimum":1,"maximum":20},"max_chars":{"type":"integer","minimum":1000,"maximum":65536},"include_related":{"type":"boolean","description":"If true, also return related import/importer/test paths without their source."}},"required":["query"],"additionalProperties":false}`)}
}

func (t SearchCode) Execute(ctx context.Context, raw json.RawMessage) domain.ToolResult {
	started := time.Now()
	var input struct {
		Query          string `json:"query"`
		MaxChunks      int    `json:"max_chunks"`
		MaxChars       int    `json:"max_chars"`
		IncludeRelated bool   `json:"include_related"`
	}
	if bad := Decode(raw, &input); bad != nil {
		return *bad
	}
	if strings.TrimSpace(input.Query) == "" || len(input.Query) > 4096 {
		return logExecute(ctx, "search_code", started, FailWithHint("invalid_input", "search query must contain 1 to 4096 UTF-8 bytes", "provide a concrete symbol, path fragment, or short keyword query"))
	}
	if input.MaxChunks != 0 && (input.MaxChunks < 1 || input.MaxChunks > 20) {
		return logExecute(ctx, "search_code", started, FailWithHint("invalid_input", "max_chunks must be between 1 and 20", "retry search_code with max_chunks between 1 and 20"))
	}
	if input.MaxChars != 0 && (input.MaxChars < 1000 || input.MaxChars > 65536) {
		return logExecute(ctx, "search_code", started, FailWithHint("invalid_input", "max_chars must be between 1000 and 65536", "retry search_code with max_chars between 1000 and 65536"))
	}
	result, err := t.FS.SearchContextWithRelations(ctx, input.Query, input.MaxChunks, input.MaxChars, input.IncludeRelated)
	if err != nil {
		return logExecute(ctx, "search_code", started, Fail("index_search_failed", err.Error()), "query", observability.Snippet(input.Query, 80))
	}
	return logExecute(ctx, "search_code", started, OK(map[string]any{
		"query": input.Query, "chunks": result.Chunks, "count": result.ReturnedChunks,
		"candidateChunks": result.CandidateChunks, "returnedChunks": result.ReturnedChunks,
		"usedChars": result.UsedChars, "maxChars": result.MaxChars, "maxChunks": result.MaxChunks,
		"truncated": result.Truncated, "relatedFiles": result.RelatedFiles,
		"relatedFilesTruncated": result.RelatedFilesTruncated,
	}), "query", observability.Snippet(input.Query, 80), "chunks", result.ReturnedChunks, "candidates", result.CandidateChunks, "truncated", result.Truncated)
}
