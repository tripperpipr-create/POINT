package httpapi

import (
	"local-agent-workbench/internal/app"
	"net/http"
	"strings"
)

func (s *Server) masterScopeHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("workspaceId")
		if id != "" && (strings.HasPrefix(r.URL.Path, "/api/master/") || strings.HasPrefix(r.URL.Path, "/api/v2/master/") || strings.HasPrefix(r.URL.Path, "/api/v2/work-orders") || strings.HasPrefix(r.URL.Path, "/api/v2/sources")) {
			ctx, err := s.app.WithMasterWorkspace(r.Context(), id)
			if err != nil {
				s.result(w, nil, err)
				return
			}
			r = r.WithContext(ctx)
		}
		next.ServeHTTP(w, r)
	})
}
func (s *Server) getFastAgentConfig(w http.ResponseWriter, r *http.Request) {
	v, e := s.app.FastAgentConfig(r.Context())
	s.result(w, v, e)
}
func (s *Server) saveFastAgentConfig(w http.ResponseWriter, r *http.Request) {
	var v app.FastAgentConfig
	if !s.decode(w, r, &v) {
		return
	}
	v, e := s.app.SaveFastAgentConfig(r.Context(), v)
	s.result(w, v, e)
}
func (s *Server) masterFiles(w http.ResponseWriter, r *http.Request) {
	v, e := s.app.MasterFiles(r.Context())
	s.result(w, v, e)
}
func (s *Server) continuePointChat(w http.ResponseWriter, r *http.Request) {
	var input struct {
		WorkspaceID string `json:"workspaceId"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	v, e := s.app.ContinuePointChat(r.Context(), r.PathValue("id"), input.WorkspaceID)
	s.result(w, v, e)
}
