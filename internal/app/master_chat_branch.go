package app

import (
	"context"
	"errors"
	"fmt"
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
	// Repositories — папка без своего Git, но с вложенными репозиториями
	// (cf-pages/, cf-vue-apps/). Рабочая копия тогда сама не репозиторий:
	// в ней по тем же относительным путям лежат worktree каждого вложенного,
	// все на ветке Name. Base и Commit верхнего уровня при этом не нужны —
	// у каждого репозитория свои.
	Repositories []MasterChatBranchRepository `json:"repositories,omitempty"`
}

type MasterChatBranchRepository struct {
	Path   string `json:"path"`
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
	nested := len(req.Repositories) > 0
	if strings.TrimSpace(chatID) == "" || strings.TrimSpace(req.Name) == "" || (!nested && (strings.TrimSpace(req.Base) == "" || strings.TrimSpace(req.Commit) == "")) {
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
	if !nested {
		if err = checkChatWorktree(ctx, target, req.Name, req.Commit); err != nil {
			return domain.Workspace{}, err
		}
		return a.store.MoveMasterChatToWorktree(ctx, a.currentWorldID(), chatID, target, req.Name, req.Base, req.Commit)
	}
	if _, err = os.Stat(filepath.Join(target, ".git")); err == nil {
		return domain.Workspace{}, errors.New("рабочая копия с вложенными репозиториями сама не должна быть репозиторием")
	}
	bases := make([]string, 0, len(req.Repositories))
	commits := make([]string, 0, len(req.Repositories))
	seen := map[string]bool{}
	for _, repo := range req.Repositories {
		clean := filepath.Clean(filepath.FromSlash(strings.TrimSpace(repo.Path)))
		if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || seen[clean] {
			return domain.Workspace{}, fmt.Errorf("недопустимый путь репозитория: %q", repo.Path)
		}
		seen[clean] = true
		if strings.TrimSpace(repo.Base) == "" || strings.TrimSpace(repo.Commit) == "" {
			return domain.Workspace{}, fmt.Errorf("%s: база и коммит обязательны", repo.Path)
		}
		if err = checkChatWorktree(ctx, filepath.Join(target, clean), req.Name, repo.Commit); err != nil {
			return domain.Workspace{}, fmt.Errorf("%s: %w", repo.Path, err)
		}
		label := filepath.ToSlash(clean)
		bases = append(bases, label+"@"+repo.Base)
		commits = append(commits, label+"@"+repo.Commit)
	}
	return a.store.MoveMasterChatToWorktree(ctx, a.currentWorldID(), chatID, target, req.Name, strings.Join(bases, ", "), strings.Join(commits, ", "))
}

// checkChatWorktree — каталог есть рабочая копия Git ровно на выбранной ветке
// и выбранном коммите: ядро не верит хосту на слово.
func checkChatWorktree(ctx context.Context, dir, name, commit string) error {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return errors.New("у рабочей копии нет Git")
	}
	head, err := osproc.CommandContext(ctx, "git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil || strings.TrimSpace(string(head)) != commit {
		return errors.New("рабочая копия не соответствует выбранному коммиту")
	}
	branch, err := osproc.CommandContext(ctx, "git", "-C", dir, "branch", "--show-current").Output()
	if err != nil || strings.TrimSpace(string(branch)) != name {
		return errors.New("рабочая копия не соответствует выбранной ветке")
	}
	return nil
}
