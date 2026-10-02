package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/security"
	"local-agent-workbench/internal/workspace"
)

type masterScopeKey struct{}
type masterScope struct {
	Workspace domain.Workspace
	FS        *workspace.FS
}

func (a *App) masterWorldID(ctx context.Context) string {
	if scope, ok := ctx.Value(masterScopeKey{}).(masterScope); ok {
		return scope.Workspace.ID
	}
	if id := a.currentWorldID(); id != "" {
		return id
	}
	id, _ := a.store.Setting(ctx, "master.point.active")
	return id
}

// WithMasterWorkspace validates a requested scope and pins it for the entire operation.
func (a *App) WithMasterWorkspace(ctx context.Context, id string) (context.Context, error) {
	if scope, ok := ctx.Value(masterScopeKey{}).(masterScope); ok && (id == "" || id == scope.Workspace.ID) {
		return ctx, nil
	}
	if id == "" {
		id = a.masterWorldID(ctx)
	}
	if id == "" {
		return a.newPointChatScope(ctx, domain.NewID("chat"), false)
	}
	if !strings.HasPrefix(id, "point-chat-") && id != a.currentWorldID() {
		return ctx, errForeignWorld
	}
	w, err := a.store.WorkspaceByID(ctx, id)
	if err != nil {
		return ctx, err
	}
	if !strings.HasPrefix(id, "point-chat-") {
		a.mu.RLock()
		fs := a.currentFS
		current := a.currentWorkspace
		a.mu.RUnlock()
		if fs != nil && current != nil && current.ID == id {
			return context.WithValue(ctx, masterScopeKey{}, masterScope{w, fs}), nil
		}
	}
	if strings.HasPrefix(id, "point-chat-") {
		home, err := os.UserHomeDir()
		if err != nil {
			return ctx, err
		}
		expected := filepath.Join(home, "POINT", "Chats", strings.TrimPrefix(id, "point-chat-"))
		if filepath.Clean(w.Path) != filepath.Clean(expected) {
			return ctx, errors.New("invalid POINT chat workspace")
		}
	}
	fs, err := workspace.Open(w.Path)
	if err != nil {
		return ctx, err
	}
	if strings.HasPrefix(id, "point-chat-") && !strings.EqualFold(filepath.Clean(fs.Root()), filepath.Clean(w.Path)) {
		return ctx, errors.New("POINT chat folder may not redirect outside its assigned directory")
	}
	return context.WithValue(ctx, masterScopeKey{}, masterScope{w, fs}), nil
}

func (a *App) newPointChatScope(ctx context.Context, id string, temporary bool) (context.Context, error) {
	if id == "" || filepath.Base(id) != id || strings.ContainsAny(id, "/\\:") || id == "." || id == ".." {
		return ctx, errors.New("invalid chat id")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ctx, err
	}
	root := filepath.Join(home, "POINT", "Chats", id)
	if err = os.MkdirAll(root, 0700); err != nil {
		return ctx, err
	}
	w := domain.Workspace{ID: "point-chat-" + id, Path: root, Name: "POINT", OpenedAt: time.Now().UTC()}
	if err = a.store.SaveWorkspace(ctx, w); err != nil {
		return ctx, err
	}
	v := MasterSession{ID: id, WorkspaceID: w.ID, ScopeKind: "point_chat", WorkspacePath: root, Title: "Новый разговор", Mode: "auto", WorkMode: "auto", Temporary: temporary}
	if err = a.store.SaveMasterConversation(ctx, v); err != nil {
		return ctx, err
	}
	if err = a.store.SaveSetting(ctx, "master.active."+w.ID, id); err != nil {
		return ctx, err
	}
	if err = a.store.SaveSetting(ctx, "master.point.active", w.ID); err != nil {
		return ctx, err
	}
	return a.WithMasterWorkspace(ctx, w.ID)
}

func (a *App) masterScopedFS(ctx context.Context) *workspace.FS {
	if scope, ok := ctx.Value(masterScopeKey{}).(masterScope); ok {
		return scope.FS
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.currentFS
}

func (a *App) masterScopedRules(ctx context.Context) workspace.ProjectRules {
	fs := a.masterScopedFS(ctx)
	if fs == nil {
		return workspace.ProjectRules{}
	}
	r := fs.ProjectRules()
	r.Text = security.Redact(r.Text)
	return r
}
