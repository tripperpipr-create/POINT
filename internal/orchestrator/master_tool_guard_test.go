package orchestrator

import (
	"encoding/json"
	"testing"

	"local-agent-workbench/internal/domain"
)

func TestMasterToolCallKeyNormalizesJSON(t *testing.T) {
	a := masterToolCallKey("list_files", json.RawMessage(`{"path":"."}`) )
	b := masterToolCallKey("list_files", json.RawMessage(`{"path": "."}`) )
	if a != b {
		t.Fatalf("expected same key, got %q vs %q", a, b)
	}
}

func TestMasterExplorationEmpty(t *testing.T) {
	emptyList, _ := json.Marshal([]any{})
	if !masterExplorationEmpty("list_files", domain.ToolResult{OK: true, Output: emptyList}) {
		t.Fatal("empty list_files should count as empty exploration")
	}
	emptyMap, _ := json.Marshal(map[string]any{
		"filesByLanguage": map[string]any{},
		"topDirectories":  []any{},
		"symbols":         []any{},
	})
	if !masterExplorationEmpty("project_map", domain.ToolResult{OK: true, Output: emptyMap}) {
		t.Fatal("empty project_map should count as empty exploration")
	}
	full, _ := json.Marshal([]any{"a.go"})
	if masterExplorationEmpty("list_files", domain.ToolResult{OK: true, Output: full}) {
		t.Fatal("non-empty list should not count as empty")
	}
}

func TestFilterOutExplorationTools(t *testing.T) {
	in := []domain.ToolDefinition{
		{Name: "project_map"},
		{Name: "read_file"},
		{Name: "list_files"},
		{Name: "search_text"},
	}
	out := filterOutExplorationTools(in)
	if len(out) != 2 || out[0].Name != "read_file" || out[1].Name != "search_text" {
		t.Fatalf("unexpected filter result: %+v", out)
	}
}
