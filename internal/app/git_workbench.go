package app

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"strings"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/gitflow"
)

type GitTarget struct {
	WorkspaceID string `json:"workspaceId"`
	RepoRoot    string `json:"repoRoot"`
}
type GitRepositoryView struct {
	Root      string `json:"root"`
	Name      string `json:"name"`
	Branch    string `json:"branch"`
	Changes   int    `json:"changes"`
	Operation string `json:"operation,omitempty"`
	Problem   string `json:"problem,omitempty"`
}

func (a *App) gitWorkspace(id string) (domain.Workspace, error) {
	ws, e := a.requireWorkspace()
	if e != nil {
		return ws, e
	}
	if id != "" && id != ws.ID {
		return ws, errForeignWorld
	}
	return ws, nil
}
func (a *App) GitRepositories(ctx context.Context, id string) (any, error) {
	ws, e := a.gitWorkspace(id)
	if e != nil {
		return nil, e
	}
	repos := []GitRepositoryView{}
	for _, r := range gitflow.Repositories(ctx, a.gitInspectRunner(), ws.Path) {
		s, e := gitflow.ReadSnapshot(ctx, a.gitInspectRunner(), r.Dir)
		v := GitRepositoryView{Root: s.Root, Name: filepath.Base(r.Dir), Branch: s.Branch, Changes: len(s.Changes), Operation: s.Operation}
		if e != nil {
			v.Root = r.Dir
			v.Problem = e.Error()
		}
		repos = append(repos, v)
	}
	return map[string]any{"workspaceId": ws.ID, "repositories": repos}, nil
}
func (a *App) gitWorkbenchRoot(ctx context.Context, t GitTarget) (string, string, error) {
	ws, e := a.gitWorkspace(t.WorkspaceID)
	if e != nil {
		return "", "", e
	}
	for _, r := range gitflow.Repositories(ctx, a.gitInspectRunner(), ws.Path) {
		root, e := a.gitInspectRunner().Run(ctx, r.Dir, "rev-parse", "--show-toplevel")
		if e != nil {
			continue
		}
		path := strings.TrimSpace(string(root))
		if filepath.Clean(t.RepoRoot) == filepath.Clean(path) || (runtime.GOOS == "windows" && strings.EqualFold(filepath.Clean(t.RepoRoot), filepath.Clean(path))) {
			return ws.ID, path, nil
		}
	}
	return "", "", errors.New("репозиторий не принадлежит открытому проекту")
}
func (a *App) GitWorkbenchStatus(ctx context.Context, t GitTarget) (any, error) {
	id, root, e := a.gitWorkbenchRoot(ctx, t)
	if e != nil {
		return nil, e
	}
	s, e := gitflow.ReadSnapshot(ctx, a.gitInspectRunner(), root)
	return map[string]any{"workspaceId": id, "git": s}, e
}

type GitWorkbenchCommand struct {
	GitTarget
	gitflow.Command
}

func (a *App) GitWorkbenchAction(ctx context.Context, c GitWorkbenchCommand) (gitflow.ActionResult, error) {
	if c.WorkspaceID == "" {
		return gitflow.ActionResult{}, errors.New("нужен workspaceId действия")
	}
	_, root, e := a.gitWorkbenchRoot(ctx, c.GitTarget)
	if e != nil {
		return gitflow.ActionResult{}, e
	}
	return gitflow.Execute(ctx, a.gitActionRunner(), root, c.Command)
}

type GitReadRequest struct {
	GitTarget
	Kind  string               `json:"kind"`
	Path  string               `json:"path,omitempty"`
	Area  string               `json:"area,omitempty"`
	Base  string               `json:"base,omitempty"`
	Head  string               `json:"head,omitempty"`
	Query gitflow.HistoryQuery `json:"query,omitempty"`
}

func (a *App) GitWorkbenchRead(ctx context.Context, q GitReadRequest) (any, error) {
	_, root, e := a.gitWorkbenchRoot(ctx, q.GitTarget)
	if e != nil {
		return nil, e
	}
	r := a.gitInspectRunner()
	switch q.Kind {
	case "files":
		for _, ref := range []string{q.Base, q.Head} {
			if ref == "" || strings.HasPrefix(ref, "-") {
				return nil, errors.New("выберите ревизии")
			}
		}
		out, e := r.Run(ctx, root, "diff", "--no-ext-diff", "--no-textconv", "--name-only", "-z", q.Base, q.Head, "--")
		return strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00"), e
	case "content":
		if _, e := gitflow.SafePath(root, q.Path); e != nil {
			return nil, e
		}
		if q.Head == "" || strings.HasPrefix(q.Head, "-") {
			return nil, errors.New("выберите ревизию")
		}
		if _, e := r.Run(ctx, root, "rev-parse", "--verify", q.Head+"^{tree}"); e != nil {
			return nil, e
		}
		if _, e := r.Run(ctx, root, "cat-file", "-e", q.Head+":"+q.Path); e != nil {
			return "", nil
		}
		out, e := r.Run(ctx, root, "show", q.Head+":"+q.Path)
		if e != nil {
			return nil, e
		}
		if len(out) > 1<<20 || strings.ContainsRune(string(out), 0) {
			return nil, errors.New("файл двоичный или больше 1 МБ")
		}
		return string(out), nil
	case "history":
		return gitflow.History(ctx, r, root, q.Query)
	case "diff":
		return gitflow.Diff(ctx, r, root, q.Path, q.Area)
	case "compare":
		return gitflow.Compare(ctx, r, root, q.Base, q.Head, q.Path)
	case "blame":
		return gitflow.Blame(ctx, r, root, q.Path)
	case "stash":
		if !strings.HasPrefix(q.Head, "stash@{") || !strings.HasSuffix(q.Head, "}") {
			return nil, errors.New("выберите stash")
		}
		out, e := r.Run(ctx, root, "stash", "show", "-p", q.Head, "--")
		return string(out), e
	case "stashes":
		out, e := r.Run(ctx, root, "stash", "list", "--format=%gd%x09%gs%x09%cI")
		return string(out), e
	default:
		return nil, errors.New("неизвестный вид Git-данных")
	}
}
