package domain

import (
	"encoding/json"
	"time"
)

type ProviderKind string

const (
	ProviderOllama ProviderKind = "ollama"
	ProviderOpenAI ProviderKind = "openai-compatible"
	// Anthropic Messages API — отдельный протокол, а не пресет к
	// OpenAI-совместимому: системная инструкция уходит своим полем, инструменты
	// приходят блоками tool_use, а поток событий устроен по-своему.
	ProviderAnthropic ProviderKind = "anthropic"
	// Azure отдаёт тот же OpenAI-протокол, но с другим заголовком авторизации,
	// путём через deployment и обязательной api-version в query.
	ProviderAzureOpenAI ProviderKind = "azure-openai"
	// Cursor / Claude Code / Codex CLI сняты с продукта (API-only cutover).
	// Константы остаются, чтобы старые записи отвергать с явной ошибкой, а не
	// принимать молча как неизвестный вид.
	ProviderCursor    ProviderKind = "cursor-cli"
	ProviderClaudeCLI ProviderKind = "claude-code-cli"
	ProviderCodexCLI  ProviderKind = "codex-cli"
)

// IsHTTPAPIProvider — допустимый мозг помощника и агента: только сетевой /
// локальный HTTP endpoint (Ollama, OpenAI-совместимый, Anthropic, Azure).
func IsHTTPAPIProvider(kind ProviderKind) bool {
	switch kind {
	case ProviderOllama, ProviderOpenAI, ProviderAnthropic, ProviderAzureOpenAI:
		return true
	default:
		return false
	}
}

// IsAgentCLIProvider — локальные/внешние CLI, выведенные из продукта.
func IsAgentCLIProvider(kind ProviderKind) bool {
	switch kind {
	case ProviderCursor, ProviderClaudeCLI, ProviderCodexCLI:
		return true
	default:
		return false
	}
}

type ApprovalMode string

const (
	ApprovalSafe   ApprovalMode = "safe"
	ApprovalAlways ApprovalMode = "always"
)

type RunStatus string

const (
	RunPending     RunStatus = "pending"
	RunRunning     RunStatus = "running"
	RunPaused      RunStatus = "paused"
	RunWaiting     RunStatus = "waiting_approval"
	RunCompleted   RunStatus = "completed"
	RunFailed      RunStatus = "failed"
	RunCancelled   RunStatus = "cancelled"
	RunInterrupted RunStatus = "interrupted"
)

type ApprovalStatus string

const (
	ApprovalPending ApprovalStatus = "pending"
	ApprovalAllowed ApprovalStatus = "allowed"
	ApprovalDenied  ApprovalStatus = "denied"
)

type AgentProfile struct {
	ID              string       `json:"id"`
	Name            string       `json:"name"`
	RoleDescription string       `json:"roleDescription"`
	SystemPrompt    string       `json:"systemPrompt"`
	Goals           []string     `json:"goals"`
	Rules           []string     `json:"rules"`
	Provider        ProviderKind `json:"provider"`
	ProviderPreset  string       `json:"providerPreset"`
	ConnectionID    string       `json:"connectionId,omitempty"`
	BaseURL         string       `json:"baseUrl"`
	// APIVersion заполняется из подключения перед запуском и нужна только
	// Azure. В профиле её не задают руками: адрес и версия принадлежат
	// подключению, а не агенту.
	APIVersion          string            `json:"apiVersion,omitempty"`
	Model               string            `json:"model"`
	Temperature         float64           `json:"temperature"`
	MaxOutputTokens     int               `json:"maxOutputTokens"`
	ContextWindowTokens int               `json:"contextWindowTokens"`
	ReasoningEffort     string            `json:"reasoningEffort"`
	AllowedTools        []string          `json:"allowedTools"`
	ToolPolicies        map[string]string `json:"toolPolicies,omitempty"`
	FallbackModels      []string          `json:"fallbackModels,omitempty"`
	EquippedSkills      []SkillRuntime    `json:"equippedSkills,omitempty"`
	MaxSteps            int               `json:"maxSteps"`
	MaxDurationSeconds  int               `json:"maxDurationSeconds"`
	ApprovalMode        ApprovalMode      `json:"approvalMode"`
	CreatedAt           time.Time         `json:"createdAt"`
	UpdatedAt           time.Time         `json:"updatedAt"`
}

