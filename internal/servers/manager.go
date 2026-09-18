package servers

import (
	"context"
	"fmt"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"

	"local-agent-workbench/internal/security"
)

type Store interface {
	SaveServerProfile(ctx context.Context, profile Profile) error
	ListServerProfiles(ctx context.Context) ([]Profile, error)
	GetServerProfile(ctx context.Context, id string) (Profile, error)
	DeleteServerProfile(ctx context.Context, id string) error
}

type Manager struct {
	Store  Store
	Runner Runner
}

func (m Manager) runner() Runner {
	if m.Runner != nil {
		return m.Runner
	}
	return OpenSSHRunner{}
}

func (m Manager) Upsert(ctx context.Context, req UpsertRequest) (Profile, error) {
	profile, err := Normalize(req)
	if err != nil {
		return Profile{}, err
	}
	if profile.ID == "" {
		profile.ID = domain.NewID("server")
	} else if existing, getErr := m.Store.GetServerProfile(ctx, profile.ID); getErr == nil {
		profile.CreatedAt = existing.CreatedAt
		if profile.SecretRef == "" {
			profile.SecretRef = existing.SecretRef
		}
		if profile.Status == StatusUnknown && existing.Status != "" {
			profile.Status = existing.Status
		}
		profile.LastError = existing.LastError
		profile.LastProbeAt = existing.LastProbeAt
	}
	if profile.AuthMethod == AuthPassword && strings.TrimSpace(profile.SecretRef) == "" {
		profile.SecretRef = "point.server." + profile.ID
	}
	profile.UpdatedAt = time.Now().UTC()
	if err = m.Store.SaveServerProfile(ctx, profile); err != nil {
		return Profile{}, err
	}
	return profile, nil
}

func (m Manager) List(ctx context.Context) ([]Profile, error) {
	return m.Store.ListServerProfiles(ctx)
}

func (m Manager) Get(ctx context.Context, id string) (Profile, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Profile{}, fmt.Errorf("не указан идентификатор сервера")
	}
	return m.Store.GetServerProfile(ctx, id)
}

func (m Manager) Delete(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("не указан идентификатор сервера")
	}
	return m.Store.DeleteServerProfile(ctx, id)
}

func (m Manager) MarkStatus(ctx context.Context, id string, status ProfileStatus, lastError string) (Profile, error) {
	profile, err := m.Get(ctx, id)
	if err != nil {
		return Profile{}, err
	}
	now := time.Now().UTC()
	profile.Status = status
	// Ошибка приходит снаружи и может нести ключ или строку подключения:
	// она сохраняется и показывается на экране «Связи».
	profile.LastError = security.Redact(lastError)
	profile.LastProbeAt = &now
	profile.UpdatedAt = now
	if err = m.Store.SaveServerProfile(ctx, profile); err != nil {
		return Profile{}, err
	}
	return profile, nil
}

type ProbeResult struct {
	OK      bool    `json:"ok"`
	Message string  `json:"message"`
	Banner  string  `json:"banner,omitempty"`
	Profile Profile `json:"profile"`
}

type ListResult struct {
	Path    string   `json:"path"`
	Entries []string `json:"entries"`
	Raw     string   `json:"raw,omitempty"`
	Profile Profile  `json:"profile"`
}

// ReadResult is a bounded text preview of one remote file. It deliberately
// has no write counterpart: editing a preview must never look like it changed
// the server.
type ReadResult struct {
	Path      string  `json:"path"`
	Content   string  `json:"content"`
	Truncated bool    `json:"truncated,omitempty"`
	Profile   Profile `json:"profile"`
}

func (m Manager) Probe(ctx context.Context, id string, password string) (ProbeResult, error) {
	profile, err := m.Get(ctx, id)
	if err != nil {
		return ProbeResult{}, err
	}
	result, runErr := m.runner().Probe(ctx, profile, password)
	if runErr != nil {
		updated, _ := m.MarkStatus(ctx, id, StatusError, runErr.Error())
		result.Profile = updated
		result.OK = false
		result.Message = runErr.Error()
		return result, nil
	}
	updated, _ := m.MarkStatus(ctx, id, StatusConnected, "")
	result.Profile = updated
	result.OK = true
	if result.Message == "" {
		result.Message = "Соединение установлено"
	}
	return result, nil
}

func (m Manager) ListRemote(ctx context.Context, id, remotePath, password string) (ListResult, error) {
	profile, err := m.Get(ctx, id)
	if err != nil {
		return ListResult{}, err
	}
	path := strings.TrimSpace(remotePath)
	if path == "" {
		path = profile.DefaultRemotePath
	}
	entries, raw, runErr := m.runner().List(ctx, profile, path, password)
	if runErr != nil {
		return ListResult{Path: path, Profile: profile}, runErr
	}
	return ListResult{Path: path, Entries: entries, Raw: raw, Profile: profile}, nil
}

func (m Manager) ReadRemote(ctx context.Context, id, remotePath, password string) (ReadResult, error) {
	profile, err := m.Get(ctx, id)
	if err != nil {
		return ReadResult{}, err
	}
	path := strings.TrimSpace(remotePath)
	if path == "" {
		return ReadResult{}, fmt.Errorf("укажите путь к удалённому файлу")
	}
	content, truncated, runErr := m.runner().Read(ctx, profile, path, password)
	if runErr != nil {
		return ReadResult{Path: path, Profile: profile}, runErr
	}
	return ReadResult{Path: path, Content: content, Truncated: truncated, Profile: profile}, nil
}

// TerminalArgv returns OpenSSH argv suitable for an IDE terminal (without ssh binary).
func TerminalArgv(profile Profile) ([]string, error) {
	if _, err := LookPathSSH(); err != nil {
		return nil, err
	}
	return buildSSHArgs(profile, false, nil), nil
}
