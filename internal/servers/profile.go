package servers

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// AuthMethod selects how OpenSSH authenticates. Prefer agent or key; password
// is interactive-only for automated probe/list/exec.
type AuthMethod string

const (
	AuthAgent    AuthMethod = "agent"
	AuthKey      AuthMethod = "key"
	AuthPassword AuthMethod = "password"
)

// ProfileStatus mirrors provider connection status labels for Hub UI.
type ProfileStatus string

const (
	StatusUnknown   ProfileStatus = "unknown"
	StatusConnected ProfileStatus = "connected"
	StatusError     ProfileStatus = "error"
)

// Profile is a durable SSH server connection without secrets.
// Password material stays in IDE SecretStorage behind SecretRef.
type Profile struct {
	ID           string        `json:"id"`
	DisplayName  string        `json:"displayName"`
	Host         string        `json:"host"`
	Port         int           `json:"port"`
	User         string        `json:"user"`
	AuthMethod   AuthMethod    `json:"authMethod"`
	PrivateKeyPath string      `json:"privateKeyPath,omitempty"`
	SecretRef    string        `json:"secretRef,omitempty"`
	DefaultRemotePath string   `json:"defaultRemotePath,omitempty"`
	Status       ProfileStatus `json:"status"`
	LastError    string        `json:"lastError,omitempty"`
	LastProbeAt  *time.Time    `json:"lastProbeAt,omitempty"`
	CreatedAt    time.Time     `json:"createdAt"`
	UpdatedAt    time.Time     `json:"updatedAt"`
}

// UpsertRequest is the public write shape for Hub / API.
type UpsertRequest struct {
	ID                string     `json:"id"`
	DisplayName       string     `json:"displayName"`
	Host              string     `json:"host"`
	Port              int        `json:"port"`
	User              string     `json:"user"`
	AuthMethod        AuthMethod `json:"authMethod"`
	PrivateKeyPath    string     `json:"privateKeyPath"`
	SecretRef         string     `json:"secretRef"`
	DefaultRemotePath string     `json:"defaultRemotePath"`
	Status            ProfileStatus `json:"status"`
}

func Normalize(req UpsertRequest) (Profile, error) {
	host := strings.TrimSpace(req.Host)
	user := strings.TrimSpace(req.User)
	if host == "" {
		return Profile{}, fmt.Errorf("укажите хост SSH-сервера")
	}
	if strings.ContainsAny(host, " \t\r\n@") {
		return Profile{}, fmt.Errorf("хост не должен содержать пробелы или @")
	}
	if user == "" {
		return Profile{}, fmt.Errorf("укажите пользователя SSH")
	}
	if strings.ContainsAny(user, " \t\r\n@") {
		return Profile{}, fmt.Errorf("имя пользователя SSH недопустимо")
	}
	port := req.Port
	if port == 0 {
		port = 22
	}
	if port < 1 || port > 65535 {
		return Profile{}, fmt.Errorf("порт SSH должен быть в диапазоне 1–65535")
	}
	method := AuthMethod(strings.ToLower(strings.TrimSpace(string(req.AuthMethod))))
	if method == "" {
		method = AuthAgent
	}
	switch method {
	case AuthAgent, AuthKey, AuthPassword:
	default:
		return Profile{}, fmt.Errorf("способ входа: agent, key или password")
	}
	keyPath := strings.TrimSpace(req.PrivateKeyPath)
	if method == AuthKey {
		if keyPath == "" {
			return Profile{}, fmt.Errorf("для входа по ключу укажите путь к приватному ключу")
		}
		if !filepath.IsAbs(keyPath) {
			return Profile{}, fmt.Errorf("путь к ключу должен быть абсолютным")
		}
	}
	name := strings.TrimSpace(req.DisplayName)
	if name == "" {
		name = fmt.Sprintf("%s@%s", user, host)
		if port != 22 {
			name = fmt.Sprintf("%s@%s:%d", user, host, port)
		}
	}
	status := req.Status
	if status == "" {
		status = StatusUnknown
	}
	remote := strings.TrimSpace(req.DefaultRemotePath)
	if remote == "" {
		remote = "~"
	}
	now := time.Now().UTC()
	profile := Profile{
		ID: strings.TrimSpace(req.ID), DisplayName: name, Host: host, Port: port, User: user,
		AuthMethod: method, PrivateKeyPath: keyPath, SecretRef: strings.TrimSpace(req.SecretRef),
		DefaultRemotePath: remote, Status: status, CreatedAt: now, UpdatedAt: now,
	}
	return profile, nil
}

// TargetHost returns the host used for network-policy checks.
func (p Profile) TargetHost() string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(p.Host), "."))
}

// Destination is user@host for OpenSSH argv.
func (p Profile) Destination() string {
	return p.User + "@" + p.Host
}
