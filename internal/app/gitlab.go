package app

// Плагин GitLab: подключение по рецепту, привязка папки к проекту и
// состояние окна. Экраны и действия владельца — gitlab_screens.go. Данные
// идут через MCP-сервер плагина (integrations/gitlab); модель здесь не
// участвует.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/integrations/gitlab"
	"local-agent-workbench/internal/mcpclient"
	"local-agent-workbench/internal/security"
)

// gitlabServerID — у плагина один сервер. Постоянный id даёт постоянную
// ссылку на токен в SecretStorage: point.mcp.mcp-gitlab.env.GITLAB_PERSONAL_ACCESS_TOKEN.
const gitlabServerID = "mcp-gitlab"

// gitlabRecipe — рецепт запуска; тест подменяет его поддельным сервером.
var gitlabRecipe = gitlab.Recipe

// GitLabReason — почему экран GitLab пуст.
type GitLabReason string

const (
	GitLabNotConfigured GitLabReason = "not_configured"
	GitLabNotTrusted    GitLabReason = "not_trusted"
	GitLabSecretLocked  GitLabReason = "secret_locked"
	GitLabToolMissing   GitLabReason = "tool_missing"
	GitLabUnreachable   GitLabReason = "unreachable"
	GitLabAuth          GitLabReason = "auth"
	GitLabNotFound      GitLabReason = "not_found"
	// GitLabRefused — GitLab выполнил запрос и отказал (MR не сливается,
	// голова ушла вперёд, нет прав на действие).
	GitLabRefused    GitLabReason = "refused"
	GitLabFormat     GitLabReason = "format"
	GitLabNoProject  GitLabReason = "no_project"
	GitLabBadRequest GitLabReason = "bad_request"
	// GitLabNotLinked — проект папки с GitLab не связан. Это не сбой, а выбор:
	// окно отвечает спокойно, и сервер плагина ради него не запускается.
	GitLabNotLinked GitLabReason = "not_linked"
)

// GitLabResponse — ответ каждого экрана GitLab. Несостоявшийся экран — это
// состояние с причиной и следующим шагом, а не ошибка запроса: окно рисует
// его так же, как данные.
type GitLabResponse struct {
	State   string       `json:"state"`
	Reason  GitLabReason `json:"reason,omitempty"`
	Problem string       `json:"problem,omitempty"`
	Fix     string       `json:"fix,omitempty"`
	Data    any          `json:"data,omitempty"`
}

type gitlabFailure struct {
	reason       GitLabReason
	problem, fix string
}

func (f *gitlabFailure) Error() string { return string(f.reason) + ": " + f.problem }

func gitlabBadRequest(format string, args ...any) error {
	return &gitlabFailure{reason: GitLabBadRequest, problem: fmt.Sprintf(format, args...)}
}

func (a *App) gitlabRespond(server domain.MCPServer, data any, err error) GitLabResponse {
	if err == nil {
		return GitLabResponse{State: "ok", Data: data}
	}
	failure := explainGitLab(server, err)
	return GitLabResponse{State: "error", Reason: failure.reason, Problem: security.Redact(failure.problem), Fix: failure.fix, Data: data}
}

