// Чертежи и проектные агенты: сохранение, удаление, перенос правок между ними.
//
// Чертёж — образец роли, проектный агент — его воплощение в конкретном мире.
// Правка ходит в обе стороны, и различия показываются человеку до применения.
package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/egress"
)

// agentName приводит имя персонажа к тому, чем оно обязано быть: одной непустой
// строкой. Живая проба показала, что ядро принимало и «», и «   », и имя с
// переводом строки внутри — в ростере это карточки, которые нельзя ни отличить
// друг от друга, ни назвать в отряде. Интерфейс пустое имя не пропускает, но он
// не единственный клиент, а гарантия нужна одна на всех.
func agentName(raw string) (string, error) {
	name := strings.TrimSpace(strings.Join(strings.Fields(raw), " "))
	if name == "" {
		return "", errors.New("укажите имя агента")
	}
	return name, nil
}

// checkProviderURL — одно правило адреса модели на компаньона, мастера и агента.
// Раньше их было два: компаньон и мастер требовали абсолютный http/https без
// учётных данных внутри, а агент не требовал ничего — и принимал
// «http://admin:s3cret@example.com/v1». Пароль в адресе оседает в конфиге, в
// хронике и в тексте ошибок, а исполняет запросы к модели как раз агент.
func checkProviderURL(subject, raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fmt.Errorf("%s base URL must be an absolute HTTP or HTTPS URL", subject)
	}
	if parsed.User != nil {
		return fmt.Errorf("credentials in the %s provider URL are not allowed", subject)
	}
	return nil
}

// checkAgentLimits предъявляет агенту границы, которые конструктор агента и так
// объявляет полями формы (min/max), но ядро до сих пор не проверяло: живая проба
// сохраняла температуру 99, температуру −5 и −10 ходов. Ноль в лимитах остаётся
// разрешённым: это «не задано», и agent_capability так и говорит человеку.
func checkAgentLimits(provider domain.ProviderKind, baseURL string, approvalMode domain.ApprovalMode, temperature float64, maxOutputTokens, maxSteps, maxDurationSeconds int) error {
	if provider != "" && domain.IsAgentCLIProvider(provider) {
		return fmt.Errorf("agent CLI provider %q is removed; use an HTTP API provider", provider)
	}
	if provider != "" && !domain.IsHTTPAPIProvider(provider) {
		return fmt.Errorf("agent provider %q is not supported", provider)
	}
	if baseURL != "" {
		if err := checkProviderURL("agent", baseURL); err != nil {
			return err
		}
	}
	// Неизвестный режим подтверждений тих и потому опасен: он не равен
	// ApprovalAlways, значит движок молча считает его «спрашивать только про
	// опасное» — из непонятного значения выходит послабление. Legacy-профили это
	// проверяли давно, проектные агенты — нет.
	switch approvalMode {
	case "", domain.ApprovalSafe, domain.ApprovalAlways:
	default:
		return fmt.Errorf("agent approval mode %q is not supported", approvalMode)
	}
	if temperature < 0 || temperature > 2 {
		return errors.New("agent temperature must be between 0 and 2")
	}
	if maxOutputTokens < 0 || maxOutputTokens > 131072 {
		return errors.New("agent max output tokens must be between 0 and 131072")
	}
	if maxSteps < 0 || maxSteps > 100 {
		return errors.New("agent max steps must be between 0 and 100")
	}
	if maxDurationSeconds < 0 {
		return errors.New("agent max duration must not be negative")
	}
	return nil
}

