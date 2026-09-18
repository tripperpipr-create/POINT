package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/flowruntime"
)

const (
	CompanionActionApply  = "apply"
	CompanionActionModify = "modify"
	CompanionActionIgnore = "ignore"
)

type CompanionActionDecision struct {
	ProposalID      string   `json:"proposalId"`
	Action          string   `json:"action"`
	Name            string   `json:"name,omitempty"`
	Description     string   `json:"description,omitempty"`
	RoleDescription string   `json:"roleDescription,omitempty"`
	Mission         string   `json:"mission,omitempty"`
	AgentIDs        []string `json:"agentIds,omitempty"`
	Instructions    string   `json:"instructions,omitempty"`
	RequiredTools   []string `json:"requiredTools,omitempty"`
	// Настройки исполнителя, которые человек правит прямо в карточке ленты.
	//
	// Раньше решение несло только имя, роль и миссию: карточка показывала
	// модель, умения и пределы, человек их менял — и всё это молча терялось по
	// дороге, а агент заводился с тем, что придумало ядро. Пустое поле по-
	// прежнему ничего не перетирает, поэтому старые карточки работают как
	// работали; температура приходит указателем, потому что 0 — законное
	// значение и от «не прислали» его надо отличать.
	AllowedTools       []string          `json:"allowedTools,omitempty"`
	ToolPolicies       map[string]string `json:"toolPolicies,omitempty"`
	ConnectionID       string            `json:"connectionId,omitempty"`
	PrimaryModel       string            `json:"primaryModel,omitempty"`
	ReasoningEffort    string            `json:"reasoningEffort,omitempty"`
	ApprovalMode       string            `json:"approvalMode,omitempty"`
	MaxSteps           int               `json:"maxSteps,omitempty"`
	MaxDurationSeconds int               `json:"maxDurationSeconds,omitempty"`
	MaxOutputTokens    int               `json:"maxOutputTokens,omitempty"`
	Temperature        *float64          `json:"temperature,omitempty"`
}

type CompanionActionResult struct {
	Proposal     domain.CompanionActionProposal `json:"proposal"`
	Flow         *domain.FlowGraph              `json:"flow,omitempty"`
	Agent        *domain.ProjectAgent           `json:"agent,omitempty"`
	Team         *domain.Team                   `json:"team,omitempty"`
	Skill        *domain.SkillDefinition        `json:"skill,omitempty"`
	ProjectSkill *domain.ProjectSkillInstance   `json:"projectSkill,omitempty"`
	Tool         *domain.CustomTool             `json:"tool,omitempty"`
}

