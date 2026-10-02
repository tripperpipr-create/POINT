package httpapi

import (
	"local-agent-workbench/internal/app"
	"net/http"
)

func (s *Server) reviewSandboxAudit(w http.ResponseWriter, r *http.Request) {
	var input app.SandboxAuditReviewRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.ReviewSandboxAudit(r.Context(), r.PathValue("id"), input)
	s.result(w, value, err)
}