func (a *App) SaveBlueprint(blueprint domain.AgentBlueprint) (domain.AgentBlueprint, error) {
	now := time.Now().UTC()
	name, err := agentName(blueprint.Name)
	if err != nil {
		return domain.AgentBlueprint{}, err
	}
	blueprint.Name = name
	blueprintProfile := domain.ProfileFromBlueprint(blueprint)
	normalizeRuntimeProfileDefaults(&blueprintProfile)
	blueprint.ProviderPreset = blueprintProfile.ProviderPreset
	blueprint.BaseURL = blueprintProfile.BaseURL
	blueprint.MaxOutputTokens = blueprintProfile.MaxOutputTokens
	blueprint.ContextWindowTokens = blueprintProfile.ContextWindowTokens
	blueprint.MaxSteps = blueprintProfile.MaxSteps
	blueprint.MaxDurationSeconds = blueprintProfile.MaxDurationSeconds
	blueprint.ApprovalMode = blueprintProfile.ApprovalMode
	if strings.TrimSpace(blueprint.ConnectionID) != "" {
		connection, resolveErr := a.ResolveConnection(ConnectionRef{ConnectionID: blueprint.ConnectionID, ProviderPreset: blueprint.ProviderPreset, Provider: blueprint.Provider, Label: "blueprint «" + blueprint.Name + "»"})
		if resolveErr != nil {
			return domain.AgentBlueprint{}, resolveErr
		}
		blueprint.Provider, blueprint.ProviderPreset, blueprint.BaseURL = connection.Provider, connection.PresetID, connection.BaseURL
		if strings.TrimSpace(blueprint.PrimaryModel) == "" && strings.TrimSpace(connection.DefaultModel) != "" {
			blueprint.PrimaryModel = connection.DefaultModel
		}
	}
	if blueprint.PrimaryModel == "" {
		blueprint.PrimaryModel = "auto"
	}
	blueprint.BaseURL = strings.TrimRight(strings.TrimSpace(blueprint.BaseURL), "/")
	if err := checkAgentLimits(blueprint.Provider, blueprint.BaseURL, blueprint.ApprovalMode, blueprint.Temperature,
		blueprint.MaxOutputTokens, blueprint.MaxSteps, blueprint.MaxDurationSeconds); err != nil {
		return domain.AgentBlueprint{}, err
	}
	if _, err := egress.CompileToolPolicies(blueprint.ToolPolicies); err != nil {
		return domain.AgentBlueprint{}, fmt.Errorf("invalid controlled egress policy: %w", err)
	}
	if err := a.validateActorDefinition(context.Background(), domain.ProfileFromBlueprint(blueprint), blueprint.SkillIDs); err != nil {
		return domain.AgentBlueprint{}, err
	}
	if blueprint.ID == "" {
		blueprint.ID = domain.NewID("blueprint")
		blueprint.CreatedAt = now
	}
	if blueprint.CreatedAt.IsZero() {
		blueprint.CreatedAt = now
	}
	blueprint.UpdatedAt = now
	if err := a.store.SaveBlueprint(context.Background(), blueprint); err != nil {
		return domain.AgentBlueprint{}, err
	}
	return blueprint, nil
}

