package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/storage"
	"local-agent-workbench/internal/tools"
	"local-agent-workbench/internal/workspace"
)

// RebuildProjectIndex, workspace I/O, search and the human-operated terminal
// form the bounded workspace surface. Keeping them together makes the host
// boundary explicit and keeps unrelated agent-run orchestration out of it.
func (a *App) RebuildProjectIndex() (workspace.IndexStatus, error) {
	fs, err := a.fs()
	if err != nil {
		return workspace.IndexStatus{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	started := time.Now()
	status, err := fs.BuildIndex(ctx)
	a.dropIndexSearchCache()
	if err != nil {
		slog.Error("project index rebuild failed", "error", err, "duration_ms", time.Since(started).Milliseconds())
	} else {
		slog.Info("project index rebuilt",
			"state", status.State, "mode", status.Mode, "files", status.Files, "chunks", status.Chunks,
			"symbols", status.Symbols, "bytes", status.ApproxBytes, "duration_ms", status.DurationMs,
		)
	}
	return status, err
}

func (a *App) UpdateProjectIndex(changed, deleted []string) (workspace.IndexStatus, error) {
	fs, err := a.fs()
	if err != nil {
		return workspace.IndexStatus{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	started := time.Now()
	status, err := fs.UpdateIndex(ctx, changed, deleted)
	a.dropIndexSearchCache()
	if err != nil {
		slog.Error("project index update failed", "changed", len(changed), "deleted", len(deleted), "error", err, "duration_ms", time.Since(started).Milliseconds())
	} else {
		slog.Info("project index updated",
			"changed", len(changed), "deleted", len(deleted),
			"state", status.State, "mode", status.Mode, "files", status.Files, "chunks", status.Chunks,
			"duration_ms", status.DurationMs,
		)
	}
	return status, err
}

func (a *App) InvalidateProjectIndex() workspace.IndexStatus {
	a.mu.RLock()
	currentFS := a.currentFS
	a.mu.RUnlock()
	if currentFS == nil {
		return workspace.IndexStatus{State: "no_workspace"}
	}
	currentFS.InvalidateIndex()
	a.dropIndexSearchCache()
	return currentFS.IndexStatus()
}

func (a *App) ProjectIndexStatus() workspace.IndexStatus {
	a.mu.RLock()
	currentFS := a.currentFS
	a.mu.RUnlock()
	if currentFS == nil {
		return workspace.IndexStatus{State: "no_workspace"}
	}
	return currentFS.IndexStatus()
}

func (a *App) SearchIndex(query string, limit int) workspace.IndexSearchResult {
	query = strings.TrimSpace(query)
	a.mu.RLock()
	currentFS := a.currentFS
	a.mu.RUnlock()
	if currentFS == nil {
		return workspace.IndexSearchResult{Query: query, Status: workspace.IndexStatus{State: "no_workspace"}, Hits: []workspace.IndexHit{}}
	}
	cacheKey := fmt.Sprintf("index-search:%d:%s", limit, strings.ToLower(query))
	var cached workspace.IndexSearchResult
	if a.cacheGet(cacheKey, &cached) {
		return cached
	}
	result := currentFS.LookupIndex(query, limit)
	a.cacheSet(cacheKey, result, 1500*time.Millisecond)
	return result
}

func (a *App) dropIndexSearchCache() {
	_ = a.cache.DeletePrefix(context.Background(), a.cachePrefix()+"index-search:")
}

func (a *App) SelectWorkspace() (WorkspaceView, error) {
	if a.ctx == nil || a.directoryPicker == nil {
		return WorkspaceView{}, errors.New("application is not ready")
	}
	path, err := a.directoryPicker(a.ctx)
	if err != nil {
		return WorkspaceView{}, err
	}
	if path == "" {
		return WorkspaceView{}, errors.New("workspace selection cancelled")
	}
	return a.OpenWorkspace(path)
}

func (a *App) OpenWorkspace(path string) (WorkspaceView, error) {
	fs, err := workspace.Open(path)
	if err != nil {
		return WorkspaceView{}, err
	}
	if a.workspaceBoundary != "" {
		rel, relErr := filepath.Rel(a.workspaceBoundary, fs.Root())
		managedRoot := filepath.Join(a.dataDir, "managed-workspaces")
		managedRel, managedErr := filepath.Rel(managedRoot, fs.Root())
		insideManaged := managedErr == nil && managedRel != ".." && !strings.HasPrefix(managedRel, ".."+string(filepath.Separator)) && !filepath.IsAbs(managedRel)
		if (relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel)) && !insideManaged {
			return WorkspaceView{}, errors.New("workspace is outside the configured server boundary")
		}
	}
	ctx := context.Background()
	stored, err := a.store.WorkspaceByPath(ctx, fs.Root())
	if storage.IsNotFound(err) {
		stored = domain.Workspace{ID: domain.NewID("ws"), Path: fs.Root(), Name: filepath.Base(fs.Root()), OpenedAt: time.Now().UTC()}
	} else if err != nil {
		return WorkspaceView{}, err
	}
	stored.OpenedAt = time.Now().UTC()
	if err = a.store.SaveWorkspace(ctx, stored); err != nil {
		return WorkspaceView{}, err
	}
	// Deep List(12) blocked health-to-ready on large trees. Tree is loaded
	// on demand via WorkspaceTree (5s TTL cache).
	a.mu.Lock()
	a.currentWorkspace = &stored
	a.currentFS = fs
	a.mu.Unlock()
	// Persisted flow state survives the core process; resume it after the
	// workspace boundary and project agents are available.
	_ = a.ResumeActiveFlowRuns(stored.ID)
	slog.Info("workspace opened", "workspace_id", stored.ID, "path", stored.Path, "name", stored.Name)
	return WorkspaceView{Workspace: stored, Tree: nil}, nil
}

func (a *App) WorkspaceTree() ([]domain.FileNode, error) {
	fs, err := a.fs()
	if err != nil {
		return nil, err
	}
	var tree []domain.FileNode
	if a.cacheGet("tree", &tree) {
		return tree, nil
	}
	tree, err = fs.List(context.Background(), 12)
	if err == nil {
		a.cacheSet("tree", tree, 5*time.Second)
	}
	return tree, err
}

func (a *App) ReadFile(path string) (workspace.FileContent, error) {
	fs, err := a.fs()
	if err != nil {
		return workspace.FileContent{}, err
	}
	var content workspace.FileContent
	key := "file:" + filepath.ToSlash(path)
	if a.cacheGet(key, &content) {
		return content, nil
	}
	content, err = fs.Read(path, false)
	if err == nil {
		a.cacheSet(key, content, 3*time.Second)
	}
	return content, err
}

func (a *App) SaveFile(path, content string) (workspace.FileContent, error) {
	fs, err := a.fs()
	if err != nil {
		return workspace.FileContent{}, err
	}
	written, err := fs.Write(path, content)
	if err != nil {
		return workspace.FileContent{}, err
	}
	_ = a.cache.DeletePrefix(context.Background(), a.cachePrefix())
	return written, nil
}

// RunTerminalCommand executes a command explicitly entered by the user in the
// IDE terminal. Model-initiated commands continue to require approval.
func (a *App) RunTerminalCommand(request TerminalCommandRequest) (TerminalCommandResult, error) {
	fs, err := a.fs()
	if err != nil {
		return TerminalCommandResult{}, err
	}
	payload, err := json.Marshal(map[string]any{
		"command":        request.Command,
		"cwd":            request.CWD,
		"reason":         "Команда введена пользователем во встроенном терминале IDE",
		"timeoutSeconds": request.TimeoutSeconds,
	})
	if err != nil {
		return TerminalCommandResult{}, err
	}
	result := (tools.RunCommand{FS: fs, MaxOutput: 256 * 1024, DefaultTimeout: 2 * time.Minute}).Execute(context.Background(), payload)
	if !result.OK {
		if result.Error != nil {
			return TerminalCommandResult{}, fmt.Errorf("%s", result.Error.Message)
		}
		return TerminalCommandResult{}, errors.New("terminal command failed")
	}
	var output TerminalCommandResult
	if err = json.Unmarshal(result.Output, &output); err != nil {
		return TerminalCommandResult{}, err
	}
	output.Truncated = result.Truncated
	return output, nil
}

func (a *App) SearchText(query string) ([]workspace.Match, error) {
	fs, err := a.fs()
	if err != nil {
		return nil, err
	}
	var matches []workspace.Match
	key := "search:" + strings.ToLower(strings.TrimSpace(query))
	if a.cacheGet(key, &matches) {
		return matches, nil
	}
	matches, err = fs.Search(context.Background(), query, 200)
	if err == nil {
		a.cacheSet(key, matches, 3*time.Second)
	}
	return matches, err
}
