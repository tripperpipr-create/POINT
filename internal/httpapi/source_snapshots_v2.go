package httpapi

import (
	"net/http"

	"local-agent-workbench/internal/app"
)

func (s *Server) previewSourceV2(w http.ResponseWriter, r *http.Request) {
	var input app.PreviewSourceV2Request
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.PreviewSourceV2(r.Context(), input)
	s.result(w, value, err)
}

func (s *Server) getSourceSnapshotV2(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.SourceSnapshotV2(r.Context(), r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) refreshSourceSnapshotV2(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.RefreshSourceV2(r.Context(), r.PathValue("id"))
	s.result(w, value, err)
}