func (a *App) SaveProjectAgent(agent domain.ProjectAgent) (domain.ProjectAgent, error) {
	now := time.Now().UTC()
	existingSkillIDs := []string{}
	ws, err := a.requireWorkspace()
	if err != nil {
		return domain.ProjectAgent{}, err
	}
	name, err := agentName(agent.Name)
	if err != nil {
		return domain.ProjectAgent{}, err
	}
	agent.Name = name
	agentProfile := domain.ProfileFromProjectAgent(agent)
	normalizeRuntimeProfileDefaults(&agentProfile)
	agent.ProviderPreset = agentProfile.ProviderPreset
	agent.BaseURL = agentProfile.BaseURL
	agent.MaxOutputTokens = agentProfile.MaxOutputTokens
	agent.ContextWindowTokens = agentProfile.ContextWindowTokens
	agent.MaxSteps = agentProfile.MaxSteps
	agent.MaxDurationSeconds = agentProfile.MaxDurationSeconds
	agent.ApprovalMode = agentProfile.ApprovalMode
	if strings.TrimSpace(agent.ConnectionID) != "" {
		connection, resolveErr := a.ResolveConnection(ConnectionRef{ConnectionID: agent.ConnectionID, ProviderPreset: agent.ProviderPreset, Provider: agent.Provider, Label: "агента «" + agent.Name + "»"})
		if resolveErr != nil {
			return domain.ProjectAgent{}, resolveErr
		}
		agent.Provider, agent.ProviderPreset, agent.BaseURL = connection.Provider, connection.PresetID, connection.BaseURL
		if strings.TrimSpace(agent.PrimaryModel) == "" && strings.TrimSpace(connection.DefaultModel) != "" {
			agent.PrimaryModel = connection.DefaultModel
		}
	}
	agent.BaseURL = strings.TrimRight(strings.TrimSpace(agent.BaseURL), "/")
	if err := checkAgentLimits(agent.Provider, agent.BaseURL, agent.ApprovalMode, agent.Temperature,
		agent.MaxOutputTokens, agent.MaxSteps, agent.MaxDurationSeconds); err != nil {
		return domain.ProjectAgent{}, err
	}
	if _, err := egress.CompileToolPolicies(agent.ToolPolicies); err != nil {
		return domain.ProjectAgent{}, fmt.Errorf("invalid controlled egress policy: %w", err)
	}
	if agent.WorkspaceID == "" {
		agent.WorkspaceID = ws.ID
	}
	if agent.WorkspaceID != ws.ID {
		return domain.ProjectAgent{}, errors.New("project agent belongs to another workspace")
	}
	if agent.ID == "" {
		agent.ID = domain.NewID("projectagent")
		if agent.Status == "" {
			agent.Status = domain.ProjectAgentActive
		}
		agent.CreatedAt = now
	} else if existing, getErr := a.store.GetProjectAgent(context.Background(), agent.ID); getErr == nil {
		if existing.WorkspaceID != ws.ID {
			return domain.ProjectAgent{}, errors.New("project agent belongs to another workspace")
		}
		// Progress counters are server-owned; constructor edits must not wipe them.
		agent.Experience = existing.Experience
		agent.Level = existing.Level
		agent.TasksCompleted = existing.TasksCompleted
		agent.SuccessCount = existing.SuccessCount
		agent.CreatedAt = existing.CreatedAt
		// Lifecycle and ownership are changed only by the dedicated operations.
		// Saving the constructor form must never activate a draft implicitly.
		agent.Status = existing.Status
		agent.RoleFamily = existing.RoleFamily
		agent.ParentAgentID = existing.ParentAgentID
		agent.OwnerQuestID = existing.OwnerQuestID
		agent.Temporary = existing.Temporary
		existingSkillIDs = append(existingSkillIDs, existing.SkillIDs...)
	}
	if agent.Status == "" {
		agent.Status = domain.ProjectAgentActive
	}
	if err = a.rejectNewDeprecatedSkills(context.Background(), agent.SkillIDs, existingSkillIDs); err != nil {
		return domain.ProjectAgent{}, err
	}
	if err = a.validateActorDefinition(context.Background(), domain.ProfileFromProjectAgent(agent), agent.SkillIDs); err != nil {
		return domain.ProjectAgent{}, err
	}
	if agent.CreatedAt.IsZero() {
		agent.CreatedAt = now
	}
	agent.UpdatedAt = now
	if agent.Level == 0 {
		agent.Level = 1
	}
	if err := a.store.SaveProjectAgent(context.Background(), agent); err != nil {
		return domain.ProjectAgent{}, err
	}
	if !agent.Temporary {
		_ = a.reconcileUserAgentPrepChains(context.Background(), agent)
	}
	return agent, nil
}

func (a *App) rejectNewDeprecatedSkills(ctx context.Context, requested, alreadyAssigned []string) error {
	skills, err := a.store.ListSkills(ctx)
	if err != nil {
		return err
	}
	deprecated := map[string]string{}
	for _, skill := range skills {
		if curatorDeprecated(skill) {
			deprecated[skill.ID] = skill.Name
		}
	}
	for _, id := range requested {
		if name, blocked := deprecated[id]; blocked && !slices.Contains(alreadyAssigned, id) {
			return fmt.Errorf("skill %q is deprecated and cannot be newly assigned", name)
		}
	}
	return nil
}