func (a *App) DecideCompanionAction(decision CompanionActionDecision) (CompanionActionResult, error) {
	workspace, err := a.requireWorkspace()
	if err != nil {
		return CompanionActionResult{}, err
	}
	decision.ProposalID = strings.TrimSpace(decision.ProposalID)
	decision.Action = strings.ToLower(strings.TrimSpace(decision.Action))
	if decision.ProposalID == "" {
		return CompanionActionResult{}, errors.New("companion action proposal id is required")
	}
	proposals, err := a.store.ListCompanionActionProposals(context.Background(), workspace.ID)
	if err != nil {
		return CompanionActionResult{}, err
	}
	var proposal *domain.CompanionActionProposal
	for index := range proposals {
		if proposals[index].ID == decision.ProposalID {
			proposal = &proposals[index]
			break
		}
	}
	if proposal == nil {
		return CompanionActionResult{}, fmt.Errorf("companion action proposal %q not found", decision.ProposalID)
	}
	if proposal.Status != "pending" && proposal.Status != "modified" {
		return CompanionActionResult{}, fmt.Errorf("companion action proposal is already %s", proposal.Status)
	}
	if !validCompanionActionPayload(*proposal) {
		return CompanionActionResult{}, fmt.Errorf("unsupported companion action kind %q", proposal.Kind)
	}
	now := time.Now().UTC()
	switch decision.Action {
	case CompanionActionIgnore:
		proposal.Status = "ignored"
		proposal.UpdatedAt = now
		if err = a.store.SaveCompanionActionProposal(context.Background(), *proposal); err != nil {
			return CompanionActionResult{}, err
		}
		return CompanionActionResult{Proposal: *proposal}, nil
	case CompanionActionModify:
		if err = a.applyCompanionActionOverrides(proposal, decision); err != nil {
			return CompanionActionResult{}, err
		}
		proposal.Status = "modified"
		proposal.UpdatedAt = now
		if err = a.store.SaveCompanionActionProposal(context.Background(), *proposal); err != nil {
			return CompanionActionResult{}, err
		}
		return CompanionActionResult{Proposal: *proposal, Flow: proposal.Flow, Agent: proposal.Agent, Team: proposal.Team, Skill: proposal.Skill}, nil
	case CompanionActionApply:
		if err = a.applyCompanionActionOverrides(proposal, decision); err != nil {
			return CompanionActionResult{}, err
		}
		result := CompanionActionResult{}
		switch proposal.Kind {
		case domain.CompanionActionCreateFlow:
			flow := *proposal.Flow
			flow.WorkspaceID = workspace.ID
			saved, saveErr := a.SaveFlow(flow)
			if saveErr != nil {
				return CompanionActionResult{}, saveErr
			}
			proposal.Flow, proposal.AppliedEntityID, result.Flow = &saved, saved.ID, &saved
		case domain.CompanionActionCreateAgent:
			agent := *proposal.Agent
			agent.WorkspaceID = workspace.ID
			saved, saveErr := a.SaveProjectAgent(agent)
			if saveErr != nil {
				return CompanionActionResult{}, saveErr
			}
			proposal.Agent, proposal.AppliedEntityID, result.Agent = &saved, saved.ID, &saved
		case domain.CompanionActionCreateTeam:
			team := *proposal.Team
			team.WorkspaceID = workspace.ID
			saved, saveErr := a.SaveTeam(team)
			if saveErr != nil {
				return CompanionActionResult{}, saveErr
			}
			proposal.Team, proposal.AppliedEntityID, result.Team = &saved, saved.ID, &saved
		case domain.CompanionActionCreateSkill:
			skill := *proposal.Skill
			saved, saveErr := a.SaveSkill(skill)
			if saveErr != nil {
				return CompanionActionResult{}, saveErr
			}
			instance, equipErr := a.EquipSkill(workspace.ID, saved.ID, saved.Configuration)
			if equipErr != nil {
				return CompanionActionResult{}, equipErr
			}
			proposal.Skill, proposal.AppliedEntityID, result.Skill, result.ProjectSkill = &saved, saved.ID, &saved, &instance
		case domain.CompanionActionCreateTool:
			// Инструмент создаётся — и остаётся лежать. В права агента он не
			// попадает: выдаёт их человек отдельным действием, иначе агент
			// расширял бы себе доступ внутри прогона.
			saved, saveErr := a.SaveCustomTool(*proposal.Tool)
			if saveErr != nil {
				return CompanionActionResult{}, saveErr
			}
			proposal.Tool, proposal.AppliedEntityID, result.Tool = &saved, saved.ID, &saved
		}
		proposal.Status = "applied"
		proposal.UpdatedAt = now
		if err = a.store.SaveCompanionActionProposal(context.Background(), *proposal); err != nil {
			return CompanionActionResult{}, err
		}
		result.Proposal = *proposal
		return result, nil
	default:
		return CompanionActionResult{}, fmt.Errorf("unknown companion action %q", decision.Action)
	}
}

func validCompanionActionPayload(proposal domain.CompanionActionProposal) bool {
	switch proposal.Kind {
	case domain.CompanionActionCreateFlow:
		return proposal.Flow != nil
	case domain.CompanionActionCreateAgent:
		return proposal.Agent != nil
	case domain.CompanionActionCreateTeam:
		return proposal.Team != nil
	case domain.CompanionActionCreateSkill:
		return proposal.Skill != nil
	case domain.CompanionActionCreateTool:
		return proposal.Tool != nil
	default:
		return false
	}
}