type Workspace struct {
	ID       string    `json:"id"`
	Path     string    `json:"path"`
	Name     string    `json:"name"`
	OpenedAt time.Time `json:"openedAt"`
}

type Run struct {
	ID                    string                   `json:"id"`
	AgentID               string                   `json:"agentId"`
	ProfileID             string                   `json:"profileId"`
	WorkspaceID           string                   `json:"workspaceId"`
	Task                  string                   `json:"task"`
	ContextItems          []RunContextItem         `json:"contextItems"`
	ConfigurationSnapshot RunConfigurationSnapshot `json:"configurationSnapshot"`
	Provider              string                   `json:"provider"`
	Model                 string                   `json:"model"`
	Status                RunStatus                `json:"status"`
	Step                  int                      `json:"step"`
	RequestCount          int                      `json:"requestCount"`
	ToolsUsed             []string                 `json:"toolsUsed"`
	ChangedFiles          []string                 `json:"changedFiles"`
	Error                 string                   `json:"error,omitempty"`
	Result                string                   `json:"result,omitempty"`
	StartedAt             time.Time                `json:"startedAt"`
	FinishedAt            *time.Time               `json:"finishedAt,omitempty"`
	DurationMs            int64                    `json:"durationMs"`
	Controller            RunControllerState       `json:"controller,omitempty"`
}

type EventType string

const (
	EventRunStarted         EventType = "run.started"
	EventModelRequested     EventType = "model.requested"
	EventModelRetrying      EventType = "model.retrying"
	EventModelStreamed      EventType = "model.streamed"
	EventModelUsage         EventType = "model.usage"
	EventModelResponded     EventType = "model.responded"
	EventContextCompacted   EventType = "context.compacted"
	EventContextAmended     EventType = "context.amended"
	EventRunMessageInjected EventType = "run.message_injected"
	EventCompletionChecked  EventType = "completion.checked"
	EventToolRequested      EventType = "tool.requested"
	EventApprovalRequested  EventType = "approval.requested"
	EventApprovalResolved   EventType = "approval.resolved"
	EventToolStarted        EventType = "tool.started"
	EventToolFinished       EventType = "tool.finished"
	EventPatchProposed      EventType = "patch.proposed"
	EventPatchApplied       EventType = "patch.applied"
	EventPatchRejected      EventType = "patch.rejected"
	EventPatchReverted      EventType = "patch.reverted"
	EventWorkspaceChanged   EventType = "workspace.changed"
	EventAgentGuardrail     EventType = "agent.guardrail"
	EventOrchestratorWatch  EventType = "orchestrator.supervision"
	EventRunCancelled       EventType = "run.cancelled"
	EventRunFailed          EventType = "run.failed"
	EventRunCompleted       EventType = "run.completed"
)

type Event struct {
	ID          string          `json:"id"`
	WorkspaceID string          `json:"workspaceId"`
	RunID       string          `json:"runId"`
	AgentID     string          `json:"agentId"`
	ExecutionID string          `json:"executionId,omitempty"`
	QuestID     string          `json:"questId,omitempty"`
	FlowRunID   string          `json:"flowRunId,omitempty"`
	FlowNodeID  string          `json:"flowNodeId,omitempty"`
	Type        EventType       `json:"type"`
	Step        int             `json:"step"`
	Actor       string          `json:"actor"`
	Data        json.RawMessage `json:"data"`
	CreatedAt   time.Time       `json:"createdAt"`
}

type Approval struct {
	ID         string          `json:"id"`
	RunID      string          `json:"runId"`
	AgentID    string          `json:"agentId"`
	ToolName   string          `json:"toolName"`
	Reason     string          `json:"reason"`
	Arguments  json.RawMessage `json:"arguments"`
	Status     ApprovalStatus  `json:"status"`
	CreatedAt  time.Time       `json:"createdAt"`
	ResolvedAt *time.Time      `json:"resolvedAt,omitempty"`
}

