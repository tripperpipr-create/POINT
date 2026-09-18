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
