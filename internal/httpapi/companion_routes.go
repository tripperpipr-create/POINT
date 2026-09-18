// Компаньон: чат в IDE, вмешательства, наблюдения редактора, настройка.
package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"local-agent-workbench/internal/app"
	"local-agent-workbench/internal/companion"
	"local-agent-workbench/internal/domain"
)

func (s *Server) companionLive(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.CompanionLive(r.URL.Query().Get("focusPath"))
	s.result(w, value, err)
}

func (s *Server) companionChat(w http.ResponseWriter, r *http.Request) {
	var input companion.ChatRequest
	if !s.decode(w, r, &input) {
		return
	}
	stream := r.URL.Query().Get("stream") == "1" || strings.Contains(r.Header.Get("Accept"), "application/x-ndjson")
	if !stream {
		value, err := s.app.CompanionChat(r.Context(), input)
		s.result(w, value, err)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(200)
	flusher, _ := w.(http.Flusher)
	enc := json.NewEncoder(w)
	writeLine := func(payload any) {
		_ = enc.Encode(payload)
		if flusher != nil {
			flusher.Flush()
		}
	}
	input.OnProgress = func(step, status string) {
		writeLine(map[string]any{"type": "progress", "step": step, "status": status})
	}
	input.OnDelta = func(reply string) {
		writeLine(map[string]any{"type": "delta", "reply": reply})
	}
	value, err := s.app.CompanionChat(r.Context(), input)
	if err != nil {
		writeLine(map[string]any{"type": "error", "error": map[string]any{"message": err.Error()}})
		return
	}
	writeLine(map[string]any{"type": "result", "response": value})
}

func (s *Server) companionPropose(w http.ResponseWriter, r *http.Request) {
	var input companion.RecommendRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.CompanionPropose(input)
	s.result(w, value, err)
}

func (s *Server) clearCompanionHistory(w http.ResponseWriter, _ *http.Request) {
	err := s.app.ClearCompanionHistory()
	s.result(w, map[string]bool{"cleared": err == nil}, err)
}

func (s *Server) companionHistory(w http.ResponseWriter, r *http.Request) {
	limit := 80
	if value, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && value > 0 && value <= 200 {
		limit = value
	}
	value, err := s.app.CompanionHistory(r.Context(), r.URL.Query().Get("speaker"), limit)
	s.result(w, value, err)
}

func (s *Server) dismissCompanionIntervention(w http.ResponseWriter, r *http.Request) {
	var input struct {
		OccurrenceKey string `json:"occurrenceKey"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	err := s.app.DismissCompanionIntervention(r.PathValue("id"), input.OccurrenceKey)
	s.result(w, map[string]bool{"dismissed": err == nil}, err)
}

func (s *Server) restoreCompanionInterventions(w http.ResponseWriter, _ *http.Request) {
	err := s.app.RestoreCompanionInterventions()
	s.result(w, map[string]bool{"restored": err == nil}, err)
}

func (s *Server) decideCompanionAction(w http.ResponseWriter, r *http.Request) {
	var input app.CompanionActionDecision
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.DecideCompanionAction(input)
	s.result(w, value, err)
}

func (s *Server) recordIDEObservations(w http.ResponseWriter, r *http.Request) {
	var input app.IDEObservationBatch
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.RecordIDEObservations(input)
	s.result(w, value, err)
}

func (s *Server) saveCompanionConfig(w http.ResponseWriter, r *http.Request) {
	var input domain.CompanionConfig
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.SaveCompanionConfig(input)
	s.result(w, value, err)
}
