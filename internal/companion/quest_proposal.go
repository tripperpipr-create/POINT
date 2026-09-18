package companion

import (
	"context"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/orchestrator"
)

func (s Service) ProposeQuest(ctx context.Context, req RecommendRequest) (domain.QuestProposal, error) {
	cfg, err := s.EnsureConfig(ctx, req.WorkspaceID)
	if err != nil {
		return domain.QuestProposal{}, err
	}
	return s.proposeQuest(ctx, req, cfg)
}

func (s Service) proposeQuest(ctx context.Context, req RecommendRequest, cfg domain.CompanionConfig) (domain.QuestProposal, error) {
	agents, err := s.Store.ListProjectAgents(ctx, req.WorkspaceID)
	if err != nil {
		return domain.QuestProposal{}, err
	}
	team := selectTeam(agents, req.Goal+" "+req.Context, 3)
	flows, err := s.Store.ListFlows(ctx, req.WorkspaceID)
	if err != nil {
		return domain.QuestProposal{}, err
	}
	importance := domain.QuestNormal
	lower := strings.ToLower(req.Goal + " " + req.Context)
	if strings.Contains(lower, "critical") || strings.Contains(lower, "prod") || strings.Contains(lower, "security") || strings.Contains(lower, "критич") || strings.Contains(lower, "безопас") {
		importance = domain.QuestCritical
	} else if strings.Contains(lower, "important") || strings.Contains(lower, "review") || strings.Contains(lower, "важн") || strings.Contains(lower, "ревью") {
		importance = domain.QuestImportant
	}
	title := "Новый квест"
	if strings.TrimSpace(req.Goal) != "" {
		title = orchestrator.NormalizeQuestTitle(req.Goal)
	} else {
		title = "Новый квест"
	}
	objectives := decomposeObjectives(req.Goal, cfg.Creativity)
	constraints := []string{"Не писать в live workspace до Apply Change Set"}
	if cfg.RiskTolerance <= 35 {
		constraints = append(constraints, "Сохранить проверяемый rollback path")
	}
	dod := []string{"Изменения проверены", "Change Set применён или отклонён"}
	if cfg.Criticality >= 60 {
		dod = append(dod, "Релевантные тесты и диагностика проходят")
	}
	proposal := domain.QuestProposal{
		ID: domain.NewID("questproposal"), WorkspaceID: req.WorkspaceID, Title: title,
		Rationale: "Собрано из цели пользователя, доступных project agents и текущего состояния проекта.",
		Unknowns:  []string{}, Objectives: objectives, Constraints: constraints, DefinitionOfDone: dod,
		TeamAgentIDs: team, FlowID: selectExistingFlow(flows, req.Goal+" "+req.Context),
		Importance: importance, Status: "pending", CreatedAt: time.Now().UTC(),
	}
	if strings.TrimSpace(req.Context) != "" {
		proposal.Unknowns = append(proposal.Unknowns, "Нужно подтвердить границы контекста: "+trim(req.Context, 120))
	}
	proposal.EstimateTokens = domain.EstimateQuestTokens(proposal)
	if err := s.Store.SaveQuestProposal(ctx, proposal); err != nil {
		return domain.QuestProposal{}, err
	}
	return proposal, nil
}

func emitProgress(fn ProgressFn, step, status string) {
	if fn == nil {
		return
	}
	fn(step, status)
}
