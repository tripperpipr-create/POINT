// Подготовка предложений: агент, отряд, состав участников.
package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"local-agent-workbench/internal/domain"
)

// pendingOwnProposal — предложение, которое Мастер сделал в этом разговоре и
// которое всё ещё ждёт решения.
//
// Ищется по своей же переписке, а не по всей очереди: `proposalId` стоит на той
// реплике, что его создала, поэтому чужие предложения — компаньона — Мастеру не
// припишутся. Дойдя до последнего названного предложения, поиск останавливается:
// если человек его уже решил, звать назад к более старым — навязчивость.
//
// Отказ хранилища здесь не ошибка хода: разговор продолжается, просто без
// напоминания. Соврать «ничего не ждёт» он при этом не может — эта функция
// отвечает только на вопрос «о чём напомнить», а не «что в очереди».
func (s ChatService) pendingOwnProposal(ctx context.Context, workspaceID string) *domain.QuestProposal {
	history, err := s.History(ctx, workspaceID, 20)
	if err != nil || len(history) == 0 {
		return nil
	}
	proposals, err := s.Store.ListQuestProposals(ctx, workspaceID)
	if err != nil {
		return nil
	}
	byID := make(map[string]domain.QuestProposal, len(proposals))
	for _, proposal := range proposals {
		byID[proposal.ID] = proposal
	}
	for index := len(history) - 1; index >= 0; index-- {
		id := strings.TrimSpace(history[index].ProposalID)
		if id == "" {
			continue
		}
		proposal, ok := byID[id]
		if ok && (proposal.Status == "pending" || proposal.Status == "modified") {
			return &proposal
		}
		return nil
	}
	return nil
}

// pendingOwnActionProposal находит последний ещё не решённый черновик Hub,
// который Мастер приложил к своей реплике. Повтор «создай агента» не должен
// плодить одинаковые карточки в очереди решений.
func (s ChatService) pendingOwnActionProposal(ctx context.Context, workspaceID string, kind domain.CompanionActionKind) *domain.CompanionActionProposal {
	history, err := s.History(ctx, workspaceID, 20)
	if err != nil || len(history) == 0 {
		return nil
	}
	proposals, err := s.Store.ListCompanionActionProposals(ctx, workspaceID)
	if err != nil {
		return nil
	}
	byID := make(map[string]domain.CompanionActionProposal, len(proposals))
	for _, proposal := range proposals {
		byID[proposal.ID] = proposal
	}
	for index := len(history) - 1; index >= 0; index-- {
		id := strings.TrimSpace(history[index].ActionProposalID)
		if id == "" {
			continue
		}
		proposal, ok := byID[id]
		if ok && proposal.Kind == kind && (proposal.Status == "pending" || proposal.Status == "modified") {
			return &proposal
		}
		// Последнее действие уже решено или было другого рода. Более старое не
		// возвращаем: это снова подняло бы карточку, от которой человек ушёл.
		return nil
	}
	return nil
}

func uniqueAgentName(base string, agents []domain.ProjectAgent) string {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "Новый агент"
	}
	used := make(map[string]bool, len(agents))
	for _, agent := range agents {
		used[strings.ToLower(strings.TrimSpace(agent.Name))] = true
	}
	if !used[strings.ToLower(base)] {
		return base
	}
	for suffix := 2; suffix < 1000; suffix++ {
		candidate := fmt.Sprintf("%s %d", base, suffix)
		if !used[strings.ToLower(candidate)] {
			return candidate
		}
	}
	return base + " · новый"
}

