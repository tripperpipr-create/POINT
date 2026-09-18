package app

import (
	"context"
	"errors"
	"strings"

	"local-agent-workbench/internal/servers"
)

type ServerProbeRequest struct {
	Password string `json:"password,omitempty"`
}

type ServerListRequest struct {
	Path     string `json:"path"`
	Password string `json:"password,omitempty"`
}

type ServerReadRequest struct {
	Path     string `json:"path"`
	Password string `json:"password,omitempty"`
}

func (a *App) serverManager() servers.Manager {
	return servers.Manager{Store: a.store}
}

func (a *App) SaveServerProfile(req servers.UpsertRequest) (servers.Profile, error) {
	return a.serverManager().Upsert(context.Background(), req)
}

func (a *App) ListServerProfiles() ([]servers.Profile, error) {
	return a.serverManager().List(context.Background())
}

func (a *App) DeleteServerProfile(id string) error {
	return a.serverManager().Delete(context.Background(), id)
}

func (a *App) ProbeServerProfile(id string, req ServerProbeRequest) (servers.ProbeResult, error) {
	return a.serverManager().Probe(context.Background(), id, req.Password)
}

func (a *App) ListServerRemotePath(id string, req ServerListRequest) (servers.ListResult, error) {
	return a.serverManager().ListRemote(context.Background(), id, req.Path, req.Password)
}

func (a *App) ReadServerRemoteFile(id string, req ServerReadRequest) (servers.ReadResult, error) {
	return a.serverManager().ReadRemote(context.Background(), id, req.Path, req.Password)
}

func (a *App) ServerTerminalArgv(id string) (map[string]any, error) {
	profile, err := a.serverManager().Get(context.Background(), id)
	if err != nil {
		return nil, err
	}
	args, err := servers.TerminalArgv(profile)
	if err != nil {
		return nil, err
	}
	sshPath, lookErr := servers.LookPathSSH()
	if lookErr != nil {
		return nil, lookErr
	}
	return map[string]any{
		"sshPath": sshPath,
		"args":    args,
		"profile": profile,
		"label":   "SSH · " + profile.DisplayName,
	}, nil
}

func (a *App) GetServerProfile(id string) (servers.Profile, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return servers.Profile{}, errors.New("id is required")
	}
	return a.serverManager().Get(context.Background(), id)
}

// serverProfileBridge adapts storage for agent SSH tools.
type serverProfileBridge struct{ app *App }

func (b serverProfileBridge) GetServerProfile(ctx context.Context, id string) (servers.Profile, error) {
	return b.app.store.GetServerProfile(ctx, id)
}
