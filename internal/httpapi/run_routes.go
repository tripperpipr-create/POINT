// Прогон агента: запуск, ход, правки на лету, разрешения.
package httpapi

import (
	"net/http"

	"local-agent-workbench/internal/app"
	"local-agent-workbench/internal/domain"
)

func (s *Server) runs(w http.ResponseWriter, _ *http.Request) {
	value, err := s.app.Runs()
	s.result(w, value, err)
}

func (s *Server) previewRun(w http.ResponseWriter, r *http.Request) {
	var input app.AgentRunPreviewRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.PreviewAgentRun(input)
	s.result(w, value, err)
}

func (s *Server) startRun(w http.ResponseWriter, r *http.Request) {
	var input app.StartRunRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.StartRun(input)
	s.result(w, value, err)
}

func (s *Server) startFastAgent(w http.ResponseWriter, r *http.Request) {
	var input app.FastAgentRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.StartFastAgent(input)
	s.result(w, value, err)
}

func (s *Server) undoRunPatches(w http.ResponseWriter, r *http.Request) {
	var input app.UndoRunRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.UndoRunPatches(r.PathValue("id"), input)
	s.result(w, value, err)
}

func (s *Server) runDetails(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.RunDetails(r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) cancelRun(w http.ResponseWriter, r *http.Request) {
	err := s.app.CancelRun(r.PathValue("id"))
	if err != nil {
		s.problem(w, 409, "cancel_failed", err.Error())
		return
	}
	s.write(w, 200, map[string]bool{"cancelled": true})
}

func (s *Server) pauseRun(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.PauseRun(r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) resumeRun(w http.ResponseWriter, r *http.Request) {
	var input app.ResumeRunRequest
	if r.ContentLength != 0 {
		if !s.decode(w, r, &input) {
			return
		}
	}
	value, err := s.app.ResumeRun(r.PathValue("id"), input)
	s.result(w, value, err)
}

func (s *Server) extendActiveTime(w http.ResponseWriter, r *http.Request) {
	var input struct {
		APIKey string `json:"apiKey,omitempty"`
	}
	if r.ContentLength != 0 {
		if !s.decode(w, r, &input) {
			return
		}
	}
	value, err := s.app.ExtendActiveTime(r.PathValue("id"), input.APIKey)
	s.result(w, value, err)
}

func (s *Server) injectRunMessage(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Message        string `json:"message"`
		LearningIntent string `json:"learningIntent,omitempty"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	if err := s.app.InjectRunMessage(r.PathValue("id"), input.Message, input.LearningIntent); err != nil {
		s.problem(w, http.StatusConflict, "message_failed", err.Error())
		return
	}
	s.write(w, http.StatusOK, map[string]bool{"queued": true})
}

func (s *Server) forbidRunFile(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Path string `json:"path"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	if err := s.app.ForbidRunFile(r.PathValue("id"), input.Path); err != nil {
		s.problem(w, http.StatusConflict, "forbid_failed", err.Error())
		return
	}
	s.write(w, http.StatusOK, map[string]bool{"forbidden": true})
}

func (s *Server) amendRunContext(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Action string `json:"action"`
		ItemID string `json:"itemId"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	if err := s.app.AmendRunContext(r.PathValue("id"), domain.ContextAmendAction(input.Action), input.ItemID); err != nil {
		s.problem(w, http.StatusConflict, "context_amend_failed", err.Error())
		return
	}
	s.write(w, http.StatusOK, map[string]bool{"queued": true})
}

func (s *Server) addRunContext(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ContextItems []domain.RunContextInput `json:"contextItems"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	preview, err := s.app.AddRunContext(r.PathValue("id"), input.ContextItems)
	if err != nil {
		s.problem(w, http.StatusConflict, "context_add_failed", err.Error())
		return
	}
	s.write(w, http.StatusAccepted, preview)
}

func (s *Server) runContextInspector(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.RunContextInspector(r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) resolveApproval(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Allow bool `json:"allow"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	if err := s.app.ResolveApproval(r.PathValue("id"), input.Allow); err != nil {
		s.problem(w, 409, "approval_failed", err.Error())
		return
	}
	s.write(w, 200, map[string]bool{"resolved": true})
}

func (s *Server) revertPatch(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.RevertPatch(r.PathValue("id"))
	s.result(w, value, err)
}
