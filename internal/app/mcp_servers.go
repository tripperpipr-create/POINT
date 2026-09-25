package app

// MCP-серверы владельца: сохранить, доверить запуск, проверить, включить
// инструменты. Запуск и объяснение сбоев — mcp_runtime.go.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/integrations/gitlab"
	"local-agent-workbench/internal/mcpclient"
	"local-agent-workbench/internal/security"
)

// MCPServerUpsert — форма сервера. Секреты приходят по имени в Secrets и
// живут только в памяти ядра; хост кладёт их в SecretStorage по ссылкам из
// ответа (secretEnv / secretHeaders сервера).
type MCPServerUpsert struct {
	ID               string               `json:"id"`
	DisplayName      string               `json:"displayName"`
	Kind             domain.MCPServerKind `json:"kind"`
	Transport        domain.MCPTransport  `json:"transport"`
	Command          string               `json:"command"`
	Args             []string             `json:"args"`
	Dir              string               `json:"dir"`
	Env              map[string]string    `json:"env"`
	SecretEnv        []string             `json:"secretEnv"`
	URL              string               `json:"url"`
	Headers          map[string]string    `json:"headers"`
	SecretHeaders    []string             `json:"secretHeaders"`
	AllowPrivateHost string               `json:"allowPrivateHost"`
	Settings         map[string]string    `json:"settings"`
	// Secrets — значения по имени: "env:NAME" или "header:NAME". Не хранятся.
	Secrets map[string]string `json:"secrets,omitempty"`
}

// MCPServerView — сервер для экрана «Интеграции».
type MCPServerView struct {
	domain.MCPServer
	Tools []domain.MCPTool `json:"tools"`
	// Trusted — запуск разрешён: для stdio отпечаток доверия совпадает с
	// текущей конфигурацией, удалённому серверу доверие не нужно — на машине
	// владельца он ничего не запускает.
	Trusted bool `json:"trusted"`
	// PendingDigest — отпечаток текущей конфигурации: его владелец видит в
	// окне доверия и его же отправляет назад в «Доверяю».
	PendingDigest   string            `json:"pendingDigest,omitempty"`
	ResolvedPreview string            `json:"resolvedPreview,omitempty"`
	OutsideSandbox  bool              `json:"outsideSandbox"`
	SecretsLocked   []string          `json:"secretsLocked,omitempty"`
	Runtime         mcpclient.Status  `json:"runtime"`
	Problem         string            `json:"problem,omitempty"`
	Fix             string            `json:"fix,omitempty"`
	ErrorKind       mcpclient.Kind    `json:"errorKind,omitempty"`
	SecretRefs      map[string]string `json:"secretRefs,omitempty"`
}