// explainGitLab переводит сбой в причину экрана. Сбой MCP объясняется по
// роду (describeMCPFailure), отказ GitLab — по причине адаптера.
func explainGitLab(server domain.MCPServer, err error) *gitlabFailure {
	var failure *gitlabFailure
	if errors.As(err, &failure) {
		return failure
	}
	pinned := fmt.Sprintf("Point рассчитан на %s@%s — нажмите «Проверить» в карточке GitLab (Общие настройки → Интеграции и MCP)", gitlab.ServerPackage, gitlab.ServerVersion)
	var adapter *gitlab.Error
	if errors.As(err, &adapter) {
		switch adapter.Reason {
		case gitlab.ReasonToolMissing:
			return &gitlabFailure{GitLabToolMissing, "сервер GitLab не отдаёт инструмент " + adapter.Tool, pinned}
		case gitlab.ReasonAuth:
			return &gitlabFailure{GitLabAuth, "GitLab отказал в доступе: " + adapter.Detail,
				"проверьте, что токен не истёк и у него есть право api (для просмотра хватает read_api)"}
		case gitlab.ReasonNotFound:
			return &gitlabFailure{GitLabNotFound, "GitLab не нашёл: " + adapter.Detail, "проверьте путь проекта и что у токена есть к нему доступ"}
		case gitlab.ReasonFormat:
			fix := "проверьте журнал MCP-сервера GitLab; ответ инструмента не похож на ожидаемый JSON"
			if server.ServerVersion != "" && server.ServerVersion != gitlab.ServerVersion {
				fix = pinned
			}
			return &gitlabFailure{GitLabFormat, "ответ инструмента " + adapter.Tool + " сервера GitLab не разобрался: " + adapter.Detail, fix}
		default:
			return &gitlabFailure{GitLabRefused, "GitLab отказал: " + adapter.Detail, "подробности — в журнале сервера GitLab (Общие настройки → Интеграции и MCP)"}
		}
	}
	kind := mcpclient.KindOf(err)
	problem, fix := describeMCPFailure(server, err)
	switch kind {
	case mcpclient.KindNotTrusted:
		return &gitlabFailure{GitLabNotTrusted, "запуск сервера GitLab не одобрен", "Общие настройки → Интеграции и MCP → GitLab: посмотрите команду и нажмите «Доверяю»"}
	case mcpclient.KindSecretLocked:
		return &gitlabFailure{GitLabSecretLocked, "ядро не получило токен GitLab", "введите токен в карточке GitLab (Общие настройки → Интеграции и MCP)"}
	case mcpclient.KindTool:
		return &gitlabFailure{GitLabRefused, "сервер GitLab отказал: " + problem, "подробности — в журнале сервера GitLab (Общие настройки → Интеграции и MCP)"}
	case "":
		return &gitlabFailure{GitLabRefused, err.Error(), ""}
	default:
		return &gitlabFailure{gitlabReasonForKind(kind), problem, fix}
	}
}

func gitlabReasonForKind(kind mcpclient.Kind) GitLabReason {
	switch kind {
	case mcpclient.KindNotTrusted:
		return GitLabNotTrusted
	case mcpclient.KindSecretLocked:
		return GitLabSecretLocked
	case mcpclient.KindAuth:
		return GitLabAuth
	default:
		return GitLabUnreachable
	}
}

// GitLabPluginUpsert — карточка плагина. Token — новое значение или пусто,
// если токен не меняется (он уже в SecretStorage и в памяти ядра).
type GitLabPluginUpsert struct {
	URL    string `json:"url"`
	CAPath string `json:"caPath"`
	Token  string `json:"token,omitempty"`
}

// SaveGitLabPlugin собирает сервер плагина из рецепта. Смена адреса или
// сертификата меняет команду запуска, и доверие нужно заново.
func (a *App) SaveGitLabPlugin(req GitLabPluginUpsert) (MCPServerView, error) {
	settings := gitlab.Settings{URL: strings.TrimSpace(req.URL), CAPath: strings.TrimSpace(req.CAPath)}
	if settings.CAPath != "" && !filepath.IsAbs(settings.CAPath) {
		return MCPServerView{}, errors.New("укажите полный путь к файлу сертификата")
	}
	launch, err := gitlabRecipe(settings)
	if err != nil {
		return MCPServerView{}, err
	}
	base, err := gitlab.BaseURL(settings.URL)
	if err != nil {
		return MCPServerView{}, err
	}
	upsert := MCPServerUpsert{
		ID: gitlabServerID, DisplayName: "GitLab", Kind: domain.MCPServerGitLab, Transport: domain.MCPTransportStdio,
		Command: launch.Command, Args: launch.Args, Env: launch.Env, SecretEnv: launch.SecretEnv,
		Settings: map[string]string{"url": base, "caPath": settings.CAPath},
	}
	if token := strings.TrimSpace(req.Token); token != "" {
		upsert.Secrets = map[string]string{"env:" + gitlab.TokenVariable: token}
	}
	view, err := a.saveMCPServer(upsert)
	if err == nil {
		a.forgetGitLabUser()
	}
	return view, err
}

