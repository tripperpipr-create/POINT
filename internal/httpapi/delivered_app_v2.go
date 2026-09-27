package httpapi

import (
	"net/http"

	"local-agent-workbench/internal/app"
)

func (s *Server) controlDeliveredApplicationV2(w http.ResponseWriter, r *http.Request) {
	var request app.DeliveredApplicationControlRequestV2
	if !s.decode(w, r, &request) {
		return
	}
	value, err := s.app.ControlDeliveredApplicationV2(r.Context(), r.PathValue("id"), r.PathValue("action"), request)
	s.result(w, value, err)
}

// deliveredApplicationStateV2 is polled by the Hub while start/stop runs:
// kind, launch method, live output and, with probe=1, running containers.
func (s *Server) deliveredApplicationStateV2(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.DeliveredApplicationStateV2(r.Context(), r.PathValue("id"), r.URL.Query().Get("probe") == "1")
	s.result(w, value, err)
}
