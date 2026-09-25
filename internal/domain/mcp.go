package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"
)

// MCPTransport — как Point говорит с MCP-сервером.
type MCPTransport string

const (
	// MCPTransportStdio — Point сам запускает программу сервера на этой машине.
	MCPTransportStdio MCPTransport = "stdio"
	// MCPTransportHTTP — сервер уже работает где-то и принимает Streamable HTTP.
	MCPTransportHTTP MCPTransport = "http"
)

// MCPServerKind — чей это сервер: встроенного плагина (у него свой экран) или
// владельца (только инструменты).
type MCPServerKind string

const (
	MCPServerCustom MCPServerKind = "custom"
	MCPServerGitLab MCPServerKind = "gitlab"
)

// MCPServer — подключённый MCP-сервер. Секреты хранятся в SecretStorage IDE;
// здесь — только ссылки на них (secretRef), значения живут в памяти ядра.
type MCPServer struct {
	ID          string        `json:"id"`
	DisplayName string        `json:"displayName"`
	Kind        MCPServerKind `json:"kind"`
	Transport   MCPTransport  `json:"transport"`

	// stdio
	Command   string            `json:"command,omitempty"`
	Args      []string          `json:"args,omitempty"`
	Dir       string            `json:"dir,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	SecretEnv map[string]string `json:"secretEnv,omitempty"`

	// Streamable HTTP
	URL              string            `json:"url,omitempty"`
	Headers          map[string]string `json:"headers,omitempty"`
	SecretHeaders    map[string]string `json:"secretHeaders,omitempty"`
	AllowPrivateHost string            `json:"allowPrivateHost,omitempty"`

	// Settings — настройки плагина, не секреты (адрес GitLab, путь к CA).
	Settings map[string]string `json:"settings,omitempty"`

	// TrustDigest — отпечаток той конфигурации, которой владелец доверил запуск
	// на своей машине. Пусто — не доверено. Отпечаток текущей конфигурации
	// считает MCPTrustDigest; расхождение — доверие потеряно.
	TrustDigest     string     `json:"trustDigest,omitempty"`
	TrustedAt       *time.Time `json:"trustedAt,omitempty"`
	ResolvedCommand string     `json:"resolvedCommand,omitempty"`

	Status          ConnectionStatus `json:"status"`
	LastError       string           `json:"lastError,omitempty"`
	LastProbeAt     *time.Time       `json:"lastProbeAt,omitempty"`
	ServerName      string           `json:"serverName,omitempty"`
	ServerVersion   string           `json:"serverVersion,omitempty"`
	ProtocolVersion string           `json:"protocolVersion,omitempty"`
	CreatedAt       time.Time        `json:"createdAt"`
	UpdatedAt       time.Time        `json:"updatedAt"`
}

// MCPToolState — как снимок инструмента соотносится с одобренным.
type MCPToolState string

const (
	// MCPToolNew — появился впервые, владелец его не смотрел.
	MCPToolNew MCPToolState = "new"
	// MCPToolOK — совпадает с одобренным.
	MCPToolOK MCPToolState = "ok"
	// MCPToolChanged — описание или схема изменились после одобрения. Описание
	// попадает в промпт модели, поэтому изменённый инструмент выключается до
	// нового взгляда владельца («подмена» инструмента).
	MCPToolChanged MCPToolState = "changed"
	// MCPToolMissing — сервер его больше не отдаёт.
	MCPToolMissing MCPToolState = "missing"
)

// MCPTool — снимок одного инструмента сервера.
type MCPTool struct {
	ServerID       string          `json:"serverId"`
	Name           string          `json:"name"`
	Title          string          `json:"title,omitempty"`
	Description    string          `json:"description,omitempty"`
	InputSchema    json.RawMessage `json:"inputSchema,omitempty"`
	Annotations    json.RawMessage `json:"annotations,omitempty"`
	Digest         string          `json:"digest"`
	ApprovedDigest string          `json:"approvedDigest,omitempty"`
	Enabled        bool            `json:"enabled"`
	State          MCPToolState    `json:"state"`
	Risk           ToolRisk        `json:"risk"`
	UpdatedAt      time.Time       `json:"updatedAt"`
}

// IntegrationAction — запись журнала действий во внешнем сервисе: что сделано,
// где и чем кончилось. Тело (текст комментария) не хранится — только его
// отпечаток и длина.
type IntegrationAction struct {
	ID         string    `json:"id"`
	Actor      string    `json:"actor"`
	ServerID   string    `json:"serverId"`
	Tool       string    `json:"tool"`
	Target     string    `json:"target"`
	Outcome    string    `json:"outcome"`
	Error      string    `json:"error,omitempty"`
	BodySHA256 string    `json:"bodySha256,omitempty"`
	BodyLength int       `json:"bodyLength,omitempty"`
	At         time.Time `json:"at"`
}

// GitLabBindMode — как папка выбирает проект GitLab.
type GitLabBindMode string

const (
	// GitLabBindAuto — проект по git remote origin папки.
	GitLabBindAuto GitLabBindMode = "auto"
	// GitLabBindManual — проект назван владельцем.
	GitLabBindManual GitLabBindMode = "manual"
	// GitLabBindAll — без проекта: MR владельца по всем проектам.
	GitLabBindAll GitLabBindMode = "all"
)

// GitLabBinding — какой проект GitLab показывать для папки. Username —
// ручная замена имени владельца, если сервер не отвечает на whoami.
type GitLabBinding struct {
	WorkspaceID string         `json:"workspaceId"`
	ServerID    string         `json:"serverId"`
	Mode        GitLabBindMode `json:"mode"`
	ProjectPath string         `json:"projectPath,omitempty"`
	Username    string         `json:"username,omitempty"`
	UpdatedAt   time.Time      `json:"updatedAt"`
}

// MCPTrustDigest — отпечаток того, что будет запущено: транспорт, команда и
// путь, в который она разрешилась, аргументы, каталог, имена переменных и
// открытые значения, имена секретов, адрес и заголовки. Значения секретов в
// отпечаток не входят: смена токена не требует нового доверия, смена команды —
// требует.
func MCPTrustDigest(server MCPServer, resolvedCommand string) string {
	canonical := struct {
		Transport        MCPTransport      `json:"transport"`
		Command          string            `json:"command"`
		Resolved         string            `json:"resolved"`
		Args             []string          `json:"args"`
		Dir              string            `json:"dir"`
		Env              map[string]string `json:"env"`
		SecretEnv        []string          `json:"secretEnv"`
		URL              string            `json:"url"`
		Headers          map[string]string `json:"headers"`
		SecretHeaders    []string          `json:"secretHeaders"`
		AllowPrivateHost string            `json:"allowPrivateHost"`
	}{
		Transport: server.Transport, Command: server.Command, Resolved: resolvedCommand,
		Args: server.Args, Dir: server.Dir, Env: server.Env, SecretEnv: sortedKeys(server.SecretEnv),
		URL: server.URL, Headers: server.Headers, SecretHeaders: sortedKeys(server.SecretHeaders),
		AllowPrivateHost: server.AllowPrivateHost,
	}
	// encoding/json пишет ключи карт по порядку — запись каноническая.
	raw, _ := json.Marshal(canonical)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// MCPToolDigest — отпечаток того, что модель и экран узнают об инструменте:
// имя, описание, схема и подсказки.
func MCPToolDigest(name, title, description string, inputSchema, annotations json.RawMessage) string {
	canonical := struct {
		Name        string          `json:"name"`
		Title       string          `json:"title"`
		Description string          `json:"description"`
		InputSchema json.RawMessage `json:"inputSchema"`
		Annotations json.RawMessage `json:"annotations"`
	}{name, title, description, compactJSON(inputSchema), compactJSON(annotations)}
	raw, _ := json.Marshal(canonical)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// compactJSON приводит JSON к одной записи: сервер вправе переставить ключи
// или пробелы между двумя списками, и это не изменение инструмента.
func compactJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("null")
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return raw
	}
	out, err := json.Marshal(value)
	if err != nil {
		return raw
	}
	return out
}
