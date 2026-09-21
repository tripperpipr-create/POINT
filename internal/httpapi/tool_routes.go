// Инструменты и профили: свои инструменты, разрешения на запуск, пробы провайдера.
package httpapi

import (
	"net/http"

	"local-agent-workbench/internal/app"
	"local-agent-workbench/internal/domain"
)

func (s *Server) saveProfile(w http.ResponseWriter, r *http.Request) {
	var input appProfile
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.SaveProfile(input.AgentProfile)
	s.result(w, value, err)
}

func (s *Server) deleteProfile(w http.ResponseWriter, r *http.Request) {
	err := s.app.DeleteProfile(r.PathValue("id"))
	if err != nil {
		s.problem(w, 400, "delete_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteBlueprint(w http.ResponseWriter, r *http.Request) {
	if err := s.app.DeleteBlueprint(r.PathValue("id")); err != nil {
		s.problem(w, 400, "delete_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteProjectAgent(w http.ResponseWriter, r *http.Request) {
	if err := s.app.DeleteProjectAgent(r.PathValue("id")); err != nil {
		s.problem(w, 400, "delete_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) activateProjectAgentDraft(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.ActivateProjectAgentDraft(r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) rejectProjectAgentDraft(w http.ResponseWriter, r *http.Request) {
	var input struct {
		APIKey string `json:"apiKey,omitempty"`
	}
	if r.ContentLength > 0 && !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.RejectProjectAgentDraft(r.PathValue("id"), input.APIKey)
	s.result(w, value, err)
}

func (s *Server) saveCustomTool(w http.ResponseWriter, r *http.Request) {
	var input domain.CustomTool
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.SaveCustomTool(input)
	s.result(w, value, err)
}

func (s *Server) previewCustomTool(w http.ResponseWriter, r *http.Request) {
	var input app.CustomToolPreviewRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.PreviewCustomTool(input)
	s.result(w, value, err)
}

func (s *Server) previewTool(w http.ResponseWriter, r *http.Request) {
	var input app.ToolExecutionRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.PreviewTool(input)
	s.result(w, value, err)
}

func (s *Server) requestToolExecutionApproval(w http.ResponseWriter, r *http.Request) {
	var input app.ToolExecutionApprovalRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.RequestToolExecutionApproval(input)
	s.result(w, value, err)
}

func (s *Server) resolveToolExecutionApproval(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Allow bool `json:"allow"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.ResolveToolExecutionApproval(r.PathValue("id"), input.Allow)
	s.result(w, value, err)
}

func (s *Server) executeTool(w http.ResponseWriter, r *http.Request) {
	var input app.ToolExecutionRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.ExecuteTool(input)
	s.result(w, value, err)
}

func (s *Server) deleteCustomTool(w http.ResponseWriter, r *http.Request) {
	if err := s.app.DeleteCustomTool(r.PathValue("id")); err != nil {
		s.problem(w, http.StatusConflict, "delete_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) probeProvider(w http.ResponseWriter, r *http.Request) {
	var input app.ProviderProbeRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.ProbeProvider(input)
	s.result(w, value, err)
}

func (s *Server) probeModelCapability(w http.ResponseWriter, r *http.Request) {
	var input app.ModelCapabilityProbeRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.ProbeModelCapability(input)
	s.result(w, value, err)
}
