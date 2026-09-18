// Разбор запроса и формат ответа — одинаковые для всех обработчиков.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"local-agent-workbench/internal/domain"
)

type appProfile struct{ domain.AgentProfile }

func (s *Server) decode(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		s.problem(w, 400, "invalid_json", err.Error())
		return false
	}
	return true
}

func (s *Server) result(w http.ResponseWriter, value any, err error) {
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, context.Canceled) {
			status = 499
		}
		s.problem(w, status, "request_failed", err.Error())
		return
	}
	s.write(w, http.StatusOK, value)
}

func (s *Server) write(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (s *Server) problem(w http.ResponseWriter, status int, code, message string) {
	if rec, ok := w.(*statusRecorder); ok {
		rec.errCode = code
		rec.errMsg = message
	}
	s.write(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
