// Мастер: разговор, каталог бесед, отзывы на сообщения.
package httpapi

import (
	"net/http"

	"local-agent-workbench/internal/app"
)

func (s *Server) masterHistory(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.MasterSessionHistory(r.Context(), r.URL.Query().Get("conversationId"), r.URL.Query().Get("full") == "1")
	s.result(w, value, err)
}

func (s *Server) masterDirectory(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.MasterChatDirectory(r.Context())
	s.result(w, value, err)
}

func (s *Server) masterChat(w http.ResponseWriter, r *http.Request) {
	var input app.MasterChatRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.MasterChat(r.Context(), input)
	s.result(w, value, err)
}

// Оценка реплики Мастера. Отдельный маршрут, а не поле хода: оценивают уже
// сказанное, иногда через день, и связывать это с отправкой новой реплики
// значило бы требовать разговора ради отметки.
func (s *Server) masterMessageFeedback(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Value string `json:"value"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	err := s.app.MasterMessageFeedback(r.Context(), r.PathValue("id"), input.Value)
	s.result(w, map[string]any{"ok": err == nil}, err)
}
