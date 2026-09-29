package httpapi

import (
	"net/http"

	"local-agent-workbench/internal/domain"
)

func (s *Server) globalModels(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.GlobalModelDefaults(r.Context())
	s.result(w, value, err)
}

func (s *Server) saveGlobalModels(w http.ResponseWriter, r *http.Request) {
	var input domain.GlobalModelDefaults
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.SaveGlobalModelDefaults(r.Context(), input)
	s.result(w, value, err)
}
