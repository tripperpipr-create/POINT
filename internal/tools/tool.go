package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/observability"
	"local-agent-workbench/internal/security"
)

type Tool interface {
	Definition() domain.ToolDefinition
	Execute(context.Context, json.RawMessage) domain.ToolResult
}

// ApprovalPreviewer lets a tool expose the effective operation that will run.
// This is essential for fixed custom commands because their executable text is
// server-owned and intentionally absent from model-controlled arguments.
type ApprovalPreviewer interface {
	ApprovalArguments(json.RawMessage) json.RawMessage
}

// ArgumentValidator rejects malformed model input before an approval is shown.
// This prevents users from approving an operation that can only fail later.
type ArgumentValidator interface {
	ValidateArguments(json.RawMessage) *domain.ToolResult
}

type Registry struct{ tools map[string]Tool }

func NewRegistry(items ...Tool) *Registry {
	r := &Registry{tools: make(map[string]Tool)}
	for _, item := range items {
		r.tools[item.Definition().Name] = item
	}
	return r
}

func (r *Registry) Get(name string) (Tool, bool) { tool, ok := r.tools[name]; return tool, ok }

// Names — все собранные инструменты по алфавиту. Нужен там, где реестр
// сверяют с каталогом: без перечисления расхождение видно только по имени,
// которое кто-то догадался проверить.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (r *Registry) Definitions(allowed []string) []domain.ToolDefinition {
	allow := make(map[string]bool, len(allowed))
	for _, name := range allowed {
		allow[name] = true
	}
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		if allow[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	defs := make([]domain.ToolDefinition, 0, len(names))
	for _, name := range names {
		defs = append(defs, r.tools[name].Definition())
	}
	return defs
}

func OK(value any) domain.ToolResult {
	data, err := json.Marshal(value)
	if err != nil {
		return Fail("encode_error", err.Error())
	}
	return domain.ToolResult{OK: true, Output: data}
}

func Fail(code, message string) domain.ToolResult {
	return FailWithHint(code, message, "")
}

// FailWithHint rejects a tool call and attaches a concise, actionable next step.
// Hints are local guidance only — they must not invent evidence or claim success.
func FailWithHint(code, message, hint string) domain.ToolResult {
	return domain.ToolResult{OK: false, Error: &domain.ToolError{Code: code, Message: message, Hint: strings.TrimSpace(hint)}}
}

func logExecute(ctx context.Context, name string, started time.Time, result domain.ToolResult, extra ...any) domain.ToolResult {
	errCode, errMsg := "", ""
	if result.Error != nil {
		errCode = result.Error.Code
		errMsg = result.Error.Message
	}
	attrs := make([]any, 0, 14+len(extra))
	attrs = append(attrs,
		"tool", name,
		"ok", result.OK,
		"error_code", errCode,
		"duration_ms", time.Since(started).Milliseconds(),
		"output_bytes", len(result.Output),
		"truncated", result.Truncated,
	)
	if errMsg != "" {
		attrs = append(attrs, "error", observability.Snippet(security.Redact(errMsg), 240))
	}
	attrs = append(attrs, extra...)
	log := observability.From(ctx)
	if result.OK {
		log.Info("tool execute", attrs...)
	} else {
		log.Warn("tool execute", attrs...)
	}
	return result
}

func Decode(input json.RawMessage, target any) *domain.ToolResult {
	if err := json.Unmarshal(input, target); err != nil {
		result := FailWithHint("invalid_input", fmt.Sprintf("invalid JSON input: %v", err), "pass a single JSON object whose keys match the tool schema; do not wrap arguments in a string")
		return &result
	}
	return nil
}
