package gitlab

import (
	"errors"
	"net/url"
	"strings"
)

// Remote — адрес git remote, разобранный до узла и пути проекта.
type Remote struct {
	Host string `json:"host"`
	Path string `json:"path"`
}

// ParseRemote понимает три вида адреса: scp (git@host:group/app.git), ssh://
// (с портом и без) и https://. Учётные данные из адреса выбрасываются: их
// не показывают и не хранят.
func ParseRemote(raw string) (Remote, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Remote{}, errors.New("у папки нет git remote origin")
	}
	var host, path string
	switch {
	case strings.Contains(raw, "://"):
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Host == "" {
			return Remote{}, errors.New("адрес git remote не разобрался")
		}
		host, path = parsed.Hostname(), parsed.Path
	default:
		// scp-вид: [user@]host:path. Путь Windows (C:\repo) сюда не попадает:
		// у него после двоеточия обратная косая или буква диска.
		at := strings.LastIndex(raw[:max(strings.Index(raw, ":"), 0)], "@")
		colon := strings.Index(raw, ":")
		if colon <= 0 || strings.HasPrefix(raw[colon+1:], "\\") || strings.HasPrefix(raw[colon+1:], "//") {
			return Remote{}, errors.New("адрес git remote не похож на адрес сервера")
		}
		host, path = raw[at+1:colon], raw[colon+1:]
	}
	path = strings.Trim(strings.TrimSuffix(strings.Trim(path, "/"), ".git"), "/")
	if host == "" || path == "" || !strings.Contains(path, "/") {
		return Remote{}, errors.New("в адресе git remote нет пути проекта")
	}
	return Remote{Host: strings.ToLower(host), Path: path}, nil
}

// ProjectFor — путь проекта GitLab, если remote ведёт на этот GitLab. У
// GitLab под префиксом (https://host/gitlab) префикс снимается с пути.
//
// Узел SSH бывает другим (ssh.gitlab.company против gitlab.company) — тогда
// совпадения нет, и владелец выбирает проект вручную.
func ProjectFor(remote Remote, gitlabURL string) (string, bool) {
	base, err := BaseURL(gitlabURL)
	if err != nil {
		return "", false
	}
	parsed, err := url.Parse(base)
	if err != nil || !strings.EqualFold(parsed.Hostname(), remote.Host) {
		return "", false
	}
	prefix := strings.Trim(parsed.Path, "/")
	path := remote.Path
	if prefix != "" {
		if !strings.HasPrefix(path, prefix+"/") {
			return path, true
		}
		path = strings.TrimPrefix(path, prefix+"/")
	}
	return path, path != ""
}
