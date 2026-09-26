package httpapi

import (
	"net/http"

	"local-agent-workbench/internal/app"
)

func (s *Server) controlWorkOrderQuestV2(w http.ResponseWriter, r *http.Request) {
	var input app.WorkOrderQuestControlRequest
	if r.ContentLength != 0 && !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.ControlWorkOrderQuestV2(r.Context(), r.PathValue("id"), r.PathValue("action"), input)
	s.result(w, value, err)
}

func (s *Server) getWorkOrderQuestV2(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.WorkOrderQuestV2(r.Context(), r.PathValue("id"))
	s.result(w, value, err)
}

// reviewManualCriterionV2 records a human decision on one manual criterion.
func (s *Server) reviewManualCriterionV2(w http.ResponseWriter, r *http.Request) {
	var input app.ManualCriterionReviewRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.ReviewManualCriterionV2(r.Context(), r.PathValue("id"), r.PathValue("criterionId"), input)
	s.result(w, value, err)
}
