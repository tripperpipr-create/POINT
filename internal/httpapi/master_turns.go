package httpapi

import (
	"encoding/json"
	"fmt"
	"local-agent-workbench/internal/app"
	"net/http"
	"strconv"
	"time"
)

func (s *Server) masterStartTurn(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 30*1024*1024)
	var req app.MasterChatRequest
	if !s.decode(w, r, &req) {
		return
	}
	v, err := s.app.StartMasterTurn(r.Context(), req)
	s.result(w, v, err)
}
func (s *Server) masterStartTurnV2(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 2*1024*1024)
	var req app.MasterTurnV2Request
	if !s.decode(w, r, &req) {
		return
	}
	v, err := s.app.StartMasterTurnV2(r.Context(), req)
	s.result(w, v, err)
}
func (s *Server) masterCancelTurn(w http.ResponseWriter, r *http.Request) {
	err := s.app.CancelMasterTurn(r.Context(), r.PathValue("id"))
	s.result(w, map[string]bool{"ok": err == nil}, err)
}
func (s *Server) masterGetTurn(w http.ResponseWriter, r *http.Request) {
	v, err := s.app.MasterTurn(r.Context(), r.PathValue("id"))
	s.result(w, v, err)
}
func (s *Server) masterPage(w http.ResponseWriter, r *http.Request) {
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	v, err := s.app.MasterPage(r.Context(), r.PathValue("id"), before, r.URL.Query().Get("q"))
	s.result(w, v, err)
}
func (s *Server) masterEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	boundTurn, err := s.app.MasterTurn(r.Context(), id)
	if err != nil {
		s.result(w, nil, err)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.problem(w, 500, "stream_unsupported", "streaming is unavailable")
		return
	}
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	if last, e := strconv.ParseInt(r.Header.Get("Last-Event-ID"), 10, 64); e == nil && last > after {
		after = last
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		events, turn, err := s.app.MasterTurnStream(r.Context(), boundTurn, after)
		if err != nil {
			return
		}
		for _, e := range events {
			raw, _ := json.Marshal(e)
			fmt.Fprintf(w, "id: %d\nevent: master\ndata: %s\n\n", e.Sequence, raw)
			after = e.Sequence
		}
		if len(events) > 0 {
			flusher.Flush()
		}
		if len(events) < 256 && (turn.Status == "completed" || turn.Status == "failed" || turn.Status == "cancelled" || turn.Status == "interrupted") {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Server) masterFork(w http.ResponseWriter, r *http.Request) {
	var input struct {
		MessageID string `json:"messageId"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	v, err := s.app.ForkMasterConversation(r.Context(), r.PathValue("id"), input.MessageID)
	s.result(w, v, err)
}
func (s *Server) masterExport(w http.ResponseWriter, r *http.Request) {
	text, err := s.app.ExportMasterConversation(r.Context(), r.PathValue("id"))
	s.result(w, map[string]string{"markdown": text}, err)
}
