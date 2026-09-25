// Package gitlab — встроенный плагин GitLab: как запустить его MCP-сервер и
// как превратить ответы инструментов в экраны Point.
//
// Point не ходит в REST API GitLab сам. Данные приходят через закреплённый
// сторонний MCP-сервер (docs/integrations-gitlab.md), а этот пакет знает
// только его инструменты и форму их ответов. Модель здесь не участвует.
package gitlab

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"local-agent-workbench/internal/domain"
)

const (
	// ServerPackage и ServerVersion — закреплённый сервер. Смена версии —
	// отдельная правка: новые фикстуры, сверка таблицы инструментов.
	ServerPackage = "@zereight/mcp-gitlab"
	ServerVersion = "2.1.66"
	// TokenVariable — переменная, в которой сервер ждёт личный токен.
	TokenVariable = "GITLAB_PERSONAL_ACCESS_TOKEN"
)

// Tools — всё, что Point вызывает у сервера. Остальные инструменты сервера
// экранам не нужны и агентам по умолчанию выключены.
var Tools = []string{
	"whoami", "get_project", "list_merge_requests", "get_merge_request", "get_merge_request_approval_state",
	"mr_discussions", "list_merge_request_changed_files", "get_merge_request_file_diff", "get_merge_request_diffs",
	"get_file_contents", "create_merge_request_note", "create_merge_request_discussion_note",
	"approve_merge_request", "unapprove_merge_request", "merge_merge_request", "list_merge_request_pipelines",
	"list_pipelines", "get_pipeline", "list_pipeline_jobs", "get_pipeline_job_output", "retry_pipeline_job",
}

// Settings — настройки плагина, которые видит владелец. Не секреты.
type Settings struct {
	// URL — адрес GitLab, как его открывают в браузере: https://gitlab.company.local
	URL string `json:"url"`
	// CAPath — путь к сертификату внутреннего удостоверяющего центра, если
	// GitLab подписан им. Node системное хранилище не читает.
	CAPath string `json:"caPath,omitempty"`
}

// Launch — из чего собирается сервер плагина.
type Launch struct {
	Command   string
	Args      []string
	Env       map[string]string
	SecretEnv []string
}

// Recipe — команда запуска закреплённого сервера для этих настроек.
func Recipe(settings Settings) (Launch, error) {
	api, err := APIURL(settings.URL)
	if err != nil {
		return Launch{}, err
	}
	env := map[string]string{
		"GITLAB_API_URL": api,
		// Наборы и отдельные инструменты — чтобы сервер не отдавал сотню
		// лишних; режим modify убирает удаление и разрушительные инструменты;
		// discover_tools добавлял бы инструменты во время работы.
		"GITLAB_TOOLSETS":           "merge_requests",
		"GITLAB_TOOLS":              strings.Join(Tools, ","),
		"GITLAB_PERMISSION_MODE":    "modify",
		"GITLAB_DENIED_TOOLS_REGEX": "^discover_tools$",
	}
	if path := strings.TrimSpace(settings.CAPath); path != "" {
		env["GITLAB_CA_CERT_PATH"] = path
		env["NODE_EXTRA_CA_CERTS"] = path
	}
	return Launch{
		Command:   "npx",
		Args:      []string{"-y", ServerPackage + "@" + ServerVersion},
		Env:       env,
		SecretEnv: []string{TokenVariable},
	}, nil
}

// APIURL приводит адрес GitLab к адресу его API: https://host[/префикс]/api/v4.
// Адрес из браузера и адрес с /api/v4 на конце дают одно и то же.
func APIURL(raw string) (string, error) {
	base, err := BaseURL(raw)
	if err != nil {
		return "", err
	}
	return base + "/api/v4", nil
}

// BaseURL — адрес GitLab без /api/v4, без запроса и без хвостового слэша.
func BaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("укажите адрес GitLab, например https://gitlab.company.local")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "", fmt.Errorf("адрес GitLab %q не разобрался", raw)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return "", fmt.Errorf("адрес GitLab должен начинаться с https://")
	}
	if parsed.User != nil {
		return "", errors.New("учётные данные в адресе GitLab не принимаются — токен вводится отдельно")
	}
	path := strings.TrimRight(parsed.Path, "/")
	path = strings.TrimSuffix(path, "/api/v4")
	return strings.ToLower(parsed.Scheme) + "://" + strings.ToLower(parsed.Host) + path, nil
}

// PresetRisk — риск инструмента плагина для агентов (этап 2). Чтение — LOW,
// запись от имени владельца — HIGH, merge — CRITICAL.
func PresetRisk(tool string) (domain.ToolRisk, bool) {
	switch tool {
	case "merge_merge_request":
		return domain.ToolRiskCritical, true
	case "create_merge_request_note", "create_merge_request_discussion_note", "approve_merge_request",
		"unapprove_merge_request", "retry_pipeline_job":
		return domain.ToolRiskHigh, true
	}
	for _, name := range Tools {
		if name == tool {
			return domain.ToolRiskLow, true
		}
	}
	return "", false
}
