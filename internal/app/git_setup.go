package app

import (
	"context"
	"errors"
	"local-agent-workbench/internal/gitflow"
	"os"
	"path/filepath"
	"strings"
)

type GitSetupInput struct {
	WorkspaceID string `json:"workspaceId"`
	Action      string `json:"action"`
	Name        string `json:"name,omitempty"`
	URL         string `json:"url,omitempty"`
}

func (a *App) GitSetup(ctx context.Context, q GitSetupInput) (any, error) {
	if q.WorkspaceID == "" {
		return nil, errors.New("нужен workspaceId")
	}
	ws, e := a.gitWorkspace(q.WorkspaceID)
	if e != nil {
		return nil, e
	}
	if q.Name == "" {
		q.Name = "."
	}
	if q.Name != "." && (filepath.Base(q.Name) != q.Name || strings.ContainsAny(q.Name, "/\\\x00") || q.Name == "..") {
		return nil, errors.New("нужно имя каталога внутри проекта")
	}
	if q.Action != "init" && q.Action != "clone" {
		return nil, errors.New("неизвестная операция создания")
	}
	root := filepath.Join(ws.Path, q.Name)
	if q.Action == "clone" && (q.Name == "." || q.URL == "" || strings.HasPrefix(q.URL, "-") || strings.Contains(q.URL, "::") || strings.ContainsAny(q.URL, "\x00\r\n")) {
		return nil, errors.New("укажите адрес и новый каталог")
	}
	unlock := gitflow.LockRepository(ctx, a.gitActionRunner(), root)
	defer unlock()
	// Existing repositories are never reinitialized or overwritten.
	if q.Action == "init" {
		if _, e := os.Stat(filepath.Join(root, ".git")); e == nil {
			return nil, errors.New("репозиторий уже существует")
		}
	}
	if q.Name != "." {
		if _, e := os.Lstat(root); e == nil {
			return nil, errors.New("каталог уже существует")
		}
	}
	if q.Action == "clone" {
		_, e = a.gitActionRunner().Run(ctx, ws.Path, "clone", "--", q.URL, root)
	} else {
		if e = os.MkdirAll(root, 0755); e != nil {
			return nil, e
		}
		_, e = a.gitActionRunner().Run(ctx, root, "init")
	}
	if e != nil {
		return nil, e
	}
	return a.GitRepositories(ctx, ws.ID)
}
