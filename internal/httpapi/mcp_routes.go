package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"local-agent-workbench/internal/app"
	"local-agent-workbench/internal/domain"
)

// MCP-серверы владельца (экран «Интеграции»). Проверка сервера может ждать
// первый запуск npx — отсюда длинный срок у probe.
func (s *Server) registerMCPRoutes() {
	s.mux.HandleFunc("GET /api/mcp/servers", s.listMCPServers)
	s.mux.HandleFunc("POST /api/mcp/servers", s.saveMCPServer)
	s.mux.HandleFunc("DELETE /api/mcp/servers/{id}", s.deleteMCPServer)
	s.mux.HandleFunc("POST /api/mcp/servers/{id}/trust", s.trustMCPServer)
	s.mux.HandleFunc("POST /api/mcp/servers/{id}/probe", s.probeMCPServer)
	s.mux.HandleFunc("POST /api/mcp/servers/{id}/stop", s.stopMCPServer)
	s.mux.HandleFunc("POST /api/mcp/servers/{id}/tools", s.setMCPTool)
	s.mux.HandleFunc("GET /api/mcp/servers/{id}/log", s.mcpServerLog)
	s.mux.HandleFunc("POST /api/mcp/import/preview", s.previewMCPImport)
	s.mux.HandleFunc("POST /api/mcp/secrets/unlock", s.unlockMCPSecrets)
}

func (s *Server) listMCPServers(w http.ResponseWriter, _ *http.Request) {
	value, err := s.app.ListMCPServers()
	s.result(w, map[string]any{"servers": value}, err)
}

func (s *Server) saveMCPServer(w http.ResponseWriter, r *http.Request) {
	var input app.MCPServerUpsert
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.SaveMCPServer(input)
	s.result(w, value, err)
}

func (s *Server) deleteMCPServer(w http.ResponseWriter, r *http.Request) {
	err := s.app.DeleteMCPServer(r.PathValue("id"))
	s.result(w, map[string]any{"ok": true}, err)
}

func (s *Server) trustMCPServer(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Digest string `json:"digest"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.TrustMCPServer(r.PathValue("id"), input.Digest)
	s.result(w, value, err)
}

func (s *Server) probeMCPServer(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	value, err := s.app.ProbeMCPServer(ctx, r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) stopMCPServer(w http.ResponseWriter, r *http.Request) {
	err := s.app.StopMCPServer(r.PathValue("id"))
	s.result(w, map[string]any{"ok": true}, err)
}

func (s *Server) setMCPTool(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name    string          `json:"name"`
		Enabled bool            `json:"enabled"`
		Risk    domain.ToolRisk `json:"risk"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.SetMCPTool(r.PathValue("id"), input.Name, input.Enabled, input.Risk)
	s.result(w, value, err)
}

func (s *Server) mcpServerLog(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.MCPServerLog(r.PathValue("id"))
	s.result(w, map[string]any{"log": value}, err)
}

// previewMCPImport принимает файл целиком как {"config": <mcp.json>}: сам
// конфиг — произвольный JSON, и строгий разбор тела (DisallowUnknownFields)
// к нему не применяется.
func (s *Server) previewMCPImport(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Config json.RawMessage `json:"config"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.PreviewMCPImport(input.Config)
	s.result(w, value, err)
}

func (s *Server) unlockMCPSecrets(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Values map[string]string `json:"values"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	err := s.app.UnlockMCPSecrets(input.Values)
	s.result(w, map[string]any{"ok": true}, err)
}
