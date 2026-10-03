package app

import (
	"context"
	"local-agent-workbench/internal/gitflow"
	"sort"
)

func (a *App) lockWorkspaceGitWrites(ctx context.Context, workspace string) func() {
	repos := gitflow.Repositories(ctx, a.gitInspectRunner(), workspace)
	sort.Slice(repos, func(i, j int) bool { return repos[i].Dir < repos[j].Dir })
	releases := []func(){}
	for _, r := range repos {
		releases = append(releases, gitflow.LockRepository(ctx, a.gitActionRunner(), r.Dir))
	}
	return func() {
		for i := len(releases) - 1; i >= 0; i-- {
			releases[i]()
		}
	}
}