var (
	mcpNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]{0,127}$`)
	// Имя переменной или заголовка, которое само говорит «секрет».
	mcpSecretName = regexp.MustCompile(`(?i)(token|secret|password|passwd|api[_-]?key|private[_-]?key|auth|credential|cookie)`)
	// Обёртки оболочки прячут настоящую команду от окна доверия: владелец
	// одобрил бы «bash», а запустилось бы что угодно из строки аргумента.
	mcpShellWrapper = map[string]bool{"sh": true, "bash": true, "zsh": true, "fish": true, "cmd": true, "cmd.exe": true,
		"powershell": true, "powershell.exe": true, "pwsh": true, "pwsh.exe": true}
)

// SaveMCPServer сохраняет сервер владельца. Сервер встроенного плагина
// собирается из рецепта плагина (SaveGitLabPlugin): форма «любого MCP» его не
// правит, иначе рецепт и экран разошлись бы.
func (a *App) SaveMCPServer(req MCPServerUpsert) (MCPServerView, error) {
	if req.Kind != "" && req.Kind != domain.MCPServerCustom {
		return MCPServerView{}, errors.New("сервер плагина настраивается на карточке плагина")
	}
	if existing, err := a.store.GetMCPServer(context.Background(), strings.TrimSpace(req.ID)); err == nil && existing.Kind != domain.MCPServerCustom {
		return MCPServerView{}, errors.New("сервер плагина настраивается на карточке плагина")
	}
	return a.saveMCPServer(req)
}

// saveMCPServer: любая правка команды, аргументов или переменных меняет
// отпечаток и снимает доверие; сервер гасится, чтобы следующее обращение
// подняло его с новой конфигурацией.
func (a *App) saveMCPServer(req MCPServerUpsert) (MCPServerView, error) {
	ctx := context.Background()
	now := time.Now().UTC()
	server := domain.MCPServer{
		ID: strings.TrimSpace(req.ID), DisplayName: strings.TrimSpace(req.DisplayName), Kind: req.Kind,
		Transport: req.Transport, Command: strings.TrimSpace(req.Command), Args: req.Args, Dir: strings.TrimSpace(req.Dir),
		Env: trimMap(req.Env), URL: strings.TrimSpace(req.URL), Headers: trimMap(req.Headers),
		AllowPrivateHost: strings.ToLower(strings.TrimSpace(req.AllowPrivateHost)), Settings: trimMap(req.Settings),
		Status: domain.ConnectionUnknown, CreatedAt: now, UpdatedAt: now,
	}
	if server.Kind == "" {
		server.Kind = domain.MCPServerCustom
	}
	if server.ID == "" {
		server.ID = domain.NewID("mcp")
	} else if existing, err := a.store.GetMCPServer(ctx, server.ID); err == nil {
		server.CreatedAt = existing.CreatedAt
		server.TrustDigest, server.TrustedAt, server.ResolvedCommand = existing.TrustDigest, existing.TrustedAt, existing.ResolvedCommand
		server.Status, server.LastError, server.LastProbeAt = existing.Status, existing.LastError, existing.LastProbeAt
		server.ServerName, server.ServerVersion, server.ProtocolVersion = existing.ServerName, existing.ServerVersion, existing.ProtocolVersion
	}
	server.SecretEnv = secretRefs(server.ID, "env", req.SecretEnv)
	server.SecretHeaders = secretRefs(server.ID, "header", req.SecretHeaders)
	if server.DisplayName == "" {
		server.DisplayName = firstNonEmpty(server.Command, hostOf(server.URL), "MCP-сервер")
	}
	if err := validateMCPServer(server); err != nil {
		return MCPServerView{}, err
	}
	for key, value := range req.Secrets {
		kind, name, ok := strings.Cut(key, ":")
		refs := map[string]map[string]string{"env": server.SecretEnv, "header": server.SecretHeaders}[kind]
		if ok && refs != nil && refs[name] != "" && value != "" {
			a.mcp().secrets.Put(refs[name], value)
		}
	}
	if err := a.store.SaveMCPServer(ctx, server); err != nil {
		return MCPServerView{}, err
	}
	a.mcp().supervisor.Stop(server.ID)
	return a.mcpServerView(ctx, server, nil), nil
}

func secretRefs(serverID, kind string, names []string) map[string]string {
	if len(names) == 0 {
		return nil
	}
	refs := map[string]string{}
	for _, name := range names {
		if name = strings.TrimSpace(name); name != "" {
			refs[name] = "point.mcp." + serverID + "." + kind + "." + name
		}
	}
	return refs
}

func validateMCPServer(server domain.MCPServer) error {
	if len([]rune(server.DisplayName)) > 120 {
		return errors.New("название сервера длиннее 120 знаков")
	}
	if server.Kind != domain.MCPServerCustom && server.Kind != domain.MCPServerGitLab {
		return fmt.Errorf("неизвестный вид сервера %q", server.Kind)
	}
	for name, value := range server.Env {
		if !mcpNamePattern.MatchString(name) {
			return fmt.Errorf("имя переменной %q недопустимо", name)
		}
		if server.SecretEnv[name] != "" {
			return fmt.Errorf("переменная %s задана и открытым значением, и секретом", name)
		}
		if looksSecret(name, value) {
			return fmt.Errorf("значение %s похоже на секрет — перенесите его в секреты сервера: открытые значения хранятся в базе Point", name)
		}
	}
	for name := range server.SecretEnv {
		if !mcpNamePattern.MatchString(name) {
			return fmt.Errorf("имя секрета %q недопустимо", name)
		}
	}
	switch server.Transport {
	case domain.MCPTransportStdio:
		if server.Command == "" {
			return errors.New("укажите команду запуска сервера")
		}
		if mcpShellWrapper[strings.ToLower(baseName(server.Command))] {
			return errors.New("команда через оболочку (sh -c, cmd /c, powershell) не принимается: окно доверия должно показывать настоящую программу")
		}
		if len(server.Args) > 64 {
			return errors.New("у сервера больше 64 аргументов")
		}
		for _, arg := range server.Args {
			if len(arg) > 4096 || looksSecret("", arg) {
				return fmt.Errorf("аргумент %q слишком длинный или похож на секрет — передайте секрет переменной окружения", clipText(arg, 40))
			}
		}
		if server.URL != "" || len(server.Headers) > 0 || len(server.SecretHeaders) > 0 {
			return errors.New("у сервера stdio не бывает адреса и заголовков")
		}
	case domain.MCPTransportHTTP:
		parsed, err := url.Parse(server.URL)
		if err != nil || parsed.Host == "" {
			return errors.New("укажите полный адрес сервера, например https://gitlab.company.local/api/v4/mcp")
		}
		if parsed.User != nil {
			return errors.New("учётные данные в адресе не принимаются — передайте токен секретным заголовком")
		}
		if parsed.Scheme != "https" && !(parsed.Scheme == "http" && isLoopbackName(parsed.Hostname())) {
			return errors.New("удалённый сервер принимается только по https")
		}
		if server.AllowPrivateHost != "" && server.AllowPrivateHost != strings.ToLower(parsed.Hostname()) {
			return fmt.Errorf("разрешить внутреннюю сеть можно только узлу из адреса (%s)", parsed.Hostname())
		}
		for name, value := range server.Headers {
			if !mcpNamePattern.MatchString(name) {
				return fmt.Errorf("имя заголовка %q недопустимо", name)
			}
			if looksSecret(name, value) {
				return fmt.Errorf("заголовок %s похож на секрет — перенесите его в секреты сервера", name)
			}
		}
		if server.Command != "" || len(server.Args) > 0 || len(server.Env) > 0 || len(server.SecretEnv) > 0 {
			return errors.New("у удалённого сервера нет команды и переменных окружения")
		}
	default:
		return fmt.Errorf("неизвестный транспорт %q", server.Transport)
	}
	return nil
}

func looksSecret(name, value string) bool {
	if strings.TrimSpace(value) == "" {
		return false
	}
	if name != "" && mcpSecretName.MatchString(name) {
		return true
	}
	return security.Redact(value) != value
}

// ListMCPServers — все серверы с их инструментами и состоянием.
func (a *App) ListMCPServers() ([]MCPServerView, error) {
	ctx := context.Background()
	servers, err := a.store.ListMCPServers(ctx)
	if err != nil {
		return nil, err
	}
	views := make([]MCPServerView, 0, len(servers))
	for _, server := range servers {
		views = append(views, a.mcpServerView(ctx, server, nil))
	}
	return views, nil
}

// TrustMCPServer — «Доверяю» владельца. Отпечаток обязан совпасть с тем,
// который владелец видел: конфигурация, изменённая между показом и нажатием,
// одобренной не считается.
func (a *App) TrustMCPServer(id, digest string) (MCPServerView, error) {
	ctx := context.Background()
	server, err := a.store.GetMCPServer(ctx, id)
	if err != nil {
		return MCPServerView{}, err
	}
	if server.Transport != domain.MCPTransportStdio {
		return a.mcpServerView(ctx, server, nil), nil
	}
	resolved, err := exec.LookPath(server.Command)
	if err != nil {
		return MCPServerView{}, fmt.Errorf("программа %s не найдена на этой машине", server.Command)
	}
	current := domain.MCPTrustDigest(server, resolved)
	if strings.TrimSpace(digest) != current {
		return MCPServerView{}, errors.New("конфигурация сервера изменилась после показа — посмотрите её заново")
	}
	now := time.Now().UTC()
	server.TrustDigest, server.TrustedAt, server.ResolvedCommand, server.UpdatedAt = current, &now, resolved, now
	if err = a.store.SaveMCPServer(ctx, server); err != nil {
		return MCPServerView{}, err
	}
	a.mcp().supervisor.Reset(id)
	return a.mcpServerView(ctx, server, nil), nil
}

// ProbeMCPServer — «Проверить»: сервер поднимается заново, список
// инструментов сверяется со снимком. Несостоявшаяся связь — состояние
// сервера (status=error, problem, fix), а не ошибка запроса.
func (a *App) ProbeMCPServer(ctx context.Context, id string) (MCPServerView, error) {
	server, err := a.store.GetMCPServer(ctx, id)
	if err != nil {
		return MCPServerView{}, err
	}
	info, tools, probeErr := a.mcp().supervisor.Probe(ctx, id)
	now := time.Now().UTC()
	server.LastProbeAt, server.UpdatedAt = &now, now
	if probeErr != nil {
		problem, fix := describeMCPFailure(server, probeErr)
		server.Status = domain.ConnectionError
		server.LastError = security.Redact(problem + " — " + fix)
	} else {
		server.Status, server.LastError = domain.ConnectionConnected, ""
		server.ServerName, server.ServerVersion, server.ProtocolVersion = info.Name, info.Version, info.ProtocolVersion
		if err = a.mergeMCPTools(ctx, server, tools); err != nil {
			return MCPServerView{}, err
		}
	}
	if err = a.store.SaveMCPServer(ctx, server); err != nil {
		return MCPServerView{}, err
	}
	return a.mcpServerView(ctx, server, probeErr), nil
}

// mergeMCPTools сверяет свежий список со снимком: новые приходят
// выключенными, изменившиеся после одобрения — выключаются, пропавшие
// остаются в списке с пометкой.
func (a *App) mergeMCPTools(ctx context.Context, server domain.MCPServer, fresh []mcpclient.Tool) error {
	previous, err := a.store.ListMCPTools(ctx, server.ID)
	if err != nil {
		return err
	}
	old := map[string]domain.MCPTool{}
	for _, tool := range previous {
		old[tool.Name] = tool
	}
	now := time.Now().UTC()
	seen := map[string]bool{}
	merged := make([]domain.MCPTool, 0, len(fresh))
	for _, tool := range fresh {
		if tool.Name == "" || seen[tool.Name] {
			continue
		}
		seen[tool.Name] = true
		var annotations json.RawMessage
		if tool.Annotations != nil {
			annotations, _ = json.Marshal(tool.Annotations)
		}
		item := domain.MCPTool{
			ServerID: server.ID, Name: clipText(tool.Name, 128), Title: clipText(tool.Title, 200),
			Description: clipText(tool.Description, 8<<10), InputSchema: capJSON(tool.InputSchema, 64<<10),
			Annotations: annotations, UpdatedAt: now,
		}
		item.Digest = domain.MCPToolDigest(item.Name, item.Title, item.Description, item.InputSchema, item.Annotations)
		prior, known := old[tool.Name]
		switch {
		case !known:
			item.State, item.Risk = domain.MCPToolNew, defaultMCPToolRisk(tool.Annotations)
			if risk, ok := gitlab.PresetRisk(item.Name); ok && server.Kind == domain.MCPServerGitLab {
				item.Risk = risk
			}
		case prior.ApprovedDigest != "" && prior.ApprovedDigest != item.Digest:
			item.State, item.Risk, item.ApprovedDigest = domain.MCPToolChanged, prior.Risk, prior.ApprovedDigest
		case prior.ApprovedDigest != "":
			item.State, item.Risk, item.ApprovedDigest, item.Enabled = domain.MCPToolOK, prior.Risk, prior.ApprovedDigest, prior.Enabled
		default:
			item.State, item.Risk = domain.MCPToolNew, prior.Risk
		}
		merged = append(merged, item)
	}
	for _, prior := range previous {
		if !seen[prior.Name] {
			prior.State, prior.Enabled, prior.UpdatedAt = domain.MCPToolMissing, false, now
			merged = append(merged, prior)
		}
	}
	return a.store.ReplaceMCPTools(ctx, server.ID, merged)
}

// defaultMCPToolRisk — риск по подсказкам сервера. Подсказки недоверенные:
// они только предзаполняют выбор, а без них инструмент считается записью.
func defaultMCPToolRisk(annotations *mcpclient.Annotations) domain.ToolRisk {
	if annotations != nil {
		if annotations.DestructiveHint != nil && *annotations.DestructiveHint {
			return domain.ToolRiskCritical
		}
		if annotations.ReadOnlyHint != nil && *annotations.ReadOnlyHint {
			return domain.ToolRiskLow
		}
	}
	return domain.ToolRiskHigh
}

// SetMCPTool включает или выключает инструмент и меняет его риск. Включение —
// это одобрение: текущий отпечаток становится одобренным.
func (a *App) SetMCPTool(serverID, name string, enabled bool, risk domain.ToolRisk) (MCPServerView, error) {
	ctx := context.Background()
	server, err := a.store.GetMCPServer(ctx, serverID)
	if err != nil {
		return MCPServerView{}, err
	}
	tools, err := a.store.ListMCPTools(ctx, serverID)
	if err != nil {
		return MCPServerView{}, err
	}
	found := false
	for index := range tools {
		tool := &tools[index]
		if tool.Name != name {
			continue
		}
		found = true
		if tool.State == domain.MCPToolMissing && enabled {
			return MCPServerView{}, errors.New("сервер больше не отдаёт этот инструмент")
		}
		switch risk {
		case "":
		case domain.ToolRiskLow, domain.ToolRiskMedium, domain.ToolRiskHigh, domain.ToolRiskCritical:
			tool.Risk = risk
		default:
			return MCPServerView{}, fmt.Errorf("неизвестный риск %q", risk)
		}
		tool.Enabled = enabled
		if enabled {
			tool.ApprovedDigest, tool.State = tool.Digest, domain.MCPToolOK
		}
		tool.UpdatedAt = time.Now().UTC()
	}
	if !found {
		return MCPServerView{}, fmt.Errorf("у сервера нет инструмента %s", name)
	}
	if err = a.store.ReplaceMCPTools(ctx, serverID, tools); err != nil {
		return MCPServerView{}, err
	}
	return a.mcpServerView(ctx, server, nil), nil
}

// StopMCPServer гасит процесс сервера. Следующее обращение поднимет его.
func (a *App) StopMCPServer(id string) error {
	if _, err := a.store.GetMCPServer(context.Background(), id); err != nil {
		return err
	}
	a.mcp().supervisor.Stop(id)
	return nil
}

// DeleteMCPServer удаляет сервер, его снимок и секреты из памяти ядра.
func (a *App) DeleteMCPServer(id string) error {
	ctx := context.Background()
	server, err := a.store.GetMCPServer(ctx, id)
	if err != nil {
		return err
	}
	a.mcp().supervisor.Forget(id)
	if err = a.store.DeleteMCPServer(ctx, id); err != nil {
		return err
	}
	for _, ref := range server.SecretEnv {
		a.mcp().secrets.Delete(ref)
	}
	for _, ref := range server.SecretHeaders {
		a.mcp().secrets.Delete(ref)
	}
	return nil
}

// MCPServerLog — зачищенный журнал сервера.
func (a *App) MCPServerLog(id string) (string, error) {
	if _, err := a.store.GetMCPServer(context.Background(), id); err != nil {
		return "", err
	}
	return a.mcp().supervisor.Log(id), nil
}

// UnlockMCPSecrets — хост передаёт значения из SecretStorage (ref → value).
// Ссылка принимается только своего вида: чужой secretRef из других связей
// через эту дверь в память MCP не попадёт.
func (a *App) UnlockMCPSecrets(values map[string]string) error {
	for ref, value := range values {
		if !strings.HasPrefix(ref, "point.mcp.") {
			return fmt.Errorf("ссылка %q не принадлежит MCP-серверу", ref)
		}
		a.mcp().secrets.Put(ref, value)
	}
	return nil
}

func (a *App) mcpServerView(ctx context.Context, server domain.MCPServer, lastErr error) MCPServerView {
	view := MCPServerView{MCPServer: server, Trusted: true}
	view.Tools, _ = a.store.ListMCPTools(ctx, server.ID)
	if view.Tools == nil {
		view.Tools = []domain.MCPTool{}
	}
	if server.Transport == domain.MCPTransportStdio {
		view.OutsideSandbox = true
		if resolved, err := exec.LookPath(server.Command); err == nil {
			view.ResolvedPreview = resolved
			view.PendingDigest = domain.MCPTrustDigest(server, resolved)
			view.Trusted = server.TrustDigest != "" && server.TrustDigest == view.PendingDigest
		} else {
			view.Trusted = false
		}
	}
	_, view.SecretsLocked = a.mcpSecretValues(server)
	view.Runtime = a.mcp().supervisor.Status(server.ID)
	if lastErr == nil {
		lastErr = view.Runtime.LastError
	}
	if lastErr != nil {
		view.ErrorKind = mcpclient.KindOf(lastErr)
		view.Problem, view.Fix = describeMCPFailure(server, lastErr)
	}
	view.SecretRefs = map[string]string{}
	for name, ref := range server.SecretEnv {
		view.SecretRefs["env:"+name] = ref
	}
	for name, ref := range server.SecretHeaders {
		view.SecretRefs["header:"+name] = ref
	}
	return view
}

func trimMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := map[string]string{}
	for key, value := range values {
		if key = strings.TrimSpace(key); key != "" {
			out[key] = value
		}
	}
	return out
}

func capJSON(raw json.RawMessage, limit int) json.RawMessage {
	if len(raw) > limit {
		return json.RawMessage(`{"type":"object","description":"schema larger than Point keeps"}`)
	}
	return raw
}

func clipText(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit])
}

func hostOf(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

func baseName(command string) string {
	command = strings.ReplaceAll(command, "\\", "/")
	if index := strings.LastIndex(command, "/"); index >= 0 {
		return command[index+1:]
	}
	return command
}

func isLoopbackName(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	return host == "127.0.0.1" || host == "::1"
}
