package app

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/forge"
	"local-agent-workbench/internal/gitflow"
	"local-agent-workbench/internal/integrations/gitlab"
)

var forgeIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)

func domainForgeID() string { return domain.NewID("forge") }

type ForgeBindingView struct {
	WorkspaceID string          `json:"workspaceId"`
	RepoRoot    string          `json:"repoRoot"`
	Candidates  []forge.Binding `json:"candidates"`
	Saved       []forge.Binding `json:"saved"`
}

func (a *App) ForgeBindings(ctx context.Context, t GitTarget) (ForgeBindingView, error) {
	id, root, e := a.gitWorkbenchRoot(ctx, t)
	out := ForgeBindingView{WorkspaceID: id, RepoRoot: root, Candidates: []forge.Binding{}, Saved: []forge.Binding{}}
	if e != nil {
		return out, e
	}
	forgeSettingsLock.Lock()
	c, e := a.loadForgeConfig(ctx)
	forgeSettingsLock.Unlock()
	if e != nil {
		return out, e
	}
	s, e := gitflow.ReadSnapshot(ctx, a.gitInspectRunner(), root)
	if e != nil {
		return out, e
	}
	for _, b := range c.Bindings {
		if b.WorkspaceID == id && b.RepoRoot == root {
			out.Saved = append(out.Saved, b)
		}
	}
	// A former workspace-level manual/off link applies only to its own repo.
	if len(out.Saved) == 0 {
		if old, e := a.store.GetGitLabBinding(ctx, id); e == nil {
			ws, _ := a.gitWorkspace(id)
			repos := gitflow.Repositories(ctx, a.gitInspectRunner(), ws.Path)
			if len(repos) == 1 && (old.Mode == domain.GitLabBindManual || old.Mode == domain.GitLabBindOff) {
				b := forge.Binding{WorkspaceID: id, RepoRoot: root, Remote: "origin", ConnectionID: "gitlab-legacy", Project: old.ProjectPath, Mode: string(old.Mode)}
				out.Saved = append(out.Saved, b)
			}
		}
	}
	for _, r := range s.Remotes {
		remote, e := gitlab.ParseRemote(r.URL)
		if e != nil {
			continue
		}
		disabled := false
		for _, b := range out.Saved {
			if b.Remote == r.Name {
				if b.Mode == "off" {
					disabled = true
				}
				if b.Mode == "manual" {
					for _, connection := range c.Connections {
						if connection.ID == b.ConnectionID && connection.Enabled {
							out.Candidates = append(out.Candidates, b)
						}
					}
					disabled = true
				}
			}
		}
		if disabled {
			continue
		}
		for _, connection := range c.Connections {
			if !connection.Enabled {
				continue
			}
			if project, ok := gitlab.ProjectFor(remote, connection.URL); ok {
				out.Candidates = append(out.Candidates, forge.Binding{WorkspaceID: id, RepoRoot: root, Remote: r.Name, ConnectionID: connection.ID, Project: project, Mode: "auto"})
			}
		}
	}
	return out, nil
}
func (a *App) forgeProvider(ctx context.Context, id string) (forge.Provider, forge.Connection, error) {
	connections, e := a.ForgeConnections(ctx)
	if e != nil {
		return nil, forge.Connection{}, e
	}
	for _, c := range connections {
		if c.ID == id {
			if !c.Enabled {
				return nil, c, &forge.Error{Reason: "disabled", Problem: "плагин отключён для этого подключения"}
			}
			token, ok := a.mcp().secrets.Get(c.SecretRef)
			if !ok || token == "" {
				return nil, c, &forge.Error{Reason: "secret_locked", Problem: "введите токен GitLab в настройках подключения"}
			}
			switch c.Provider {
			case "gitlab":
				p, e := gitlab.NewRESTClient(c, token)
				return p, c, e
			}
		}
	}
	return nil, forge.Connection{}, errors.New("подключение не найдено")
}
func (a *App) RunForgeRequest(ctx context.Context, q forge.Request) (forge.Response, error) {
	p, connection, e := a.forgeProvider(ctx, q.ConnectionID)
	if e != nil {
		return forge.Response{}, e
	}
	if forge.IsWrite(q.Action) && q.Action != "create" && q.Action != "retryJob" && q.Action != "retryPipeline" && q.IID <= 0 {
		return forge.Response{}, errors.New("нужен MR действия")
	}
	response, e := p.Execute(ctx, q)
	if forge.IsWrite(q.Action) {
		a.auditGitLab(ctx, domain.MCPServer{ID: connection.ID}, q.Action, fmt.Sprintf("%s!%d@%s", q.Project, q.IID, q.ExpectedSHA), q.Body, e)
	}
	var issue *forge.Error
	if errors.As(e, &issue) {
		response.Uncertain = issue.Uncertain
	}
	return response, e
}
