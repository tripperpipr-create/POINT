package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"local-agent-workbench/internal/app"
	"local-agent-workbench/internal/forge"
	"local-agent-workbench/internal/security"
)

func (s *Server) registerForgeRoutes() {
	s.mux.HandleFunc("GET /api/v2/forge/connections", s.forgeConnections)
	s.mux.HandleFunc("PUT /api/v2/forge/connections", s.forgeSaveConnection)
	s.mux.HandleFunc("DELETE /api/v2/forge/connections/{id}", s.forgeDeleteConnection)
	s.mux.HandleFunc("POST /api/v2/forge/secrets/unlock", s.forgeUnlock)
	s.mux.HandleFunc("GET /api/v2/forge/bindings", s.forgeBindings)
	s.mux.HandleFunc("PUT /api/v2/forge/bindings", s.forgeSaveBinding)
	s.mux.HandleFunc("POST /api/v2/forge/request", s.forgeRequest)
}
func (s *Server) forgeConnections(w http.ResponseWriter, r *http.Request) {
	v, e := s.app.ForgeConnections(r.Context())
	s.result(w, v, e)
}
func (s *Server) forgeSaveConnection(w http.ResponseWriter, r *http.Request) {
	var q app.ForgeConnectionInput
	if !s.decode(w, r, &q) {
		return
	}
	v, e := s.app.SaveForgeConnection(r.Context(), q)
	s.result(w, v, e)
}
func (s *Server) forgeDeleteConnection(w http.ResponseWriter, r *http.Request) {
	e := s.app.DeleteForgeConnection(r.Context(), r.PathValue("id"))
	s.result(w, map[string]bool{"deleted": e == nil}, e)
}
func (s *Server) forgeUnlock(w http.ResponseWriter, r *http.Request) {
	var q struct {
		Values map[string]string `json:"values"`
	}
	if !s.decode(w, r, &q) {
		return
	}
	e := s.app.UnlockForgeSecrets(r.Context(), q.Values)
	s.result(w, map[string]bool{"unlocked": e == nil}, e)
}
func (s *Server) forgeBindings(w http.ResponseWriter, r *http.Request) {
	v, e := s.app.ForgeBindings(r.Context(), app.GitTarget{WorkspaceID: r.URL.Query().Get("workspaceId"), RepoRoot: r.URL.Query().Get("repoRoot")})
	s.result(w, v, e)
}
func (s *Server) forgeSaveBinding(w http.ResponseWriter, r *http.Request) {
	var q forge.Binding
	if !s.decode(w, r, &q) {
		return
	}
	v, e := s.app.SaveForgeBinding(r.Context(), q)
	s.result(w, v, e)
}
func (s *Server) forgeRequest(w http.ResponseWriter, r *http.Request) {
	var q forge.Request
	if !s.decode(w, r, &q) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 35*time.Second)
	defer cancel()
	v, e := s.app.RunForgeRequest(ctx, q)
	if e != nil {
		reason := "request_failed"
		var issue *forge.Error
		if errors.As(e, &issue) {
			reason = issue.Reason
		}
		s.write(w, http.StatusOK, map[string]any{"state": "error", "reason": reason, "problem": security.Redact(e.Error()), "uncertain": v.Uncertain})
		return
	}
	s.write(w, http.StatusOK, map[string]any{"state": "ok", "response": v})
}
