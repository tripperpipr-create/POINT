package httpapi

import (
	"net/http"

	"local-agent-workbench/internal/app"
	"local-agent-workbench/internal/domain"
)

func (s *Server) createWorkOrderV2(w http.ResponseWriter, r *http.Request) {
	var input domain.WorkOrder
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.SaveWorkOrderV2(r.Context(), input)
	s.result(w, value, err)
}

func (s *Server) getWorkOrderV2(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.WorkOrderV2(r.Context(), r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) listWorkOrderDiffsV2(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.WorkOrderDiffsV2(r.Context(), r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) reviseWorkOrderV2(w http.ResponseWriter, r *http.Request) {
	var input app.ReviseWorkOrderV2Request
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.ReviseWorkOrderV2(r.Context(), r.PathValue("id"), input)
	s.result(w, value, err)
}

func (s *Server) approveWorkOrderV2(w http.ResponseWriter, r *http.Request) {
	var input app.ApproveWorkOrderV2Request
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.ApproveWorkOrderV2(r.Context(), r.PathValue("id"), input)
	s.result(w, value, err)
}