// DeleteBlueprint убирает класс из списка найма.
//
// Конструктор, сохраняя персонажа «с нуля», заводит и класс под него. Пока
// распустить персонажа было нечем, это не бросалось в глаза; теперь три круга
// «создал — распустил» оставляют в списке найма три класса от несуществующих
// персонажей, и убрать их нечем. Класс, которым кто-то пользуется, не удаляем и
// говорим, кто именно держит.
func (a *App) DeleteBlueprint(blueprintID string) error {
	ctx := context.Background()
	if _, err := a.store.GetBlueprint(ctx, blueprintID); err != nil {
		return err
	}
	// Чертёж глобален, а персонажи живут в проектах. Проверять занятость только
	// в открытом проекте — значит разрешить удалить класс из-под персонажа
	// соседнего проекта; заодно удаление перестаёт требовать открытого проекта,
	// как и было у профилей.
	agents, err := a.store.ListAllProjectAgents(ctx)
	if err != nil {
		return err
	}
	for _, agent := range agents {
		if agent.BlueprintID == blueprintID {
			return fmt.Errorf("класс занят персонажем %q — распустите его или смените класс", agent.Name)
		}
	}
	return a.store.DeleteBlueprint(ctx, blueprintID)
}

// DeleteProjectAgent распускает персонажа из ростера.
//
// Раньше кнопка «Распустить» слала deleteProfile: маршрут удаления был только у
// legacy-профилей, а у проектных агентов — никакого. Ядро отвечало 204, панель
// обновлялась, и персонаж оставался на месте. Отказ, выданный за успех, хуже
// отсутствия кнопки.
//
// Отказываем там, где роспуск оставил бы после себя ссылку в пустоту, и всегда
// называем, что именно держит — как это делает DeleteProfile со сценариями.
func (a *App) DeleteProjectAgent(projectAgentID string) error {
	agent, err := a.currentProjectAgent(projectAgentID)
	if err != nil {
		return err
	}
	ctx := context.Background()
	executions, err := a.store.ListExecutions(ctx, agent.WorkspaceID, 0)
	if err != nil {
		return err
	}
	for _, execution := range executions {
		if execution.ProjectAgentID != agent.ID {
			continue
		}
		switch execution.Status {
		case domain.RunPending, domain.RunRunning, domain.RunPaused, domain.RunWaiting:
			return errors.New("персонаж занят в запуске — остановите запуск и повторите")
		}
	}
	teams, err := a.store.ListTeams(ctx, agent.WorkspaceID)
	if err != nil {
		return err
	}
	for _, team := range teams {
		for _, member := range team.AgentIDs {
			if member == agent.ID {
				// Отказ обязан называть не только причину, но и место, где её снимают:
				// состав отряда править негде, а распустить отряд можно в «Отрядах».
				return fmt.Errorf("персонаж состоит в отряде %q — распустите отряд в разделе «Отряды»", team.Name)
			}
		}
	}
	// Узел Flow держит агента по идентификатору, и на запуске узел просто не
	// найдёт карточку: наружу выйдет сырая ошибка хранилища посреди работы, без
	// связи с давним роспуском. Отказ на месте роспуска называет схему.
	flows, err := a.store.ListFlows(ctx, agent.WorkspaceID)
	if err != nil {
		return err
	}
	for _, flow := range flows {
		for _, node := range flow.Nodes {
			if node.AgentID == agent.ID {
				return fmt.Errorf("персонаж стоит в узле %q схемы %q — замените его в схеме", node.Name, flow.Name)
			}
		}
	}
	return a.store.DeleteProjectAgent(ctx, agent.ID)
}

