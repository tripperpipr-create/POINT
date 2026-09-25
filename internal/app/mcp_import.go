package app

// Импорт mcp.json: владелец вставляет конфиг, как для Claude, Cursor или
// VS Code, и видит, что из него получится, до сохранения. Предпросмотр ничего
// не хранит; сохранение идёт обычным SaveMCPServer по каждому принятому
// серверу — с теми же проверками, что у формы.

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"local-agent-workbench/internal/domain"
)

const (
	mcpImportMaxBytes   = 256 << 10
	mcpImportMaxServers = 32
)

// MCPImportCandidate — один сервер из файла. Secrets в Server заполнены
// значениями из файла: они уходят хосту, который кладёт их в SecretStorage и
// вычищает перед показом в webview.
type MCPImportCandidate struct {
	Name       string          `json:"name"`
	Server     MCPServerUpsert `json:"server"`
	NeedsValue []string        `json:"needsValue,omitempty"`
	Warnings   []string        `json:"warnings,omitempty"`
	Refused    string          `json:"refused,omitempty"`
}

type MCPImportPreview struct {
	Candidates []MCPImportCandidate `json:"candidates"`
}

type mcpImportEntry struct {
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	Cwd     string            `json:"cwd"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

// Заглушка ввода VS Code (${input:id}) и чужого окружения (${env:NAME}):
// значения в файле нет, его вводит владелец.
var mcpPlaceholder = regexp.MustCompile(`\$\{(input|env):[^}]*\}`)

// PreviewMCPImport разбирает файл и объясняет каждое решение.
func (a *App) PreviewMCPImport(raw []byte) (MCPImportPreview, error) {
	if len(raw) > mcpImportMaxBytes {
		return MCPImportPreview{}, fmt.Errorf("файл больше %d КБ — это не конфиг MCP", mcpImportMaxBytes>>10)
	}
	var document struct {
		MCPServers map[string]mcpImportEntry `json:"mcpServers"`
		Servers    map[string]mcpImportEntry `json:"servers"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		return MCPImportPreview{}, errors.New("файл не разобрался как JSON: ожидается объект с mcpServers (Claude, Cursor) или servers (VS Code)")
	}
	entries := document.MCPServers
	if len(entries) == 0 {
		entries = document.Servers
	}
	if len(entries) == 0 {
		return MCPImportPreview{}, errors.New("в файле нет ни mcpServers, ни servers")
	}
	if len(entries) > mcpImportMaxServers {
		return MCPImportPreview{}, fmt.Errorf("в файле больше %d серверов", mcpImportMaxServers)
	}
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	preview := MCPImportPreview{Candidates: make([]MCPImportCandidate, 0, len(names))}
	for _, name := range names {
		preview.Candidates = append(preview.Candidates, importCandidate(name, entries[name]))
	}
	return preview, nil
}

func importCandidate(name string, entry mcpImportEntry) MCPImportCandidate {
	candidate := MCPImportCandidate{Name: clipText(strings.TrimSpace(name), 120)}
	upsert := MCPServerUpsert{DisplayName: candidate.Name, Kind: domain.MCPServerCustom, Secrets: map[string]string{}}
	kind := strings.ToLower(strings.TrimSpace(entry.Type))
	switch {
	case kind == "sse":
		candidate.Refused = "устаревший транспорт HTTP+SSE не поддерживается — укажите адрес Streamable HTTP того же сервера"
		return candidate
	case kind == "http" || kind == "streamable-http" || kind == "streamablehttp" || (kind == "" && entry.URL != "" && entry.Command == ""):
		upsert.Transport = domain.MCPTransportHTTP
		upsert.URL = strings.TrimSpace(entry.URL)
		upsert.Headers = map[string]string{}
		for header, value := range entry.Headers {
			if strings.EqualFold(header, "authorization") || looksSecret(header, value) || mcpPlaceholder.MatchString(value) {
				upsert.SecretHeaders = append(upsert.SecretHeaders, header)
				if mcpPlaceholder.MatchString(value) {
					candidate.NeedsValue = append(candidate.NeedsValue, "header:"+header)
				} else {
					upsert.Secrets["header:"+header] = value
				}
				continue
			}
			upsert.Headers[header] = value
		}
	case kind == "stdio" || kind == "":
		upsert.Transport = domain.MCPTransportStdio
		upsert.Command = strings.TrimSpace(entry.Command)
		upsert.Args = entry.Args
		upsert.Dir = strings.TrimSpace(entry.Cwd)
		upsert.Env = map[string]string{}
		for variable, value := range entry.Env {
			if looksSecret(variable, value) || mcpPlaceholder.MatchString(value) {
				upsert.SecretEnv = append(upsert.SecretEnv, variable)
				if mcpPlaceholder.MatchString(value) {
					candidate.NeedsValue = append(candidate.NeedsValue, "env:"+variable)
				} else {
					upsert.Secrets["env:"+variable] = value
				}
				continue
			}
			upsert.Env[variable] = value
		}
		for _, arg := range upsert.Args {
			if strings.Contains(arg, "@latest") || (strings.HasPrefix(arg, "@") && !strings.Contains(arg[1:], "@")) {
				candidate.Warnings = append(candidate.Warnings, fmt.Sprintf("версия %s не закреплена: npx возьмёт то, что окажется в реестре в день запуска", arg))
			}
			if mcpPlaceholder.MatchString(arg) {
				candidate.Refused = "в аргументах заглушка ввода — Point подставляет секреты только в переменные окружения"
				return candidate
			}
		}
	default:
		candidate.Refused = fmt.Sprintf("неизвестный тип сервера %q", entry.Type)
		return candidate
	}
	sort.Strings(upsert.SecretEnv)
	sort.Strings(upsert.SecretHeaders)
	sort.Strings(candidate.NeedsValue)
	probe := domain.MCPServer{
		ID: "preview", DisplayName: upsert.DisplayName, Kind: upsert.Kind, Transport: upsert.Transport,
		Command: upsert.Command, Args: upsert.Args, Dir: upsert.Dir, Env: trimMap(upsert.Env), URL: upsert.URL,
		Headers: trimMap(upsert.Headers), SecretEnv: secretRefs("preview", "env", upsert.SecretEnv),
		SecretHeaders: secretRefs("preview", "header", upsert.SecretHeaders),
	}
	if err := validateMCPServer(probe); err != nil {
		candidate.Refused = err.Error()
		return candidate
	}
	if upsert.Transport == domain.MCPTransportStdio {
		candidate.Warnings = append(candidate.Warnings, "программа запустится на этой машине вне песочницы — только после «Доверяю»")
	}
	candidate.Server = upsert
	return candidate
}
