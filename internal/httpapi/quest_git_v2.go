package httpapi

import (
	"net/http"
	"os"

	"local-agent-workbench/internal/app"
)

type questGitActionRequest struct {
	Repo   string `json:"repo,omitempty"`
	APIKey string `json:"apiKey,omitempty"`
}

// runQuestGitActionV2 — git-действие квеста по кнопке человека: commit, push,
// merge_request, revert.
func (s *Server) runQuestGitActionV2(w http.ResponseWriter, r *http.Request) {
	var input questGitActionRequest
	if r.ContentLength != 0 && !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.RunQuestGitAction(r.Context(), r.PathValue("id"), r.PathValue("action"), input.Repo, "button", input.APIKey)
	if err != nil && value.Git != nil {
		s.write(w, http.StatusConflict, map[string]any{"error": err.Error(), "git": value.Git, "message": value.Message})
		return
	}
	s.result(w, value, err)
}

func (s *Server) inspectGitV2(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		s.write(w, http.StatusBadRequest, map[string]string{"error": "папка проекта недоступна"})
		return
	}
	value, err := s.app.InspectGitPath(r.Context(), path)
	s.result(w, value, err)
}

func (s *Server) questGitPolicy(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.QuestGitPolicy(r.Context())
	s.result(w, value, err)
}

func (s *Server) saveQuestGitPolicy(w http.ResponseWriter, r *http.Request) {
	var input app.QuestGitPolicy
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.SaveQuestGitPolicy(r.Context(), input)
	s.result(w, value, err)
}
