// Инфраструктура: Docker и серверные профили.
package httpapi

import (
	"net/http"
	"strconv"

	"local-agent-workbench/internal/app"
	"local-agent-workbench/internal/servers"
)

func (s *Server) dockerOverview(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.DockerOverview()
	s.result(w, value, err)
}

func (s *Server) dockerLogs(w http.ResponseWriter, r *http.Request) {
	tail := 100
	if raw := r.URL.Query().Get("tail"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			tail = parsed
		}
	}
	value, err := s.app.DockerContainerLogs(r.URL.Query().Get("container"), tail)
	s.result(w, value, err)
}

func (s *Server) dockerContainerAction(w http.ResponseWriter, r *http.Request) {
	var input app.DockerContainerActionRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.DockerContainerAction(input)
	s.result(w, value, err)
}

func (s *Server) saveServerProfile(w http.ResponseWriter, r *http.Request) {
	var input servers.UpsertRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.SaveServerProfile(input)
	s.result(w, value, err)
}

func (s *Server) listServerProfiles(w http.ResponseWriter, _ *http.Request) {
	value, err := s.app.ListServerProfiles()
	s.result(w, value, err)
}

func (s *Server) deleteServerProfile(w http.ResponseWriter, r *http.Request) {
	err := s.app.DeleteServerProfile(r.PathValue("id"))
	s.result(w, map[string]any{"ok": true}, err)
}

func (s *Server) probeServerProfile(w http.ResponseWriter, r *http.Request) {
	var input app.ServerProbeRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.ProbeServerProfile(r.PathValue("id"), input)
	s.result(w, value, err)
}

func (s *Server) listServerRemote(w http.ResponseWriter, r *http.Request) {
	var input app.ServerListRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.ListServerRemotePath(r.PathValue("id"), input)
	s.result(w, value, err)
}

func (s *Server) readServerRemote(w http.ResponseWriter, r *http.Request) {
	var input app.ServerReadRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.ReadServerRemoteFile(r.PathValue("id"), input)
	s.result(w, value, err)
}

func (s *Server) serverTerminal(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.ServerTerminalArgv(r.PathValue("id"))
	s.result(w, value, err)
}