// mcpToolCaller — Caller адаптера поверх вызова инструмента ядром.
type mcpToolCaller struct {
	app      *App
	serverID string
}

func (c mcpToolCaller) CallTool(ctx context.Context, tool string, arguments any) (mcpclient.CallResult, error) {
	return c.app.CallMCPTool(ctx, c.serverID, tool, arguments)
}

type gitlabSession struct {
	server domain.MCPServer
	client *gitlab.Client
}

// gitlabSession проверяет, что плагин готов, и собирает адаптер по
// последнему снимку инструментов. Первый снимок снимается здесь же.
func (a *App) gitlabSession(ctx context.Context) (gitlabSession, error) {
	server, err := a.store.GetMCPServer(ctx, gitlabServerID)
	if err != nil {
		return gitlabSession{}, &gitlabFailure{GitLabNotConfigured, "GitLab не подключён",
			"Общие настройки → Интеграции и MCP → GitLab: адрес сервера и личный токен"}
	}
	// Once migrated, old routes use the same connection transport as the workbench.
	if _, settingErr := a.store.Setting(ctx, forgeSettingsKey); settingErr == nil {
		p, connection, err := a.forgeProvider(ctx, "gitlab-legacy")
		if err != nil {
			return gitlabSession{server: server}, err
		}
		if rest, ok := p.(*gitlab.RESTClient); ok {
			server.Settings["url"] = connection.URL
			return gitlabSession{server: server, client: rest.LegacyClient()}, nil
		}
	}
	server = a.gitlabRefreshRecipe(ctx, server)
	view := a.mcpServerView(ctx, server, nil)
	if !view.Trusted && server.TrustDigest != "" {
		return gitlabSession{server: server}, &gitlabFailure{GitLabNotTrusted, "Point обновил команду запуска сервера GitLab",
			"Общие настройки → Интеграции и MCP → GitLab: посмотрите новую команду и нажмите «Доверяю»"}
	}
	if !view.Trusted {
		return gitlabSession{server: server}, &gitlabFailure{GitLabNotTrusted, "запуск сервера GitLab не одобрен",
			"Общие настройки → Интеграции и MCP → GitLab: посмотрите команду и нажмите «Доверяю»"}
	}
	if len(view.SecretsLocked) > 0 {
		return gitlabSession{server: server}, &gitlabFailure{GitLabSecretLocked, "ядро не получило токен GitLab",
			"введите токен в карточке GitLab (Общие настройки → Интеграции и MCP)"}
	}
	tools := liveMCPTools(view.Tools)
	if len(tools) == 0 {
		probed, probeErr := a.ProbeMCPServer(ctx, server.ID)
		if probeErr != nil {
			return gitlabSession{server: server}, probeErr
		}
		server = probed.MCPServer
		if probed.ErrorKind != "" {
			return gitlabSession{server: server}, &gitlabFailure{gitlabReasonForKind(probed.ErrorKind), probed.Problem, probed.Fix}
		}
		tools = liveMCPTools(probed.Tools)
	}
	return gitlabSession{server: server, client: gitlab.NewClient(mcpToolCaller{a, server.ID}, server.Settings["url"], tools)}, nil
}

