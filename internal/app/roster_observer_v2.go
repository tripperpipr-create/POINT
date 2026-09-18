package app

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/orchestrator"
)

// Наблюдатель ростера отвечает на один вопрос: кем делать эту работу.
//
// Раньше карточка запуска обещала исполнителя, которого никто не подбирал: она
// копировала предложенный состав, а на пустом проекте вставляла один и тот же
// черновик «Разработчик проекта» с заранее выписанным набором инструментов. Всё
// нужное для настоящего подбора в ядре уже лежало — ранжирование агента под
// цель, подбор чертежа под текст задачи, поиск пробела по роли, оценка
// готовности, — но каждый механизм жил своей дорогой и до карточки не доходил.
//
// Наблюдатель их склеивает и ничего не создаёт: он читает мир и возвращает
// наблюдение. Агентов по-прежнему заводит человек — согласием в карточке или
// мастерской, — и эта граница здесь не сдвигается.
const (
	rosterConsideredLimit  = 5
	rosterBlueprintLimit   = 3
	rosterBlockingLimit    = 3
	rosterTextLimit        = 400
	rosterDefaultMaxAgents = 2
)

// RosterNeed — сводка требований. Единственный вход наблюдателя: и сборка
// карточки, и инструмент Мастера описывают потребность одинаково.
type RosterNeed struct {
	WorkspaceID    string   `json:"workspaceId,omitempty"`
	Goal           string   `json:"goal"`
	Scope          []string `json:"scope,omitempty"`
	Criteria       []string `json:"criteria,omitempty"`
	RequiredTools  []string `json:"requiredTools,omitempty"`
	StackCategory  string   `json:"stackCategory,omitempty"`
	Mode           string   `json:"mode,omitempty"`
	MaxAgents      int      `json:"maxAgents,omitempty"`
	AllowSubagents bool     `json:"allowSubagents,omitempty"`
	// ProposedIDs — состав, предложенный разговором. Сильный приоритет, но не
	// приговор: заблокированный агент не попадёт в наряд и отсюда.
	ProposedIDs []string `json:"proposedIds,omitempty"`
}

// RosterCandidate — проектный агент с причиной выбора на языке человека.
type RosterCandidate struct {
	AgentID      string                         `json:"agentId"`
	Name         string                         `json:"name"`
	Role         string                         `json:"role,omitempty"`
	Mission      string                         `json:"mission,omitempty"`
	Tools        []string                       `json:"tools,omitempty"`
	Matched      []string                       `json:"matched,omitempty"`
	Blocking     []string                       `json:"blocking,omitempty"`
	MissingTools []string                       `json:"missingTools,omitempty"`
	Score        int                            `json:"score"`
	Readiness    string                         `json:"readiness"` // READY | DEGRADED | BLOCKED
	WhyNot       string                         `json:"whyNot,omitempty"`
	Breakdown    domain.AgentSelectionBreakdown `json:"breakdown"`
}

// RosterBlueprintMatch — чертёж, из которого можно завести исполнителя.
type RosterBlueprintMatch struct {
	BlueprintID string   `json:"blueprintId"`
	Name        string   `json:"name"`
	Role        string   `json:"role,omitempty"`
	Why         string   `json:"why,omitempty"`
	Tools       []string `json:"tools,omitempty"`
	Matched     []string `json:"matched,omitempty"`
}

// RosterGap — кого не хватает и чем это закрыть.
type RosterGap struct {
	Kind        string                 `json:"kind"` // missing_primary | blocked_primary | missing_subagent
	Requirement domain.RoleRequirement `json:"requirement"`
	Blueprints  []RosterBlueprintMatch `json:"blueprints,omitempty"`
	Draft       domain.AgentDraft      `json:"draft,omitempty"`
	Subagent    *domain.SubagentPlan   `json:"subagent,omitempty"`
}

// RosterObservation — весь ответ наблюдателя.
type RosterObservation struct {
	Need       RosterNeed        `json:"need"`
	Selected   []RosterCandidate `json:"selected,omitempty"`
	Considered []RosterCandidate `json:"considered,omitempty"`
	Gaps       []RosterGap       `json:"gaps,omitempty"`
	MaxAgents  int               `json:"maxAgents"`
	Reason     string            `json:"reason,omitempty"`
}

