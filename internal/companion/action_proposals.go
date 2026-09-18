package companion

import (
	"context"
	"fmt"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
)

// Три предложения, которые компаньон готовит на подтверждение: Skill, агент и
// отряд. Ни одно из них ничего не применяет само — каждое кладёт в хранилище
// CompanionActionProposal со статусом pending, а решение остаётся за человеком.
// Лежали в service.go рядом с Chat, хотя с разговором связаны только вызовом.

func (s Service) proposeSkillAction(ctx context.Context, cfg domain.CompanionConfig, message string, projectContext gatheredContext) (ChatResponse, error) {
	response := ChatResponse{Level: "suggestion", Mode: "deterministic", FactsUsed: projectContext.Facts}
	name := companionSkillName(message)
	existing, err := s.Store.ListSkills(ctx)
	if err != nil {
		return ChatResponse{}, err
	}
	for _, skill := range existing {
		if strings.EqualFold(strings.TrimSpace(skill.Name), name) {
			response.Reply = fmt.Sprintf("Skill «%s» уже существует. Я не создал дубликат; его можно подключить к проекту или изменить в разделе Skills.", skill.Name)
			return response, nil
		}
	}
	now := time.Now().UTC()
	skill := domain.SkillDefinition{
		ID: domain.NewID("skill"), Name: name,
		Description:     companionSkillDescription(message, name),
		Instructions:    companionSkillInstructions(message),
		RequiredTools:   companionSkillTools(message),
		PermissionDelta: map[string]domain.ToolPolicy{}, Configuration: map[string]any{},
		CreatedAt: now, UpdatedAt: now,
	}
	proposal := domain.CompanionActionProposal{
		ID: domain.NewID("companionaction"), WorkspaceID: cfg.WorkspaceID, Kind: domain.CompanionActionCreateSkill,
		Title:     "Создать Skill · " + skill.Name,
		Rationale: fmt.Sprintf("Подготовлен Skill с %d явными tool requirements, без scripts, references и скрытого расширения permissions. После подтверждения definition будет создан и подключён к текущему проекту.", len(skill.RequiredTools)),
		Skill:     &skill, Status: "pending", CreatedAt: now, UpdatedAt: now,
	}
	if err = s.Store.SaveCompanionActionProposal(ctx, proposal); err != nil {
		return ChatResponse{}, err
	}
	response.ActionProposal = &proposal
	response.Reply = fmt.Sprintf("Подготовил Skill «%s». Проверьте инструкции и требуемые tools; создание и подключение произойдут только после подтверждения.", skill.Name)
	return response, nil
}

func (s Service) proposeAgentAction(ctx context.Context, cfg domain.CompanionConfig, message string, projectContext gatheredContext) (ChatResponse, error) {
	response := ChatResponse{Level: "suggestion", Mode: "deterministic", FactsUsed: projectContext.Facts}
	blueprints, err := s.Store.ListBlueprints(ctx)
	if err != nil {
		return ChatResponse{}, err
	}
	if len(blueprints) == 0 {
		response.Reply = "Для создания Project Agent нужен хотя бы один Blueprint. Сначала добавьте шаблон агента."
		response.Questions = []string{"Какой Blueprint использовать для первого агента?"}
		return response, nil
	}
	blueprint := selectBlueprint(blueprints, message)
	agent := domain.ProjectAgentFromBlueprint(cfg.WorkspaceID, blueprint)
	agent.Name = companionAgentName(message, blueprint.Name)
	agent.Mission = trim("Выполнять задачи текущего проекта по запросу: "+strings.TrimSpace(message), 1200)
	now := time.Now().UTC()
	proposal := domain.CompanionActionProposal{
		ID: domain.NewID("companionaction"), WorkspaceID: cfg.WorkspaceID, Kind: domain.CompanionActionCreateAgent,
		Title:     "Создать агента · " + agent.Name,
		Rationale: fmt.Sprintf("Черновик основан на Blueprint «%s». Tools, permissions и модель не расширены относительно шаблона.", blueprint.Name),
		Agent:     &agent, Status: "pending", CreatedAt: now, UpdatedAt: now,
	}
	if err = s.Store.SaveCompanionActionProposal(ctx, proposal); err != nil {
		return ChatResponse{}, err
	}
	response.ActionProposal = &proposal
	response.Reply = fmt.Sprintf("Подготовил Project Agent «%s» на основе Blueprint «%s». Проверьте роль, миссию, модель и разрешения перед созданием.", agent.Name, blueprint.Name)
	return response, nil
}

func (s Service) proposeTeamAction(ctx context.Context, cfg domain.CompanionConfig, message string, projectContext gatheredContext) (ChatResponse, error) {
	response := ChatResponse{Level: "suggestion", Mode: "deterministic", FactsUsed: projectContext.Facts}
	agents, err := s.Store.ListProjectAgents(ctx, cfg.WorkspaceID)
	if err != nil {
		return ChatResponse{}, err
	}
	if len(agents) == 0 {
		response.Reply = "Для команды нужен хотя бы один Project Agent. Сначала создайте агента в текущем проекте."
		response.Questions = []string{"Какого первого агента создать для команды?"}
		return response, nil
	}
	now := time.Now().UTC()
	team := domain.Team{
		ID: domain.NewID("team"), WorkspaceID: cfg.WorkspaceID, Name: companionTeamName(message),
		Description: "Companion draft from an explicit user request", AgentIDs: selectTeam(agents, message, 4),
		CreatedAt: now, UpdatedAt: now,
	}
	proposal := domain.CompanionActionProposal{
		ID: domain.NewID("companionaction"), WorkspaceID: cfg.WorkspaceID, Kind: domain.CompanionActionCreateTeam,
		Title:     "Создать отряд · " + team.Name,
		Rationale: fmt.Sprintf("Подобрано %d Project Agents по роли и содержанию запроса. Команда появится только после подтверждения.", len(team.AgentIDs)),
		Team:      &team, Status: "pending", CreatedAt: now, UpdatedAt: now,
	}
	if err = s.Store.SaveCompanionActionProposal(ctx, proposal); err != nil {
		return ChatResponse{}, err
	}
	response.ActionProposal = &proposal
	response.Reply = fmt.Sprintf("Подготовил отряд «%s» из %d агентов. Проверьте состав и подтвердите создание.", team.Name, len(team.AgentIDs))
	return response, nil
}
