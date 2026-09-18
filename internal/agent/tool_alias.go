package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"local-agent-workbench/internal/providers"
)

var toolNameAliases = map[string]string{
	"read":        "read_file",
	"readfile":    "read_file",
	"cat":         "read_file",
	"skill":       "read_skill",
	"readskill":   "read_skill",
	"read_skill":  "read_skill",
	"write":       "propose_patch",
	"writefile":   "propose_patch",
	"strreplace":  "propose_patch",
	"str_replace": "propose_patch",
	"apply_patch": "propose_patch",
	"edit":        "propose_patch",
	"grep":        "search_code",
	"search":      "search_code",
	"ripgrep":     "search_code",
	"glob":        "list_files",
	"ls":          "list_files",
	"listdir":     "list_files",
	"list_dir":    "list_files",
	"shell":       "run_command",
	"bash":        "run_command",
	"cmd":         "run_command",
	"terminal":    "run_command",
	"run":         "run_command",
}

func prepareToolCall(call providers.ToolCall, allowed []string) providers.ToolCall {
	mapped, _ := remapToolName(call.Name, allowed)
	call.Name = mapped
	call.Arguments = normalizeToolArguments(mapped, call.Arguments)
	return call
}

func remapToolName(name string, allowed []string) (string, string) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return name, ""
	}
	if contains(allowed, trimmed) {
		return trimmed, ""
	}
	key := strings.ToLower(strings.ReplaceAll(trimmed, "-", "_"))
	if contains(allowed, key) {
		return key, ""
	}
	mapped, ok := toolNameAliases[key]
	if !ok {
		return trimmed, ""
	}
	if mapped == "search_code" && !contains(allowed, "search_code") && contains(allowed, "search_text") {
		mapped = "search_text"
	}
	if contains(allowed, mapped) {
		return mapped, ""
	}
	return trimmed, fmt.Sprintf("Tool %q is not available. Use %s instead. Allowed: %s.", trimmed, mapped, strings.Join(allowed, ", "))
}

func unknownToolHint(name string, allowed []string) string {
	if len(allowed) == 0 {
		return fmt.Sprintf("Unknown tool %q. No tools are allowed on this run.", name)
	}
	return fmt.Sprintf("Unknown tool %q. Use one of: %s. Paths must be workspace-relative (for example src/main.go), not absolute.", name, strings.Join(allowed, ", "))
}

func normalizeToolArguments(name string, raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return raw
	}
	switch name {
	case "read_file":
		copyArg(obj, "path", "file", "target", "filename")
		if _, ok := obj["startLine"]; !ok {
			if offset, ok := asPositiveInt(obj["offset"]); ok {
				obj["startLine"] = offset
				if limit, ok := asPositiveInt(obj["limit"]); ok {
					obj["endLine"] = offset + limit - 1
				}
			}
		}
	case "read_skill":
		copyArg(obj, "id", "skill_id", "skillId", "skill")
		copyArg(obj, "name", "skill_name", "title")
	case "list_files":
		copyArg(obj, "path", "file", "target", "directory", "dir", "glob", "glob_pattern")
	case "search_code", "search_text":
		copyArg(obj, "query", "pattern", "q", "text", "search")
	case "run_command":
		copyArg(obj, "command", "cmd", "script")
		copyArg(obj, "cwd", "working_directory", "dir", "directory")
		copyArg(obj, "reason", "explanation", "description")
	case "propose_patch":
		copyArg(obj, "path", "file", "target", "filename")
		copyArg(obj, "reason", "explanation", "description", "comment")
		if _, hasEdits := obj["edits"]; !hasEdits {
			oldText, hasOld := takeString(obj, "old_string", "oldText")
			newText, hasNew := takeString(obj, "new_string", "newText")
			if hasOld && hasNew {
				obj["edits"] = []any{map[string]any{"oldText": oldText, "newText": newText}}
			}
		}
		if _, hasContent := obj["content"]; !hasContent {
			if _, hasEdits := obj["edits"]; !hasEdits {
				copyArg(obj, "content", "contents")
			}
		}
		for _, alias := range []string{"file", "target", "filename", "explanation", "description", "comment", "contents", "old_string", "new_string", "oldText", "newText"} {
			delete(obj, alias)
		}
	default:
		return raw
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return raw
	}
	return out
}

func copyArg(obj map[string]any, dest string, aliases ...string) {
	if _, ok := obj[dest]; ok {
		return
	}
	for _, alias := range aliases {
		if value, ok := obj[alias]; ok {
			obj[dest] = value
			return
		}
	}
}

func takeString(obj map[string]any, keys ...string) (string, bool) {
	for _, key := range keys {
		value, ok := obj[key]
		if !ok {
			continue
		}
		text, ok := value.(string)
		if ok {
			return text, true
		}
	}
	return "", false
}

func asPositiveInt(value any) (int, bool) {
	switch n := value.(type) {
	case float64:
		i := int(n)
		if n >= 1 && n == float64(i) {
			return i, true
		}
	case int:
		if n >= 1 {
			return n, true
		}
	case json.Number:
		i, err := n.Int64()
		if err == nil && i >= 1 {
			return int(i), true
		}
	}
	return 0, false
}
