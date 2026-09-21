package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/modeljson"
	"local-agent-workbench/internal/providers"
)

// TaskAgentSummary is the complete, bounded hand-off from Master to the
// dedicated selector. It intentionally contains no conversation history.
type TaskAgentSummary struct {
	Goal           string   `json:"goal"`
	Scope          []string `json:"scope,omitempty"`
	Criteria       []string `json:"criteria,omitempty"`
	ResultKind     string   `json:"resultKind,omitempty"`
	StackCategory  string   `json:"stackCategory,omitempty"`
	RequiredTools  []string `json:"requiredTools,omitempty"`
	AllowSubagents bool     `json:"allowSubagents"`
	MaxAgents      int      `json:"maxAgents"`
	Rejected       []string `json:"rejectedRoleFamilies,omitempty"`
}

type selectorRosterAgent struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	RoleFamily string   `json:"roleFamily,omitempty"`
	Role       string   `json:"role"`
	Mission    string   `json:"mission,omitempty"`
	Tools      []string `json:"tools,omitempty"`
	Readiness  string   `json:"readiness"`
	Blocking   []string `json:"blocking,omitempty"`
	Subagents  []string `json:"subagents,omitempty"`
}

type selectorDecision struct {
	AgentIDs     []string `json:"agentIds"`
	RoleFamilies []string `json:"roleFamilies"`
}

type agentSelectionResult struct {
	AgentIDs []string
	Digest   string
	Fallback string
}

type roleFamilyTemplate struct {
	Name, Role, Mission, Prompt string
	Tools                       []string
}

var roleFamilyTemplates = map[string]roleFamilyTemplate{
	"developer":        {"Разработчик", "Разработчик", "Реализовывать и проверять изменения проекта", "Ты общий разработчик проекта. Изучай код, делай минимальные корректные изменения и подтверждай результат проверками.", []string{"project_map", "list_files", "read_file", "search_code", "propose_patch", "run_command", "git_diff"}},
	"tester":           {"Тестировщик", "Тестировщик", "Проверять поведение и воспроизводить дефекты", "Ты общий тестировщик проекта. Строй воспроизводимые проверки и отделяй наблюдения от выводов.", []string{"project_map", "list_files", "read_file", "search_code", "run_command", "git_diff"}},
	"designer":         {"Дизайнер", "Дизайнер интерфейсов", "Проектировать понятные пользовательские интерфейсы", "Ты общий дизайнер интерфейсов проекта. Учитывай сценарии, иерархию, доступность и согласованность.", []string{"project_map", "list_files", "read_file", "search_code"}},
	"analyst":          {"Аналитик", "Аналитик", "Исследовать требования, данные и ограничения", "Ты общий аналитик проекта. Проверяй факты и превращай неопределённость в проверяемые требования.", []string{"project_map", "list_files", "read_file", "search_code"}},
	"devops":           {"DevOps", "DevOps-инженер", "Поддерживать сборку, доставку и окружение", "Ты общий DevOps-инженер проекта. Делай окружение воспроизводимым и проверяй каждый операционный шаг.", []string{"project_map", "list_files", "read_file", "search_code", "propose_patch", "run_command", "git_diff"}},
	"security_auditor": {"Аудитор безопасности", "Аудитор безопасности", "Находить и объяснять риски безопасности", "Ты общий аудитор безопасности проекта. Не расширяй права и подтверждай риски конкретными доказательствами.", []string{"project_map", "list_files", "read_file", "search_code", "git_diff"}},
}

const agentSelectorPrompt = `Ты отдельный системный агент-комплектовщик Point. Ты не Мастер и не выполняешь задачу.
Получив короткую сводку и снимок ростера, выбери минимальный достаточный набор существующих активных общих агентов.
Если подходящей общей роли нет, запроси только семейства из списка: developer, tester, designer, analyst, devops, security_auditor.
Технологии и фреймворки (Symfony, React, Go и подобные) не являются общими ролями: для них выбирай developer; узкого субагента позже создаст родитель.
Не выбирай временных субагентов, draft или BLOCKED. Не придумывай ID. Не превышай maxAgents.
Верни один JSON без markdown: {"agentIds":["существующие ID"],"roleFamilies":["семейства новых общих драфтов"]}.`

