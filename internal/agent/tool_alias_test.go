package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"local-agent-workbench/internal/providers"
)

func TestRemapToolNameAliases(t *testing.T) {
	allowed := []string{"read_file", "search_code", "list_files", "propose_patch", "run_command"}
	cases := map[string]string{
		"Read":        "read_file",
		"readFile":    "read_file",
		"Grep":        "search_code",
		"Shell":       "run_command",
		"Bash":        "run_command",
		"Write":       "propose_patch",
		"StrReplace":  "propose_patch",
		"Glob":        "list_files",
		"ls":          "list_files",
		"read_file":   "read_file",
		"search-code": "search_code",
	}
	for from, want := range cases {
		got, hint := remapToolName(from, allowed)
		if got != want || hint != "" {
			t.Fatalf("%q -> %q hint=%q, want %q", from, got, hint, want)
		}
	}
	got, hint := remapToolName("Grep", []string{"search_text", "read_file"})
	if got != "search_text" || hint != "" {
		t.Fatalf("Grep without search_code -> %q hint=%q", got, hint)
	}
	got, hint = remapToolName("Shell", []string{"read_file"})
	if got != "Shell" || !strings.Contains(hint, "run_command") {
		t.Fatalf("disabled alias -> %q hint=%q", got, hint)
	}
}

func TestNormalizeToolArgumentsAliases(t *testing.T) {
	read := normalizeToolArguments("read_file", json.RawMessage(`{"file":"src/main.go","offset":10,"limit":5}`))
	var readObj map[string]any
	if err := json.Unmarshal(read, &readObj); err != nil {
		t.Fatal(err)
	}
	if readObj["path"] != "src/main.go" || readObj["startLine"].(float64) != 10 || readObj["endLine"].(float64) != 14 {
		t.Fatalf("read args=%s", read)
	}
	search := normalizeToolArguments("search_code", json.RawMessage(`{"pattern":"RotateToken"}`))
	if !strings.Contains(string(search), `"query":"RotateToken"`) {
		t.Fatalf("search args=%s", search)
	}
	patch := normalizeToolArguments("propose_patch", json.RawMessage(`{"file":"main.go","old_string":"old","new_string":"new","explanation":"fix"}`))
	var patchObj map[string]any
	if err := json.Unmarshal(patch, &patchObj); err != nil {
		t.Fatal(err)
	}
	if patchObj["path"] != "main.go" || patchObj["reason"] != "fix" {
		t.Fatalf("patch args=%s", patch)
	}
	if _, ok := patchObj["old_string"]; ok {
		t.Fatalf("alias leaked into patch args: %s", patch)
	}
	edits, _ := patchObj["edits"].([]any)
	if len(edits) != 1 {
		t.Fatalf("edits=%s", patch)
	}
}

func TestPrepareToolCallRemapsNameAndArgs(t *testing.T) {
	call := prepareToolCall(providers.ToolCall{Name: "Grep", Arguments: json.RawMessage(`{"pattern":"foo"}`)}, []string{"search_code", "read_file"})
	if call.Name != "search_code" || !strings.Contains(string(call.Arguments), `"query":"foo"`) {
		t.Fatalf("prepared=%#v", call)
	}
}
