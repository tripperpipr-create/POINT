// Гильдия: чертежи, проектные агенты, навыки, отряды, память.
package httpapi

import (
	"net/http"

	"local-agent-workbench/internal/connections"
	"local-agent-workbench/internal/domain"
)

func (s *Server) saveConnection(w http.ResponseWriter, r *http.Request) {
	var input connections.UpsertRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.SaveConnection(input)
	s.result(w, value, err)
}

func (s *Server) saveBlueprint(w http.ResponseWriter, r *http.Request) {
	var input domain.AgentBlueprint
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.SaveBlueprint(input)
	s.result(w, value, err)
}

func (s *Server) previewCompiledPrompt(w http.ResponseWriter, r *http.Request) {
	var input domain.ProjectAgent
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.PreviewCompiledPrompt(input)
	s.result(w, value, err)
}

func (s *Server) saveProjectAgent(w http.ResponseWriter, r *http.Request) {
	var input domain.ProjectAgent
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.SaveProjectAgent(input)
	s.result(w, value, err)
}

func (s *Server) applyBlueprintToAgent(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.ApplyBlueprintToProjectAgent(r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) updateBlueprintFromAgent(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.UpdateBlueprintFromProjectAgent(r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) diffProjectAgent(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.DiffProjectAgentBlueprint(r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) saveSkill(w http.ResponseWriter, r *http.Request) {
	var input domain.SkillDefinition
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.SaveSkill(input)
	s.result(w, value, err)
}

func (s *Server) equipSkill(w http.ResponseWriter, r *http.Request) {
	var input struct {
		WorkspaceID   string         `json:"workspaceId"`
		SkillID       string         `json:"skillId"`
		Configuration map[string]any `json:"configuration"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.EquipSkill(input.WorkspaceID, input.SkillID, input.Configuration)
	s.result(w, value, err)
}

func (s *Server) previewSkillEquip(w http.ResponseWriter, r *http.Request) {
	var input struct {
		WorkspaceID string `json:"workspaceId"`
		SkillID     string `json:"skillId"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.PreviewSkillEquip(input.WorkspaceID, input.SkillID)
	s.result(w, value, err)
}

func (s *Server) saveTeam(w http.ResponseWriter, r *http.Request) {
	var input domain.Team
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.SaveTeam(input)
	s.result(w, value, err)
}

func (s *Server) saveQuest(w http.ResponseWriter, r *http.Request) {
	var input domain.Quest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.SaveQuest(input)
	s.result(w, value, err)
}

func (s *Server) deleteQuest(w http.ResponseWriter, r *http.Request) {
	if err := s.app.DeleteQuest(r.PathValue("id")); err != nil {
		s.problem(w, 400, "delete_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteTeam(w http.ResponseWriter, r *http.Request) {
	if err := s.app.DeleteTeam(r.PathValue("id")); err != nil {
		s.problem(w, 400, "delete_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteWorkOrderV2(w http.ResponseWriter, r *http.Request) {
	if err := s.app.DeleteWorkOrderV2(r.Context(), r.PathValue("id")); err != nil {
		s.problem(w, 400, "delete_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) saveMemory(w http.ResponseWriter, r *http.Request) {
	var input domain.MemoryRecord
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.SaveMemory(input)
	s.result(w, value, err)
}

func (s *Server) deleteMemory(w http.ResponseWriter, r *http.Request) {
	err := s.app.DeleteMemory(r.PathValue("id"))
	s.result(w, map[string]bool{"deleted": err == nil}, err)
}
