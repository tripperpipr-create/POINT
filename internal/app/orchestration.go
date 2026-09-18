package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/orchestrator"
	"local-agent-workbench/internal/security"
	"local-agent-workbench/internal/textutil"
)

type QuestProposalAction string

const (
	QuestProposalStart  QuestProposalAction = "start"
	QuestProposalModify QuestProposalAction = "modify"
	QuestProposalIgnore QuestProposalAction = "ignore"
)

type QuestProposalDecision struct {
	Brief           *domain.TaskBrief   `json:"brief,omitempty"`
	ExpectedVersion int                 `json:"expectedVersion,omitempty"`
	ApproveVersion  int                 `json:"approveVersion,omitempty"`
	ProposalID      string              `json:"proposalId"`
	Action          QuestProposalAction `json:"action"`
	// Optional overrides when action=modify or start with edits.
	Title            string                 `json:"title,omitempty"`
	Objectives       []string               `json:"objectives,omitempty"`
	Constraints      []string               `json:"constraints,omitempty"`
	DefinitionOfDone []string               `json:"definitionOfDone,omitempty"`
	TeamAgentIDs     []string               `json:"teamAgentIds,omitempty"`
	FlowID           string                 `json:"flowId,omitempty"`
	Importance       domain.QuestImportance `json:"importance,omitempty"`
	StartFlow        bool                   `json:"startFlow"`
	// Transient credential from IDE SecretStorage. It is used only for the
	// separate Orchestrator planning turn and is never persisted.
	OrchestratorAPIKey string `json:"orchestratorApiKey,omitempty"`
}

type QuestProposalResult struct {
	Proposal          domain.QuestProposal `json:"proposal"`
	Quest             *domain.Quest        `json:"quest,omitempty"`
	Team              *domain.Team         `json:"team,omitempty"`
	Flow              *domain.FlowGraph    `json:"flow,omitempty"`
	FlowRun           *domain.FlowRun      `json:"flowRun,omitempty"`
	OrchestratorNote  string               `json:"orchestratorNote,omitempty"`
	OrchestratorMode  string               `json:"orchestratorMode,omitempty"`
	OrchestratorModel string               `json:"orchestratorModel,omitempty"`
	PlannerFallback   string               `json:"plannerFallback,omitempty"`
}

func (a *App) DecideQuestProposal(decision QuestProposalDecision) (QuestProposalResult, error) {
	return a.DecideQuestProposalContext(context.Background(), decision)
}

