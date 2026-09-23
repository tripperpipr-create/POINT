package domain

import "strings"

// toolNameAliases maps the tool names models habitually invent onto the tools
// Point actually exposes. Custom tools are named customtool_<hex>, so an alias
// can never shadow a user's own tool and never grants authority by itself:
// every caller still checks the resolved name against the enabled set.
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

// ToolNameAlias resolves an already normalized lookup key (lower case, dashes
// replaced by underscores) to its Point tool name.
func ToolNameAlias(key string) (string, bool) {
	mapped, ok := toolNameAliases[key]
	return mapped, ok
}

// CanonicalToolName renames a synonym to the tool Point exposes. An unknown
// name is returned trimmed but unchanged: naming a tool is not enabling it, and
// the caller decides whether the resolved name is allowed for this run.
func CanonicalToolName(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return ""
	}
	if mapped, ok := toolNameAliases[strings.ToLower(strings.ReplaceAll(trimmed, "-", "_"))]; ok {
		return mapped
	}
	return trimmed
}
