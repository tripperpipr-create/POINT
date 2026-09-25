package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"local-agent-workbench/internal/app"
)

// Окно GitLab. Путь проекта GitLab содержит «/», поэтому проект, MR и файл
// передаются параметрами запроса, а не сегментами пути. Каждый ответ —
// app.GitLabResponse {state, reason, problem, fix, data}; несостоявшийся
// экран — 200 с причиной, неверный запрос — 400.
func (s *Server) registerGitLabRoutes() {
	s.mux.HandleFunc("GET /api/integrations/gitlab/status", s.gitlabStatus)
	s.mux.HandleFunc("POST /api/integrations/gitlab/plugin", s.saveGitLabPlugin)
	s.mux.HandleFunc("PUT /api/integrations/gitlab/binding", s.saveGitLabBinding)
	s.mux.HandleFunc("GET /api/integrations/gitlab/merge-requests", s.gitlabMergeRequests)
	s.mux.HandleFunc("GET /api/integrations/gitlab/merge-request", s.gitlabMergeRequest)
	s.mux.HandleFunc("GET /api/integrations/gitlab/merge-request/discussions", s.gitlabDiscussions)
	s.mux.HandleFunc("GET /api/integrations/gitlab/merge-request/changes", s.gitlabChanges)
	s.mux.HandleFunc("GET /api/integrations/gitlab/merge-request/diff", s.gitlabFileDiff)
	s.mux.HandleFunc("POST /api/integrations/gitlab/merge-request/notes", s.gitlabComment)
	s.mux.HandleFunc("POST /api/integrations/gitlab/merge-request/approval", s.gitlabApproval)
	s.mux.HandleFunc("POST /api/integrations/gitlab/merge-request/merge", s.gitlabMerge)
	s.mux.HandleFunc("GET /api/integrations/gitlab/file", s.gitlabFile)
	s.mux.HandleFunc("GET /api/integrations/gitlab/pipelines", s.gitlabPipelines)
	s.mux.HandleFunc("GET /api/integrations/gitlab/jobs", s.gitlabJobs)
	s.mux.HandleFunc("GET /api/integrations/gitlab/job-log", s.gitlabJobLog)
	s.mux.HandleFunc("POST /api/integrations/gitlab/jobs/retry", s.gitlabRetryJob)
	s.mux.HandleFunc("GET /api/integrations/actions", s.integrationActions)
}

// gitlabContext — первый вызов может ждать, пока npx скачает сервер.
func gitlabContext(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), 2*time.Minute)
}

func (s *Server) gitlabWrite(w http.ResponseWriter, value app.GitLabResponse) {
	status := http.StatusOK
	if value.Reason == app.GitLabBadRequest {
		status = http.StatusBadRequest
	}
	s.write(w, status, value)
}

// queryInt — число из параметра; неверное становится 0, и экран отвечает
// bad_request со своим объяснением.
func queryInt(r *http.Request, name string) int {
	value, err := strconv.Atoi(r.URL.Query().Get(name))
	if err != nil {
		return 0
	}
	return value
}

func (s *Server) gitlabStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := gitlabContext(r)
	defer cancel()
	s.gitlabWrite(w, s.app.GitLabStatus(ctx))
}

func (s *Server) saveGitLabPlugin(w http.ResponseWriter, r *http.Request) {
	var input app.GitLabPluginUpsert
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.SaveGitLabPlugin(input)
	s.result(w, value, err)
}

func (s *Server) saveGitLabBinding(w http.ResponseWriter, r *http.Request) {
	var input app.GitLabBindingUpsert
	if !s.decode(w, r, &input) {
		return
	}
	ctx, cancel := gitlabContext(r)
	defer cancel()
	s.gitlabWrite(w, s.app.SaveGitLabBinding(ctx, input))
}

func (s *Server) gitlabMergeRequests(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := gitlabContext(r)
	defer cancel()
	s.gitlabWrite(w, s.app.GitLabMergeRequests(ctx, r.URL.Query().Get("scope")))
}

func (s *Server) gitlabMergeRequest(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := gitlabContext(r)
	defer cancel()
	s.gitlabWrite(w, s.app.GitLabMergeRequest(ctx, r.URL.Query().Get("project"), queryInt(r, "iid")))
}

func (s *Server) gitlabDiscussions(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := gitlabContext(r)
	defer cancel()
	s.gitlabWrite(w, s.app.GitLabDiscussions(ctx, r.URL.Query().Get("project"), queryInt(r, "iid")))
}

func (s *Server) gitlabChanges(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := gitlabContext(r)
	defer cancel()
	s.gitlabWrite(w, s.app.GitLabChanges(ctx, r.URL.Query().Get("project"), queryInt(r, "iid")))
}

func (s *Server) gitlabFileDiff(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := gitlabContext(r)
	defer cancel()
	query := r.URL.Query()
	s.gitlabWrite(w, s.app.GitLabFileDiff(ctx, query.Get("project"), queryInt(r, "iid"), query.Get("path")))
}

func (s *Server) gitlabComment(w http.ResponseWriter, r *http.Request) {
	var input app.GitLabCommentRequest
	if !s.decode(w, r, &input) {
		return
	}
	ctx, cancel := gitlabContext(r)
	defer cancel()
	s.gitlabWrite(w, s.app.GitLabComment(ctx, input))
}

func (s *Server) gitlabApproval(w http.ResponseWriter, r *http.Request) {
	var input app.GitLabApprovalRequest
	if !s.decode(w, r, &input) {
		return
	}
	ctx, cancel := gitlabContext(r)
	defer cancel()
	s.gitlabWrite(w, s.app.GitLabApproval(ctx, input))
}

func (s *Server) gitlabMerge(w http.ResponseWriter, r *http.Request) {
	var input app.GitLabMergeCommand
	if !s.decode(w, r, &input) {
		return
	}
	ctx, cancel := gitlabContext(r)
	defer cancel()
	s.gitlabWrite(w, s.app.GitLabMerge(ctx, input))
}

func (s *Server) gitlabFile(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := gitlabContext(r)
	defer cancel()
	query := r.URL.Query()
	s.gitlabWrite(w, s.app.GitLabFile(ctx, query.Get("project"), query.Get("path"), query.Get("ref")))
}

func (s *Server) gitlabPipelines(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := gitlabContext(r)
	defer cancel()
	query := r.URL.Query()
	s.gitlabWrite(w, s.app.GitLabPipelines(ctx, query.Get("project"), query.Get("ref"), queryInt(r, "mr")))
}

func (s *Server) gitlabJobs(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := gitlabContext(r)
	defer cancel()
	s.gitlabWrite(w, s.app.GitLabJobs(ctx, r.URL.Query().Get("project"), queryInt(r, "pipeline")))
}

func (s *Server) gitlabJobLog(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := gitlabContext(r)
	defer cancel()
	s.gitlabWrite(w, s.app.GitLabJobLog(ctx, r.URL.Query().Get("project"), queryInt(r, "job")))
}

func (s *Server) gitlabRetryJob(w http.ResponseWriter, r *http.Request) {
	var input app.GitLabRetryCommand
	if !s.decode(w, r, &input) {
		return
	}
	ctx, cancel := gitlabContext(r)
	defer cancel()
	s.gitlabWrite(w, s.app.GitLabRetryJob(ctx, input))
}

func (s *Server) integrationActions(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.IntegrationActions(queryInt(r, "limit"))
	s.result(w, map[string]any{"actions": value}, err)
}
