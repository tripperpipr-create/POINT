package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/policy"
)

// Что агент сможет и чего не сможет — посчитанное тем же кодом, который это
// разрешает и запрещает.
//
// Раньше форма настройки показывала поля: список умений, число ходов, модель.
// Пользователь видел ввод, но не результат — сможет ли агент вообще править
// файлы, что у него спросят перед запуском команды, и почему квест не
// завершится. Готовность считалась в JS отдельным набором правил, который знал
// про допуски меньше, чем движок, и расходился с ним на пользовательских
// инструментах.

type AgentCapability struct {
	State          string `json:"state"` // READY | DEGRADED | BLOCKED
	CanRead        bool   `json:"canRead"`
	CanWrite       bool   `json:"canWrite"`
	CanRunCommands bool   `json:"canRunCommands"`
	CanVerify      bool   `json:"canVerify"`

	// NeedsApproval — умения, которые остановятся и спросят человека.
	NeedsApproval []string `json:"needsApproval,omitempty"`
	// Blockers — почему запуск невозможен, с указанием шага настройки, где это
	// чинится. Пустой список означает готовность.
	Blockers []CapabilityBlocker `json:"blockers,omitempty"`
	// Blocking — те же причины одним текстом. Выводится из Blockers, отдельно не
	// заполняется: два независимых списка рано или поздно разойдутся.
	Blocking []string `json:"blocking,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
	// Lines — то же самое человеческим языком, в порядке важности.
	Lines []string `json:"lines"`
}

// CapabilityBlocker — причина блокировки вместе с адресом починки.
//
// Раньше интерфейс угадывал шаг формы по словам в тексте причины. Стоило
// переформулировать причину — кнопка «исправить» молча уводила не туда, и ни
// один тест этого не замечал: текст-то оставался осмысленным. Шаг называет тот,
// кто знает причину.
type CapabilityBlocker struct {
	Code  string `json:"code"`
	Field string `json:"field,omitempty"`
	Text  string `json:"text"`
	// Step — идентификатор шага настройки: identity, model, tools, limits.
	Step string `json:"step"`
}

// block записывает причину один раз в оба представления.
func (c *AgentCapability) block(code, step, field, text string) {
	c.Blockers = append(c.Blockers, CapabilityBlocker{Code: code, Field: field, Text: text, Step: step})
	c.Blocking = append(c.Blocking, text)
}

// ActorReadiness is the canonical readiness contract. AgentCapability remains
// an alias-compatible API name for clients shipped before readiness states.
type ActorReadiness = AgentCapability

// AgentCapabilityFor описывает профиль так, как его увидит движок.
func (a *App) AgentCapabilityFor(ctx context.Context, profile domain.AgentProfile) AgentCapability {
	if strings.TrimSpace(profile.ConnectionID) != "" {
		if err := a.applyConnectionEndpoint(&profile); err != nil {
			result := AgentCapability{State: "BLOCKED"}
			result.block("connection_unavailable", "model", "connectionId", err.Error())
			result.Lines = capabilityLines(result)
			return result
		}
	}
	catalog := map[string]domain.ToolCatalogItem{}
	for _, item := range domain.BuiltInToolCatalog() {
		catalog[item.Name] = item
	}
	verificationTools := map[string]bool{}
	customTools := map[string]bool{}
	if tools, err := a.store.ListCustomTools(ctx); err == nil {
		for _, tool := range tools {
			customTools[tool.ID] = true
			if tool.ProvidesVerification {
				verificationTools[tool.ID] = true
			}
		}
	}

	result := AgentCapability{}
	engine := policy.Engine{TrustedCustomTool: a.trustedCustomTool}
	approvals := map[string]bool{}

	for _, name := range profile.AllowedTools {
		item, known := catalog[name]
		if !known && !customTools[name] {
			result.block("unknown_tool", "tools", "allowedTools", fmt.Sprintf("неизвестный инструмент %q", name))
			continue
		}
		switch {
		case name == "propose_patch":
			result.CanWrite = true
		case name == "run_command":
			result.CanRunCommands = true
			result.CanVerify = true
		case known && slices.Contains(policy.ProjectReadingGroups(), item.Category):
			result.CanRead = true
		}
		if verificationTools[name] {
			result.CanVerify = true
		}
		if engine.Evaluate(profile, name).RequiresApproval {
			label := name
			if known && strings.TrimSpace(item.DisplayName) != "" {
				label = item.DisplayName
			}
			approvals[label] = true
		}
	}
	for label := range approvals {
		result.NeedsApproval = append(result.NeedsApproval, label)
	}
	sort.Strings(result.NeedsApproval)

	// Блокирующее — то, из-за чего запуск не состоится вовсе.
	if strings.TrimSpace(profile.Model) == "" {
		result.block("missing_model", "model", "model", "не выбрана модель — агенту нечем думать")
	}
	// Модель без провайдера выглядела готовым агентом: проверка спрашивала только
	// про имя модели, а обратиться к ней не через что. Отказ приходил уже на
	// старте квеста и звучал как «unsupported provider kind ""» — на этом месте
	// человек узнавал, что настройка не закончена.
	if profile.Provider == "" {
		result.block("missing_provider", "model", "provider", "не выбран провайдер — модель названа, но обратиться к ней не через что")
	} else if domain.IsAgentCLIProvider(profile.Provider) {
		result.block("unsupported_provider", "model", "provider", "CLI-провайдеры сняты — подключите HTTP API")
	} else if !domain.IsHTTPAPIProvider(profile.Provider) {
		result.block("unsupported_provider", "model", "provider", fmt.Sprintf("провайдер %q не поддерживается", profile.Provider))
	} else {
		if profile.Provider == domain.ProviderAzureOpenAI && strings.TrimSpace(profile.BaseURL) == "" {
			result.block("missing_endpoint", "model", "connectionId", "Azure OpenAI требует подключение с адресом ресурса")
		} else if strings.TrimSpace(profile.BaseURL) != "" {
			if err := checkProviderURL("agent", profile.BaseURL); err != nil {
				result.block("invalid_endpoint", "model", "baseUrl", err.Error())
			}
		}
		if profile.Provider == domain.ProviderAzureOpenAI && strings.TrimSpace(profile.APIVersion) == "" {
			result.block("missing_api_version", "model", "connectionId", "подключению Azure OpenAI не задана API version")
		}
	}
	if len(profile.AllowedTools) == 0 {
		// A model-only actor is useful for analysis, review and synthesis. Keep an
		// incomplete profile blocked, but do not reject an otherwise valid
		// read-only actor merely because it deliberately has no execution tools.
		if profile.Provider == "" || strings.TrimSpace(profile.Model) == "" {
			result.block("missing_tools", "tools", "allowedTools", "не выбрано ни одного умения — агент сможет только отвечать моделью")
		} else {
			result.Warnings = append(result.Warnings, "инструменты не выбраны — агент сможет только анализировать переданный контекст")
		}
	}
	// Право менять файлы без доказательства — самая частая причина того, что
	// квест «завершён», а работоспособность никто не подтвердил.
	if result.CanWrite && !result.CanVerify {
		result.block("missing_verifier", "tools", "allowedTools",
			"агент правит файлы, но не может подтвердить результат: добавьте «Запуск команд» или инструмент-верификатор")
	}
	if profile.MaxSteps < 1 {
		result.block("missing_step_limit", "limits", "maxSteps", "лимит ходов не задан")
	} else if profile.MaxSteps < 20 {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("лимит ходов низкий (%d): агент может упереться в него посреди задачи", profile.MaxSteps))
	}
	if result.CanRunCommands && !result.CanRead {
		result.Warnings = append(result.Warnings,
			"агент запускает команды, но не читает файлы — он будет действовать вслепую")
	}
	if profile.MaxDurationSeconds < 0 || profile.MaxDurationSeconds > 3600 {
		result.block("invalid_duration_limit", "limits", "maxDurationSeconds", "тайм-аут должен быть от 1 до 3600 секунд")
	}
	if profile.MaxOutputTokens < 0 || profile.MaxOutputTokens > 131072 {
		result.block("invalid_output_limit", "limits", "maxOutputTokens", "лимит ответа должен быть от 1 до 131072 токенов")
	}
	if profile.ContextWindowTokens != 0 && (profile.ContextWindowTokens < 4096 || profile.ContextWindowTokens > 1048576 || profile.ContextWindowTokens-profile.MaxOutputTokens < 1024) {
		result.block("invalid_context_limit", "limits", "contextWindowTokens", "окно контекста должно оставлять минимум 1024 токена для входа")
	}
	if len(result.Blockers) > 0 {
		result.State = "BLOCKED"
	} else if len(result.Warnings) > 0 {
		result.State = "DEGRADED"
	} else {
		result.State = "READY"
	}

	result.Lines = capabilityLines(result)
	return result
}

// normalizeRuntimeProfileDefaults applies the compatibility defaults that the
// engine historically used implicitly. New writes persist the same values;
// legacy rows are normalized in memory before readiness and snapshot capture.
func normalizeRuntimeProfileDefaults(profile *domain.AgentProfile) {
	if profile == nil {
		return
	}
	if profile.ProviderPreset == "" {
		profile.ProviderPreset = string(profile.Provider)
	}
	if profile.MaxOutputTokens == 0 {
		profile.MaxOutputTokens = domain.MinThinkingOutputTokens
	}
	if profile.ContextWindowTokens == 0 {
		profile.ContextWindowTokens = 32768
		if profile.ContextWindowTokens-profile.MaxOutputTokens < 1024 {
			profile.ContextWindowTokens = profile.MaxOutputTokens + 4096
		}
	}
	// MaxSteps=0 intentionally stays unset. It is valid draft data, but the
	// readiness gate must reject it; silently turning a new draft into a
	// runnable actor made Master previews disagree with the saved form.
	if profile.MaxDurationSeconds == 0 {
		profile.MaxDurationSeconds = 900
	}
	if profile.ApprovalMode == "" {
		profile.ApprovalMode = domain.ApprovalSafe
	}
	if strings.TrimSpace(profile.BaseURL) == "" {
		switch profile.Provider {
		case domain.ProviderOllama:
			profile.BaseURL = "http://127.0.0.1:11434"
		case domain.ProviderOpenAI:
			profile.BaseURL = "https://api.openai.com/v1"
		case domain.ProviderAnthropic:
			profile.BaseURL = "https://api.anthropic.com/v1"
		}
	}
}

func capabilityLines(c AgentCapability) []string {
	can := make([]string, 0, 4)
	if c.CanRead {
		can = append(can, "читать код")
	}
	if c.CanWrite {
		can = append(can, "предлагать правки")
	}
	if c.CanRunCommands {
		can = append(can, "запускать команды")
	}
	lines := make([]string, 0, 5)
	if len(can) > 0 {
		lines = append(lines, "Сможет: "+strings.Join(can, ", ")+".")
	} else {
		lines = append(lines, "Пока не сможет ничего: умения не выбраны.")
	}
	if len(c.NeedsApproval) > 0 {
		lines = append(lines, "Остановится и спросит вас: "+strings.Join(c.NeedsApproval, ", ")+".")
	} else if c.CanWrite || c.CanRunCommands {
		lines = append(lines, "Ничего не спросит: подтверждения отключены профилем.")
	}
	if c.CanVerify {
		lines = append(lines, "Завершение подтвердит проверкой — «готово» без доказательства не примут.")
	} else if c.CanWrite {
		lines = append(lines, "Подтвердить результат нечем: квест закроется без доказательства.")
	}
	return lines
}

func (a *App) readyProjectAgents(ctx context.Context, agents []domain.ProjectAgent) ([]domain.ProjectAgent, map[string]AgentCapability) {
	ready := make([]domain.ProjectAgent, 0, len(agents))
	blocked := make(map[string]AgentCapability)
	for _, projectAgent := range agents {
		capability := a.projectAgentReadiness(ctx, projectAgent)
		if capability.State == "BLOCKED" {
			blocked[projectAgent.ID] = capability
			continue
		}
		ready = append(ready, projectAgent)
	}
	return ready, blocked
}

func (a *App) projectAgentReadiness(ctx context.Context, projectAgent domain.ProjectAgent) AgentCapability {
	profile := domain.ProfileFromProjectAgent(projectAgent)
	normalizeRuntimeProfileDefaults(&profile)
	if err := a.enrichProjectAgentForRun(projectAgent.WorkspaceID, projectAgent, &profile, nil); err != nil {
		result := a.AgentCapabilityFor(ctx, profile)
		result.block("skill_unavailable", "tools", "skillIds", err.Error())
		result.State = "BLOCKED"
		result.Lines = capabilityLines(result)
		return result
	}
	return a.AgentCapabilityFor(ctx, profile)
}

func readinessFailure(agent domain.ProjectAgent, readiness AgentCapability) error {
	if len(readiness.Blockers) == 0 {
		return nil
	}
	return fmt.Errorf("агент %q не готов: %s", agent.Name, strings.Join(readiness.Blocking, "; "))
}

func (a *App) requireProjectAgentsReady(ctx context.Context, workspaceID string, agentIDs []string) error {
	if len(agentIDs) == 0 {
		return errors.New("для запуска не выбран ни один агент")
	}
	agents, err := a.store.ListProjectAgents(ctx, workspaceID)
	if err != nil {
		return err
	}
	byID := make(map[string]domain.ProjectAgent, len(agents))
	for _, projectAgent := range agents {
		byID[projectAgent.ID] = projectAgent
	}
	for _, id := range agentIDs {
		projectAgent, ok := byID[id]
		if !ok {
			return fmt.Errorf("агент %q недоступен в текущем проекте", id)
		}
		if err := readinessFailure(projectAgent, a.projectAgentReadiness(ctx, projectAgent)); err != nil {
			return err
		}
	}
	return nil
}