func (s ChatService) prepareAgentAction(ctx context.Context, req ChatRequest, agents []domain.ProjectAgent, goal, continuationPrompt, continuationLabel string) (*domain.CompanionActionProposal, *HireSuggestion, error) {
	if pending := s.pendingOwnActionProposal(ctx, req.WorkspaceID, domain.CompanionActionCreateAgent); pending != nil {
		if pending.Agent != nil {
			// Прямой черновик агента мог уже ждать решения, когда человек затем
			// назвал работу. Привязываем первую такую задачу к существующей
			// карточке вместо второго черновика и не теряем её после Apply.
			if pending.ContinuationPrompt == "" && strings.TrimSpace(continuationPrompt) != "" {
				pending.ContinuationPrompt = strings.TrimSpace(continuationPrompt)
				pending.ContinuationLabel = strings.TrimSpace(continuationLabel)
				pending.UpdatedAt = s.now()
				if err := s.Store.SaveCompanionActionProposal(ctx, *pending); err != nil {
					return nil, nil, err
				}
			}
			return pending, nil, nil
		}
	}
	blueprints, err := s.Store.ListBlueprints(ctx)
	if err != nil {
		return nil, nil, err
	}
	hire := suggestHire(goal, blueprints)
	if hire == nil {
		return nil, nil, nil
	}
	var blueprint *domain.AgentBlueprint
	for index := range blueprints {
		if blueprints[index].ID == hire.BlueprintID {
			blueprint = &blueprints[index]
			break
		}
	}
	if blueprint == nil {
		return nil, nil, errors.New("чертёж предложенного агента не найден")
	}
	draft := domain.ProjectAgentFromBlueprint(req.WorkspaceID, *blueprint)
	draft.Name = uniqueAgentName(draft.Name, agents)
	now := s.now()
	proposal := domain.CompanionActionProposal{
		ID: s.newID("hubaction"), WorkspaceID: req.WorkspaceID,
		Kind: domain.CompanionActionCreateAgent, Title: "Создать агента · " + draft.Name,
		Rationale: hire.Why, Agent: &draft, Status: "pending",
		ContinuationPrompt: strings.TrimSpace(continuationPrompt), ContinuationLabel: strings.TrimSpace(continuationLabel),
		CreatedAt: now, UpdatedAt: now,
	}
	if err = s.Store.SaveCompanionActionProposal(ctx, proposal); err != nil {
		return nil, nil, err
	}
	return &proposal, hire, nil
}

func boundedTeamName(goal string) string {
	name := "Отряд · " + questTitle(goal)
	runes := []rune(name)
	if len(runes) > 88 {
		return strings.TrimSpace(string(runes[:87])) + "…"
	}
	return name
}

func (s ChatService) prepareTeamAction(ctx context.Context, req ChatRequest, agents []domain.ProjectAgent) (*domain.CompanionActionProposal, []PartyMember, string, error) {
	if pending := s.pendingOwnActionProposal(ctx, req.WorkspaceID, domain.CompanionActionCreateTeam); pending != nil {
		if pending.Team != nil {
			members := s.partyMembers(agents, pending.Team.AgentIDs, req.Message)
			return pending, members, pending.Rationale, nil
		}
	}
	assignment := s.assignParty(ctx, req, agents, nil)
	if len(assignment.AgentIDs) == 0 {
		return nil, nil, "", nil
	}
	members := s.partyMembers(agents, assignment.AgentIDs, req.Message)
	why := chatPartyWhy(req.Config, assignment)
	now := s.now()
	team := domain.Team{
		ID: s.newID("team"), WorkspaceID: req.WorkspaceID, Name: boundedTeamName(req.Message),
		Description: why, AgentIDs: append([]string(nil), assignment.AgentIDs...), CreatedAt: now, UpdatedAt: now,
	}
	proposal := domain.CompanionActionProposal{
		ID: s.newID("hubaction"), WorkspaceID: req.WorkspaceID,
		Kind: domain.CompanionActionCreateTeam, Title: "Создать отряд · " + team.Name,
		Rationale: why, Team: &team, Status: "pending", CreatedAt: now, UpdatedAt: now,
	}
	if err := s.Store.SaveCompanionActionProposal(ctx, proposal); err != nil {
		return nil, nil, "", err
	}
	return &proposal, members, why, nil
}

func (s ChatService) partyMembers(agents []domain.ProjectAgent, ids []string, goal string) []PartyMember {
	byID := make(map[string]domain.ProjectAgent, len(agents))
	for _, agent := range agents {
		byID[agent.ID] = agent
	}
	members := make([]PartyMember, 0, len(ids))
	for _, id := range ids {
		agent, ok := byID[id]
		if !ok {
			continue
		}
		score := scoreAgentForGoal(agent, goal)
		members = append(members, PartyMember{
			AgentID: agent.ID, Name: agent.Name, Role: strings.TrimSpace(agent.RoleDescription),
			Score: &score, Matched: matchedTerms(agent, goal), Blocking: s.blockersFor(agent),
		})
	}
	return members
}
