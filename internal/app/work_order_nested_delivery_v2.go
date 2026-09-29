package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/osproc"
	workbenchtools "local-agent-workbench/internal/tools"
)

// Доставка в папку с несколькими Git-проектами (Q06).
//
// Открытая папка — не репозиторий, а внутри лежат cf-pages/ и cf-vue-apps/.
// Прежде такая папка молча получала CommitMode=none: файлы ложились в рабочие
// деревья вложенных репозиториев, коммита не было, и об этом никто не
// говорил. Теперь каждый вложенный репозиторий получает свой squash-коммит
// ровно из своих файлов доставки; файл вне всех репозиториев — честный отказ,
// потому что закоммитить его некуда. Сбой коммита в одном репозитории
// откатывает уже сделанные в других: доставка одна, и половины не бывает.

// cleanNestedGitRepos — вложенные репозитории папки, если корень сам не
// репозиторий, а все вложенные чисты. Грязный репозиторий squash-доставку
// всё равно отверг бы: изменения вне утверждённой доставки.
func cleanNestedGitRepos(root string) ([]string, bool) {
	if hasRootGitEntry(root) {
		return nil, false
	}
	repos := workbenchtools.DiscoverGitRepos(root)
	nested := make([]string, 0, len(repos))
	for _, repo := range repos {
		if repo == "." {
			return nil, false
		}
		output, err := osproc.Command("git", "-C", filepath.Join(root, filepath.FromSlash(repo)), "status", "--porcelain", "--untracked-files=normal").Output()
		if err != nil || strings.TrimSpace(string(output)) != "" {
			return nil, false
		}
		nested = append(nested, repo)
	}
	return nested, len(nested) > 0
}

func hasRootGitEntry(root string) bool {
	_, err := os.Stat(filepath.Join(root, ".git"))
	return err == nil
}

// groupDeliveryByRepository раскладывает пути доставки (относительно открытой
// папки) по ближайшему вложенному репозиторию; пути внутри репозитория
// становятся относительными его корня. Пути вне всех репозиториев — отдельно.
func groupDeliveryByRepository(repos []string, changed []string) (map[string][]string, []string) {
	ordered := append([]string(nil), repos...)
	// Длинный префикс раньше короткого: у вложенного в вложенный — свой.
	sort.Slice(ordered, func(i, j int) bool { return len(ordered[i]) > len(ordered[j]) })
	groups := map[string][]string{}
	var outside []string
	for _, item := range changed {
		path := filepath.ToSlash(filepath.Clean(strings.TrimSpace(item)))
		placed := false
		for _, repo := range ordered {
			if strings.HasPrefix(path, repo+"/") {
				groups[repo] = append(groups[repo], strings.TrimPrefix(path, repo+"/"))
				placed = true
				break
			}
		}
		if !placed {
			outside = append(outside, path)
		}
	}
	return groups, outside
}

// commitWorkOrderDeliveryV2 создаёт squash-коммит доставки: один — если
// открытая папка сама лежит в репозитории, по одному на вложенный — если нет.
func commitWorkOrderDeliveryV2(ctx context.Context, root, questID, evidenceID string, changed []string) ([]domain.RepositoryCommit, error) {
	if inside, err := runGitV2(ctx, root, nil, "rev-parse", "--is-inside-work-tree"); err == nil && strings.TrimSpace(inside) == "true" {
		commitID, commitErr := createWorkOrderSquashCommitV2(ctx, root, questID, evidenceID, changed)
		if commitErr != nil {
			return nil, commitErr
		}
		return []domain.RepositoryCommit{{Repo: ".", CommitID: commitID}}, nil
	}
	repos := workbenchtools.DiscoverGitRepos(root)
	if len(repos) == 0 {
		return nil, fmt.Errorf("squash delivery requires a Git workspace")
	}
	groups, outside := groupDeliveryByRepository(repos, changed)
	if len(outside) > 0 {
		return nil, fmt.Errorf("файлы доставки вне Git-репозиториев папки, закоммитить их некуда: %s", strings.Join(outside, ", "))
	}
	names := make([]string, 0, len(groups))
	for repo := range groups {
		names = append(names, repo)
	}
	sort.Strings(names)
	var commits []domain.RepositoryCommit
	previous := map[string]string{}
	for _, repo := range names {
		repoPath := filepath.Join(root, filepath.FromSlash(repo))
		head, _ := runGitV2(ctx, repoPath, nil, "rev-parse", "--verify", "HEAD")
		previous[repo] = strings.TrimSpace(head)
		commitID, err := createWorkOrderSquashCommitV2(ctx, repoPath, questID, evidenceID, groups[repo])
		if err != nil {
			undoRepositoryCommitsV2(root, commits, previous, groups)
			return nil, fmt.Errorf("%s: %w", repo, err)
		}
		commits = append(commits, domain.RepositoryCommit{Repo: repo, CommitID: commitID})
	}
	return commits, nil
}

// undoRepositoryCommitsV2 снимает коммиты доставки, уже сделанные в других
// репозиториях: ветка возвращается на прежнюю вершину, индекс — к ней же.
// Файлы откатывает вызывающий вместе с наборами изменений.
func undoRepositoryCommitsV2(root string, commits []domain.RepositoryCommit, previous map[string]string, groups map[string][]string) {
	ctx := context.Background()
	for index := len(commits) - 1; index >= 0; index-- {
		repo := commits[index].Repo
		repoPath := filepath.Join(root, filepath.FromSlash(repo))
		if head := previous[repo]; head != "" {
			_, _ = runGitV2(ctx, repoPath, nil, "reset", "--mixed", head)
			continue
		}
		// Репозиторий без коммитов до доставки: ветка исчезает вместе с ним.
		_, _ = runGitV2(ctx, repoPath, nil, "update-ref", "-d", "HEAD")
		_, _ = runGitV2(ctx, repoPath, nil, append([]string{"rm", "--cached", "-r", "--ignore-unmatch", "--"}, groups[repo]...)...)
	}
}
