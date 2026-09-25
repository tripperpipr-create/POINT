package app

// Запуск MCP-серверов: из чего собирается соединение и как сбой объясняется
// человеку. Учёт серверов (сохранить, доверить, проверить) — mcp_servers.go,
// импорт mcp.json — mcp_import.go.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"local-agent-workbench/internal/dbconn"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/mcpclient"
	"local-agent-workbench/internal/observability"
)

// mcpRuntime — всё, что ядру нужно для MCP во время работы: значения секретов
// (в памяти, как у баз данных) и надзор за процессами. Создаётся по первому
// обращению: ядро, в котором MCP не настроен, не держит ни одного лишнего
// горутина.
type mcpRuntime struct {
	mu         sync.Mutex
	secrets    *dbconn.MemorySecrets
	supervisor *mcpclient.Supervisor
}

func (a *App) mcp() *mcpRuntime {
	a.mcpRuntime.mu.Lock()
	defer a.mcpRuntime.mu.Unlock()
	if a.mcpRuntime.supervisor == nil {
		a.mcpRuntime.secrets = dbconn.NewMemorySecrets()
		a.mcpRuntime.supervisor = mcpclient.NewSupervisor(a.resolveMCPServer, mcpclient.SupervisorOptions{})
	}
	return &a.mcpRuntime
}

// stopMCPServers гасит все серверы при остановке ядра. Если MCP в этом ядре
// не поднимался, делать нечего.
func (a *App) stopMCPServers() {
	a.mcpRuntime.mu.Lock()
	supervisor := a.mcpRuntime.supervisor
	a.mcpRuntime.mu.Unlock()
	if supervisor != nil {
		supervisor.StopAll()
	}
}

// resolveMCPServer — Resolver надзора: хранилище + доверие + секреты.
func (a *App) resolveMCPServer(ctx context.Context, id string) (mcpclient.Config, error) {
	server, err := a.store.GetMCPServer(ctx, id)
	if err != nil {
		return mcpclient.Config{}, &mcpclient.Error{Kind: mcpclient.KindProtocol, Detail: err.Error()}
	}
	values, locked := a.mcpSecretValues(server)
	if len(locked) > 0 {
		return mcpclient.Config{}, &mcpclient.Error{Kind: mcpclient.KindSecretLocked, Detail: strings.Join(locked, ", ")}
	}
	secrets := make([]string, 0, len(values))
	for _, value := range values {
		secrets = append(secrets, value)
	}
	cfg := mcpclient.Config{
		Name: server.DisplayName, Secrets: secrets, ClientVersion: Version,
	}
	switch server.Transport {
	case domain.MCPTransportStdio:
		resolved, lookErr := exec.LookPath(server.Command)
		if lookErr != nil {
			return mcpclient.Config{}, &mcpclient.Error{Kind: mcpclient.KindSpawn, Detail: server.Command + " not found on PATH"}
		}
		if server.TrustDigest == "" || server.TrustDigest != domain.MCPTrustDigest(server, resolved) {
			return mcpclient.Config{}, &mcpclient.Error{Kind: mcpclient.KindNotTrusted, Detail: "command is not trusted"}
		}
		env := map[string]string{}
		for name, value := range server.Env {
			env[name] = value
		}
		for name := range server.SecretEnv {
			env[name] = values["env:"+name]
		}
		cfg.Transport = mcpclient.TransportStdio
		cfg.Command, cfg.Args, cfg.Dir = resolved, server.Args, server.Dir
		cfg.Env = mcpclient.ChildEnvironment(os.Environ(), env)
	case domain.MCPTransportHTTP:
		headers := map[string]string{}
		for name, value := range server.Headers {
			headers[name] = value
		}
		for name := range server.SecretHeaders {
			headers[name] = values["header:"+name]
		}
		cfg.Transport = mcpclient.TransportHTTP
		cfg.URL, cfg.Headers = server.URL, headers
		cfg.DialContext = mcpclient.PinnedDialer(nil, server.AllowPrivateHost)
	default:
		return mcpclient.Config{}, &mcpclient.Error{Kind: mcpclient.KindProtocol, Detail: "unknown transport " + string(server.Transport)}
	}
	return cfg, nil
}

// mcpSecretValues — значения секретов сервера, какие ядро уже получило, и
// имена тех, каких нет. Ключи — "env:NAME" и "header:NAME".
func (a *App) mcpSecretValues(server domain.MCPServer) (map[string]string, []string) {
	store := a.mcp().secrets
	values := map[string]string{}
	var locked []string
	collect := func(prefix string, refs map[string]string) {
		for name, ref := range refs {
			value, ok := store.Get(ref)
			if !ok || value == "" {
				locked = append(locked, name)
				continue
			}
			values[prefix+name] = value
		}
	}
	collect("env:", server.SecretEnv)
	collect("header:", server.SecretHeaders)
	sort.Strings(locked)
	return values, locked
}

