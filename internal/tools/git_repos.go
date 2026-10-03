package tools

// Несколько Git-проектов в одной папке. Человек открывает каталог вроде
// «фронт cf», в котором сам Git не живёт, а живёт в cf-pages и cf-vue-apps.
// Прежде git-инструменты смотрели только на корень и отвечали
// git_unavailable — Мастер 29.09 потратил на это круг, а о вложенных
// репозиториях не узнал вовсе.

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/workspace"
)

const (
	gitRepoSearchDepth = 3
	gitRepoSearchLimit = 32
)

// gitRepoSkipDirs — где репозиториев не ищут: зависимости и артефакты сборки
// бывают огромны, а свои .git в них не бывают проектом человека.
var gitRepoSkipDirs = map[string]bool{
	"node_modules": true, "vendor": true, "dist": true, "build": true, "out": true, "target": true,
}

// DiscoverGitRepos — относительные (через /) пути каталогов с .git внутри
// корня: сам корень записывается как ".", вложенные — до трёх уровней вглубь,
// не больше 32. .git-файл (worktree, submodule) считается так же, как каталог.
// includeNested разрешает поиск внутри найденных репозиториев для Git workspace.
func DiscoverGitRepos(root string, includeNested ...bool) []string {
	repos := []string{}
	if hasGitEntry(root) {
		repos = append(repos, ".")
	}
	var walk func(dir, rel string, depth int)
	walk = func(dir, rel string, depth int) {
		if depth > gitRepoSearchDepth || len(repos) >= gitRepoSearchLimit {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, entry := range entries {
			name := entry.Name()
			if !entry.IsDir() || strings.HasPrefix(name, ".") || gitRepoSkipDirs[strings.ToLower(name)] {
				continue
			}
			child, childRel := filepath.Join(dir, name), name
			if rel != "" {
				childRel = rel + "/" + name
			}
			if hasGitEntry(child) {
				if len(repos) < gitRepoSearchLimit {
					repos = append(repos, childRel)
				}
				if len(includeNested) == 0 || !includeNested[0] {
					continue
				}
			}
			walk(child, childRel, depth+1)
		}
	}
	walk(root, "", 1)
	sort.Strings(repos)
	return repos
}

func hasGitEntry(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// GitAvailable — есть ли с чем работать git-инструментам: корень — рабочее
// дерево или внутри лежит хотя бы один репозиторий.
func GitAvailable(ctx context.Context, root string) bool {
	return gitWorkTreeAvailable(ctx, root) || len(nestedGitRepos(root)) > 0
}

func nestedGitRepos(root string) []string {
	repos := DiscoverGitRepos(root)
	nested := repos[:0]
	for _, repo := range repos {
		if repo != "." {
			nested = append(nested, repo)
		}
	}
	return nested
}

// gitRepoSchemaProperty — аргумент repo в схемах git-инструментов.
const gitRepoSchemaProperty = `"repo":{"type":"string","description":"Workspace-relative directory of the Git repository when the workspace holds several (for example cf-vue-apps). Optional: inferred from path, or the workspace root."}`

// gitReposNote дописывается к описанию инструмента, когда корень сам не
// репозиторий: модель сразу видит, какой repo передать, и не тратит круг на
// отказ.
func gitReposNote(fs *workspace.FS) string {
	if fs == nil || hasGitEntry(fs.Root()) {
		return ""
	}
	repos := nestedGitRepos(fs.Root())
	if len(repos) == 0 {
		return ""
	}
	return " The workspace root is not a Git repository; it contains these repositories: " + strings.Join(repos, ", ") + ". Pass repo to choose one."
}

// resolveGitRepo выбирает репозиторий для вызова: явный repo, ближайший
// предок path с .git, корень, если он рабочее дерево, или единственный
// вложенный. Несколько вложенных без подсказки — отказ со списком.
// Возвращает абсолютный каталог репозитория и его путь относительно корня.
func resolveGitRepo(ctx context.Context, fs *workspace.FS, repo, path string) (string, string, *domain.ToolResult) {
	root := fs.Root()
	if repo = strings.TrimSpace(repo); repo != "" && repo != "." {
		dir, err := fs.Resolve(repo, false)
		if err != nil {
			result := FailWithHint("invalid_repo", err.Error(), "pass a workspace-relative repository directory")
			return "", "", &result
		}
		if !gitWorkTreeAvailable(ctx, dir) {
			result := FailWithHint("git_unavailable", repo+" is not a usable Git repository", gitRepoHint(root))
			return "", "", &result
		}
		return dir, relativeRepo(root, dir), nil
	}
	if path = strings.TrimSpace(path); path != "" && repo == "" {
		if resolved, err := fs.Resolve(path, true); err == nil {
			for dir := resolved; ; dir = filepath.Dir(dir) {
				rel, relErr := filepath.Rel(root, dir)
				if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
					break
				}
				if rel != "." && hasGitEntry(dir) && gitWorkTreeAvailable(ctx, dir) {
					return dir, filepath.ToSlash(rel), nil
				}
				if rel == "." || filepath.Dir(dir) == dir {
					break
				}
			}
		}
	}
	if gitWorkTreeAvailable(ctx, root) {
		return root, ".", nil
	}
	repos := nestedGitRepos(root)
	switch len(repos) {
	case 0:
		result := FailWithHint("git_unavailable", "workspace is not a usable Git repository", "filtered-copy sandboxes omit .git; use a PreferWorktree sandbox, or inspect Change Sets instead of git tools")
		return "", "", &result
	case 1:
		dir := filepath.Join(root, filepath.FromSlash(repos[0]))
		if gitWorkTreeAvailable(ctx, dir) {
			return dir, repos[0], nil
		}
	}
	result := FailWithHint("git_repo_required",
		"the workspace root is not a Git repository; it contains several: "+strings.Join(repos, ", "),
		"pass repo (for example "+repos[0]+") or a path inside the repository you mean")
	return "", "", &result
}

func gitRepoHint(root string) string {
	if repos := nestedGitRepos(root); len(repos) > 0 {
		return "repositories in this workspace: " + strings.Join(repos, ", ")
	}
	return "filtered-copy sandboxes omit .git; use a PreferWorktree sandbox, or inspect Change Sets instead of git tools"
}

func relativeRepo(root, dir string) string {
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return dir
	}
	return filepath.ToSlash(rel)
}

