package app

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"

	"local-agent-workbench/internal/forge"
	"local-agent-workbench/internal/gitflow"
	"local-agent-workbench/internal/integrations/gitlab"
	"local-agent-workbench/internal/storage"
)

const forgeSettingsKey = "git.forge.v1"

var forgeSettingsLock sync.Mutex

type forgeConfig struct {
	Connections []forge.Connection `json:"connections"`
	Bindings    []forge.Binding    `json:"bindings"`
}

func (a *App) loadForgeConfig(ctx context.Context) (forgeConfig, error) {
	raw, e := a.store.Setting(ctx, forgeSettingsKey)
	if e == nil {
		var c forgeConfig
		e = json.Unmarshal([]byte(raw), &c)
		return c, e
	}
	if !storage.IsNotFound(e) {
		return forgeConfig{}, e
	}
	c := forgeConfig{Connections: []forge.Connection{}, Bindings: []forge.Binding{}}
	if legacy, e := a.store.GetMCPServer(ctx, gitlabServerID); e == nil && legacy.Settings["url"] != "" {
		c.Connections = append(c.Connections, forge.Connection{ID: "gitlab-legacy", Provider: "gitlab", Name: "GitLab", URL: legacy.Settings["url"], CAPath: legacy.Settings["caPath"], SecretRef: legacy.SecretEnv[gitlab.TokenVariable], Enabled: true})
	}
	return c, a.saveForgeConfig(ctx, c)
}
func (a *App) saveForgeConfig(ctx context.Context, c forgeConfig) error {
	raw, e := json.Marshal(c)
	if e != nil {
		return e
	}
	return a.store.SaveSetting(ctx, forgeSettingsKey, string(raw))
}
func (a *App) ForgeConnections(ctx context.Context) ([]forge.Connection, error) {
	forgeSettingsLock.Lock()
	defer forgeSettingsLock.Unlock()
	c, e := a.loadForgeConfig(ctx)
	if c.Connections == nil {
		c.Connections = []forge.Connection{}
	}
	return c.Connections, e
}

type ForgeConnectionInput struct {
	forge.Connection
	Token string `json:"token,omitempty"`
}

func (a *App) SaveForgeConnection(ctx context.Context, input ForgeConnectionInput) (forge.Connection, error) {
	forgeSettingsLock.Lock()
	defer forgeSettingsLock.Unlock()
	c, e := a.loadForgeConfig(ctx)
	if e != nil {
		return forge.Connection{}, e
	}
	v := input.Connection
	if v.Provider != "gitlab" {
		return v, errors.New("провайдер недоступен")
	}
	if v.ID == "" {
		v.ID = domainForgeID()
	}
	if !forgeIDPattern.MatchString(v.ID) {
		return v, errors.New("неверный id подключения")
	}
	base, e := gitlab.BaseURL(v.URL)
	if e != nil {
		return v, e
	}
	v.URL = base
	if v.CAPath != "" && !filepath.IsAbs(v.CAPath) {
		return v, errors.New("нужен абсолютный путь CA")
	}
	if _, e = gitlab.NewRESTClient(v, ""); e != nil {
		return v, e
	}
	found := -1
	for i, old := range c.Connections {
		if old.ID == v.ID {
			found = i
			v.SecretRef = old.SecretRef
			if old.URL != v.URL && input.Token == "" {
				return v, errors.New("при смене сервера введите токен для нового адреса")
			}
		}
	}
	if found < 0 {
		v.SecretRef = "point.forge." + v.ID + ".token"
	}
	if v.Name == "" {
		v.Name = "GitLab"
	}
	if found >= 0 {
		c.Connections[found] = v
	} else {
		c.Connections = append(c.Connections, v)
	}
	if e = a.saveForgeConfig(ctx, c); e != nil {
		return v, e
	}
	if input.Token != "" {
		a.mcp().secrets.Put(v.SecretRef, input.Token)
	}
	return v, nil
}
func (a *App) DeleteForgeConnection(ctx context.Context, id string) error {
	forgeSettingsLock.Lock()
	defer forgeSettingsLock.Unlock()
	c, e := a.loadForgeConfig(ctx)
	if e != nil {
		return e
	}
	out := []forge.Connection{}
	var ref string
	for _, v := range c.Connections {
		if v.ID == id {
			ref = v.SecretRef
		} else {
			out = append(out, v)
		}
	}
	if ref == "" {
		return errors.New("подключение не найдено")
	}
	c.Connections = out
	if e = a.saveForgeConfig(ctx, c); e != nil {
		return e
	}
	a.mcp().secrets.Delete(ref)
	return nil
}
func (a *App) UnlockForgeSecrets(ctx context.Context, values map[string]string) error {
	connections, e := a.ForgeConnections(ctx)
	if e != nil {
		return e
	}
	allowed := map[string]bool{}
	for _, c := range connections {
		allowed[c.SecretRef] = true
	}
	for ref, value := range values {
		if !allowed[ref] {
			return errors.New("секрет не принадлежит подключению Git")
		}
		a.mcp().secrets.Put(ref, value)
	}
	return nil
}
func bindingKey(b forge.Binding) string {
	return b.WorkspaceID + "\x00" + filepath.Clean(b.RepoRoot) + "\x00" + b.Remote
}
func (a *App) SaveForgeBinding(ctx context.Context, b forge.Binding) (forge.Binding, error) {
	if b.WorkspaceID == "" {
		return b, errors.New("нужен workspaceId связи")
	}
	id, root, e := a.gitWorkbenchRoot(ctx, GitTarget{WorkspaceID: b.WorkspaceID, RepoRoot: b.RepoRoot})
	if e != nil {
		return b, e
	}
	b.WorkspaceID = id
	b.RepoRoot = root
	if b.Mode != "auto" && b.Mode != "manual" && b.Mode != "off" {
		return b, errors.New("неверный режим связи")
	}
	s, e := gitflow.ReadSnapshot(ctx, a.gitInspectRunner(), root)
	if e != nil {
		return b, e
	}
	found := false
	for _, r := range s.Remotes {
		if r.Name == b.Remote {
			found = true
		}
	}
	if !found {
		return b, errors.New("remote не найден")
	}
	forgeSettingsLock.Lock()
	defer forgeSettingsLock.Unlock()
	c, e := a.loadForgeConfig(ctx)
	if e != nil {
		return b, e
	}
	if b.Mode == "manual" {
		valid := false
		for _, v := range c.Connections {
			if v.ID == b.ConnectionID && v.Enabled {
				valid = true
			}
		}
		if !valid || b.Project == "" || strings.Contains(b.Project, "..") {
			return b, errors.New("выберите подключение и проект")
		}
	}
	found = false
	for i, old := range c.Bindings {
		if bindingKey(old) == bindingKey(b) {
			c.Bindings[i] = b
			found = true
		}
	}
	if !found {
		c.Bindings = append(c.Bindings, b)
	}
	return b, a.saveForgeConfig(ctx, c)
}
