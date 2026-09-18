package httpapi

import (
	"net/http"

	"local-agent-workbench/internal/app"
)

func (s *Server) getWorkspaceModelRouting(w http.ResponseWriter, _ *http.Request) {
	value, err := s.app.GetWorkspaceModelRouting()
	s.result(w, value, err)
}

func (s *Server) saveWorkspaceModelRouting(w http.ResponseWriter, r *http.Request) {
	var input app.WorkspaceModelRouting
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.SaveWorkspaceModelRouting(input)
	s.result(w, value, err)
}
