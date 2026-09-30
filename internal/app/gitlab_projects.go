package app

// Проекты GitLab: список, карточка, коммиты, ветки и дерево. Всё — чтение и
// адресуется явным проектом, поэтому от связи папки не зависит: карточка
// проекта открывается и из папки, которая с GitLab не связана. Клон делает
// git на машине владельца, ядро в нём не участвует.

import (
	"context"
	"strings"

	"local-agent-workbench/internal/integrations/gitlab"
)

// GitLabProjectsView — список проектов. Current — проект, с которым связана
// открытая папка: окно отмечает его «эта папка».
type GitLabProjectsView struct {
	Scope   gitlab.ProjectScope `json:"scope"`
	Search  string              `json:"search,omitempty"`
	Current string              `json:"current,omitempty"`
	Items   []gitlab.Project    `json:"items"`
}

func (a *App) GitLabProjects(ctx context.Context, search, scope string) GitLabResponse {
	selected := gitlab.ProjectScope(scope)
	if selected == "" {
		selected = gitlab.ProjectsMember
	}
	search = strings.TrimSpace(search)
	session, err := a.gitlabSession(ctx)
	switch {
	case err != nil:
	case selected != gitlab.ProjectsMember && selected != gitlab.ProjectsOwned:
		err = gitlabBadRequest("неизвестный список проектов %q", clipText(scope, 20))
	case len(search) > 100 || strings.ContainsAny(search, "\x00\r\n"):
		err = gitlabBadRequest("строка поиска длиннее 100 знаков или с переводом строки")
	}
	if err != nil {
		return a.gitlabRespond(session.server, nil, err)
	}
	items, err := session.client.Projects(ctx, search, selected)
	if err != nil {
		return a.gitlabRespond(session.server, nil, err)
	}
	view := GitLabProjectsView{Scope: selected, Search: search, Items: nonNilSlice(items)}
	if binding := a.gitlabBinding(ctx, session.server); binding.linked() {
		view.Current = binding.Project
	}
	return a.gitlabRespond(session.server, view, nil)
}

// GitLabProjectCardView — карточка проекта. Current — открытая папка и есть
// этот проект (по связи): тогда клон не нужен.
type GitLabProjectCardView struct {
	Project gitlab.Project `json:"project"`
	Current bool           `json:"current"`
}

func (a *App) GitLabProjectDetail(ctx context.Context, project string) GitLabResponse {
	session, err := a.gitlabProject(ctx, project)
	if err != nil {
		return a.gitlabRespond(session.server, nil, err)
	}
	detail, err := session.client.ProjectDetail(ctx, project)
	if err != nil {
		return a.gitlabRespond(session.server, nil, err)
	}
	binding := a.gitlabBinding(ctx, session.server)
	return a.gitlabRespond(session.server, GitLabProjectCardView{Project: detail, Current: binding.linked() && strings.EqualFold(binding.Project, detail.Path)}, nil)
}

// GitLabCommitsView — страница истории ветки. More — страница полная, за ней
// может быть следующая.
type GitLabCommitsView struct {
	Project string          `json:"project"`
	Ref     string          `json:"ref,omitempty"`
	Page    int             `json:"page"`
	More    bool            `json:"more"`
	Items   []gitlab.Commit `json:"items"`
}

func (a *App) GitLabCommits(ctx context.Context, project, ref string, page int) GitLabResponse {
	session, err := a.gitlabProject(ctx, project)
	switch {
	case err != nil:
	case ref != "" && !gitlabRefPattern.MatchString(ref):
		err = gitlabBadRequest("ветка %q недопустима", clipText(ref, 80))
	case page < 0 || page > 1000:
		err = gitlabBadRequest("номер страницы вне 1…1000")
	}
	if err != nil {
		return a.gitlabRespond(session.server, nil, err)
	}
	if page == 0 {
		page = 1
	}
	items, err := session.client.Commits(ctx, project, ref, page)
	if err != nil {
		return a.gitlabRespond(session.server, nil, err)
	}
	return a.gitlabRespond(session.server, GitLabCommitsView{Project: project, Ref: ref, Page: page, More: len(items) >= 30, Items: nonNilSlice(items)}, nil)
}

func (a *App) GitLabCommit(ctx context.Context, project, sha string) GitLabResponse {
	session, err := a.gitlabProject(ctx, project)
	if err == nil && !gitlabSHAPattern.MatchString(sha) {
		err = gitlabBadRequest("коммит %q недопустим", clipText(sha, 80))
	}
	if err != nil {
		return a.gitlabRespond(session.server, nil, err)
	}
	detail, err := session.client.Commit(ctx, project, sha)
	if err != nil {
		return a.gitlabRespond(session.server, nil, err)
	}
	return a.gitlabRespond(session.server, map[string]any{"project": project, "commit": detail}, nil)
}

func (a *App) GitLabBranches(ctx context.Context, project, search string) GitLabResponse {
	session, err := a.gitlabProject(ctx, project)
	search = strings.TrimSpace(search)
	if err == nil && (len(search) > 100 || strings.ContainsAny(search, "\x00\r\n")) {
		err = gitlabBadRequest("строка поиска длиннее 100 знаков или с переводом строки")
	}
	if err != nil {
		return a.gitlabRespond(session.server, nil, err)
	}
	items, err := session.client.Branches(ctx, project, search)
	if err != nil {
		return a.gitlabRespond(session.server, nil, err)
	}
	return a.gitlabRespond(session.server, map[string]any{"project": project, "items": nonNilSlice(items)}, nil)
}

func (a *App) GitLabTree(ctx context.Context, project, path, ref string) GitLabResponse {
	session, err := a.gitlabProject(ctx, project)
	path = strings.Trim(path, "/")
	switch {
	case err != nil:
	case path != "" && validGitLabPath(path) != nil:
		err = validGitLabPath(path)
	case strings.Contains("/"+path+"/", "/../"):
		err = gitlabBadRequest("путь %q недопустим", clipText(path, 80))
	case ref != "" && !gitlabRefPattern.MatchString(ref):
		err = gitlabBadRequest("ветка %q недопустима", clipText(ref, 80))
	}
	if err != nil {
		return a.gitlabRespond(session.server, nil, err)
	}
	tree, err := session.client.Tree(ctx, project, path, ref)
	if err != nil {
		return a.gitlabRespond(session.server, nil, err)
	}
	return a.gitlabRespond(session.server, map[string]any{"project": project, "tree": tree}, nil)
}