type PatchProposal struct {
	ID              string    `json:"id"`
	RunID           string    `json:"runId"`
	ApprovalID      string    `json:"approvalId"`
	SourceTool      string    `json:"sourceTool,omitempty"`
	Path            string    `json:"path"`
	OriginalHash    string    `json:"originalHash"`
	OriginalExisted bool      `json:"originalExisted"`
	Original        string    `json:"original"`
	Proposed        string    `json:"proposed"`
	Diff            string    `json:"diff"`
	Status          string    `json:"status"`
	CreatedAt       time.Time `json:"createdAt"`
}

type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type ToolResult struct {
	OK        bool            `json:"ok"`
	Output    json.RawMessage `json:"output,omitempty"`
	Error     *ToolError      `json:"error,omitempty"`
	Truncated bool            `json:"truncated,omitempty"`
}

type ToolError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	// Hint is an optional, deterministic next-step suggestion for the model.
	// It must not invent workspace state or claim that an action already succeeded.
	Hint string `json:"hint,omitempty"`
}

type FileNode struct {
	Name     string     `json:"name"`
	Path     string     `json:"path"`
	IsDir    bool       `json:"isDir"`
	Size     int64      `json:"size,omitempty"`
	Children []FileNode `json:"children,omitempty"`
}

func DefaultProfile() AgentProfile {
	now := time.Now().UTC()
	return AgentProfile{
		ID: "default", Name: "Локальный агент", RoleDescription: "Аккуратный помощник по проекту",
		SystemPrompt: "Ты аккуратный агент-разработчик. Изучи рабочую папку, внеси минимальное корректное изменение и проверь результат. Объясняй каждый вызов инструмента. Не утверждай, что изменение выполнено, пока пользователь его не подтвердил.",
		Goals:        []string{"Выполнять поставленную задачу проверяемо и с минимальным изменением проекта"},
		Rules:        []string{"Сначала изучить релевантный код", "Не обходить подтверждения", "После изменений запускать узкий verifier; предпочитать process-инструмент с providesVerification, если он доступен"},
		Provider:     ProviderOllama, ProviderPreset: "ollama", BaseURL: "http://127.0.0.1:11434", Model: "qwen2.5-coder:7b",
		AllowedTools: []string{"project_map", "search_code", "list_files", "read_file", "search_text", "propose_patch", "run_command", "git_diff"},
		Temperature:  0.2, MaxOutputTokens: MinThinkingOutputTokens, ContextWindowTokens: 32768, ReasoningEffort: "medium",
		MaxSteps: 30, MaxDurationSeconds: 600, ApprovalMode: ApprovalSafe, CreatedAt: now, UpdatedAt: now,
	}
}

// BuiltInCursorProfile is the interactive Cursor Agent profile. It deliberately
// has no server-side tools: Cursor executes it through the extension/CLI.
var BuiltInCursorProfile = DefaultCursorProfile()

func DefaultCursorProfile() AgentProfile {
	now := time.Now().UTC()
	return AgentProfile{
		ID: "cursor-default", Name: "Cursor Agent", RoleDescription: "Интерактивный агент-разработчик Cursor",
		SystemPrompt: "Ты агент-разработчик Cursor. Сначала изучи относящийся к задаче код, затем внеси минимальные корректные изменения и проверь их. Явно сообщай, какие действия выполнил и что осталось проверить. Не утверждай, что работа завершена без фактической проверки.",
		Goals:        []string{"Решать задачи по коду с минимальными проверяемыми изменениями"},
		Rules:        []string{"Сначала изучать релевантный код", "Проверять результат подходящими командами", "Не обходить подтверждения Cursor"},
		Provider:     ProviderCursor, ProviderPreset: "cursor", Model: "auto",
		Temperature: 0.2, MaxOutputTokens: MinThinkingOutputTokens, ContextWindowTokens: 32768, ReasoningEffort: "medium",
		AllowedTools: []string{}, MaxSteps: 30, MaxDurationSeconds: 900, ApprovalMode: ApprovalSafe, CreatedAt: now, UpdatedAt: now,
	}
}
