package app

// Экраны окна GitLab и действия владельца из него. Действие владельца
// выполняется сразу и оставляет запись в журнале integration_actions: что,
// где, чем кончилось, отпечаток и длина текста — сам текст не хранится.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/integrations/gitlab"
	"local-agent-workbench/internal/observability"
	"local-agent-workbench/internal/security"
)

const maxGitLabCommentBytes = 256 << 10

// GitLabMergeRequestsView — список MR раздела окна.
type GitLabMergeRequestsView struct {
	Scope   gitlab.Scope          `json:"scope"`
	Project string                `json:"project,omitempty"`
	Items   []gitlab.MergeRequest `json:"items"`
}

// GitLabMergeRequests — «Мои», «На моём ревью», «Все открытые» проекта папки.
func (a *App) GitLabMergeRequests(ctx context.Context, scope string) GitLabResponse {
	session, err := a.gitlabSession(ctx)
	if err != nil {
		return a.gitlabRespond(session.server, nil, err)
	}
	selected := gitlab.Scope(scope)
	if selected != gitlab.ScopeMine && selected != gitlab.ScopeReview && selected != gitlab.ScopeProject {
		return a.gitlabRespond(session.server, nil, gitlabBadRequest("неизвестный раздел %q", clipText(scope, 20)))
	}
	binding := a.gitlabBinding(ctx, session.server)
	if binding.Project == "" && (binding.Mode != domain.GitLabBindAll || selected == gitlab.ScopeProject) {
		return a.gitlabRespond(session.server, nil, gitlabNoProject(binding))
	}
	username := binding.Username
	if username == "" && selected == gitlab.ScopeReview {
		user, userErr := a.gitlabUser(ctx, session)
		if userErr != nil {
			return a.gitlabRespond(session.server, nil, userErr)
		}
		username = user.Username
	}
	items, err := session.client.MergeRequests(ctx, binding.Project, selected, username)
	if err != nil {
		return a.gitlabRespond(session.server, nil, err)
	}
	return a.gitlabRespond(session.server, GitLabMergeRequestsView{Scope: selected, Project: binding.Project, Items: items}, nil)
}

func gitlabNoProject(binding GitLabBindingView) error {
	problem := "у папки нет проекта GitLab"
	if binding.Note != "" {
		problem += ": " + binding.Note
	}
	return &gitlabFailure{GitLabNoProject, problem, "выберите проект вручную в шапке окна или включите «Все мои проекты»"}
}

// GitLabMergeRequestView — карточка MR: шапка, одобрения, пайплайны.
// Обсуждение и изменения грузятся по своим вкладкам.
type GitLabMergeRequestView struct {
	MergeRequest gitlab.MergeRequestDetail `json:"mergeRequest"`
	Approvals    *gitlab.Approvals         `json:"approvals,omitempty"`
	Pipelines    []gitlab.Pipeline         `json:"pipelines,omitempty"`
	Mine         bool                      `json:"mine"`
	ApprovedByMe bool                      `json:"approvedByMe"`
	// Missing — части карточки, которые не загрузились, с причиной.
	Missing []string `json:"missing,omitempty"`
}

func (a *App) GitLabMergeRequest(ctx context.Context, project string, iid int) GitLabResponse {
	session, err := a.gitlabTarget(ctx, project, iid)
	if err != nil {
		return a.gitlabRespond(session.server, nil, err)
	}
	detail, err := session.client.MergeRequest(ctx, project, iid)
	if err != nil {
		return a.gitlabRespond(session.server, nil, err)
	}
	view := GitLabMergeRequestView{MergeRequest: detail}
	user, _ := a.gitlabUser(ctx, session)
	view.Mine = user.ID != 0 && user.ID == detail.Author.ID
	if session.client.Capabilities()[gitlab.FeatureApprovals].Available {
		if approvals, approvalsErr := session.client.Approvals(ctx, project, iid); approvalsErr == nil {
			view.Approvals = &approvals
			for _, approver := range approvals.ApprovedBy {
				view.ApprovedByMe = view.ApprovedByMe || user.ID != 0 && approver.ID == user.ID
			}
		} else {
			view.Missing = append(view.Missing, "одобрения: "+explainGitLab(session.server, approvalsErr).problem)
		}
	}
	if session.client.Capabilities()[gitlab.FeatureMRPipelines].Available {
		if pipelines, pipelinesErr := session.client.MergeRequestPipelines(ctx, project, iid); pipelinesErr == nil {
			view.Pipelines = pipelines
		} else {
			view.Missing = append(view.Missing, "пайплайны: "+explainGitLab(session.server, pipelinesErr).problem)
		}
	}
	return a.gitlabRespond(session.server, view, nil)
}