// gitlabRefreshRecipe сверяет сохранённую команду сервера с рецептом этой
// версии Point. Рецепт — код (gitlab.Recipe), а запись в базе — его снимок на
// момент сохранения карточки: сервер, сохранённый до раздела «Проекты», так и
// запускался без list_projects в GITLAB_TOOLS, и «Проверить» перечитывал тот
// же урезанный список. Расхождение переписывает запись рецептом; команда
// запуска при этом новая, и доверие к ней владелец даёт заново.
func (a *App) gitlabRefreshRecipe(ctx context.Context, server domain.MCPServer) domain.MCPServer {
	if server.Transport != domain.MCPTransportStdio {
		return server
	}
	launch, err := gitlabRecipe(gitlab.Settings{URL: server.Settings["url"], CAPath: server.Settings["caPath"]})
	if err != nil || server.Command == launch.Command && slices.Equal(server.Args, launch.Args) && maps.Equal(server.Env, launch.Env) {
		return server
	}
	view, err := a.saveMCPServer(MCPServerUpsert{
		ID: server.ID, DisplayName: server.DisplayName, Kind: server.Kind, Transport: server.Transport,
		Command: launch.Command, Args: launch.Args, Env: launch.Env, SecretEnv: launch.SecretEnv, Settings: server.Settings,
	})
	if err != nil {
		slog.Warn("gitlab recipe refresh failed", "error", security.Redact(err.Error()))
		return server
	}
	if refreshed, getErr := a.store.GetMCPServer(ctx, server.ID); getErr == nil {
		return refreshed
	}
	return view.MCPServer
}

func liveMCPTools(tools []domain.MCPTool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		if tool.State != domain.MCPToolMissing {
			names = append(names, tool.Name)
		}
	}
	return names
}

// gitlabUser — владелец токена. Кэш сбрасывается сохранением плагина.
func (a *App) gitlabUser(ctx context.Context, session gitlabSession) (gitlab.User, error) {
	runtime := a.mcp()
	runtime.mu.Lock()
	user, ok := runtime.gitlabUsers[session.server.ID]
	runtime.mu.Unlock()
	if ok {
		return user, nil
	}
	user, err := session.client.WhoAmI(ctx)
	if err != nil {
		return gitlab.User{}, err
	}
	runtime.mu.Lock()
	if runtime.gitlabUsers == nil {
		runtime.gitlabUsers = map[string]gitlab.User{}
	}
	runtime.gitlabUsers[session.server.ID] = user
	runtime.mu.Unlock()
	return user, nil
}

func (a *App) forgetGitLabUser() {
	runtime := a.mcp()
	runtime.mu.Lock()
	delete(runtime.gitlabUsers, gitlabServerID)
	runtime.mu.Unlock()
}

// GitLabBindingView — какой проект показывает окно и почему.
type GitLabBindingView struct {
	Mode domain.GitLabBindMode `json:"mode"`
	// Project — действующий проект; пусто в режиме «все мои проекты» и когда
	// проект не нашёлся.
	Project   string `json:"project,omitempty"`
	Manual    string `json:"manual,omitempty"`
	Detected  string `json:"detected,omitempty"`
	Remote    string `json:"remote,omitempty"`
	Branch    string `json:"branch,omitempty"`
	Username  string `json:"username,omitempty"`
	Workspace string `json:"workspace,omitempty"`
	Note      string `json:"note,omitempty"`
}

// linked — окну есть что показать: проект найден или выбраны «все мои
// проекты». Сохранённый auto, чей origin больше не ведёт на этот GitLab,
// тоже не связан — причину называет Note.
func (v GitLabBindingView) linked() bool {
	return v.Mode == domain.GitLabBindAll || (v.Mode != domain.GitLabBindOff && v.Project != "")
}