// ActivateProjectAgentDraft is the only transition that makes a selector-made
// draft runnable. Constructor saves deliberately preserve the draft status.
func (a *App) ActivateProjectAgentDraft(projectAgentID string) (domain.ProjectAgent, error) {
	ctx := context.Background()
	agent, err := a.currentProjectAgent(projectAgentID)
	if err != nil {
		return domain.ProjectAgent{}, err
	}
	if agent.Status != domain.ProjectAgentDraft {
		return domain.ProjectAgent{}, errors.New("активировать можно только черновик агента")
	}
	// Evaluate the configured profile as active without persisting the state.
	probe := agent
	probe.Status = domain.ProjectAgentActive
	readiness := a.projectAgentReadiness(ctx, probe)
	if readiness.State == "BLOCKED" {
		return domain.ProjectAgent{}, readinessFailure(agent, readiness)
	}
	if err = a.store.SetProjectAgentStatus(ctx, agent.ID, domain.ProjectAgentDraft, domain.ProjectAgentActive); err != nil {
		return domain.ProjectAgent{}, err
	}
	_ = a.store.SaveAgentLifecycleEvent(ctx, domain.AgentLifecycleEvent{
		ID: domain.NewID("agentlife"), WorkspaceID: agent.WorkspaceID, AgentID: agent.ID,
		Kind: "draft_activated", Detail: map[string]any{"roleFamily": agent.RoleFamily}, CreatedAt: time.Now().UTC(),
	})
	agent.Status = domain.ProjectAgentActive
	return agent, nil
}

type RejectProjectAgentDraftResult struct {
	AgentID             string   `json:"agentId"`
	WorkOrderIDs        []string `json:"workOrderIds,omitempty"`
	ReplacementAgentIDs []string `json:"replacementAgentIds,omitempty"`
}

func (a *App) RejectProjectAgentDraft(projectAgentID string, apiKeys ...string) (RejectProjectAgentDraftResult, error) {
	ctx := context.Background()
	agent, err := a.currentProjectAgent(projectAgentID)
	if err != nil {
		return RejectProjectAgentDraftResult{}, err
	}
	if agent.Status != domain.ProjectAgentDraft {
		return RejectProjectAgentDraftResult{}, errors.New("отклонить можно только черновик агента")
	}
	orders, err := a.store.ListWorkOrdersForAgentBinding(ctx, agent.ID)
	if err != nil {
		return RejectProjectAgentDraftResult{}, err
	}
	detail := map[string]any{"roleFamily": agent.RoleFamily}
	workOrderID := ""
	if len(orders) > 0 {
		workOrderID = orders[0]
	}
	if err = a.store.RejectProjectAgentDraft(ctx, agent.ID, domain.AgentLifecycleEvent{
		WorkOrderID: workOrderID, Detail: detail,
	}); err != nil {
		return RejectProjectAgentDraftResult{}, err
	}
	result := RejectProjectAgentDraftResult{AgentID: agent.ID, WorkOrderIDs: orders}
	apiKey := ""
	if len(apiKeys) > 0 {
		apiKey = apiKeys[0]
	}
	for _, orderID := range orders {
		order, loadErr := a.store.GetWorkOrderV2(ctx, orderID)
		if loadErr != nil || order.State != "ready" || order.ApprovedDigest != "" {
			continue
		}
		cfg, cfgErr := a.masterConfig(ctx, order.WorkspaceID)
		if cfgErr != nil {
			continue
		}
		selection, selectErr := a.selectAgentsForWorkOrder(ctx, order, cfg, apiKey)
		if selectErr != nil {
			return result, selectErr
		}
		order.Version++
		order.Digest = ""
		order.Roster = a.rosterFromAgentIDs(ctx, selection.AgentIDs)
		order.UpdatedAt = time.Now().UTC()
		saved, saveErr := a.SaveWorkOrderV2(ctx, order)
		if saveErr != nil {
			return result, saveErr
		}
		if bindErr := a.store.ReplaceAgentSelectionBindings(ctx, saved.ID, saved.ConversationID, saved.WorkspaceID, selection.Digest, saved.Version, selection.AgentIDs); bindErr != nil {
			return result, bindErr
		}
		result.ReplacementAgentIDs = append(result.ReplacementAgentIDs, selection.AgentIDs...)
	}
	return result, nil
}