// DecideQuestProposalContext keeps the model-planning turn attached to the HTTP
// request. If the IDE goes away or its generous deadline is reached, the
// provider request is cancelled instead of finishing a quest behind the user's
// back after the UI has already reported a timeout.
func (a *App) DecideQuestProposalContext(ctx context.Context, decision QuestProposalDecision) (QuestProposalResult, error) {
	unlock := lockTaskProposal(decision.ProposalID)
	defer unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	ws, err := a.requireWorkspace()
	if err != nil {
		return QuestProposalResult{}, err
	}
	proposals, err := a.store.ListQuestProposals(ctx, ws.ID)
	if err != nil {
		return QuestProposalResult{}, err
	}
	var proposal *domain.QuestProposal
	for index := range proposals {
		if proposals[index].ID == decision.ProposalID {
			proposal = &proposals[index]
			break
		}
	}
	if proposal == nil {
		return QuestProposalResult{}, fmt.Errorf("quest proposal %s not found", decision.ProposalID)
	}
	if proposal.Brief != nil && proposal.Status == "started" && decision.Action != QuestProposalStart && decision.Action != QuestProposalRevise {
		return QuestProposalResult{}, errors.New("запущенное задание меняйте через паузу и revise (новая версия + утверждение), не через карточку предложения")
	}
	if decision.Action == QuestProposalRevise {
		if proposal.Status != "started" || proposal.Brief == nil {
			return QuestProposalResult{}, errors.New("revise applies only to a started structured quest")
		}
		quests, listErr := a.store.ListQuests(ctx, ws.ID)
		if listErr != nil {
			return QuestProposalResult{}, listErr
		}
		var questID string
		for _, q := range quests {
			if q.FlowID != "" && proposal.FlowID != "" && q.FlowID == proposal.FlowID {
				questID = q.ID
				break
			}
			if q.Title == proposal.Title && q.Brief != nil {
				questID = q.ID
				break
			}
		}
		if questID == "" {
			return QuestProposalResult{}, errors.New("started quest for revise was not found")
		}
		if decision.Brief == nil {
			return QuestProposalResult{}, errors.New("revise requires a brief")
		}
		quest, reviseErr := a.ReviseActiveQuestBrief(ctx, questID, *decision.Brief, decision.ExpectedVersion, decision.ApproveVersion)
		if reviseErr != nil {
			return QuestProposalResult{}, reviseErr
		}
		proposal.Brief = quest.Brief
		syncProposalBrief(proposal)
		if err = a.store.SaveQuestProposal(ctx, *proposal); err != nil {
			return QuestProposalResult{}, err
		}
		return QuestProposalResult{Proposal: *proposal, Quest: &quest}, nil
	}
	if decision.Action == QuestProposalModify || decision.Action == QuestProposalStart {
		if err = a.validateProposalDecision(ctx, ws.ID, decision); err != nil {
			return QuestProposalResult{}, err
		}
	}
	if decision.Action != QuestProposalIgnore {
		if err = applyTaskBriefDecision(proposal, decision); err != nil {
			return QuestProposalResult{}, err
		}
	}
	switch decision.Action {
	case QuestProposalIgnore:
		proposal.Status = "ignored"
		if err = a.store.SaveQuestProposal(ctx, *proposal); err != nil {
			return QuestProposalResult{}, err
		}
		return QuestProposalResult{Proposal: *proposal}, nil
	case QuestProposalModify:
		applyProposalOverrides(proposal, decision)
		proposal.Status = "modified"
		if err = a.store.SaveQuestProposal(ctx, *proposal); err != nil {
			return QuestProposalResult{}, err
		}
		return QuestProposalResult{Proposal: *proposal}, nil
	case QuestProposalStart:
		// Запущенное предложение второй раз не запускается.
		//
		// Запуск не повторяет старое, а делает второе: создаются свой квест,
		// отряд, Flow и прогон, и те же агенты выходят на ту же задачу, тратя
		// бюджет дважды. Между нажатием и ответом успевает пройти заметное
		// время — ключ оркестратора, решение ядра, bootstrap, старт прогона, —
		// и второе нажатие в этот промежуток обычное человеческое действие.
		// Кнопку прячет и интерфейс, но запрет обязан жить здесь: маршрут
		// открыт всем клиентам, а гонку двух нажатий экран не разрешает.
		if proposal.Status == "started" {
			return QuestProposalResult{}, fmt.Errorf("предложение %q уже запущено — повторный запуск создал бы второй квест", proposal.Title)
		}
		applyProposalOverrides(proposal, decision)
		// Explicit Start is the user review confirmation for quest + FlowRun.
		startFlow := true
		if cfg, ok := a.loadOrchestratorConfig(ws.ID); ok {
			startFlow = orchestrator.ShouldAutoStartFlow(cfg, true)
		}
		return a.startQuestFromProposal(ctx, ws, *proposal, startFlow, proposal.TeamAgentIDsLocked, decision.OrchestratorAPIKey)
	default:
		return QuestProposalResult{}, fmt.Errorf("unknown proposal action %q", decision.Action)
	}
}

func applyProposalOverrides(proposal *domain.QuestProposal, decision QuestProposalDecision) {
	if proposal.Brief != nil {
		if decision.TeamAgentIDs != nil {
			proposal.TeamAgentIDs = append([]string(nil), decision.TeamAgentIDs...)
			proposal.TeamAgentIDsLocked = true
		}
		return
	}
	if title := strings.TrimSpace(decision.Title); title != "" {
		proposal.Title = title
	}
	if decision.Objectives != nil {
		proposal.Objectives = append([]string(nil), decision.Objectives...)
	}
	if decision.Constraints != nil {
		proposal.Constraints = append([]string(nil), decision.Constraints...)
	}
	if !textutil.ContainsFold(proposal.Constraints, "live workspace") && !textutil.ContainsFold(proposal.Constraints, "change set") {
		proposal.Constraints = append(proposal.Constraints, "Не писать в live workspace до Apply Change Set")
	}
	if decision.DefinitionOfDone != nil {
		proposal.DefinitionOfDone = append([]string(nil), decision.DefinitionOfDone...)
	}
	if decision.TeamAgentIDs != nil {
		proposal.TeamAgentIDs = append([]string(nil), decision.TeamAgentIDs...)
		// Пустой явный список означает «вернуть автоподбор». Непустой —
		// зафиксированный человеком состав, который нельзя переиграть при
		// последующем Start без открытого редактора.
		proposal.TeamAgentIDsLocked = len(decision.TeamAgentIDs) > 0
	}
	if flowID := strings.TrimSpace(decision.FlowID); flowID != "" {
		proposal.FlowID = flowID
	}
	if decision.Importance != "" {
		proposal.Importance = decision.Importance
	}
}

