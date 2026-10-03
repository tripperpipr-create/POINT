package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/integrations/gitlab"
	"local-agent-workbench/internal/mcpclient/mcptest"
)

const (
	fakeGitLabHead    = "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678"
	fakeGitLabRevoked = "glpat-revoked0000000000000000"
)

// runFakeGitLabServer отвечает фикстурами спайка тем же списком инструментов,
// что у закреплённого сервера.
func runFakeGitLabServer() {
	dir := os.Getenv("POINT_FAKE_GITLAB_FIXTURES")
	raw, _ := os.ReadFile(filepath.Join(dir, "tools-list.json"))
	var list struct {
		Tools []mcptest.Tool `json:"tools"`
	}
	_ = json.Unmarshal(raw, &list)
	mcptest.Serve(os.Stdin, os.Stdout, "zereight-gitlab-mcp-server", list.Tools, func(name string, args json.RawMessage) (string, bool) {
		if os.Getenv(gitlab.TokenVariable) == fakeGitLabRevoked {
			return "GitLab API error: 401 Unauthorized - {\"message\":\"401 Unauthorized\"}", true
		}
		var arguments map[string]any
		_ = json.Unmarshal(args, &arguments)
		switch name {
		case "merge_merge_request":
			if arguments["sha"] != fakeGitLabHead {
				return "GitLab API error: 409 Conflict - SHA does not match HEAD of source branch", true
			}
		case "create_merge_request_note", "create_merge_request_discussion_note":
			return `{"id":600}`, false
		case "approve_merge_request", "unapprove_merge_request":
			return `{}`, false
		}
		for _, file := range []string{name + ".json", name + ".txt"} {
			if body, err := os.ReadFile(filepath.Join(dir, "responses", file)); err == nil {
				return string(body), false
			}
		}
		return "GitLab API error: 404 Not Found", true
	})
}

type gitlabGitRunner map[string]string

func (f gitlabGitRunner) Run(_ context.Context, _ string, arguments ...string) ([]byte, error) {
	if out, ok := f[strings.Join(arguments, " ")]; ok {
		return []byte(out + "\n"), nil
	}
	return nil, os.ErrNotExist
}

func useFakeGitLab(t *testing.T, application *App) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	fixtures, err := filepath.Abs("../integrations/gitlab/testdata/zereight-2.1.66")
	if err != nil {
		t.Fatal(err)
	}
	original := gitlabRecipe
	gitlabRecipe = func(settings gitlab.Settings) (gitlab.Launch, error) {
		launch, recipeErr := original(settings)
		launch.Command, launch.Args = self, []string{"-test.run=^$"}
		if launch.Env != nil {
			launch.Env["POINT_FAKE_MCP"] = "gitlab"
			launch.Env["POINT_FAKE_GITLAB_FIXTURES"] = fixtures
		}
		return launch, recipeErr
	}
	t.Cleanup(func() { gitlabRecipe = original })
	application.gitRunner = gitlabGitRunner{
		"remote get-url origin":       "git@gitlab.example.test:billing/payments.git",
		"rev-parse --abbrev-ref HEAD": "fix/webhook-retry",
	}
	if _, err = application.OpenWorkspace(t.TempDir()); err != nil {
		t.Fatal(err)
	}
}

