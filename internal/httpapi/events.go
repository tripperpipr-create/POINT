// Поток событий ядра для интерфейса.
package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	workspaceID := s.app.CurrentWorkspaceID()
	if workspaceID == "" {
		s.problem(w, 409, "workspace_required", "open a workspace before subscribing to events")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.problem(w, 500, "stream_unsupported", "streaming is unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-store")
	w.Header().Set("Connection", "keep-alive")
	eventStream, cancel := s.eventHub.SubscribeWorkspace(workspaceID)
	defer cancel()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	fmt.Fprint(w, "event: ready\ndata: {}\n\n")
	flusher.Flush()
	for {
		select {
		case event, open := <-eventStream:
			if !open {
				return
			}
			data, _ := json.Marshal(event)
			fmt.Fprintf(w, "event: workbench\ndata: %s\n\n", data)
			flusher.Flush()
		case <-heartbeat.C:
			fmt.Fprint(w, ": heartbeat\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}
