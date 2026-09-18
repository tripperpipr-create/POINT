package httpapi

import "net/http"

func (s *Server) modelCandidates(w http.ResponseWriter, _ *http.Request) {
	value, err := s.app.ModelCandidates()
	s.result(w, value, err)
}

func (s *Server) modelCapabilityEvidence(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.ModelCapabilityEvidence(r.URL.Query().Get("connectionId"), r.URL.Query().Get("model"))
	s.result(w, value, err)
}

func (s *Server) agentPrepChains(w http.ResponseWriter, _ *http.Request) {
	value, err := s.app.ListAgentPrepChains()
	s.result(w, value, err)
}

func (s *Server) teamEvents(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.ListTeamEvents(r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) reconcileProjectController(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.ReconcileProjectController(r.Context(), r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) resumeExternalExecution(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.ResumeExternalCLIExecution(r.Context(), r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) stopExternalExecution(w http.ResponseWriter, r *http.Request) {
	s.result(w, map[string]bool{"stopped": true}, s.app.StopExternalCLIExecution(r.PathValue("id")))
}