func (a *App) ObserveRoster(ctx context.Context, need RosterNeed) (RosterObservation, error) {
	need = normalizeRosterNeed(need)
	if need.WorkspaceID == "" {
		return RosterObservation{}, errors.New("подбор исполнителей требует открытого проекта")
	}
	agents, err := a.store.ListProjectAgents(ctx, need.WorkspaceID)
	if err != nil {
		return RosterObservation{}, err
	}
	// Временные субагенты живут под своим родителем и в состав наряда сами не
	// входят: иначе бюджет проектных агентов тратился бы на помощников.
	permanent := permanentProjectAgents(agents)
	ready, blocked := a.readyProjectAgents(ctx, permanent)

	goal := rosterGoalText(need)
	cfg, cfgErr := a.masterConfig(ctx, need.WorkspaceID)
	if cfgErr != nil {
		cfg = domain.OrchestratorConfig{}
	}
	assignment := orchestrator.AssignPartyWithSignals(cfg, ready, need.ProposedIDs, goal, a.projectAgentSelectionSignals(ctx, need.WorkspaceID))

	byID := make(map[string]domain.ProjectAgent, len(permanent))
	for _, agent := range permanent {
		byID[agent.ID] = agent
	}
	breakdowns := make(map[string]domain.AgentSelectionBreakdown, len(assignment.Breakdown))
	for _, item := range assignment.Breakdown {
		breakdowns[item.AgentID] = item
	}

	observation := RosterObservation{Need: need, MaxAgents: need.MaxAgents}
	chosen := map[string]bool{}
	for _, id := range assignment.AgentIDs {
		agent, ok := byID[id]
		if !ok {
			continue
		}
		if len(observation.Selected) >= need.MaxAgents {
			// Состав подбирается по настройке оркестратора, а бюджет агентов
			// задаёт наряд, и это два разных числа: при пресете «дирижёр» подбор
			// отдавал трёх исполнителей на бюджет в двух, домен отвергал такой
			// ростер, и ход Мастера пропадал целиком. Бюджет наряда старше.
			observation.Reason = "бюджет проекта — " + countOfAgentsV2(need.MaxAgents) + ", остальные подходящие остались в запасе"
			break
		}
		chosen[id] = true
		observation.Selected = append(observation.Selected, a.rosterCandidate(agent, breakdowns[id], blocked[id], need, ""))
	}

	for _, agent := range permanent {
		if chosen[agent.ID] || len(observation.Considered) >= rosterConsideredLimit {
			continue
		}
		observation.Considered = append(observation.Considered, a.rosterCandidate(agent, breakdowns[agent.ID], blocked[agent.ID], need, rosterWhyNot(agent, blocked, observation.Reason)))
	}
	sort.SliceStable(observation.Considered, func(i, j int) bool {
		return observation.Considered[i].Score > observation.Considered[j].Score
	})

	if gap := a.rosterGap(ctx, need, goal, permanent, ready); gap != nil {
		observation.Gaps = append(observation.Gaps, *gap)
	}
	return observation, nil
}

// rosterGap описывает нехватку словами предметной области и сразу прикладывает,
// чем её закрыть: подходящими чертежами и готовым черновиком.
func (a *App) rosterGap(ctx context.Context, need RosterNeed, goal string, permanent, ready []domain.ProjectAgent) *RosterGap {
	brief := rosterBriefFromNeed(need)
	assessment := a.assessAgentGap(ctx, &brief, permanent, ready)
	if assessment == nil {
		return nil
	}
	gap := RosterGap{Kind: assessment.Kind, Requirement: assessment.Requirement}
	gap.Requirement.RequiredTools = a.filterKnownTools(ctx, appendUniqueStrings(assessment.Requirement.RequiredTools, need.RequiredTools...))

	if blueprints, err := a.store.ListBlueprints(ctx); err == nil {
		for _, hire := range orchestrator.RankHires(goal, blueprints, rosterBlueprintLimit) {
			gap.Blueprints = append(gap.Blueprints, RosterBlueprintMatch{
				BlueprintID: hire.BlueprintID,
				Name:        boundedMasterReadText(hire.Name, rosterTextLimit),
				Role:        boundedMasterReadText(hire.Role, rosterTextLimit),
				Why:         boundedMasterReadText(hire.Why, rosterTextLimit),
				Tools:       a.filterKnownTools(ctx, hire.Tools),
				Matched:     hire.Matched,
			})
		}
	}

	if assessment.Kind == "missing_subagent" {
		// Субагент — помощник под готовым агентом, и права он получает только из
		// пересечения с родителем: временный исполнитель не может уметь больше
		// того, кто за него отвечает.
		if need.AllowSubagents && assessment.Parent != nil {
			gap.Subagent = &domain.SubagentPlan{
				ParentAgentID: assessment.Parent.ID,
				Role:          gap.Requirement.Role,
				Mission:       rosterMission(gap.Requirement),
				RequiredTools: intersectTools(assessment.Parent.AllowedTools, gap.Requirement.RequiredTools),
			}
		}
		return &gap
	}
	gap.Draft = a.rosterDraft(ctx, gap)
	return &gap
}

