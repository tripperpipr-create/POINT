package httpapi

import (
	"net/http"

	"local-agent-workbench/internal/orchestrator"
)

func (s *Server) generateReport(w http.ResponseWriter, r *http.Request) {
	var request orchestrator.ReportRequest
	if !s.decode(w, r, &request) {
		return
	}
	value, err := s.app.GenerateReport(r.Context(), request)
	s.result(w, value, err)
}