func (a *App) applyCompanionActionOverrides(proposal *domain.CompanionActionProposal, decision CompanionActionDecision) error {
	switch proposal.Kind {
	case domain.CompanionActionCreateFlow:
		if err := applyCompanionFlowOverrides(proposal, decision); err != nil {
			return err
		}
		if err := flowruntime.ValidateGraph(*proposal.Flow); err != nil {
			return fmt.Errorf("invalid companion flow draft: %w", err)
		}
		return nil
	case domain.CompanionActionCreateAgent:
		return a.applyCompanionAgentOverrides(proposal, decision)
	case domain.CompanionActionCreateTeam:
		if err := applyCompanionTeamOverrides(proposal, decision); err != nil {
			return err
		}
		return a.validateCompanionTeamDraft(*proposal.Team)
	case domain.CompanionActionCreateSkill:
		return applyCompanionSkillOverrides(proposal, decision)
	case domain.CompanionActionCreateTool:
		return applyCompanionToolOverrides(proposal, decision)
	default:
		return fmt.Errorf("unsupported companion action kind %q", proposal.Kind)
	}
}

func applyCompanionSkillOverrides(proposal *domain.CompanionActionProposal, decision CompanionActionDecision) error {
	if name := strings.TrimSpace(decision.Name); name != "" {
		proposal.Skill.Name = name
		proposal.Title = "Создать Skill · " + name
	}
	if description := strings.TrimSpace(decision.Description); description != "" {
		proposal.Skill.Description = description
	}
	if instructions := strings.TrimSpace(decision.Instructions); instructions != "" {
		proposal.Skill.Instructions = instructions
	}
	if decision.RequiredTools != nil {
		proposal.Skill.RequiredTools = append([]string(nil), decision.RequiredTools...)
	}
	if err := validateCompanionSkillDraft(*proposal.Skill); err != nil {
		return err
	}
	tools, err := normalizeSkillList("required tools", proposal.Skill.RequiredTools, 16, 200)
	if err != nil {
		return err
	}
	proposal.Skill.RequiredTools = tools
	if proposal.Skill.PermissionDelta == nil {
		proposal.Skill.PermissionDelta = map[string]domain.ToolPolicy{}
	}
	if proposal.Skill.Configuration == nil {
		proposal.Skill.Configuration = map[string]any{}
	}
	return nil
}

func validateCompanionSkillDraft(skill domain.SkillDefinition) error {
	if strings.TrimSpace(skill.Name) == "" || len([]rune(strings.TrimSpace(skill.Name))) > 120 {
		return errors.New("companion skill name is required and must not exceed 120 characters")
	}
	if len([]rune(strings.TrimSpace(skill.Description))) > 4096 {
		return errors.New("companion skill description exceeds 4096 characters")
	}
	if strings.TrimSpace(skill.Instructions) == "" || len([]rune(strings.TrimSpace(skill.Instructions))) > 32768 {
		return errors.New("companion skill instructions are required and must not exceed 32768 characters")
	}
	if len(skill.References) > 0 || len(skill.Scripts) > 0 {
		return errors.New("companion skill drafts cannot add references or executable scripts")
	}
	if len(skill.PermissionDelta) > 0 {
		return errors.New("companion skill drafts cannot expand permissions")
	}
	tools, err := normalizeSkillList("required tools", skill.RequiredTools, 16, 200)
	if err != nil {
		return err
	}
	if len(tools) == 0 {
		return errors.New("companion skill must declare at least one required tool")
	}
	known := map[string]bool{}
	for _, item := range domain.BuiltInToolCatalog() {
		known[item.Name] = true
	}
	for _, tool := range tools {
		if !known[tool] {
			return fmt.Errorf("companion skill references unknown built-in tool %q", tool)
		}
	}
	return nil
}