// rosterDraft готовит черновик исполнителя: из подходящего чертежа, когда он
// есть, иначе с нуля по требованию роли.
func (a *App) rosterDraft(ctx context.Context, gap RosterGap) domain.AgentDraft {
	name, role := rosterRoleNaming(gap.Requirement.Role)
	draft := domain.AgentDraft{
		ID:              domain.NewID("agentdraft"),
		Name:            name,
		Role:            role,
		Mission:         rosterMission(gap.Requirement),
		RequiredTools:   gap.Requirement.RequiredTools,
		RequiresConsent: true,
	}
	if best, ok := rosterBlueprintForRole(gap); ok {
		// Чертёж берём только когда он про эту роль. Совпадения слов мало:
		// универсальный «Локальный агент» совпадает почти с любой задачей и
		// выдавал бы себя за подходящего backend-разработчика.
		draft.BlueprintID = best.BlueprintID
		draft.Name = best.Name
		if best.Role != "" {
			draft.Role = best.Role
		}
		draft.RequiredTools = a.filterKnownTools(ctx, appendUniqueStrings(best.Tools, gap.Requirement.RequiredTools...))
	}
	return draft
}

// rosterBlueprintForRole отвечает, есть ли в каталоге чертёж именно этой роли.
// Универсальный чертёж совпадает словами почти с любой задачей, и без проверки
// роли карточка предлагала «Локального агента» там, где нужен backend.
func rosterBlueprintForRole(gap RosterGap) (RosterBlueprintMatch, bool) {
	role := strings.ToLower(strings.TrimSpace(gap.Requirement.Role))
	for _, blueprint := range gap.Blueprints {
		haystack := strings.ToLower(blueprint.Name + " " + blueprint.Role)
		if role != "" && role != "developer" && (strings.Contains(haystack, role) || roleAliasMatches(role, haystack)) {
			return blueprint, true
		}
		// Работу без своей специальности ведёт любой разработчик: здесь
		// универсальный чертёж и есть точное попадание.
		if (role == "" || role == "developer") && len(blueprint.Matched) > 0 {
			return blueprint, true
		}
	}
	return RosterBlueprintMatch{}, false
}

func (a *App) rosterCandidate(agent domain.ProjectAgent, breakdown domain.AgentSelectionBreakdown, capability AgentCapability, need RosterNeed, whyNot string) RosterCandidate {
	readiness := capability.State
	if readiness == "" {
		readiness = "READY"
	}
	blocking := capability.Blocking
	if len(blocking) > rosterBlockingLimit {
		blocking = blocking[:rosterBlockingLimit]
	}
	return RosterCandidate{
		AgentID:      agent.ID,
		Name:         boundedMasterReadText(agent.Name, rosterTextLimit),
		Role:         boundedMasterReadText(agent.RoleDescription, rosterTextLimit),
		Mission:      boundedMasterReadText(agent.Mission, rosterTextLimit),
		Tools:        agent.AllowedTools,
		Matched:      breakdown.Matched,
		Blocking:     blocking,
		MissingTools: missingRosterTools(agent.AllowedTools, need.RequiredTools),
		Score:        breakdown.Total,
		Readiness:    readiness,
		WhyNot:       whyNot,
		Breakdown:    breakdown,
	}
}