func (a *App) GitLabDiscussions(ctx context.Context, project string, iid int) GitLabResponse {
	session, err := a.gitlabTarget(ctx, project, iid)
	if err != nil {
		return a.gitlabRespond(session.server, nil, err)
	}
	discussions, err := session.client.Discussions(ctx, project, iid)
	if err != nil {
		return a.gitlabRespond(session.server, nil, err)
	}
	return a.gitlabRespond(session.server, map[string]any{"discussions": nonNilSlice(discussions)}, nil)
}

func (a *App) GitLabChanges(ctx context.Context, project string, iid int) GitLabResponse {
	session, err := a.gitlabTarget(ctx, project, iid)
	if err != nil {
		return a.gitlabRespond(session.server, nil, err)
	}
	files, err := session.client.ChangedFiles(ctx, project, iid)
	if err != nil {
		return a.gitlabRespond(session.server, nil, err)
	}
	return a.gitlabRespond(session.server, map[string]any{"files": files}, nil)
}

// GitLabFileDiff — diff одного файла текстом: запасной вид, если
// содержимое файла на ревизиях недоступно.
func (a *App) GitLabFileDiff(ctx context.Context, project string, iid int, path string) GitLabResponse {
	session, err := a.gitlabTarget(ctx, project, iid)
	if err == nil {
		err = validGitLabPath(path)
	}
	if err != nil {
		return a.gitlabRespond(session.server, nil, err)
	}
	diff, err := session.client.FileDiff(ctx, project, iid, path)
	if err != nil {
		return a.gitlabRespond(session.server, nil, err)
	}
	return a.gitlabRespond(session.server, diff, nil)
}

// GitLabFile — содержимое файла на ревизии для нативного diff IDE.
func (a *App) GitLabFile(ctx context.Context, project, path, ref string) GitLabResponse {
	session, err := a.gitlabProject(ctx, project)
	if err == nil {
		err = validGitLabPath(path)
	}
	if err == nil && !gitlabRefPattern.MatchString(ref) {
		err = gitlabBadRequest("ревизия %q недопустима", clipText(ref, 80))
	}
	if err != nil {
		return a.gitlabRespond(session.server, nil, err)
	}
	content, err := session.client.FileContent(ctx, project, path, ref)
	if err != nil {
		return a.gitlabRespond(session.server, nil, err)
	}
	return a.gitlabRespond(session.server, content, nil)
}

// GitLabPipelinesView — пайплайны ветки или MR.
type GitLabPipelinesView struct {
	Project string            `json:"project"`
	Ref     string            `json:"ref,omitempty"`
	MR      int               `json:"mr,omitempty"`
	Items   []gitlab.Pipeline `json:"items"`
}

// GitLabPipelines: без проекта — проект папки, без ветки — текущая ветка
// папки; mr > 0 — пайплайны MR.
func (a *App) GitLabPipelines(ctx context.Context, project, ref string, mr int) GitLabResponse {
	session, err := a.gitlabSession(ctx)
	if err != nil {
		return a.gitlabRespond(session.server, nil, err)
	}
	if project == "" {
		binding := a.gitlabBinding(ctx, session.server)
		if binding.Project == "" {
			return a.gitlabRespond(session.server, nil, gitlabNoProject(binding))
		}
		project = binding.Project
		if ref == "" && mr == 0 {
			ref = binding.Branch
		}
	}
	switch {
	case !validGitLabProject(project):
		err = gitlabBadRequest("путь проекта %q недопустим", clipText(project, 80))
	case ref != "" && !gitlabRefPattern.MatchString(ref):
		err = gitlabBadRequest("ветка %q недопустима", clipText(ref, 80))
	case mr < 0:
		err = gitlabBadRequest("номер MR должен быть положительным")
	}
	if err != nil {
		return a.gitlabRespond(session.server, nil, err)
	}
	view := GitLabPipelinesView{Project: project, Ref: ref, MR: mr}
	if mr > 0 {
		view.Ref = ""
		view.Items, err = session.client.MergeRequestPipelines(ctx, project, mr)
	} else {
		view.Items, err = session.client.Pipelines(ctx, project, ref)
	}
	if err != nil {
		return a.gitlabRespond(session.server, nil, err)
	}
	return a.gitlabRespond(session.server, view, nil)
}

func (a *App) GitLabJobs(ctx context.Context, project string, pipelineID int) GitLabResponse {
	session, err := a.gitlabProject(ctx, project)
	if err == nil && pipelineID <= 0 {
		err = gitlabBadRequest("номер пайплайна должен быть положительным")
	}
	if err != nil {
		return a.gitlabRespond(session.server, nil, err)
	}
	jobs, err := session.client.Jobs(ctx, project, pipelineID)
	if err != nil {
		return a.gitlabRespond(session.server, nil, err)
	}
	return a.gitlabRespond(session.server, map[string]any{"project": project, "pipelineId": pipelineID, "jobs": jobs}, nil)
}

