// Рабочая область: файлы, поиск, индекс проекта, состояние и резервные копии.
package httpapi

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"local-agent-workbench/internal/app"
	"local-agent-workbench/internal/domain"
)

func (s *Server) bootstrap(w http.ResponseWriter, _ *http.Request) {
	value, err := s.app.Bootstrap()
	s.result(w, value, err)
}

func (s *Server) systemDiagnostics(w http.ResponseWriter, r *http.Request) {
	s.write(w, http.StatusOK, s.app.SystemDiagnostics(r.Context()))
}

func (s *Server) createSystemBackup(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Reason string `json:"reason"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	reason := strings.TrimSpace(input.Reason)
	switch reason {
	case "":
		reason = "manual"
	case "manual", "recovery-point", "before-update":
	default:
		s.problem(w, http.StatusBadRequest, "invalid_backup_reason", "reason must be manual, recovery-point, or before-update")
		return
	}
	snapshot, err := s.app.CreateBackup(r.Context(), reason)
	if err != nil {
		s.result(w, nil, err)
		return
	}
	s.write(w, http.StatusOK, map[string]any{
		"id": snapshot.ID, "reason": snapshot.Reason, "createdAt": snapshot.CreatedAt,
		"sizeBytes": snapshot.Database.SizeBytes, "sha256": snapshot.Database.SHA256,
		"integrity": snapshot.Database.Integrity,
	})
}

func (s *Server) listSystemBackups(w http.ResponseWriter, r *http.Request) {
	snapshots, err := s.app.ListBackups(r.Context())
	if err != nil {
		s.result(w, nil, err)
		return
	}
	result := make([]map[string]any, 0, len(snapshots))
	for _, snapshot := range snapshots {
		result = append(result, map[string]any{
			"id": snapshot.ID, "reason": snapshot.Reason, "createdAt": snapshot.CreatedAt,
			"sizeBytes": snapshot.Database.SizeBytes, "sha256": snapshot.Database.SHA256,
			"integrity": snapshot.Database.Integrity,
		})
	}
	s.write(w, http.StatusOK, result)
}

func (s *Server) runtimeState(w http.ResponseWriter, _ *http.Request) {
	value, err := s.app.RuntimeState()
	s.result(w, value, err)
}

func (s *Server) guildState(w http.ResponseWriter, _ *http.Request) {
	value, err := s.app.GuildState()
	s.result(w, value, err)
}

func (s *Server) openWorkspace(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Path string `json:"path"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.OpenWorkspace(input.Path)
	s.result(w, value, err)
}

func (s *Server) workspaceTree(w http.ResponseWriter, _ *http.Request) {
	value, err := s.app.WorkspaceTree()
	s.result(w, value, err)
}

func (s *Server) readFile(w http.ResponseWriter, r *http.Request) {
	path, err := url.QueryUnescape(r.URL.Query().Get("path"))
	if err != nil {
		s.problem(w, 400, "invalid_path", err.Error())
		return
	}
	value, err := s.app.ReadFile(path)
	s.result(w, value, err)
}

func (s *Server) saveFile(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.SaveFile(input.Path, input.Content)
	s.result(w, value, err)
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.SearchText(r.URL.Query().Get("q"))
	s.result(w, value, err)
}

func (s *Server) runTerminalCommand(w http.ResponseWriter, r *http.Request) {
	var input app.TerminalCommandRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.RunTerminalCommand(input)
	s.result(w, value, err)
}

func (s *Server) projectIndexStatus(w http.ResponseWriter, _ *http.Request) {
	s.write(w, http.StatusOK, s.app.ProjectIndexStatus())
}

func (s *Server) searchIndex(w http.ResponseWriter, r *http.Request) {
	limit := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}
	s.write(w, http.StatusOK, s.app.SearchIndex(r.URL.Query().Get("q"), limit))
}

func (s *Server) updateProjectIndex(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Changed []string `json:"changed"`
		Deleted []string `json:"deleted"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.UpdateProjectIndex(input.Changed, input.Deleted)
	s.result(w, value, err)
}

func (s *Server) invalidateProjectIndex(w http.ResponseWriter, _ *http.Request) {
	s.write(w, http.StatusOK, s.app.InvalidateProjectIndex())
}

func (s *Server) rebuildProjectIndex(w http.ResponseWriter, _ *http.Request) {
	value, err := s.app.RebuildProjectIndex()
	s.result(w, value, err)
}

func (s *Server) previewContext(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ContextItems []domain.RunContextInput `json:"contextItems"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.PreviewContext(input.ContextItems)
	s.result(w, value, err)
}

func (s *Server) fileHistory(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.FileHistory(r.Context(), r.URL.Query().Get("path"))
	s.result(w, value, err)
}
