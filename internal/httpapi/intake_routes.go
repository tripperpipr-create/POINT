// Приёмка задачи: заявка, доказательства, расширение, утверждение.
package httpapi

import (
	"net/http"

	"local-agent-workbench/internal/app"
)

func (s *Server) createIntake(w http.ResponseWriter, r *http.Request) {
	var input app.CreateIntakeRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.CreateIntake(r.Context(), input)
	s.result(w, value, err)
}

func (s *Server) listIntakes(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.ListIntakeSessions(r.Context())
	s.result(w, value, err)
}

func (s *Server) getIntake(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.IntakeSession(r.Context(), r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) approveIntake(w http.ResponseWriter, r *http.Request) {
	var input app.ApproveIntakeRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.ApproveIntake(r.Context(), r.PathValue("id"), input)
	s.result(w, value, err)
}

func (s *Server) intakeEvidence(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.IntakeEvidence(r.Context(), r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) expandIntake(w http.ResponseWriter, r *http.Request) {
	var input app.ExpandIntakeRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.ExpandIntake(r.Context(), r.PathValue("id"), input)
	s.result(w, value, err)
}

func (s *Server) questEvidenceBundle(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.EvidenceBundle(r.Context(), r.PathValue("id"))
	s.result(w, value, err)
}