// gitlabBinding: проект — по git remote origin открытой папки, если
// владелец не назвал его сам. Ветка — текущая ветка папки. Пока владелец
// не выбрал ничего, проект связан, только если origin ведёт на подключённый
// GitLab: не каждый проект обязан жить в нём.
func (a *App) gitlabBinding(ctx context.Context, server domain.MCPServer) GitLabBindingView {
	view := GitLabBindingView{}
	workspace, err := a.requireWorkspace()
	if err != nil {
		view.Mode, view.Note = domain.GitLabBindAll, "папка не открыта — показаны MR по всем вашим проектам"
		return view
	}
	view.Workspace = workspace.Name
	if stored, getErr := a.store.GetGitLabBinding(ctx, workspace.ID); getErr == nil {
		view.Mode, view.Manual, view.Username = stored.Mode, stored.ProjectPath, stored.Username
	}
	runner := a.gitRunner
	if runner == nil {
		runner = execGitRunner{}
	}
	gitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if out, runErr := runner.Run(gitCtx, workspace.Path, "remote", "get-url", "origin"); runErr != nil {
		view.Note = "у папки нет git remote origin"
	} else if remote, parseErr := gitlab.ParseRemote(string(out)); parseErr != nil {
		view.Note = parseErr.Error()
	} else {
		view.Remote = remote.Host + "/" + remote.Path
		if project, ok := gitlab.ProjectFor(remote, server.Settings["url"]); ok {
			view.Detected = project
		} else {
			view.Note = fmt.Sprintf("origin ведёт на %s, а плагин подключён к %s", remote.Host, firstNonEmpty(hostOf(server.Settings["url"]), "другому GitLab"))
		}
	}
	if out, runErr := runner.Run(gitCtx, workspace.Path, "rev-parse", "--abbrev-ref", "HEAD"); runErr == nil {
		if branch := strings.TrimSpace(string(out)); branch != "HEAD" && gitlabRefPattern.MatchString(branch) {
			view.Branch = branch
		}
	}
	// Выбор владельца сильнее git remote; без выбора связь даёт только origin,
	// ведущий на этот GitLab.
	switch {
	case view.Mode == domain.GitLabBindOff:
		view.Note = "связь с GitLab отключена в настройках проекта"
	case view.Mode == "" && view.Detected != "":
		view.Mode = domain.GitLabBindAuto
	case view.Mode == "":
		view.Mode = domain.GitLabBindOff
	}
	switch view.Mode {
	case domain.GitLabBindManual:
		view.Project = view.Manual
	case domain.GitLabBindAuto:
		view.Project = view.Detected
	}
	return view
}

var (
	gitlabProjectPattern  = regexp.MustCompile(`^(?:[0-9]+|[A-Za-z0-9_.][A-Za-z0-9_.-]*(?:/[A-Za-z0-9_.][A-Za-z0-9_.-]*)+)$`)
	gitlabRefPattern      = regexp.MustCompile(`^[A-Za-z0-9_.][A-Za-z0-9_./+@-]{0,254}$`)
	gitlabUsernamePattern = regexp.MustCompile(`^[A-Za-z0-9_.][A-Za-z0-9_.-]{0,254}$`)
	gitlabSHAPattern      = regexp.MustCompile(`^[0-9a-f]{7,64}$`)
	// id нити обсуждения GitLab — 40 шестнадцатеричных знаков.
	gitlabDiscussionPattern = regexp.MustCompile(`^[A-Za-z0-9]{1,64}$`)
)

// GitLabBindingUpsert — выбор проекта для открытой папки.
type GitLabBindingUpsert struct {
	Mode     domain.GitLabBindMode `json:"mode"`
	Project  string                `json:"project"`
	Username string                `json:"username"`
}

func (a *App) SaveGitLabBinding(ctx context.Context, req GitLabBindingUpsert) GitLabResponse {
	server, _ := a.store.GetMCPServer(ctx, gitlabServerID)
	workspace, err := a.requireWorkspace()
	if err != nil {
		return a.gitlabRespond(server, nil, gitlabBadRequest("откройте папку проекта — привязка хранится для папки"))
	}
	binding := domain.GitLabBinding{WorkspaceID: workspace.ID, ServerID: gitlabServerID, Mode: req.Mode,
		Username: strings.TrimSpace(req.Username), UpdatedAt: time.Now().UTC()}
	switch req.Mode {
	case domain.GitLabBindManual:
		binding.ProjectPath = strings.Trim(strings.TrimSpace(req.Project), "/")
		if !validGitLabProject(binding.ProjectPath) {
			return a.gitlabRespond(server, nil, gitlabBadRequest("путь проекта %q не похож на путь GitLab (группа/проект)", clipText(binding.ProjectPath, 80)))
		}
	case domain.GitLabBindAuto, domain.GitLabBindAll, domain.GitLabBindOff:
	default:
		return a.gitlabRespond(server, nil, gitlabBadRequest("неизвестный режим привязки %q", clipText(string(req.Mode), 20)))
	}
	if binding.Username != "" && !gitlabUsernamePattern.MatchString(binding.Username) {
		return a.gitlabRespond(server, nil, gitlabBadRequest("имя пользователя GitLab %q недопустимо", clipText(binding.Username, 80)))
	}
	if err = a.store.SaveGitLabBinding(ctx, binding); err != nil {
		return a.gitlabRespond(server, nil, err)
	}
	return a.gitlabRespond(server, a.gitlabBinding(ctx, server), nil)
}