func decodeData[T any](t *testing.T, response GitLabResponse) T {
	t.Helper()
	if response.State != "ok" {
		t.Fatalf("response: %+v", response)
	}
	raw, err := json.Marshal(response.Data)
	if err != nil {
		t.Fatal(err)
	}
	var value T
	if err = json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestGitLabFormatErrorNamesTheToolAtPinnedVersion(t *testing.T) {
	server := domain.MCPServer{ServerVersion: gitlab.ServerVersion}
	failure := explainGitLab(server, &gitlab.Error{Reason: gitlab.ReasonFormat, Tool: "whoami", Detail: "answer did not parse"})
	if failure.reason != GitLabFormat || !strings.Contains(failure.problem, "whoami") || strings.Contains(failure.fix, "Point рассчитан") {
		t.Fatalf("pinned server got misleading version advice: %+v", failure)
	}
}

func TestGitLabPluginConnectsAndShowsScreens(t *testing.T) {
	application := newTestApp(t)
	useFakeGitLab(t, application)
	ctx := context.Background()

	if status := application.GitLabStatus(ctx, ""); status.Reason != GitLabNotConfigured || status.Data.(GitLabStatusView).Configured {
		t.Fatalf("status before connect: %+v", status)
	}
	view, err := application.SaveGitLabPlugin(GitLabPluginUpsert{URL: "https://GitLab.example.test/", Token: "glpat-abcdefghijklmnopqrstuvwx"})
	if err != nil {
		t.Fatal(err)
	}
	if view.Kind != domain.MCPServerGitLab || view.Trusted || !view.OutsideSandbox || view.Settings["url"] != "https://gitlab.example.test" ||
		view.SecretRefs["env:"+gitlab.TokenVariable] != "point.mcp.mcp-gitlab.env."+gitlab.TokenVariable || view.Env["GITLAB_API_URL"] != "https://gitlab.example.test/api/v4" {
		t.Fatalf("plugin server: %+v", view)
	}
	// Форма «любого MCP» не правит сервер плагина и не создаёт его.
	if _, err = application.SaveMCPServer(MCPServerUpsert{ID: gitlabServerID, Transport: domain.MCPTransportStdio, Command: "npx"}); err == nil {
		t.Fatal("generic form edited the plugin server")
	}
	if _, err = application.SaveMCPServer(MCPServerUpsert{Kind: domain.MCPServerGitLab, Transport: domain.MCPTransportStdio, Command: "npx"}); err == nil {
		t.Fatal("generic form created a plugin server")
	}
	if status := application.GitLabStatus(ctx, ""); status.Reason != GitLabNotTrusted || !strings.Contains(status.Fix, "Доверяю") {
		t.Fatalf("untrusted status: %+v", status)
	}
	if response := application.GitLabMergeRequests(ctx, "mine"); response.Reason != GitLabNotTrusted {
		t.Fatalf("untrusted list: %+v", response)
	}
	if _, err = application.TrustMCPServer(view.ID, view.PendingDigest); err != nil {
		t.Fatal(err)
	}

	status := decodeData[GitLabStatusView](t, application.GitLabStatus(ctx, ""))
	if status.User == nil || status.User.Username != "anna" || !status.Linked || status.Binding.Project != "billing/payments" ||
		status.Binding.Branch != "fix/webhook-retry" || status.Binding.Mode != domain.GitLabBindAuto || !status.Capabilities[gitlab.FeatureMerge].Available {
		t.Fatalf("status: %+v", status)
	}
	servers, err := application.ListMCPServers()
	if err != nil || len(servers) != 1 {
		t.Fatalf("servers: %+v, %v", servers, err)
	}
	for name, risk := range map[string]domain.ToolRisk{"merge_merge_request": domain.ToolRiskCritical, "create_merge_request_note": domain.ToolRiskHigh, "list_merge_requests": domain.ToolRiskLow} {
		if tool := toolByName(servers[0].Tools, name); tool.Risk != risk || tool.Enabled {
			t.Fatalf("%s: %+v", name, tool)
		}
	}

	review := decodeData[GitLabMergeRequestsView](t, application.GitLabMergeRequests(ctx, "review"))
	if review.Project != "billing/payments" || len(review.Items) != 2 || review.Items[1].WebURL != "" {
		t.Fatalf("review list: %+v", review)
	}
	if response := application.GitLabMergeRequests(ctx, "everything"); response.Reason != GitLabBadRequest {
		t.Fatalf("unknown scope: %+v", response)
	}

	card := decodeData[GitLabMergeRequestView](t, application.GitLabMergeRequest(ctx, "billing/payments", 12))
	if !card.Mine || card.ApprovedByMe || card.Approvals == nil || len(card.Pipelines) != 2 || len(card.Missing) != 0 ||
		card.MergeRequest.DiffRefs.HeadSHA != fakeGitLabHead {
		t.Fatalf("card: %+v", card)
	}
	if response := application.GitLabMergeRequest(ctx, "../../etc", 12); response.Reason != GitLabBadRequest {
		t.Fatalf("bad project: %+v", response)
	}
	discussions := decodeData[map[string][]gitlab.Discussion](t, application.GitLabDiscussions(ctx, "billing/payments", 12))
	if len(discussions["discussions"]) != 2 {
		t.Fatalf("discussions: %+v", discussions)
	}
	changes := decodeData[map[string][]gitlab.ChangedFile](t, application.GitLabChanges(ctx, "billing/payments", 12))
	if len(changes["files"]) != 3 {
		t.Fatalf("changes: %+v", changes)
	}
	if diff := decodeData[gitlab.FileDiff](t, application.GitLabFileDiff(ctx, "billing/payments", 12, "internal/billing/retry.go")); diff.Diff == "" {
		t.Fatalf("diff: %+v", diff)
	}
	if file := decodeData[gitlab.FileContent](t, application.GitLabFile(ctx, "billing/payments", "internal/billing/retry.go", fakeGitLabHead)); !strings.HasPrefix(file.Content, "package billing") {
		t.Fatalf("file: %+v", file)
	}
	if response := application.GitLabFile(ctx, "billing/payments", "/etc/passwd", "main"); response.Reason != GitLabBadRequest {
		t.Fatalf("absolute path: %+v", response)
	}
	pipelines := decodeData[GitLabPipelinesView](t, application.GitLabPipelines(ctx, "", "", 0))
	if pipelines.Project != "billing/payments" || pipelines.Ref != "fix/webhook-retry" || len(pipelines.Items) != 2 {
		t.Fatalf("pipelines: %+v", pipelines)
	}
	log := decodeData[gitlab.JobLog](t, application.GitLabJobLog(ctx, "billing/payments", 77002))
	if strings.Contains(log.Text, "glcbt-") || !strings.Contains(log.Text, "--- FAIL") {
		t.Fatalf("job log: %+v", log)
	}

	// Действия владельца: сразу и с записью в журнале без текста.
	secretComment := "Готово, проверил на стенде"
	if response := application.GitLabComment(ctx, GitLabCommentRequest{Project: "billing/payments", IID: 12, Body: secretComment}); response.State != "ok" {
		t.Fatalf("comment: %+v", response)
	}
	if response := application.GitLabComment(ctx, GitLabCommentRequest{Project: "billing/payments", IID: 12, Body: "  "}); response.Reason != GitLabBadRequest {
		t.Fatalf("empty comment: %+v", response)
	}
	if response := application.GitLabMerge(ctx, GitLabMergeCommand{Project: "billing/payments", IID: 12, ExpectedSHA: fakeGitLabHead}); response.Reason != GitLabBadRequest {
		t.Fatalf("unconfirmed merge: %+v", response)
	}
	stale := application.GitLabMerge(ctx, GitLabMergeCommand{Project: "billing/payments", IID: 12, ExpectedSHA: "ffffffffffffffffffffffffffffffffffffffff", Confirmed: true})
	if stale.Reason != GitLabRefused || !strings.Contains(stale.Problem, "SHA does not match") {
		t.Fatalf("stale merge: %+v", stale)
	}
	merged := decodeData[map[string]gitlab.MergeRequestDetail](t, application.GitLabMerge(ctx, GitLabMergeCommand{Project: "billing/payments", IID: 12, ExpectedSHA: fakeGitLabHead, Confirmed: true, RemoveSourceBranch: true}))
	if merged["mergeRequest"].State != "merged" {
		t.Fatalf("merge: %+v", merged)
	}
	if response := application.GitLabRetryJob(ctx, GitLabRetryCommand{Project: "billing/payments", JobID: 77002}); response.State != "ok" {
		t.Fatalf("retry: %+v", response)
	}
	actions, err := application.IntegrationActions(10)
	if err != nil || len(actions) != 4 {
		t.Fatalf("journal: %+v, %v", actions, err)
	}
	outcomes := map[string]domain.IntegrationAction{}
	for _, action := range actions {
		outcomes[action.Tool+" "+action.Outcome] = action
		raw, _ := json.Marshal(action)
		if strings.Contains(string(raw), secretComment) {
			t.Fatalf("journal keeps the comment text: %s", raw)
		}
	}
	if note := outcomes["create_merge_request_note ok"]; note.BodyLength != len(secretComment) || note.BodySHA256 == "" || note.Target != "billing/payments!12" {
		t.Fatalf("comment record: %+v", note)
	}
	if refused := outcomes["merge_merge_request error"]; !strings.Contains(refused.Error, "refused") {
		t.Fatalf("refused merge record: %+v", refused)
	}
	if outcomes["merge_merge_request ok"].ID == "" || outcomes["retry_pipeline_job ok"].ID == "" {
		t.Fatalf("journal: %+v", outcomes)
	}

	// Привязка: ручной проект, «все мои проекты», неверный путь.
	if response := application.SaveGitLabBinding(ctx, GitLabBindingUpsert{Mode: domain.GitLabBindManual, Project: "not a path"}); response.Reason != GitLabBadRequest {
		t.Fatalf("bad manual binding: %+v", response)
	}
	manual := decodeData[GitLabBindingView](t, application.SaveGitLabBinding(ctx, GitLabBindingUpsert{Mode: domain.GitLabBindManual, Project: "/platform/api/"}))
	if manual.Project != "platform/api" || manual.Detected != "billing/payments" {
		t.Fatalf("manual binding: %+v", manual)
	}
	decodeData[GitLabBindingView](t, application.SaveGitLabBinding(ctx, GitLabBindingUpsert{Mode: domain.GitLabBindAll}))
	if response := application.GitLabMergeRequests(ctx, "project"); response.Reason != GitLabNoProject {
		t.Fatalf("project list without a project: %+v", response)
	}
	if mine := decodeData[GitLabMergeRequestsView](t, application.GitLabMergeRequests(ctx, "mine")); mine.Project != "" {
		t.Fatalf("all-projects list: %+v", mine)
	}

	// Отозванный токен: новое значение, та же команда — доверие остаётся,
	// а окно честно говорит о доступе.
	if view, err = application.SaveGitLabPlugin(GitLabPluginUpsert{URL: "https://gitlab.example.test", Token: fakeGitLabRevoked}); err != nil || !view.Trusted {
		t.Fatalf("token change: %+v, %v", view, err)
	}
	if denied := application.GitLabStatus(ctx, ""); denied.Reason != GitLabAuth || !strings.Contains(denied.Fix, "токен") ||
		strings.Contains(denied.Problem, fakeGitLabRevoked) {
		t.Fatalf("revoked token: %+v", denied)
	}
	// Другой адрес — другая команда запуска: доверие нужно заново.
	if view, err = application.SaveGitLabPlugin(GitLabPluginUpsert{URL: "https://gitlab.other.test"}); err != nil || view.Trusted {
		t.Fatalf("url change: %+v, %v", view, err)
	}
	if _, err = application.SaveGitLabPlugin(GitLabPluginUpsert{URL: "https://gitlab.example.test", CAPath: "ca.pem"}); err == nil {
		t.Fatal("relative CA path accepted")
	}
}

// Подключение GitLab общее, а связь — выбор проекта. Без выбора владельца
// проект связывает только origin на подключённый сервер; несвязанный проект
// сервер плагина не запускает и получает спокойное not_linked.
func TestGitLabProjectLinkIsOptional(t *testing.T) {
	application := newTestApp(t)
	useFakeGitLab(t, application)
	ctx := context.Background()
	view, err := application.SaveGitLabPlugin(GitLabPluginUpsert{URL: "https://gitlab.example.test", Token: "glpat-abcdefghijklmnopqrstuvwx"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = application.TrustMCPServer(view.ID, view.PendingDigest); err != nil {
		t.Fatal(err)
	}
	unprobed := func(step string) {
		t.Helper()
		if servers, listErr := application.ListMCPServers(); listErr != nil || len(servers) != 1 || len(servers[0].Tools) != 0 {
			t.Fatalf("%s started the plugin server: %+v, %v", step, servers, listErr)
		}
	}

	application.gitRunner = gitlabGitRunner{"remote get-url origin": "git@github.com:anna/dotfiles.git", "rev-parse --abbrev-ref HEAD": "main"}
	unlinked := decodeData[GitLabStatusView](t, application.GitLabStatus(ctx, ""))
	if unlinked.Linked || unlinked.Binding.Mode != domain.GitLabBindOff || unlinked.Binding.Project != "" || unlinked.User != nil ||
		unlinked.ServerVersion != "" || !strings.Contains(unlinked.Binding.Note, "github.com") {
		t.Fatalf("unlinked status: %+v", unlinked)
	}
	for _, response := range []GitLabResponse{application.GitLabMergeRequests(ctx, "mine"), application.GitLabPipelines(ctx, "", "", 0)} {
		if response.State != "error" || response.Reason != GitLabNotLinked || !strings.Contains(response.Fix, "настройках проекта") {
			t.Fatalf("unlinked screen: %+v", response)
		}
	}
	unprobed("unlinked project")
	if response := application.GitLabStatus(ctx, "everything"); response.Reason != GitLabBadRequest {
		t.Fatalf("unknown status scope: %+v", response)
	}
	// Карточке плагина в общих настройках нужно здоровье сервера в любом проекте.
	plugin := decodeData[GitLabStatusView](t, application.GitLabStatus(ctx, GitLabStatusPlugin))
	if plugin.Linked || plugin.User == nil || plugin.User.Username != "anna" || plugin.Binding.Mode != domain.GitLabBindOff {
		t.Fatalf("plugin status in an unlinked project: %+v", plugin)
	}

	// origin на подключённый GitLab связывает проект без выбора владельца.
	application.gitRunner = gitlabGitRunner{"remote get-url origin": "https://gitlab.example.test/billing/payments.git", "rev-parse --abbrev-ref HEAD": "main"}
	auto := decodeData[GitLabStatusView](t, application.GitLabStatus(ctx, ""))
	if !auto.Linked || auto.Binding.Mode != domain.GitLabBindAuto || auto.Binding.Project != "billing/payments" || auto.User == nil {
		t.Fatalf("auto status: %+v", auto)
	}

	// Явное «Не связывать» сильнее origin: окно молчит, списков нет.
	off := decodeData[GitLabBindingView](t, application.SaveGitLabBinding(ctx, GitLabBindingUpsert{Mode: domain.GitLabBindOff}))
	if off.Mode != domain.GitLabBindOff || off.Project != "" || off.Detected != "billing/payments" || !strings.Contains(off.Note, "отключена") {
		t.Fatalf("off binding: %+v", off)
	}
	if status := decodeData[GitLabStatusView](t, application.GitLabStatus(ctx, "")); status.Linked || status.User != nil {
		t.Fatalf("status after opting out: %+v", status)
	}
	if response := application.GitLabMergeRequests(ctx, "review"); response.Reason != GitLabNotLinked {
		t.Fatalf("list after opting out: %+v", response)
	}
	// Общее окно из общих настроек смотрит на все проекты, какой бы ни была
	// папка и её связь: «Не связывать» папки его не гасит.
	if all := decodeData[GitLabStatusView](t, application.GitLabStatus(ctx, GitLabStatusAll)); !all.Linked || all.Binding.Mode != domain.GitLabBindAll || all.Binding.Workspace != "" || all.User == nil {
		t.Fatalf("general window status: %+v", all)
	}
	if mine := decodeData[GitLabMergeRequestsView](t, application.GitLabAllMergeRequests(ctx, "mine")); mine.Project != "" || len(mine.Items) == 0 {
		t.Fatalf("general window list: %+v", mine)
	}
	if response := application.GitLabAllMergeRequests(ctx, "project"); response.Reason != GitLabNoProject {
		t.Fatalf("general window has no project list: %+v", response)
	}
	// Возврат к git remote связывает снова.
	if back := decodeData[GitLabBindingView](t, application.SaveGitLabBinding(ctx, GitLabBindingUpsert{Mode: domain.GitLabBindAuto})); back.Project != "billing/payments" {
		t.Fatalf("auto binding: %+v", back)
	}
	if mine := decodeData[GitLabMergeRequestsView](t, application.GitLabMergeRequests(ctx, "mine")); mine.Project != "billing/payments" {
		t.Fatalf("list after linking back: %+v", mine)
	}
}

// Сервер, сохранённый прошлой версией Point, запускался со своим снимком
// рецепта: без list_projects в GITLAB_TOOLS раздел «Проекты» отвечал «сервер
// не отдаёт инструмент», и «Проверить» этого не лечил. Ядро сверяет запись с
// рецептом, переписывает её и честно просит доверие к новой команде.
func TestGitLabStaleRecipeIsRefreshedAndAsksTrust(t *testing.T) {
	application := newTestApp(t)
	useFakeGitLab(t, application)
	ctx := context.Background()
	view, err := application.SaveGitLabPlugin(GitLabPluginUpsert{URL: "https://gitlab.example.test", Token: "glpat-abcdefghijklmnopqrstuvwx"})
	if err != nil {
		t.Fatal(err)
	}
	stale, err := application.store.GetMCPServer(ctx, view.ID)
	if err != nil {
		t.Fatal(err)
	}
	stale.Env["GITLAB_TOOLS"] = "whoami,list_merge_requests"
	if err = application.store.SaveMCPServer(ctx, stale); err != nil {
		t.Fatal(err)
	}
	old := application.mcpServerView(ctx, stale, nil)
	if _, err = application.TrustMCPServer(old.ID, old.PendingDigest); err != nil {
		t.Fatal(err)
	}

	status := application.GitLabStatus(ctx, "")
	if status.Reason != GitLabNotTrusted || !strings.Contains(status.Problem, "обновил") {
		t.Fatalf("stale recipe status: %+v", status)
	}
	refreshed, err := application.store.GetMCPServer(ctx, view.ID)
	if err != nil || !strings.Contains(refreshed.Env["GITLAB_TOOLS"], "list_projects") {
		t.Fatalf("recipe not refreshed: %+v, %v", refreshed.Env, err)
	}
	fresh := application.mcpServerView(ctx, refreshed, nil)
	if _, err = application.TrustMCPServer(fresh.ID, fresh.PendingDigest); err != nil {
		t.Fatal(err)
	}
	if projects := application.GitLabProjects(ctx, "", "member"); projects.State != "ok" {
		t.Fatalf("projects after re-trust: %+v", projects)
	}
	// Совпадающая запись не переписывается: доверие не сбрасывается зря.
	if again := application.GitLabStatus(ctx, ""); again.State != "ok" {
		t.Fatalf("status after re-trust: %+v", again)
	}
}