func (a *App) ApplyBlueprintToProjectAgent(projectAgentID string) (domain.ProjectAgent, error) {
	agent, err := a.currentProjectAgent(projectAgentID)
	if err != nil {
		return domain.ProjectAgent{}, err
	}
	blueprint, err := a.store.GetBlueprint(context.Background(), agent.BlueprintID)
	if err != nil {
		return domain.ProjectAgent{}, err
	}
	updated := domain.ProjectAgentFromBlueprint(agent.WorkspaceID, blueprint)
	updated.ID = agent.ID
	updated.Experience = agent.Experience
	updated.Level = agent.Level
	updated.TasksCompleted = agent.TasksCompleted
	updated.SuccessCount = agent.SuccessCount
	updated.ProjectRules = append([]string(nil), agent.ProjectRules...)
	updated.CreatedAt = agent.CreatedAt
	return a.SaveProjectAgent(updated)
}

func (a *App) UpdateBlueprintFromProjectAgent(projectAgentID string) (domain.AgentBlueprint, error) {
	agent, err := a.currentProjectAgent(projectAgentID)
	if err != nil {
		return domain.AgentBlueprint{}, err
	}
	blueprint, err := a.store.GetBlueprint(context.Background(), agent.BlueprintID)
	if err != nil {
		return domain.AgentBlueprint{}, err
	}
	blueprint.Name = agent.Name
	blueprint.RoleDescription = agent.RoleDescription
	blueprint.Personality = agent.Personality
	blueprint.Mission = agent.Mission
	blueprint.SystemPrompt = agent.SystemPrompt
	blueprint.Goals = append([]string(nil), agent.Goals...)
	blueprint.Rules = append([]string(nil), agent.Rules...)
	blueprint.Constraints = append([]string(nil), agent.Constraints...)
	blueprint.SkillIDs = append([]string(nil), agent.SkillIDs...)
	blueprint.AllowedTools = append([]string(nil), agent.AllowedTools...)
	blueprint.ToolPolicies = cloneMap(agent.ToolPolicies)
	blueprint.ConnectionID = agent.ConnectionID
	blueprint.Provider = agent.Provider
	blueprint.ProviderPreset = agent.ProviderPreset
	blueprint.BaseURL = agent.BaseURL
	blueprint.PrimaryModel = agent.PrimaryModel
	blueprint.FallbackModels = append([]string(nil), agent.FallbackModels...)
	blueprint.Temperature = agent.Temperature
	blueprint.MaxOutputTokens = agent.MaxOutputTokens
	blueprint.ContextWindowTokens = agent.ContextWindowTokens
	blueprint.ReasoningEffort = agent.ReasoningEffort
	blueprint.MaxSteps = agent.MaxSteps
	blueprint.MaxDurationSeconds = agent.MaxDurationSeconds
	blueprint.ApprovalMode = agent.ApprovalMode
	return a.SaveBlueprint(blueprint)
}

type AgentBlueprintDiffField struct {
	Key            string `json:"key"`
	Label          string `json:"label"`
	AgentValue     any    `json:"agentValue"`
	BlueprintValue any    `json:"blueprintValue"`
}

type AgentBlueprintDiff struct {
	ProjectAgentID string                    `json:"projectAgentId"`
	BlueprintID    string                    `json:"blueprintId"`
	AgentName      string                    `json:"agentName"`
	BlueprintName  string                    `json:"blueprintName"`
	Fields         []AgentBlueprintDiffField `json:"fields"`
	ProjectOnly    map[string]any            `json:"projectOnly,omitempty"`
	HasChanges     bool                      `json:"hasChanges"`
}