func workOrderAgentSummary(order domain.WorkOrder, rejected []string) TaskAgentSummary {
	summary := TaskAgentSummary{
		Goal: boundedMasterFact(order.Goal, 600), Scope: boundedStrings(order.Scope, 8),
		ResultKind: order.Stack.ID, StackCategory: order.Stack.Category,
		MaxAgents: order.Budget.MaxProjectAgents, Rejected: append([]string(nil), rejected...),
	}
	if summary.MaxAgents <= 0 {
		summary.MaxAgents = 1
	}
	if summary.MaxAgents > 8 {
		summary.MaxAgents = 8
	}
	for _, criterion := range order.Criteria {
		if len(summary.Criteria) == 8 {
			break
		}
		summary.Criteria = append(summary.Criteria, boundedMasterFact(criterion.Text, 300))
		if tool := strings.TrimSpace(criterion.Tool); tool != "" {
			summary.RequiredTools = append(summary.RequiredTools, tool)
		}
	}
	summary.RequiredTools = append(summary.RequiredTools, "list_files", "read_file", "search_code")
	if len(order.Scope) > 0 {
		summary.RequiredTools = append(summary.RequiredTools, "propose_patch", "run_command")
	}
	sort.Strings(summary.RequiredTools)
	summary.RequiredTools = slicesCompact(summary.RequiredTools)
	summary.AllowSubagents = order.Budget.MaxProjectAgents > 1
	return summary
}

func slicesCompact(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	result := values[:1]
	for _, value := range values[1:] {
		if value != result[len(result)-1] {
			result = append(result, value)
		}
	}
	return result
}

