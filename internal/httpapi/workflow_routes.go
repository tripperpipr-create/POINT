// Linear workflows — совместимый контур рядом с потоками.
package httpapi

import (
	"net/http"

	"local-agent-workbench/internal/app"
	"local-agent-workbench/internal/domain"
)

func (s *Server) saveWorkflow(w http.ResponseWriter, r *http.Request) {
	var input domain.AgentWorkflow
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.SaveWorkflow(input)
	s.result(w, value, err)
}

func (s *Server) validateWorkflow(w http.ResponseWriter, r *http.Request) {
	var input domain.AgentWorkflow
	if !s.decode(w, r, &input) {
		return
	}
	err := s.app.ValidateWorkflow(input)
	if err != nil {
		s.problem(w, http.StatusBadRequest, "validation_failed", err.Error())
		return
	}
	s.write(w, http.StatusOK, map[string]bool{"valid": true})
}

func (s *Server) deleteWorkflow(w http.ResponseWriter, r *http.Request) {
	if err := s.app.DeleteWorkflow(r.PathValue("id")); err != nil {
		s.problem(w, http.StatusConflict, "delete_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) workflowRuns(w http.ResponseWriter, _ *http.Request) {
	value, err := s.app.WorkflowRuns()
	s.result(w, value, err)
}

func (s *Server) startWorkflow(w http.ResponseWriter, r *http.Request) {
	var input app.StartWorkflowRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.StartWorkflow(input)
	s.result(w, value, err)
}

func (s *Server) workflowRunDetails(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.WorkflowRunDetails(r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) cancelWorkflowRun(w http.ResponseWriter, r *http.Request) {
	if err := s.app.CancelWorkflowRun(r.PathValue("id")); err != nil {
		s.problem(w, http.StatusConflict, "cancel_failed", err.Error())
		return
	}
	s.write(w, http.StatusOK, map[string]bool{"cancelled": true})
}

func (s *Server) claimWorkflowStep(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.ClaimWorkflowStep(r.PathValue("id"), r.PathValue("stepId"))
	s.result(w, value, err)
}

func (s *Server) heartbeatWorkflowStep(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ClaimToken string `json:"claimToken"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	if err := s.app.HeartbeatWorkflowStep(r.PathValue("id"), r.PathValue("stepId"), input.ClaimToken); err != nil {
		s.problem(w, http.StatusConflict, "heartbeat_failed", err.Error())
		return
	}
	s.write(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) completeWorkflowStep(w http.ResponseWriter, r *http.Request) {
	var input app.WorkflowStepCompletion
	if !s.decode(w, r, &input) {
		return
	}
	if err := s.app.CompleteWorkflowStep(r.PathValue("id"), r.PathValue("stepId"), input); err != nil {
		s.problem(w, http.StatusConflict, "completion_failed", err.Error())
		return
	}
	s.write(w, http.StatusOK, map[string]bool{"accepted": true})
}

func (s *Server) compileWorkflow(w http.ResponseWriter, r *http.Request) {
	var input struct {
		WorkflowID string `json:"workflowId"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.CompileWorkflowToFlow(input.WorkflowID)
	s.result(w, value, err)
}
