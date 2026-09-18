package httpapi

import (
	"net/http"

	"local-agent-workbench/internal/app"
)

func (s *Server) registerDBRoutes() {
	s.mux.HandleFunc("POST /api/db-connections", s.saveDBConnection)
	s.mux.HandleFunc("DELETE /api/db-connections/{id}", s.deleteDBConnection)
	s.mux.HandleFunc("POST /api/db-connections/{id}/test", s.testDBConnection)
	s.mux.HandleFunc("POST /api/db-connections/{id}/query", s.queryDBConnection)
	s.mux.HandleFunc("POST /api/db-connections/{id}/schema", s.schemaDBConnection)
	s.mux.HandleFunc("POST /api/db-connections/unlock", s.unlockDBSecret)
}

func (s *Server) saveDBConnection(w http.ResponseWriter, r *http.Request) {
	var input app.DBConnectionUpsert
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.SaveDBConnection(input)
	s.result(w, value, err)
}

func (s *Server) deleteDBConnection(w http.ResponseWriter, r *http.Request) {
	err := s.app.DeleteDBConnection(r.PathValue("id"))
	s.result(w, map[string]any{"ok": true}, err)
}

func (s *Server) testDBConnection(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Password string `json:"password"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.TestDBConnection(r.PathValue("id"), input.Password)
	s.result(w, value, err)
}

func (s *Server) queryDBConnection(w http.ResponseWriter, r *http.Request) {
	var input app.DBQueryRequest
	if !s.decode(w, r, &input) {
		return
	}
	input.ConnectionID = r.PathValue("id")
	value, err := s.app.QueryDBConnection(input)
	s.result(w, value, err)
}

func (s *Server) schemaDBConnection(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Password string `json:"password"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.SchemaDBConnection(r.PathValue("id"), input.Password)
	s.result(w, value, err)
}

func (s *Server) unlockDBSecret(w http.ResponseWriter, r *http.Request) {
	var input app.DBUnlockRequest
	if !s.decode(w, r, &input) {
		return
	}
	err := s.app.UnlockDBSecret(input)
	s.result(w, map[string]any{"ok": true}, err)
}