func (a *App) GitLabJobLog(ctx context.Context, project string, jobID int) GitLabResponse {
	session, err := a.gitlabProject(ctx, project)
	if err == nil && jobID <= 0 {
		err = gitlabBadRequest("номер джоба должен быть положительным")
	}
	if err != nil {
		return a.gitlabRespond(session.server, nil, err)
	}
	log, err := session.client.JobLog(ctx, project, jobID)
	if err != nil {
		return a.gitlabRespond(session.server, nil, err)
	}
	return a.gitlabRespond(session.server, log, nil)
}

// GitLabCommentRequest — комментарий к MR или ответ в нить.
type GitLabCommentRequest struct {
	Project      string `json:"project"`
	IID          int    `json:"iid"`
	DiscussionID string `json:"discussionId"`
	Body         string `json:"body"`
}

// GitLabComment публикует комментарий и возвращает свежее обсуждение.
func (a *App) GitLabComment(ctx context.Context, req GitLabCommentRequest) GitLabResponse {
	session, err := a.gitlabTarget(ctx, req.Project, req.IID)
	body := strings.TrimSpace(req.Body)
	switch {
	case err != nil:
	case body == "":
		err = gitlabBadRequest("комментарий пуст")
	case len(body) > maxGitLabCommentBytes || !utf8.ValidString(body):
		err = gitlabBadRequest("комментарий длиннее 256 КБ или не в UTF-8")
	case req.DiscussionID != "" && !gitlabDiscussionPattern.MatchString(req.DiscussionID):
		err = gitlabBadRequest("нить обсуждения %q не найдена", clipText(req.DiscussionID, 40))
	}
	if err != nil {
		return a.gitlabRespond(session.server, nil, err)
	}
	tool, target := "create_merge_request_note", gitlabMRTarget(req.Project, req.IID)
	if req.DiscussionID != "" {
		tool, target = "create_merge_request_discussion_note", target+"#"+req.DiscussionID
	}
	err = session.client.Comment(ctx, req.Project, req.IID, req.DiscussionID, body)
	a.auditGitLab(ctx, session.server, tool, target, body, err)
	if err != nil {
		return a.gitlabRespond(session.server, nil, err)
	}
	return a.gitlabRefreshed(ctx, session, func() (any, error) {
		discussions, refreshErr := session.client.Discussions(ctx, req.Project, req.IID)
		return map[string]any{"discussions": nonNilSlice(discussions)}, refreshErr
	})
}

// GitLabApprovalRequest — одобрить или снять одобрение. SHA — голова,
// которую владелец видел: GitLab откажет, если она ушла вперёд.
type GitLabApprovalRequest struct {
	Project string `json:"project"`
	IID     int    `json:"iid"`
	Approve bool   `json:"approve"`
	SHA     string `json:"sha"`
}

func (a *App) GitLabApproval(ctx context.Context, req GitLabApprovalRequest) GitLabResponse {
	session, err := a.gitlabTarget(ctx, req.Project, req.IID)
	if err == nil && req.SHA != "" && !gitlabSHAPattern.MatchString(req.SHA) {
		err = gitlabBadRequest("sha %q недопустим", clipText(req.SHA, 80))
	}
	if err != nil {
		return a.gitlabRespond(session.server, nil, err)
	}
	tool := "unapprove_merge_request"
	if req.Approve {
		tool = "approve_merge_request"
	}
	err = session.client.SetApproval(ctx, req.Project, req.IID, req.Approve, req.SHA)
	a.auditGitLab(ctx, session.server, tool, gitlabMRTarget(req.Project, req.IID), "", err)
	if err != nil {
		return a.gitlabRespond(session.server, nil, err)
	}
	return a.gitlabRefreshed(ctx, session, func() (any, error) {
		return session.client.Approvals(ctx, req.Project, req.IID)
	})
}

// GitLabMergeCommand — merge из окна. Confirmed — владелец подтвердил в
// модальном окне IDE; ExpectedSHA — голова MR, которую он видел.
type GitLabMergeCommand struct {
	Project            string `json:"project"`
	IID                int    `json:"iid"`
	ExpectedSHA        string `json:"expectedSha"`
	Confirmed          bool   `json:"confirmed"`
	RemoveSourceBranch bool   `json:"removeSourceBranch"`
}