// repoRelativePath — путь фильтра git относительно выбранного репозитория.
// Путь вне него — отказ: git ответил бы непонятной ошибкой о пути вне дерева.
func repoRelativePath(fs *workspace.FS, repoDir, path string) (string, *domain.ToolResult) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}
	resolved, err := fs.Resolve(path, false)
	if err != nil {
		result := Fail("invalid_path", err.Error())
		return "", &result
	}
	rel, err := filepath.Rel(repoDir, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		result := FailWithHint("invalid_path", path+" is outside the selected repository", "pass repo for the repository that contains this path, or omit repo")
		return "", &result
	}
	return filepath.ToSlash(rel), nil
}

// WithoutUnusableGitTools убирает git-инструменты из того, что видит модель,
// когда работать им не с чем: ни корень, ни вложенные каталоги не
// репозитории. Реестр их сохраняет — критерии, названные по имени, проверяются
// по нему. Общий для агента и Мастера: оба тратили ход на git_unavailable.
func WithoutUnusableGitTools(ctx context.Context, root string, definitions []domain.ToolDefinition) []domain.ToolDefinition {
	hasGit := false
	for _, definition := range definitions {
		if IsGitReadTool(definition.Name) {
			hasGit = true
			break
		}
	}
	if !hasGit || GitAvailable(ctx, root) {
		return definitions
	}
	filtered := make([]domain.ToolDefinition, 0, len(definitions))
	for _, definition := range definitions {
		if !IsGitReadTool(definition.Name) {
			filtered = append(filtered, definition)
		}
	}
	return filtered
}