// RosterPlan переводит наблюдение в ростер наряда. previous — ростер открытой
// карточки: неподтверждённый черновик обязан сохранить свой идентификатор между
// ходами, иначе согласие человека сошлётся на исчезнувший черновик.
func (o RosterObservation) RosterPlan(previous domain.AgentRosterPlan) domain.AgentRosterPlan {
	plan := domain.AgentRosterPlan{}
	limit := o.MaxAgents
	if limit <= 0 {
		limit = 1
	}
	for _, candidate := range o.Selected {
		if len(plan.Permanent) >= limit {
			break
		}
		plan.Permanent = append(plan.Permanent, domain.AgentDraft{
			ID: candidate.AgentID, Existing: true, Name: candidate.Name,
			Role: candidate.Role, Mission: candidate.Mission, RequiredTools: candidate.Tools,
		})
	}
	if len(plan.Permanent) == 0 {
		for _, gap := range o.Gaps {
			if gap.Draft.Name == "" {
				continue
			}
			plan.Permanent = append(plan.Permanent, carryRosterDraftIdentity(gap.Draft, previous))
			break
		}
	}
	for _, gap := range o.Gaps {
		if gap.Subagent == nil || len(plan.Permanent) == 0 {
			continue
		}
		parent := rosterSubagentParent(plan.Permanent, *gap.Subagent)
		if parent == "" {
			continue
		}
		subagent := *gap.Subagent
		subagent.ParentAgentID = parent
		plan.Temporary = append(plan.Temporary, subagent)
		break
	}
	return plan
}

// rosterSubagentParent выбирает родителя строго среди утверждаемых агентов.
// Пробел роли называет родителем первого запускаемого агента, а он мог не
// пережить усечение по бюджету — домен такой наряд отвергает целиком.
func rosterSubagentParent(permanent []domain.AgentDraft, subagent domain.SubagentPlan) string {
	for _, draft := range permanent {
		if draft.ID == subagent.ParentAgentID {
			return draft.ID
		}
	}
	for _, draft := range permanent {
		haystack := strings.ToLower(draft.Name + " " + draft.Role + " " + draft.Mission)
		if strings.Contains(haystack, strings.ToLower(subagent.Role)) {
			return draft.ID
		}
	}
	if permanent[0].Existing {
		return permanent[0].ID
	}
	// Под несозданным черновиком помощника заводить нечего: сначала человек
	// подтвердит самого исполнителя.
	return ""
}

func carryRosterDraftIdentity(draft domain.AgentDraft, previous domain.AgentRosterPlan) domain.AgentDraft {
	for _, earlier := range previous.Permanent {
		if earlier.Existing || !strings.EqualFold(strings.TrimSpace(earlier.Role), strings.TrimSpace(draft.Role)) {
			continue
		}
		draft.ID = earlier.ID
		if earlier.Name != "" {
			draft.Name = earlier.Name
		}
		if earlier.Mission != "" {
			draft.Mission = earlier.Mission
		}
		if earlier.BlueprintID != "" && draft.BlueprintID == "" {
			draft.BlueprintID = earlier.BlueprintID
		}
		break
	}
	return draft
}

// validateRosterPlanV2 — зеркало доменных правил ростера. Наблюдатель проверяет
// себя сам: ошибка валидации поднимается из сохранения наряда наружу и стоит
// человеку всего хода, а не одной карточки.
func validateRosterPlanV2(plan domain.AgentRosterPlan, budget domain.BudgetEnvelope) error {
	permanentIDs := make(map[string]bool, len(plan.Permanent))
	for _, draft := range plan.Permanent {
		permanentIDs[draft.ID] = true
		if draft.Existing {
			if strings.TrimSpace(draft.ID) == "" {
				return errors.New("существующий агент без идентификатора")
			}
			continue
		}
		if draft.Name == "" || draft.Role == "" || draft.Mission == "" {
			return errors.New("черновик агента без имени, роли или миссии")
		}
		if draft.BlueprintID == "" && !draft.RequiresConsent {
			return errors.New("новый агент без согласия человека")
		}
	}
	if budget.MaxProjectAgents > 0 && len(plan.Permanent) > budget.MaxProjectAgents {
		return errors.New("ростер выходит за бюджет проектных агентов")
	}
	for _, temporary := range plan.Temporary {
		if !permanentIDs[temporary.ParentAgentID] || strings.TrimSpace(temporary.Role) == "" || strings.TrimSpace(temporary.Mission) == "" {
			return errors.New("субагент без родителя, роли или миссии")
		}
	}
	return nil
}

