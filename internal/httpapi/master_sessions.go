package httpapi

import (
	"local-agent-workbench/internal/app"
	"net/http"
)

func (s *Server) masterSessionUpdate(w http.ResponseWriter, r *http.Request) {
	var input app.MasterSessionUpdate
	if !s.decode(w, r, &input) {
		return
	}
	_, err := s.app.UpdateMasterSession(r.Context(), input)
	if err != nil {
		s.result(w, nil, err)
		return
	}
	value, err := s.app.MasterHistory(r.Context())
	s.result(w, value, err)
}
