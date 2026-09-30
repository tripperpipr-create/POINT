package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/workspace"
)

// ValidateSyntax проверяет синтаксис JSON и YAML без запуска команд.
//
// Агенты проверяли YAML, ставя `js-yaml` во временную папку: в песочнице
// `/tmp` не живёт между командами, и в квесте cba8 за три прогона пакет
// ставился восемь раз, а проверка всё равно падала на «Cannot find module».
// Здесь разбор идёт в ядре, мгновенно и без сети.
type ValidateSyntax struct{ FS *workspace.FS }

func (t ValidateSyntax) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{Name: "validate_syntax", Description: "Check that a JSON or YAML file parses (all YAML documents, anchors and aliases included), without running commands or installing packages. Returns valid=true, or the line, column and parser message of the first error. Syntax only: it does not prove that a CI pipeline, Compose file or config is semantically correct.", InputSchema: schema(`{"type":"object","properties":{"path":{"type":"string","description":"Workspace-relative path of the file to check"},"format":{"type":"string","enum":["json","yaml"],"description":"Optional; inferred from the extension (.json, .yml, .yaml) when omitted"}},"required":["path"],"additionalProperties":false}`)}
}

func (t ValidateSyntax) Execute(ctx context.Context, raw json.RawMessage) domain.ToolResult {
	started := time.Now()
	var input struct {
		Path   string `json:"path"`
		Format string `json:"format"`
	}
	if bad := Decode(raw, &input); bad != nil {
		return *bad
	}
	format := strings.ToLower(strings.TrimSpace(input.Format))
	if format == "" {
		switch strings.ToLower(path.Ext(strings.ReplaceAll(input.Path, "\\", "/"))) {
		case ".json":
			format = "json"
		case ".yml", ".yaml":
			format = "yaml"
		}
	}
	if format != "json" && format != "yaml" {
		return logExecute(ctx, "validate_syntax", started, FailWithHint("unsupported_format", "only JSON and YAML files can be checked", "pass format=json or format=yaml, or use the project's own linter through run_command"), "path", input.Path)
	}
	content, err := t.FS.Read(input.Path, false)
	if err != nil {
		return logExecute(ctx, "validate_syntax", started, FailWithHint("read_failed", err.Error(), "confirm the path with list_files"), "path", input.Path)
	}
	if content.Truncated {
		return logExecute(ctx, "validate_syntax", started, FailWithHint("file_too_large", "the file is larger than the read limit", "check it with the project's own tooling through run_command"), "path", input.Path)
	}
	payload := map[string]any{"path": content.Path, "format": format, "valid": true}
	var parseErr error
	if format == "json" {
		parseErr = validateJSON(content.Content)
	} else {
		parseErr = validateYAML(content.Content)
	}
	if parseErr != nil {
		payload["valid"] = false
		payload["message"] = parseErr.Error()
		if line, column := syntaxErrorPosition(parseErr, content.Content); line > 0 {
			payload["line"] = line
			if column > 0 {
				payload["column"] = column
			}
			if text := lineText(content.Content, line); text != "" {
				payload["lineText"] = text
				if format == "yaml" && strings.Contains(text, "*") && (strings.Contains(text, "- *") || strings.Contains(text, ": *")) {
					payload["hint"] = "an unquoted value starting with * is a YAML alias; quote it, for example - \"*.tgz\""
				}
			}
		}
	}
	return logExecute(ctx, "validate_syntax", started, OK(payload), "path", content.Path, "format", format, "valid", parseErr == nil)
}

func validateJSON(text string) error {
	decoder := json.NewDecoder(strings.NewReader(text))
	var value any
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	if decoder.More() {
		return errors.New("unexpected data after the top-level JSON value")
	}
	return nil
}

func validateYAML(text string) error {
	decoder := yaml.NewDecoder(bytes.NewReader([]byte(text)))
	for {
		var node yaml.Node
		err := decoder.Decode(&node)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

var yamlLinePattern = regexp.MustCompile(`line (\d+)(?::\s*column (\d+))?`)

// syntaxErrorPosition достаёт строку и колонку: YAML пишет их в тексте
// ошибки, JSON сообщает смещение в байтах.
func syntaxErrorPosition(err error, text string) (int, int) {
	var syntax *json.SyntaxError
	if errors.As(err, &syntax) {
		return lineColumnAt(text, int(syntax.Offset))
	}
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		return lineColumnAt(text, int(typeErr.Offset))
	}
	if match := yamlLinePattern.FindStringSubmatch(err.Error()); match != nil {
		line, _ := strconv.Atoi(match[1])
		column, _ := strconv.Atoi(match[2])
		return line, column
	}
	return 0, 0
}

func lineColumnAt(text string, offset int) (int, int) {
	if offset <= 0 || offset > len(text) {
		return 0, 0
	}
	before := text[:offset]
	line := strings.Count(before, "\n") + 1
	column := offset - strings.LastIndex(before, "\n")
	return line, column
}

func lineText(text string, line int) string {
	lines := strings.Split(text, "\n")
	if line < 1 || line > len(lines) {
		return ""
	}
	return strings.TrimRight(lines[line-1], "\r")
}