func (a *App) GitLabMerge(ctx context.Context, req GitLabMergeCommand) GitLabResponse {
	session, err := a.gitlabTarget(ctx, req.Project, req.IID)
	switch {
	case err != nil:
	case !req.Confirmed:
		err = gitlabBadRequest("merge выполняется только после подтверждения")
	case !gitlabSHAPattern.MatchString(req.ExpectedSHA):
		err = gitlabBadRequest("нужна голова MR (sha), которую вы видели")
	}
	if err != nil {
		return a.gitlabRespond(session.server, nil, err)
	}
	merged, err := session.client.Merge(ctx, req.Project, req.IID, req.ExpectedSHA, req.RemoveSourceBranch)
	a.auditGitLab(ctx, session.server, "merge_merge_request", gitlabMRTarget(req.Project, req.IID)+"@"+req.ExpectedSHA, "", err)
	if err != nil {
		return a.gitlabRespond(session.server, nil, err)
	}
	return a.gitlabRespond(session.server, map[string]any{"mergeRequest": merged}, nil)
}

// GitLabRetryCommand — перезапуск джоба.
type GitLabRetryCommand struct {
	Project string `json:"project"`
	JobID   int    `json:"jobId"`
}

func (a *App) GitLabRetryJob(ctx context.Context, req GitLabRetryCommand) GitLabResponse {
	session, err := a.gitlabProject(ctx, req.Project)
	if err == nil && req.JobID <= 0 {
		err = gitlabBadRequest("номер джоба должен быть положительным")
	}
	if err != nil {
		return a.gitlabRespond(session.server, nil, err)
	}
	job, err := session.client.RetryJob(ctx, req.Project, req.JobID)
	a.auditGitLab(ctx, session.server, "retry_pipeline_job", fmt.Sprintf("%s job %d", req.Project, req.JobID), "", err)
	if err != nil {
		return a.gitlabRespond(session.server, nil, err)
	}
	return a.gitlabRespond(session.server, map[string]any{"job": job}, nil)
}

// IntegrationActions — журнал действий во внешних сервисах, новые сверху.
func (a *App) IntegrationActions(limit int) ([]domain.IntegrationAction, error) {
	actions, err := a.store.ListIntegrationActions(context.Background(), limit)
	return nonNilSlice(actions), err
}

func (a *App) gitlabProject(ctx context.Context, project string) (gitlabSession, error) {
	if !validGitLabProject(project) {
		server, _ := a.store.GetMCPServer(ctx, gitlabServerID)
		return gitlabSession{server: server}, gitlabBadRequest("путь проекта %q недопустим", clipText(project, 80))
	}
	return a.gitlabSession(ctx)
}

func (a *App) gitlabTarget(ctx context.Context, project string, iid int) (gitlabSession, error) {
	session, err := a.gitlabProject(ctx, project)
	if err == nil && iid <= 0 {
		err = gitlabBadRequest("номер MR должен быть положительным")
	}
	return session, err
}

func validGitLabPath(path string) error {
	if path == "" || len(path) > 1000 || strings.HasPrefix(path, "/") || strings.ContainsAny(path, "\x00\r\n") {
		return gitlabBadRequest("путь файла %q недопустим", clipText(path, 80))
	}
	return nil
}

func gitlabMRTarget(project string, iid int) string { return fmt.Sprintf("%s!%d", project, iid) }

// gitlabRefreshed — ответ действия со свежим видом. Действие уже
// выполнено: если свежий вид не загрузился, окно просто перечитает экран.
func (a *App) gitlabRefreshed(ctx context.Context, session gitlabSession, load func() (any, error)) GitLabResponse {
	data, err := load()
	if err != nil {
		observability.From(ctx).Warn("gitlab refresh after action failed", "error", security.Redact(err.Error()))
		return a.gitlabRespond(session.server, nil, nil)
	}
	return a.gitlabRespond(session.server, data, nil)
}

// auditGitLab записывает действие владельца. Запись не мешает действию:
// сбой журнала попадает в лог ядра.
func (a *App) auditGitLab(ctx context.Context, server domain.MCPServer, tool, target, body string, err error) {
	action := domain.IntegrationAction{ID: domain.NewID("ia"), Actor: "owner", ServerID: server.ID, Tool: tool,
		Target: clipText(target, 500), Outcome: "ok", At: time.Now().UTC()}
	if body != "" {
		sum := sha256.Sum256([]byte(body))
		action.BodySHA256, action.BodyLength = hex.EncodeToString(sum[:]), len(body)
	}
	if err != nil {
		action.Outcome = "error"
		failure := explainGitLab(server, err)
		action.Error = clipText(security.Redact(string(failure.reason)+": "+failure.problem), 500)
	}
	logger := observability.From(ctx)
	if storeErr := a.store.AppendIntegrationAction(context.Background(), action); storeErr != nil {
		logger.Error("integration action journal failed", "tool", tool, "error", storeErr)
	}
	logger.Info("integration action", "server", server.ID, "tool", tool, "target", action.Target, "outcome", action.Outcome)
}
