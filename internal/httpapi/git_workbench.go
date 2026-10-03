package httpapi

import (
	"context"
	"net/http"
	"time"

	"local-agent-workbench/internal/app"
	"local-agent-workbench/internal/security"
)

func (s *Server) registerGitWorkbenchRoutes() {
	s.mux.HandleFunc("POST /api/v2/git/setup", func(w http.ResponseWriter, r *http.Request) {
		var q app.GitSetupInput
		if !s.decode(w, r, &q) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
		defer cancel()
		v, e := s.app.GitSetup(ctx, q)
		s.result(w, v, e)
	})
	s.mux.HandleFunc("GET /api/v2/git/assistance", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.app.GitAssistanceConfig(r.Context(), app.GitTarget{WorkspaceID: r.URL.Query().Get("workspaceId"), RepoRoot: r.URL.Query().Get("repoRoot")})
		s.result(w, v, e)
	})
	s.mux.HandleFunc("POST /api/v2/git/suggest", func(w http.ResponseWriter, r *http.Request) {
		var q app.GitSuggestionInput
		if !s.decode(w, r, &q) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 65*time.Second)
		defer cancel()
		v, e := s.app.GitSuggest(ctx, q)
		s.result(w, v, e)
	})
	s.mux.HandleFunc("GET /api/v2/git/repositories", s.gitWorkbenchRepositories)
	s.mux.HandleFunc("GET /api/v2/git/status", s.gitWorkbenchStatus)
	s.mux.HandleFunc("POST /api/v2/git/read", s.gitWorkbenchRead)
	s.mux.HandleFunc("POST /api/v2/git/actions", s.gitWorkbenchAction)
}
func (s *Server) gitWorkbenchRepositories(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	v, e := s.app.GitRepositories(ctx, r.URL.Query().Get("workspaceId"))
	s.result(w, v, e)
}
func (s *Server) gitWorkbenchStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	v, e := s.app.GitWorkbenchStatus(ctx, app.GitTarget{WorkspaceID: r.URL.Query().Get("workspaceId"), RepoRoot: r.URL.Query().Get("repoRoot")})
	s.result(w, v, e)
}
func (s *Server) gitWorkbenchRead(w http.ResponseWriter, r *http.Request) {
	var input app.GitReadRequest
	if !s.decode(w, r, &input) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	v, e := s.app.GitWorkbenchRead(ctx, input)
	s.result(w, v, e)
}
func (s *Server) gitWorkbenchAction(w http.ResponseWriter, r *http.Request) {
	var input app.GitWorkbenchCommand
	if !s.decode(w, r, &input) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	v, e := s.app.GitWorkbenchAction(ctx, input)
	if e != nil {
		message := security.Redact(e.Error())
		if v.Commit != "" {
			message = "Коммит " + v.Commit + " создан и сохранён; " + message
		}
		s.write(w, http.StatusConflict, map[string]any{"error": map[string]string{"message": message}, "result": v})
		return
	}
	s.result(w, v, nil)
}