func (a *App) validateProposalDecision(ctx context.Context, workspaceID string, decision QuestProposalDecision) error {
	if len(decision.OrchestratorAPIKey) > 64*1024 {
		return errors.New("API-ключ оркестратора превышает 64 КиБ")
	}
	if len([]rune(strings.TrimSpace(decision.Title))) > 160 {
		return errors.New("название квеста превышает 160 символов")
	}
	if decision.Importance != "" && decision.Importance != domain.QuestNormal && decision.Importance != domain.QuestImportant && decision.Importance != domain.QuestCritical {
		return fmt.Errorf("неизвестная важность квеста %q", decision.Importance)
	}
	for label, values := range map[string][]string{
		"целей": decision.Objectives, "ограничений": decision.Constraints, "критериев готовности": decision.DefinitionOfDone,
	} {
		if len(values) > 20 {
			return fmt.Errorf("в квесте слишком много %s: максимум 20", label)
		}
		for _, value := range values {
			if len([]rune(strings.TrimSpace(value))) > 1000 {
				return fmt.Errorf("один из пунктов раздела «%s» превышает 1000 символов", label)
			}
		}
	}
	if len(decision.TeamAgentIDs) > 8 {
		return errors.New("в отряде квеста может быть не больше 8 агентов")
	}
	if len(decision.TeamAgentIDs) > 0 {
		agents, err := a.store.ListProjectAgents(ctx, workspaceID)
		if err != nil {
			return err
		}
		allowed := make(map[string]bool, len(agents))
		for _, agent := range agents {
			allowed[agent.ID] = true
		}
		seen := map[string]bool{}
		for _, agentID := range decision.TeamAgentIDs {
			if !allowed[agentID] {
				return fmt.Errorf("в отряде указан недоступный агент %q", agentID)
			}
			if seen[agentID] {
				return fmt.Errorf("агент %q добавлен в отряд дважды", agentID)
			}
			seen[agentID] = true
		}
	}
	if decision.FlowID != "" {
		flow, err := a.store.GetFlow(ctx, decision.FlowID)
		if err != nil {
			return fmt.Errorf("не удалось загрузить сценарий квеста: %w", err)
		}
		if flow.WorkspaceID != workspaceID {
			return errors.New("сценарий квеста принадлежит другому проекту")
		}
	}
	return nil
}

func flowProjectAgentIDs(flow domain.FlowGraph) []string {
	seen := make(map[string]bool)
	agentIDs := make([]string, 0)
	for _, node := range flow.Nodes {
		if node.Kind != domain.FlowNodeAgent && node.Kind != domain.FlowNodeTool {
			continue
		}
		agentID := strings.TrimSpace(node.AgentID)
		if agentID == "" || seen[agentID] {
			continue
		}
		seen[agentID] = true
		agentIDs = append(agentIDs, agentID)
		fallbackID := strings.TrimSpace(node.FailurePolicy.FallbackAgentID)
		if fallbackID != "" && !seen[fallbackID] {
			seen[fallbackID] = true
			agentIDs = append(agentIDs, fallbackID)
		}
	}
	return agentIDs
}

func plannerFallbackText(err error) string {
	if err == nil {
		return ""
	}
	message := strings.TrimSpace(security.Redact(err.Error()))
	runes := []rune(message)
	if len(runes) > 300 {
		message = string(runes[:300]) + "…"
	}
	return message
}