func selectorDigest(summary TaskAgentSummary) string {
	raw, _ := json.Marshal(summary)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func (a *App) selectAgentsForWorkOrder(ctx context.Context, order domain.WorkOrder, cfg domain.OrchestratorConfig, apiKey string) (agentSelectionResult, error) {
	rejected, _ := a.store.RejectedRoleFamiliesForWorkOrder(ctx, order.ID)
	summary := workOrderAgentSummary(order, rejected)
	digest := selectorDigest(summary)
	existing, bindingsErr := a.store.ListAgentSelectionBindings(ctx, order.ID)
	if bindingsErr == nil && len(existing) > 0 && existing[0].SelectionDigest == digest {
		ids := make([]string, 0, len(existing))
		for _, binding := range existing {
			if _, getErr := a.store.GetProjectAgent(ctx, binding.AgentID); getErr == nil {
				ids = append(ids, binding.AgentID)
			}
		}
		if len(ids) > 0 {
			return agentSelectionResult{AgentIDs: ids, Digest: digest}, nil
		}
	}
	if bindingsErr == nil && len(existing) > 0 && existing[0].SelectionDigest != digest {
		for _, binding := range existing {
			agent, getErr := a.store.GetProjectAgent(ctx, binding.AgentID)
			if getErr != nil || agent.Status != domain.ProjectAgentDraft || agent.Temporary || agent.BlueprintID != "" {
				continue
			}
			_ = a.store.DeleteSupersededProjectAgentDraft(ctx, agent.ID, domain.AgentLifecycleEvent{
				WorkOrderID: order.ID, Detail: map[string]any{"previousDigest": binding.SelectionDigest, "selectionDigest": digest, "roleFamily": agent.RoleFamily},
			})
		}
	}
	agents, err := a.store.ListProjectAgents(ctx, order.WorkspaceID)
	if err != nil {
		return agentSelectionResult{}, err
	}
	roster := a.selectorRoster(ctx, agents)
	decision, modelErr := a.askAgentSelector(ctx, cfg, apiKey, summary, roster, order.WorkspaceID)
	result := agentSelectionResult{Digest: digest}
	if modelErr != nil {
		result.Fallback = modelErr.Error()
		decision = a.fallbackAgentSelection(ctx, order, agents, rejected)
	}
	result.AgentIDs = a.validateSelectorAgentIDs(ctx, order.WorkspaceID, decision.AgentIDs, summary.MaxAgents)
	seenFamily := map[string]bool{}
	for _, family := range rejected {
		seenFamily[family] = true
	}
	for _, family := range decision.RoleFamilies {
		family = strings.TrimSpace(strings.ToLower(family))
		if len(result.AgentIDs) >= summary.MaxAgents || seenFamily[family] {
			continue
		}
		if _, ok := roleFamilyTemplates[family]; !ok {
			continue
		}
		seenFamily[family] = true
		draft, createErr := a.createRoleFamilyDraft(order.WorkspaceID, order.ID, family, cfg)
		if createErr != nil {
			return agentSelectionResult{}, createErr
		}
		result.AgentIDs = append(result.AgentIDs, draft.ID)
	}
	if len(result.AgentIDs) == 0 && !seenFamily["developer"] {
		draft, createErr := a.createRoleFamilyDraft(order.WorkspaceID, order.ID, "developer", cfg)
		if createErr != nil {
			return agentSelectionResult{}, createErr
		}
		result.AgentIDs = []string{draft.ID}
		if result.Fallback == "" {
			result.Fallback = "комплектовщик не вернул пригодного исполнителя"
		}
	}
	return result, nil
}

func (a *App) selectorRoster(ctx context.Context, agents []domain.ProjectAgent) []selectorRosterAgent {
	children := map[string][]string{}
	for _, agent := range agents {
		if agent.Temporary && agent.ParentAgentID != "" {
			children[agent.ParentAgentID] = append(children[agent.ParentAgentID], agent.RoleDescription)
		}
	}
	var result []selectorRosterAgent
	for _, agent := range agents {
		if agent.Temporary || (agent.Status != "" && agent.Status != domain.ProjectAgentActive) {
			continue
		}
		capability := a.projectAgentReadiness(ctx, agent)
		result = append(result, selectorRosterAgent{ID: agent.ID, Name: agent.Name, RoleFamily: agent.RoleFamily, Role: agent.RoleDescription, Mission: agent.Mission, Tools: agent.AllowedTools, Readiness: capability.State, Blocking: capability.Blocking, Subagents: children[agent.ID]})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func (a *App) askAgentSelector(ctx context.Context, cfg domain.OrchestratorConfig, apiKey string, summary TaskAgentSummary, roster []selectorRosterAgent, workspaceID string) (selectorDecision, error) {
	if strings.TrimSpace(cfg.Model) == "" || cfg.Provider == "" {
		return selectorDecision{}, errors.New("модель комплектовщика не настроена")
	}
	model, err := a.budgetedModelFactory(modelBudgetScope{WorkspaceID: workspaceID, ProjectAgentID: "agent-selector", Outcome: "agent_creator_selection"})(providers.Config{
		Kind: cfg.Provider, Preset: cfg.ProviderPreset, BaseURL: cfg.BaseURL, APIVersion: cfg.APIVersion, APIKey: apiKey, TimeoutSeconds: 90,
	})
	if err != nil {
		return selectorDecision{}, err
	}
	payload, _ := json.Marshal(map[string]any{"task": summary, "roster": roster})
	request := providers.ModelRequest{Model: cfg.Model, Messages: []providers.Message{{Role: "system", Content: agentSelectorPrompt}, {Role: "user", Content: "UNTRUSTED INPUT:\n" + string(payload)}}, Temperature: 0, MaxOutputTokens: 1024, ContextWindowTokens: 16384}
	var raw strings.Builder
	callCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err = model.Stream(callCtx, request, func(event providers.ModelEvent) error {
		if event.Kind == providers.EventTextDelta {
			if raw.Len()+len(event.Delta) > 16*1024 {
				return errors.New("ответ комплектовщика слишком велик")
			}
			raw.WriteString(event.Delta)
		}
		return nil
	}); err != nil {
		return selectorDecision{}, err
	}
	text, err := modeljson.Payload(raw.String())
	if err != nil {
		return selectorDecision{}, err
	}
	if narrowed, ok := modeljson.Braces(text); ok {
		text = narrowed
	}
	var decision selectorDecision
	if err = json.Unmarshal([]byte(text), &decision); err != nil {
		return selectorDecision{}, err
	}
	return decision, nil
}

func (a *App) validateSelectorAgentIDs(ctx context.Context, workspaceID string, ids []string, limit int) []string {
	seen := map[string]bool{}
	var result []string
	for _, id := range ids {
		if len(result) >= limit || seen[id] {
			continue
		}
		agent, err := a.store.GetProjectAgent(ctx, strings.TrimSpace(id))
		if err != nil || agent.WorkspaceID != workspaceID || agent.Temporary || (agent.Status != "" && agent.Status != domain.ProjectAgentActive) || a.projectAgentReadiness(ctx, agent).State == "BLOCKED" {
			continue
		}
		seen[id] = true
		result = append(result, id)
	}
	return result
}

func (a *App) fallbackAgentSelection(ctx context.Context, order domain.WorkOrder, agents []domain.ProjectAgent, rejected []string) selectorDecision {
	need := rosterNeedFromOrder(order, nil)
	observation, err := a.ObserveRoster(ctx, need)
	if err == nil && len(observation.Selected) > 0 {
		decision := selectorDecision{}
		for _, item := range observation.Selected {
			decision.AgentIDs = append(decision.AgentIDs, item.AgentID)
		}
		return decision
	}
	for _, family := range rejected {
		if family == "developer" {
			return selectorDecision{}
		}
	}
	return selectorDecision{RoleFamilies: []string{"developer"}}
}

func (a *App) createRoleFamilyDraft(workspaceID, workOrderID, family string, cfg domain.OrchestratorConfig) (domain.ProjectAgent, error) {
	template, ok := roleFamilyTemplates[family]
	if !ok {
		return domain.ProjectAgent{}, errors.New("неизвестное семейство роли")
	}
	agent := domain.ProjectAgent{
		WorkspaceID: workspaceID, Status: domain.ProjectAgentDraft, RoleFamily: family,
		Name: template.Name, RoleDescription: template.Role, Mission: template.Mission, SystemPrompt: template.Prompt,
		Goals: []string{template.Mission}, Rules: []string{"Работай только в границах утверждённого задания и подтверждай результат доказательствами."},
		AllowedTools: a.filterKnownTools(context.Background(), template.Tools), ConnectionID: cfg.ConnectionID,
		Provider: cfg.Provider, ProviderPreset: cfg.ProviderPreset, BaseURL: cfg.BaseURL, PrimaryModel: cfg.Model,
		Temperature: 0.2, MaxOutputTokens: 4096, ContextWindowTokens: 32768, ReasoningEffort: "medium",
		MaxSteps: 24, MaxDurationSeconds: 1800, ApprovalMode: domain.ApprovalSafe,
	}
	saved, err := a.SaveProjectAgent(agent)
	if err != nil {
		return domain.ProjectAgent{}, err
	}
	_ = a.store.SaveAgentLifecycleEvent(context.Background(), domain.AgentLifecycleEvent{ID: domain.NewID("agentlife"), WorkspaceID: workspaceID, AgentID: saved.ID, WorkOrderID: workOrderID, Kind: "draft_created", Detail: map[string]any{"roleFamily": family}, CreatedAt: time.Now().UTC()})
	return saved, nil
}