func applyCompanionFlowOverrides(proposal *domain.CompanionActionProposal, decision CompanionActionDecision) error {
	name := strings.TrimSpace(decision.Name)
	if name != "" {
		proposal.Flow.Name = name
		proposal.Title = "Создать Flow · " + name
	}
	description := strings.TrimSpace(decision.Description)
	if description != "" {
		proposal.Flow.Description = description
	}
	if strings.TrimSpace(proposal.Flow.Name) == "" {
		return errors.New("companion flow name is required")
	}
	if len([]rune(proposal.Flow.Name)) > 200 {
		return errors.New("companion flow name exceeds 200 characters")
	}
	if len([]rune(proposal.Flow.Description)) > 4096 {
		return errors.New("companion flow description exceeds 4096 characters")
	}
	return nil
}

func (a *App) applyCompanionAgentOverrides(proposal *domain.CompanionActionProposal, decision CompanionActionDecision) error {
	if name := strings.TrimSpace(decision.Name); name != "" {
		proposal.Agent.Name = name
		proposal.Title = "Создать агента · " + name
	}
	if role := strings.TrimSpace(decision.RoleDescription); role != "" {
		proposal.Agent.RoleDescription = role
	}
	if mission := strings.TrimSpace(decision.Mission); mission != "" {
		proposal.Agent.Mission = mission
	}
	applyCompanionAgentRuntime(proposal.Agent, decision)
	if strings.TrimSpace(proposal.Agent.Name) == "" || len([]rune(proposal.Agent.Name)) > 120 {
		return errors.New("companion agent name is required and must not exceed 120 characters")
	}
	if strings.TrimSpace(proposal.Agent.RoleDescription) == "" || len([]rune(proposal.Agent.RoleDescription)) > 1000 {
		return errors.New("companion agent role is required and must not exceed 1000 characters")
	}
	if len([]rune(proposal.Agent.Mission)) > 4096 {
		return errors.New("companion agent mission exceeds 4096 characters")
	}
	if strings.TrimSpace(proposal.Agent.SystemPrompt) == "" || strings.TrimSpace(string(proposal.Agent.Provider)) == "" || strings.TrimSpace(proposal.Agent.PrimaryModel) == "" {
		return errors.New("companion agent draft is missing Blueprint runtime configuration")
	}
	// Правки человека проверяются тем же уставом, что и сохранение агента:
	// неизвестное умение, чужая политика и режим подтверждений вне устава
	// обязаны отбиваться на «Изменить», а не всплывать при создании. Устав
	// один — validateActorDefinition; своя копия проверок разошлась бы с ним.
	if err := a.validateActorDefinition(context.Background(), domain.ProfileFromProjectAgent(*proposal.Agent), proposal.Agent.SkillIDs); err != nil {
		return err
	}
	return checkAgentLimits(proposal.Agent.Provider, proposal.Agent.BaseURL, proposal.Agent.ApprovalMode,
		proposal.Agent.Temperature, proposal.Agent.MaxOutputTokens, proposal.Agent.MaxSteps, proposal.Agent.MaxDurationSeconds)
}

