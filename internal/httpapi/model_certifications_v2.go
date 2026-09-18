package httpapi

import (
	"net/http"
	"strings"
)

func (s *Server) modelCertificationsV2(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.ModelCertificationsV2(r.URL.Query().Get("connectionId"), r.URL.Query().Get("model"))
	s.result(w, value, err)
}

func (s *Server) connectionCapabilitiesV2(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	connections, err := s.app.ListConnections()
	if err != nil {
		s.result(w, nil, err)
		return
	}
	for _, connection := range connections {
		if connection.ID != id {
			continue
		}
		certifications, certErr := s.app.ModelCertificationsV2(id, "")
		if certErr != nil {
			s.result(w, nil, certErr)
			return
		}
		s.write(w, http.StatusOK, map[string]any{
			"connectionId":   connection.ID,
			"provider":       connection.Provider,
			"status":         connection.Status,
			"models":         connection.Models,
			"certifications": certifications,
		})
		return
	}
	http.Error(w, "connection not found", http.StatusNotFound)
}