func validGitLabProject(path string) bool {
	return len(path) <= 255 && gitlabProjectPattern.MatchString(path) && !strings.Contains(path, "..")
}

// GitLabStatusView — шапка окна: куда подключён плагин, кто владелец токена,
// какой проект и что умеет сервер. Linked — связан ли с GitLab проект папки.
type GitLabStatusView struct {
	ServerID      string                               `json:"serverId"`
	Configured    bool                                 `json:"configured"`
	URL           string                               `json:"url,omitempty"`
	Pinned        string                               `json:"pinned"`
	ServerVersion string                               `json:"serverVersion,omitempty"`
	User          *gitlab.User                         `json:"user,omitempty"`
	Binding       GitLabBindingView                    `json:"binding"`
	Linked        bool                                 `json:"linked"`
	Capabilities  map[gitlab.Feature]gitlab.Capability `json:"capabilities,omitempty"`
}

// Чей статус спрашивают: окна проекта (по умолчанию), карточки плагина в
// общих настройках (здоровье сервера в любом проекте) или общего окна GitLab,
// открытого из общих настроек: оно смотрит на все проекты владельца, какая бы
// папка ни была открыта.
const (
	GitLabStatusProject = "project"
	GitLabStatusPlugin  = "plugin"
	GitLabStatusAll     = "all"
)

// gitlabAllBinding — привязка общего окна: все проекты, без папки.
func gitlabAllBinding() GitLabBindingView {
	return GitLabBindingView{Mode: domain.GitLabBindAll, Note: "общее окно GitLab — все ваши проекты, без привязки к папке"}
}

// GitLabStatus — состояние окна. Даже при сбое в data остаются адрес и
// привязка: окно показывает, что подключено, и кнопку следующего шага.
// Проекту, не связанному с GitLab, сервер плагина не нужен: ответ приходит
// сразу, без запуска npx и whoami — и без таймаута, если GitLab за VPN.
func (a *App) GitLabStatus(ctx context.Context, scope string) GitLabResponse {
	status := GitLabStatusView{ServerID: gitlabServerID, Pinned: gitlab.ServerPackage + "@" + gitlab.ServerVersion}
	server, err := a.store.GetMCPServer(ctx, gitlabServerID)
	if scope != "" && scope != GitLabStatusProject && scope != GitLabStatusPlugin && scope != GitLabStatusAll {
		return a.gitlabRespond(server, nil, gitlabBadRequest("неизвестная область статуса %q", clipText(scope, 20)))
	}
	configured := err == nil
	if configured {
		status.Configured, status.URL = true, server.Settings["url"]
	}
	if scope == GitLabStatusAll {
		status.Binding = gitlabAllBinding()
	} else {
		status.Binding = a.gitlabBinding(ctx, server)
	}
	status.Linked = status.Binding.linked()
	if configured && !status.Linked && scope != GitLabStatusPlugin {
		return a.gitlabRespond(server, status, nil)
	}
	session, err := a.gitlabSession(ctx)
	if err != nil {
		return a.gitlabRespond(session.server, status, err)
	}
	status.Capabilities, status.ServerVersion = session.client.Capabilities(), session.server.ServerVersion
	user, err := a.gitlabUser(ctx, session)
	if err != nil && gitlab.ReasonOf(err) != gitlab.ReasonToolMissing {
		return a.gitlabRespond(session.server, status, err)
	}
	if user.Username != "" {
		status.User = &user
	}
	return a.gitlabRespond(session.server, status, nil)
}