// applyCompanionAgentRuntime переносит в черновик то, что человек выбрал в
// карточке: подключение и модель, набор умений с политиками, режим
// подтверждений и пределы хода. Пустое значение не перетирает ничего —
// карточка присылает только то, что показывает, а старые карточки не
// присылают ничего и работают как раньше.
func applyCompanionAgentRuntime(agent *domain.ProjectAgent, decision CompanionActionDecision) {
	if connection := strings.TrimSpace(decision.ConnectionID); connection != "" {
		agent.ConnectionID = connection
	}
	if model := strings.TrimSpace(decision.PrimaryModel); model != "" {
		agent.PrimaryModel = model
	}
	if effort := strings.TrimSpace(decision.ReasoningEffort); effort != "" {
		agent.ReasoningEffort = effort
	}
	if mode := strings.TrimSpace(decision.ApprovalMode); mode != "" {
		agent.ApprovalMode = domain.ApprovalMode(mode)
	}
	if decision.AllowedTools != nil {
		agent.AllowedTools = append([]string(nil), decision.AllowedTools...)
	}
	if decision.ToolPolicies != nil {
		policies := make(map[string]string, len(decision.ToolPolicies))
		for key, value := range decision.ToolPolicies {
			policies[key] = value
		}
		agent.ToolPolicies = policies
	}
	if decision.MaxSteps > 0 {
		agent.MaxSteps = decision.MaxSteps
	}
	if decision.MaxDurationSeconds > 0 {
		agent.MaxDurationSeconds = decision.MaxDurationSeconds
	}
	if decision.MaxOutputTokens > 0 {
		agent.MaxOutputTokens = decision.MaxOutputTokens
	}
	if decision.Temperature != nil {
		agent.Temperature = *decision.Temperature
	}
}

func applyCompanionTeamOverrides(proposal *domain.CompanionActionProposal, decision CompanionActionDecision) error {
	if name := strings.TrimSpace(decision.Name); name != "" {
		proposal.Team.Name = name
		proposal.Title = "Создать отряд · " + name
	}
	if description := strings.TrimSpace(decision.Description); description != "" {
		proposal.Team.Description = description
	}
	if decision.AgentIDs != nil {
		proposal.Team.AgentIDs = append([]string(nil), decision.AgentIDs...)
	}
	if strings.TrimSpace(proposal.Team.Name) == "" || len([]rune(proposal.Team.Name)) > 120 {
		return errors.New("companion team name is required and must not exceed 120 characters")
	}
	if len([]rune(proposal.Team.Description)) > 4096 {
		return errors.New("companion team description exceeds 4096 characters")
	}
	return nil
}

func (a *App) validateCompanionTeamDraft(team domain.Team) error {
	workspace, err := a.requireWorkspace()
	if err != nil {
		return err
	}
	if team.WorkspaceID != "" && team.WorkspaceID != workspace.ID {
		return errors.New("companion team belongs to another workspace")
	}
	if len(team.AgentIDs) == 0 || len(team.AgentIDs) > 8 {
		return errors.New("в отряде должен быть хотя бы один агент (не больше 8)")
	}
	agents, err := a.store.ListProjectAgents(context.Background(), workspace.ID)
	if err != nil {
		return err
	}
	available := make(map[string]bool, len(agents))
	for _, agent := range agents {
		available[agent.ID] = true
	}
	seen := map[string]bool{}
	for _, agentID := range team.AgentIDs {
		if !available[agentID] {
			return fmt.Errorf("в отряде выбран недоступный агент %q — обновите состав", agentID)
		}
		if seen[agentID] {
			return fmt.Errorf("агент %q выбран в отряд дважды", agentID)
		}
		seen[agentID] = true
	}
	return nil
}

// applyCompanionToolOverrides — правки человека поверх черновика инструмента.
//
// Команду человек правит здесь же: черновик несёт ровно то, что он написал в
// сообщении, и подменить её моделью нельзя ни на каком шаге.
func applyCompanionToolOverrides(proposal *domain.CompanionActionProposal, decision CompanionActionDecision) error {
	if name := strings.TrimSpace(decision.Name); name != "" {
		proposal.Tool.DisplayName = name
		proposal.Title = "Создать инструмент · " + name
	}
	if description := strings.TrimSpace(decision.Description); description != "" {
		proposal.Tool.Description = description
	}
	if instructions := strings.TrimSpace(decision.Instructions); instructions != "" {
		proposal.Tool.Command = instructions
	}
	if strings.TrimSpace(proposal.Tool.Command) == "" {
		return errors.New("инструмент без команды создать нельзя")
	}
	return nil
}