// CallMCPTool вызывает инструмент сервера от имени ядра — для экранов
// встроенных плагинов (GitLab). Инструмент обязан быть в последнем снимке
// сервера: вызывать то, чего владелец не видел в списке, нельзя.
func (a *App) CallMCPTool(ctx context.Context, serverID, tool string, arguments any) (mcpclient.CallResult, error) {
	tools, err := a.store.ListMCPTools(ctx, serverID)
	if err != nil {
		return mcpclient.CallResult{}, err
	}
	known := false
	for _, item := range tools {
		if item.Name == tool && item.State != domain.MCPToolMissing {
			known = true
			break
		}
	}
	if !known {
		return mcpclient.CallResult{}, &mcpclient.Error{Kind: mcpclient.KindTool, Detail: "tool " + tool + " is not in the server's tool list"}
	}
	raw, err := json.Marshal(arguments)
	if err != nil {
		return mcpclient.CallResult{}, err
	}
	var result mcpclient.CallResult
	err = a.mcp().supervisor.Call(ctx, serverID, func(client *mcpclient.Client) error {
		var callErr error
		result, callErr = client.CallTool(ctx, tool, raw)
		return callErr
	})
	return result, err
}

// describeMCPFailure переводит сбой MCP в причину и следующий шаг — по роду
// сбоя, а не по тексту: род назначает клиент, и формулировки не зависят от
// того, что написал чужой сервер.
func describeMCPFailure(server domain.MCPServer, err error) (problem, fix string) {
	if err == nil {
		return "", ""
	}
	typed, _ := err.(*mcpclient.Error)
	detail := ""
	if typed != nil {
		detail = typed.Detail
	}
	host := observability.HostOnly(server.URL)
	command := filepath.Base(server.Command)
	switch mcpclient.KindOf(err) {
	case mcpclient.KindNotTrusted:
		return "команда сервера изменилась или ещё не одобрена",
			"посмотрите команду запуска в карточке сервера и нажмите «Доверяю»"
	case mcpclient.KindSecretLocked:
		return fmt.Sprintf("ядро не получило секрет: %s", detail),
			"введите значение в карточке сервера — Point хранит его в SecretStorage и передаёт ядру при запуске"
	case mcpclient.KindSpawn:
		if command == "npx" || command == "npx.cmd" || command == "node" {
			return fmt.Sprintf("программа %s не запустилась", command),
				"установите Node.js 18 или новее и проверьте, что npx доступен в PATH"
		}
		return fmt.Sprintf("программа %s не запустилась", command), "проверьте команду и что программа установлена на этой машине"
	case mcpclient.KindExited:
		return "сервер завершился: " + lastLine(typed), "проверьте адрес и токен; последние строки журнала сервера — в карточке"
	case mcpclient.KindHandshake:
		return "сервер ответил не по протоколу MCP: " + detail, "проверьте, что команда запускает MCP-сервер, и его версию"
	case mcpclient.KindTimeout:
		return "сервер не ответил вовремя",
			"первый запуск через npx скачивает пакет — повторите проверку через минуту; иначе проверьте сеть до сервиса"
	case mcpclient.KindAuth:
		return fmt.Sprintf("сервер %s отказал в доступе", host), "проверьте токен и его права"
	case mcpclient.KindPrivateHostDenied:
		return fmt.Sprintf("адрес %s во внутренней сети", host),
			"если это ваш сервер, разрешите узел в карточке сервера — Point пускает во внутреннюю сеть только названный узел"
	case mcpclient.KindNetwork:
		return fmt.Sprintf("до %s не достучаться", firstNonEmpty(host, server.DisplayName)), "проверьте адрес, VPN и сертификат сервера"
	case mcpclient.KindUnavailable:
		return "сервер падал несколько раз подряд и остановлен", "посмотрите журнал сервера и нажмите «Проверить», чтобы запустить его снова"
	case mcpclient.KindTooLarge:
		return "сервер прислал слишком большой ответ", "сузьте запрос; ответы больше 8 МБ Point не принимает"
	default:
		return "сервер ответил ошибкой: " + detail, "подробности — в журнале сервера"
	}
}

func lastLine(err *mcpclient.Error) string {
	if err == nil {
		return "без объяснения"
	}
	lines := strings.Split(strings.TrimSpace(err.Stderr), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		if line := strings.TrimSpace(lines[index]); line != "" {
			if len(line) > 200 {
				line = line[:200]
			}
			return line
		}
	}
	return err.Detail
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
