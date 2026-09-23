package httpapi

import (
	"local-agent-workbench/internal/domain"
	"net/http"
)

func (s *Server) masterDevelopment(w http.ResponseWriter, r *http.Request) {
	v, err := s.app.MasterDevelopment(r.Context())
	s.result(w, v, err)
}
func (s *Server) masterLearningUpdate(w http.ResponseWriter, r *http.Request) {
	var input domain.MasterLearningConfig
	if !s.decode(w, r, &input) {
		return
	}
	v, err := s.app.SetMasterLearning(r.Context(), input)
	s.result(w, v, err)
}
func (s *Server) masterSkillRollback(w http.ResponseWriter, r *http.Request) {
	v, err := s.app.RollbackMasterSkill(r.Context(), r.PathValue("id"))
	s.result(w, v, err)
}
