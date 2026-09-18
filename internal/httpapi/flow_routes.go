// Поток и наборы изменений: узлы, песочницы, применение и откат.
package httpapi

import (
	"net/http"

	"local-agent-workbench/internal/app"
	"local-agent-workbench/internal/changesets"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/sandbox"
)

func (s *Server) deleteFlow(w http.ResponseWriter, r *http.Request) {
	if err := s.app.DeleteFlow(r.PathValue("id")); err != nil {
		s.problem(w, 400, "delete_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) saveFlow(w http.ResponseWriter, r *http.Request) {
	var input domain.FlowGraph
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.SaveFlow(input)
	s.result(w, value, err)
}

func (s *Server) getFlow(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.GetFlow(r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) startFlowRun(w http.ResponseWriter, r *http.Request) {
	var input struct {
		FlowID  string         `json:"flowId"`
		QuestID string         `json:"questId"`
		Input   map[string]any `json:"input"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.StartFlowRun(input.FlowID, input.QuestID, input.Input)
	s.result(w, value, err)
}

func (s *Server) tickFlowRun(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.TickFlowRun(r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) resolveFlowNode(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Approved bool `json:"approved"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.ResumeFlowApproval(r.PathValue("id"), r.PathValue("nodeId"), input.Approved)
	s.result(w, value, err)
}

func (s *Server) resolveFlowSandboxMerge(w http.ResponseWriter, r *http.Request) {
	var input sandbox.MergeResolution
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.ResolveFlowSandboxMerge(r.PathValue("id"), r.PathValue("nodeId"), input)
	s.result(w, value, err)
}

func (s *Server) startSandboxedExecution(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ProjectAgentID string `json:"projectAgentId"`
		Task           string `json:"task"`
		QuestID        string `json:"questId"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.StartSandboxedExecution(input.ProjectAgentID, input.Task, input.QuestID)
	s.result(w, value, err)
}

func (s *Server) launchPendingExecution(w http.ResponseWriter, r *http.Request) {
	var input struct {
		APIKey string `json:"apiKey"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.LaunchPendingExecution(r.PathValue("id"), input.APIKey)
	s.result(w, value, err)
}

func (s *Server) buildChangeSet(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.BuildChangeSet(r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) revertExecution(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.RevertExecution(r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) revertQuest(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.RevertQuest(r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) revertFlowNode(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.RevertFlowNode(r.PathValue("id"), r.PathValue("nodeId"))
	s.result(w, value, err)
}

func (s *Server) applyChangeSet(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.ApplyChangeSet(r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) rejectChangeSet(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.RejectChangeSet(r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) revertChangeSet(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.RevertChangeSet(r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) resolveChangeSet(w http.ResponseWriter, r *http.Request) {
	var input changesets.ResolveRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.ResolveChangeSetConflict(r.PathValue("id"), input)
	s.result(w, value, err)
}

func (s *Server) beginCursorExecution(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.BeginCursorExecution(r.Context(), r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) completeCursorExecution(w http.ResponseWriter, r *http.Request) {
	var input app.CursorExecutionCompletion
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.CompleteCursorExecution(r.Context(), r.PathValue("id"), input)
	s.result(w, value, err)
}
