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

func (s *Server) masterChatBranchOffer(w http.ResponseWriter, r *http.Request) {
	var input struct { State string `json:"state"` }
	if !s.decode(w, r, &input) { return }
	err := s.app.SetMasterChatBranchOffer(r.Context(), r.PathValue("id"), input.State)
	s.result(w, map[string]string{"state": input.State}, err)
}

func (s *Server) masterChatBindBranch(w http.ResponseWriter, r *http.Request) {
	var input app.MasterChatBranchBindRequest
	if !s.decode(w, r, &input) { return }
	value, err := s.app.BindMasterChatBranch(r.Context(), r.PathValue("id"), input)
	s.result(w, value, err)
}