// filterKnownTools отсекает имена, которых нет ни в каталоге, ни среди
// пользовательских инструментов. Выдуманное имя создаёт агента, который родится
// заблокированным: наряд заводит исполнителя в обход проверки профиля.
func (a *App) filterKnownTools(ctx context.Context, names []string) []string {
	allowed := make(map[string]bool, 32)
	for _, item := range domain.BuiltInToolCatalog() {
		allowed[item.Name] = true
	}
	if custom, err := a.store.ListCustomTools(ctx); err == nil {
		for _, tool := range custom {
			allowed[tool.ID] = true
		}
	}
	result := make([]string, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] || !allowed[name] {
			continue
		}
		seen[name] = true
		result = append(result, name)
	}
	return result
}

func normalizeRosterNeed(need RosterNeed) RosterNeed {
	need.WorkspaceID = strings.TrimSpace(need.WorkspaceID)
	need.Goal = strings.TrimSpace(need.Goal)
	need.StackCategory = strings.TrimSpace(need.StackCategory)
	if need.MaxAgents <= 0 {
		need.MaxAgents = rosterDefaultMaxAgents
	}
	if need.MaxAgents > 8 {
		need.MaxAgents = 8
	}
	return need
}

// rosterBriefFromNeed собирает задание в том виде, в каком его читает поиск
// пробела: тот смотрит только цель, объём, критерии и разрешения.
func rosterBriefFromNeed(need RosterNeed) domain.TaskBrief {
	brief := domain.TaskBrief{Goal: need.Goal, Scope: append([]string(nil), need.Scope...)}
	for _, text := range need.Criteria {
		brief.Criteria = append(brief.Criteria, domain.AcceptanceCriterion{Text: text})
	}
	for _, tool := range need.RequiredTools {
		switch tool {
		case "propose_patch":
			brief.Permissions.WriteFiles = true
		case "run_command":
			brief.Permissions.ExecuteCommands = true
		}
	}
	brief.Permissions.ProvisionProjectAgents = need.AllowSubagents
	return brief
}

func rosterGoalText(need RosterNeed) string {
	parts := append([]string{need.Goal}, need.Scope...)
	parts = append(parts, need.Criteria...)
	return strings.TrimSpace(strings.Join(parts, " "))
}

func rosterWhyNot(agent domain.ProjectAgent, blocked map[string]AgentCapability, budgetReason string) string {
	if capability, ok := blocked[agent.ID]; ok && len(capability.Blocking) > 0 {
		return capability.Blocking[0]
	}
	if budgetReason != "" {
		return budgetReason
	}
	return "задача ближе к другому исполнителю"
}

func missingRosterTools(available, required []string) []string {
	have := make(map[string]bool, len(available))
	for _, tool := range available {
		have[tool] = true
	}
	missing := make([]string, 0, len(required))
	for _, tool := range required {
		if !have[tool] {
			missing = append(missing, tool)
		}
	}
	return missing
}

// rosterRoleNaming даёт черновику имя и роль на языке продукта. Требование роли
// написано по-английски — оно машинное, и человеку его читать не обязательно.
func rosterRoleNaming(role string) (string, string) {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "frontend":
		return "Frontend-разработчик", "Владелец интерфейса"
	case "backend":
		return "Backend-разработчик", "Владелец серверной части"
	case "database":
		return "Инженер данных", "Владелец схемы и миграций"
	case "testing":
		return "Инженер проверки", "Владелец проверок и доказательств"
	case "security":
		return "Ревьюер безопасности", "Владелец проверки безопасности"
	default:
		return "Разработчик проекта", "Владелец реализации"
	}
}

func rosterMission(requirement domain.RoleRequirement) string {
	name, _ := rosterRoleNaming(requirement.Role)
	if name == "Разработчик проекта" {
		return "Реализовать утверждённый WorkOrder и предоставить проверяемые доказательства каждого критерия."
	}
	return "Вести часть задания, за которую отвечает " + name + ", и подтвердить каждый критерий проверкой."
}

func countOfAgentsV2(count int) string {
	mod100 := count % 100
	mod10 := mod100 % 10
	switch {
	case mod100 >= 11 && mod100 <= 19:
		return strconv.Itoa(count) + " агентов"
	case mod10 == 1:
		return strconv.Itoa(count) + " агент"
	case mod10 >= 2 && mod10 <= 4:
		return strconv.Itoa(count) + " агента"
	default:
		return strconv.Itoa(count) + " агентов"
	}
}