func (a *App) DiffProjectAgentBlueprint(projectAgentID string) (AgentBlueprintDiff, error) {
	agent, err := a.currentProjectAgent(projectAgentID)
	if err != nil {
		return AgentBlueprintDiff{}, err
	}
	blueprint, err := a.store.GetBlueprint(context.Background(), agent.BlueprintID)
	if err != nil {
		return AgentBlueprintDiff{}, err
	}
	diff := AgentBlueprintDiff{
		ProjectAgentID: agent.ID, BlueprintID: blueprint.ID, AgentName: agent.Name, BlueprintName: blueprint.Name,
		Fields: []AgentBlueprintDiffField{}, ProjectOnly: map[string]any{"projectRules": append([]string(nil), agent.ProjectRules...)},
	}
	add := func(key, label string, agentValue, blueprintValue any) {
		if !reflect.DeepEqual(agentValue, blueprintValue) {
			diff.Fields = append(diff.Fields, AgentBlueprintDiffField{Key: key, Label: label, AgentValue: agentValue, BlueprintValue: blueprintValue})
		}
	}
	add("name", "Имя", agent.Name, blueprint.Name)
	add("roleDescription", "Роль", agent.RoleDescription, blueprint.RoleDescription)
	add("personality", "Personality", agent.Personality, blueprint.Personality)
	add("mission", "Миссия", agent.Mission, blueprint.Mission)
	add("systemPrompt", "Инструкции", agent.SystemPrompt, blueprint.SystemPrompt)
	add("goals", "Цели", agent.Goals, blueprint.Goals)
	add("rules", "Правила", agent.Rules, blueprint.Rules)
	add("constraints", "Ограничения", agent.Constraints, blueprint.Constraints)
	add("skillIds", "Skills", agent.SkillIDs, blueprint.SkillIDs)
	add("allowedTools", "Tools", agent.AllowedTools, blueprint.AllowedTools)
	add("toolPolicies", "Tool policy", agent.ToolPolicies, blueprint.ToolPolicies)
	add("connectionId", "Подключение", agent.ConnectionID, blueprint.ConnectionID)
	add("provider", "Provider", agent.Provider, blueprint.Provider)
	add("providerPreset", "Provider preset", agent.ProviderPreset, blueprint.ProviderPreset)
	add("baseUrl", "Base URL", agent.BaseURL, blueprint.BaseURL)
	add("primaryModel", "Primary model", agent.PrimaryModel, blueprint.PrimaryModel)
	add("fallbackModels", "Fallback models", agent.FallbackModels, blueprint.FallbackModels)
	add("temperature", "Температура", agent.Temperature, blueprint.Temperature)
	add("maxOutputTokens", "Макс. output tokens", agent.MaxOutputTokens, blueprint.MaxOutputTokens)
	add("contextWindowTokens", "Окно контекста", agent.ContextWindowTokens, blueprint.ContextWindowTokens)
	add("reasoningEffort", "Reasoning", agent.ReasoningEffort, blueprint.ReasoningEffort)
	add("maxSteps", "Макс. шагов", agent.MaxSteps, blueprint.MaxSteps)
	add("maxDurationSeconds", "Тайм-аут", agent.MaxDurationSeconds, blueprint.MaxDurationSeconds)
	add("approvalMode", "Подтверждения", agent.ApprovalMode, blueprint.ApprovalMode)
	diff.HasChanges = len(diff.Fields) > 0
	return diff, nil
}

func (a *App) currentProjectAgent(projectAgentID string) (domain.ProjectAgent, error) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return domain.ProjectAgent{}, err
	}
	agent, err := a.store.GetProjectAgent(context.Background(), projectAgentID)
	if err != nil {
		return domain.ProjectAgent{}, err
	}
	if agent.WorkspaceID != ws.ID {
		return domain.ProjectAgent{}, errors.New("project agent belongs to another workspace")
	}
	return agent, nil
}
