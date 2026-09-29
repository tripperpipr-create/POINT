package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/osproc"
)

type MasterChatBranchBindRequest struct {
	Path   string `json:"path"`
	Name   string `json:"name"`
	Base   string `json:"base"`
	Commit string `json:"commit"`
}

func (a *App) SetMasterChatBranchOffer(ctx context.Context, chatID, state string) error {
	if strings.TrimSpace(chatID) == "" {
		return errors.New("чат не указан")
	}
	return a.store.SetMasterChatBranchOffer(ctx, a.currentWorldID(), chatID, state)
}

func (a *App) BindMasterChatBranch(ctx context.Context, chatID string, req MasterChatBranchBindRequest) (domain.Workspace, error) {
	if strings.TrimSpace(chatID) == "" || strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.Base) == "" || strings.TrimSpace(req.Commit) == "" {
		return domain.Workspace{}, errors.New("ветка, база, коммит и чат обязательны")
	}
	managed, err := filepath.EvalSymlinks(filepath.Join(a.dataDir, "managed-workspaces"))
	if err != nil {
		return domain.Workspace{}, err
	}
	target, err := filepath.EvalSymlinks(req.Path)
	if err != nil {
		return domain.Workspace{}, err
	}
	rel, err := filepath.Rel(managed, target)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return domain.Workspace{}, errors.New("рабочая копия вне каталога Point")
	}
	if _, err = os.Stat(filepath.Join(target, ".git")); err != nil {
		return domain.Workspace{}, errors.New("у рабочей копии нет Git")
	}
	head, err := osproc.CommandContext(ctx, "git", "-C", target, "rev-parse", "HEAD").Output()
	if err != nil || strings.TrimSpace(string(head)) != req.Commit {
		return domain.Workspace{}, errors.New("рабочая копия не соответствует выбранному коммиту")
	}
	branch, err := osproc.CommandContext(ctx, "git", "-C", target, "branch", "--show-current").Output()
	if err != nil || strings.TrimSpace(string(branch)) != req.Name {
		return domain.Workspace{}, errors.New("рабочая копия не соответствует выбранной ветке")
	}
	return a.store.MoveMasterChatToWorktree(ctx, a.currentWorldID(), chatID, target, req.Name, req.Base, req.Commit)
}
