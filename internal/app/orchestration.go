package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"local-agent-workbench/internal/agent"
	"local-agent-workbench/internal/changesets"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/flowruntime"
	"local-agent-workbench/internal/orchestrator"
	"local-agent-workbench/internal/policy"
	"local-agent-workbench/internal/sandbox"
	"local-agent-workbench/internal/security"
	"local-agent-workbench/internal/workspace"
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

// CursorExecutionLaunch is the reviewed sandbox task handed to the interactive
// Cursor SDK owned by the IDE process. The local core remains the source of
// truth for execution and Flow state before and after that external turn.
type CursorExecutionLaunch struct {
	Execution   domain.ExecutionInstance `json:"execution"`
	Profile     domain.AgentProfile      `json:"profile"`
	SandboxPath string                   `json:"sandboxPath"`
}

type CursorExecutionCompletion struct {
	Status string `json:"status"`
	Result string `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
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
	if !containsFolded(proposal.Constraints, "live workspace") && !containsFolded(proposal.Constraints, "change set") {
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

func containsFolded(values []string, needle string) bool {
	needle = strings.ToLower(needle)
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), needle) {
			return true
		}
	}
	return false
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

func (a *App) startQuestFromProposal(ctx context.Context, ws domain.Workspace, proposal domain.QuestProposal, startFlow, userPickedTeam bool, orchestratorAPIKey string) (QuestProposalResult, error) {
	return a.startQuestFromProposalUsingQuest(ctx, ws, proposal, startFlow, userPickedTeam, orchestratorAPIKey, nil, true)
}

// startQuestFromProposalUsingQuest lets the v2 approval transaction hand its
// already-created root quest to the proven Flow planner. The root must be
// reused: creating a second legacy quest after one approval would split
// budgets, controls and evidence across two identities.
func (a *App) startQuestFromProposalUsingQuest(ctx context.Context, ws domain.Workspace, proposal domain.QuestProposal, startFlow, userPickedTeam bool, orchestratorAPIKey string, existingQuest *domain.Quest, persistProposal bool) (QuestProposalResult, error) {
	if err := a.validateTaskEnvironment(proposal.Brief); err != nil {
		return QuestProposalResult{}, err
	}
	proposal.Title = orchestrator.NormalizeQuestTitle(proposal.Title)
	now := time.Now().UTC()
	agents, err := a.store.ListProjectAgents(ctx, ws.ID)
	if err != nil {
		return QuestProposalResult{}, err
	}
	runnableAgents, blockedAgents := a.readyProjectAgents(ctx, agents)
	goal := strings.TrimSpace(proposal.Title + " " + proposal.Rationale + " " + strings.Join(proposal.Objectives, " "))
	selectionSignals := a.projectAgentSelectionSignals(ctx, ws.ID)
	selectionBreakdown := append([]domain.AgentSelectionBreakdown(nil), proposal.SelectionBreakdown...)
	cfg, hasOrchestrator := a.loadOrchestratorConfig(ws.ID)
	if proposal.Brief != nil && proposal.Brief.WorkOrder != nil && proposal.Brief.WorkOrder.Routing.Mode == "auto" {
		cfg, err = a.autoRouterConfigV2(ctx, cfg, proposal.Brief.WorkOrder)
		if err != nil {
			return QuestProposalResult{}, fmt.Errorf("approved Auto routing preflight: %w", err)
		}
		hasOrchestrator = true
	}
	orchNote := ""
	orchMode := "deterministic"
	orchModel := ""
	plannerFallback := ""
	agentIDs := proposal.TeamAgentIDs
	availableAgents := make(map[string]bool, len(runnableAgents))
	for _, agent := range agents {
		if capability, blocked := blockedAgents[agent.ID]; blocked {
			if userPickedTeam && containsString(agentIDs, agent.ID) {
				return QuestProposalResult{}, readinessFailure(agent, capability)
			}
			continue
		}
		availableAgents[agent.ID] = true
	}

	var selectedFlow *domain.FlowGraph
	if proposal.FlowID != "" {
		loaded, loadErr := a.store.GetFlow(ctx, proposal.FlowID)
		if loadErr != nil {
			return QuestProposalResult{}, fmt.Errorf("load proposed flow: %w", loadErr)
		}
		if loaded.WorkspaceID != ws.ID {
			return QuestProposalResult{}, errors.New("proposed flow belongs to another workspace")
		}
		selectedFlow = &loaded
		flowAgents := flowProjectAgentIDs(loaded)
		if len(flowAgents) == 0 {
			return QuestProposalResult{}, errors.New("selected flow has no runnable project agents")
		}
		if err = a.requireProjectAgentsReady(ctx, ws.ID, flowAgents); err != nil {
			return QuestProposalResult{}, err
		}
		if userPickedTeam {
			for _, agentID := range flowAgents {
				if !containsString(agentIDs, agentID) {
					return QuestProposalResult{}, fmt.Errorf("selected party is missing flow agent %q", agentID)
				}
			}
			orchNote = "Flow и отряд выбраны пользователем"
		} else {
			agentIDs = flowAgents
			orchNote = "отряд взят из выбранного Flow"
		}
		orchMode = "user-flow"
	}

	importance := proposal.Importance
	if importance == "" {
		importance = domain.QuestNormal
	}
	var draftQuest *domain.Quest
	if existingQuest != nil {
		copy := *existingQuest
		draftQuest = &copy
	}
	var modelPlan *orchestrator.PlanResult
	if proposal.Brief != nil && !userPickedTeam {
		if gap := a.assessAgentGap(ctx, proposal.Brief, permanentProjectAgents(agents), permanentProjectAgents(runnableAgents)); gap != nil {
			if gap.Kind == "missing_subagent" && (!proposal.Brief.Permissions.ProvisionProjectAgents || proposal.Brief.Budget.MaxProjectAgents <= 0) {
				return QuestProposalResult{}, fmt.Errorf("агенту %q нужен временный субагент %q; включите чекбокс «Разрешить временных субагентов под выбранным агентом» в редакторе задания и задайте предел временных агентов больше нуля", gap.Parent.Name, gap.Requirement.Role)
			}
			parent := domain.Quest{
				ID: domain.NewID("quest"), WorkspaceID: ws.ID, Title: proposal.Title, Brief: cloneTaskBrief(proposal.Brief),
				Description: questDescription(proposal), Objectives: append([]string(nil), proposal.Objectives...),
				Constraints: append([]string(nil), proposal.Constraints...), DefinitionOfDone: append([]string(nil), proposal.DefinitionOfDone...),
				Importance: importance, Status: domain.QuestDraft, BudgetTokens: proposal.EstimateTokens,
				CreatedAt: now, UpdatedAt: now,
			}
			if proposal.EstimateCents != nil {
				parent.BudgetCents = *proposal.EstimateCents
			} else {
				parent.BudgetCents = proposal.Brief.Budget.CostCents
			}
			initializeQuestController(&parent)
			if err = a.store.SaveQuest(ctx, parent); err != nil {
				return QuestProposalResult{}, err
			}
			chain, prepErr := a.createAgentPrepChain(ctx, &parent, gap.Requirement)
			if prepErr != nil {
				return QuestProposalResult{}, prepErr
			}
			if chain.State != "ready" {
				proposal.Status = "started"
				if err = a.store.SaveQuestProposal(ctx, proposal); err != nil {
					return QuestProposalResult{}, err
				}
				note := "Основной квест ждёт сопровождаемого пользователем создания агента"
				if gap.Kind == "blocked_primary" {
					note = "Основной квест ждёт донастройки агента: " + strings.Join(gap.Blockers, "; ")
				}
				return QuestProposalResult{Proposal: proposal, Quest: &parent, OrchestratorMode: "waiting_prerequisite", OrchestratorNote: note}, nil
			}
			if gap.Kind == "missing_subagent" && chain.CandidateAgentID != "" {
				proposal.TeamAgentIDs = []string{chain.CandidateAgentID}
				agentIDs = append([]string(nil), proposal.TeamAgentIDs...)
			}
			parent.Status, parent.ControllerState, parent.UpdatedAt = domain.QuestDraft, controllerPlanning, time.Now().UTC()
			if err = a.store.SaveQuest(ctx, parent); err != nil {
				return QuestProposalResult{}, err
			}
			draftQuest = &parent
			agents, err = a.store.ListProjectAgents(ctx, ws.ID)
			if err != nil {
				return QuestProposalResult{}, err
			}
			runnableAgents, blockedAgents = a.readyProjectAgents(ctx, agents)
			availableAgents = make(map[string]bool, len(runnableAgents))
			for _, agent := range runnableAgents {
				if _, blocked := blockedAgents[agent.ID]; !blocked {
					availableAgents[agent.ID] = true
				}
			}
		}
	}
	useModelPlanner := selectedFlow == nil && hasOrchestrator && orchestrator.UsesModelPlanner(cfg) && (proposal.Brief == nil || proposal.Brief.Mode != domain.TaskModePrecise)
	if useModelPlanner {
		// Create the root quest before the planner model call so reservation
		// hits QuestBudgetTokens. On budget failure the quest stays draft.
		if draftQuest == nil {
			quest := domain.Quest{
				ID: domain.NewID("quest"), WorkspaceID: ws.ID, Title: proposal.Title, Brief: cloneTaskBrief(proposal.Brief),
				Description: questDescription(proposal), Objectives: append([]string(nil), proposal.Objectives...),
				Constraints: append([]string(nil), proposal.Constraints...), DefinitionOfDone: append([]string(nil), proposal.DefinitionOfDone...),
				Importance: importance, Status: domain.QuestDraft, BudgetTokens: proposal.EstimateTokens, CreatedAt: now, UpdatedAt: now,
			}
			initializeQuestController(&quest)
			if proposal.EstimateCents != nil {
				quest.BudgetCents = *proposal.EstimateCents
			}
			if err = a.store.SaveQuest(ctx, quest); err != nil {
				return QuestProposalResult{}, err
			}
			draftQuest = &quest
		}
		quest := *draftQuest

		locked := []string(nil)
		if userPickedTeam {
			locked = append(locked, agentIDs...)
		}
		modelCandidates, candidateErr := a.modelCandidates(ctx)
		if candidateErr != nil {
			return QuestProposalResult{}, candidateErr
		}
		planned, planErr := (orchestrator.Planner{NewModel: a.budgetedModelFactory(modelBudgetScope{
			WorkspaceID: ws.ID, QuestID: quest.ID, ProjectAgentID: "master", Outcome: "orchestrator_plan",
		})}).Plan(ctx, orchestrator.PlanRequest{
			Config: cfg, Proposal: proposal, Agents: runnableAgents, LockedAgentIDs: locked, APIKey: orchestratorAPIKey,
			Project: a.masterProjectFacts(ctx), Signals: selectionSignals, ModelCandidates: modelCandidates,
		})
		if planErr == nil {
			modelPlan = &planned
			agentIDs = append([]string(nil), planned.Plan.AgentIDs...)
			selectionBreakdown = orchestrator.AssignPartyWithSignals(cfg, runnableAgents, agentIDs, goal, selectionSignals).Breakdown
			orchMode = "model"
			orchModel = planned.Model
			orchNote = "модель " + planned.Model + " · " + planned.Plan.Rationale
		} else {
			if ctxErr := ctx.Err(); ctxErr != nil {
				if draftQuest != nil {
					if err := a.store.DeleteUnstartedQuest(context.Background(), ws.ID, draftQuest.ID); err != nil {
						slog.Warn("draft quest not removed after cancel", "quest_id", draftQuest.ID, "error", err)
					}
				}
				return QuestProposalResult{}, ctxErr
			}
			if strings.Contains(strings.ToLower(planErr.Error()), "budget blocked") ||
				strings.Contains(strings.ToLower(planErr.Error()), "reserve model budget") ||
				strings.Contains(strings.ToLower(planErr.Error()), "quest token budget") ||
				strings.Contains(strings.ToLower(planErr.Error()), "quest cost budget") {
				return QuestProposalResult{}, planErr
			}
			plannerFallback = plannerFallbackText(planErr)
			orchMode = "model-fallback"
			orchModel = cfg.Model
			if userPickedTeam {
				orchNote = "отряд выбран пользователем · модель недоступна, Flow собран движком Point"
			} else {
				assignment := orchestrator.AssignPartyWithSignals(cfg, runnableAgents, proposal.TeamAgentIDs, goal, selectionSignals)
				agentIDs = assignment.AgentIDs
				selectionBreakdown = assignment.Breakdown
				orchNote = assignment.Reason
			}
		}
	} else if selectedFlow == nil && hasOrchestrator && !userPickedTeam {
		assignment := orchestrator.AssignPartyWithSignals(cfg, runnableAgents, proposal.TeamAgentIDs, goal, selectionSignals)
		agentIDs = assignment.AgentIDs
		selectionBreakdown = assignment.Breakdown
		orchNote = assignment.Reason
		orchMode = assignment.Mode
	} else if selectedFlow == nil && userPickedTeam {
		orchNote = "отряд выбран пользователем"
		orchMode = "user"
	} else if selectedFlow == nil && len(agentIDs) == 0 {
		for i, agent := range runnableAgents {
			if i >= 3 {
				break
			}
			agentIDs = append(agentIDs, agent.ID)
		}
		orchNote = "оркестратор не настроен · первые доступные агенты"
		orchMode = "unconfigured"
	}
	if len(agentIDs) == 0 {
		return QuestProposalResult{}, errors.New("no project agents available for quest party")
	}
	for _, agentID := range agentIDs {
		if !availableAgents[agentID] {
			return QuestProposalResult{}, fmt.Errorf("quest proposal references unavailable project agent %q", agentID)
		}
	}
	// Re-read and re-evaluate immediately before durable orchestration state is
	// created. A proposal preview is not authority to launch a profile that was
	// edited or disconnected in the meantime.
	if err = a.requireProjectAgentsReady(ctx, ws.ID, agentIDs); err != nil {
		return QuestProposalResult{}, err
	}

	if proposal.Brief != nil && proposal.Brief.Mode == domain.TaskModePrecise && len(agentIDs) > 1 {
		agentIDs = agentIDs[:1]
	}
	partyLabel := "Companion-proposed party"
	if hasOrchestrator {
		partyLabel = "Orchestrator-assigned party · " + cfg.Preset
		if orchMode == "model" {
			partyLabel = "Orchestrator model party · " + cfg.Model
		}
		if orchNote != "" {
			partyLabel += " · " + orchNote
		}
	} else if orchNote != "" {
		partyLabel = orchNote
	}
	team := domain.Team{
		ID: domain.NewID("team"), WorkspaceID: ws.ID, Name: questPartyName(proposal.Title),
		Description: partyLabel, AgentIDs: agentIDs, CreatedAt: now, UpdatedAt: now,
	}
	if err = a.store.SaveTeam(ctx, team); err != nil {
		return QuestProposalResult{}, err
	}

	var flow domain.FlowGraph
	var projectGraph *domain.WorkGraph
	if proposal.Brief != nil && proposal.Brief.Mode == domain.TaskModePrecise {
		flow = orchestrator.CompileModelFlow(orchestrator.CompileRequest{Title: proposal.Title}, orchestrator.ModelPlan{AgentIDs: agentIDs, Rationale: "Точное поручение: выполнить заданный результат и остановиться", Stages: []orchestrator.PlanStage{{Name: "Выполнить поручение", AgentID: agentIDs[0], Instruction: proposal.Task, Phase: 1}}}, "bounded-task")
		flow.WorkspaceID = ws.ID
		annotateFlowVerifier(&flow, proposal.Brief)
		if err = a.store.SaveFlow(ctx, flow); err != nil {
			return QuestProposalResult{}, err
		}
	} else if selectedFlow != nil {
		flow = *selectedFlow
	} else if modelPlan != nil {
		flow = orchestrator.CompileModelFlow(orchestrator.CompileRequest{
			Title: proposal.Title, Importance: importance, AgentIDs: agentIDs,
			PlanningDepth: cfg.PlanningDepth, Parallelism: cfg.Parallelism,
			ApprovalStrictness: cfg.ApprovalStrictness, Preset: cfg.Preset,
		}, modelPlan.Plan, modelPlan.Model)
		flow.WorkspaceID = ws.ID
		annotateFlowVerifier(&flow, proposal.Brief)
		if err = a.store.SaveFlow(ctx, flow); err != nil {
			return QuestProposalResult{}, err
		}
	} else if hasOrchestrator {
		flow = orchestrator.CompileFlow(orchestrator.CompileRequest{
			Title: proposal.Title, Importance: importance, AgentIDs: agentIDs,
			PlanningDepth: cfg.PlanningDepth, Parallelism: cfg.Parallelism,
			ApprovalStrictness: cfg.ApprovalStrictness, Preset: cfg.Preset,
		})
		flow.WorkspaceID = ws.ID
		annotateFlowVerifier(&flow, proposal.Brief)
		if err = a.store.SaveFlow(ctx, flow); err != nil {
			return QuestProposalResult{}, err
		}
	} else {
		primary := agentIDs[0]
		reviewer := primary
		if len(agentIDs) > 1 {
			reviewer = agentIDs[1]
		}
		flow = flowruntime.ImportanceTemplate(importance, primary, reviewer)
		flow.WorkspaceID = ws.ID
		flow.Name = string(importance) + " · " + proposal.Title
		flow.Description = "Compiled from quest importance template"
		if err = a.store.SaveFlow(ctx, flow); err != nil {
			return QuestProposalResult{}, err
		}
	}
	if proposal.Brief != nil && proposal.Brief.Mode == domain.TaskModeProject {
		if selectedFlow == nil && modelPlan == nil {
			graph := orchestrator.DefaultProjectWorkGraph(agentIDs)
			compiled, compileErr := orchestrator.CompileWorkGraph(graph, proposal.Title)
			if compileErr != nil {
				return QuestProposalResult{}, compileErr
			}
			compiled.WorkspaceID = ws.ID
			annotateFlowVerifier(&compiled, proposal.Brief)
			if err = a.store.SaveFlow(ctx, compiled); err != nil {
				return QuestProposalResult{}, err
			}
			flow = compiled
			projectGraph = &graph
		} else {
			flow = orchestrator.EnsureProjectPipeline(flow, agentIDs)
			flow.UpdatedAt = time.Now().UTC()
			if err = a.store.SaveFlow(ctx, flow); err != nil {
				return QuestProposalResult{}, err
			}
		}
		if err = a.requireIsolatedProjectWriters(flow); err != nil {
			return QuestProposalResult{}, err
		}
	}
	// WorkOrder v2 remains the authority even though the proven Flow engine is
	// reused underneath it. Apply the approved routing contract after every
	// compiler/pipeline transformation so no legacy template can silently put
	// an agent's default model back into the run.
	if proposal.Brief != nil && proposal.Brief.WorkOrder != nil {
		if err = a.applyApprovedWorkOrderFlowPolicyV2(&flow, proposal.Brief.WorkOrder); err != nil {
			return QuestProposalResult{}, err
		}
		flow.UpdatedAt = time.Now().UTC()
		if err = a.store.SaveFlow(ctx, flow); err != nil {
			return QuestProposalResult{}, err
		}
	}

	// Описание квеста — сама задача. Раньше сюда уходил Rationale, объяснение
	// выбора отряда («пресет conductor · отряд 1 · движком Point»), и оно же
	// попадало в контекст исполняющего агента: он читал, как его выбирали,
	// вместо того что нужно сделать. Обоснование выбора при этом не теряется —
	// у него своё место в описании отряда.
	//
	// Предложения, созданные до появления поля, задачи не несут: для них
	// оставляем прежнее поведение, иначе описание у них станет пустым.
	var quest domain.Quest
	if draftQuest != nil {
		quest = *draftQuest
		quest.Status = domain.QuestActive
		if quest.Controller != nil && quest.Controller["source"] == "work_order_v2" {
			quest.Status = domain.QuestRunning
		}
		quest.TeamID = team.ID
		quest.FlowID = flow.ID
		quest.UpdatedAt = now
	} else {
		quest = domain.Quest{
			ID: domain.NewID("quest"), WorkspaceID: ws.ID, Title: proposal.Title, Brief: cloneTaskBrief(proposal.Brief),
			Description: questDescription(proposal), Objectives: append([]string(nil), proposal.Objectives...),
			Constraints:      append([]string(nil), proposal.Constraints...),
			DefinitionOfDone: append([]string(nil), proposal.DefinitionOfDone...),
			Importance:       importance, Status: domain.QuestActive, TeamID: team.ID, FlowID: flow.ID,
			BudgetTokens: proposal.EstimateTokens, CreatedAt: now, UpdatedAt: now,
		}
		initializeQuestController(&quest)
		if proposal.EstimateCents != nil {
			quest.BudgetCents = *proposal.EstimateCents
		}
	}
	if modelPlan != nil {
		if quest.Controller == nil {
			quest.Controller = map[string]any{}
		}
		quest.Controller["planPreview"] = planStagePreview(modelPlan.Plan.Stages)
	}
	if projectGraph != nil {
		if quest.Controller == nil {
			quest.Controller = map[string]any{}
		}
		quest.Controller["workGraph"] = *projectGraph
	}
	if err = a.store.SaveQuest(ctx, quest); err != nil {
		return QuestProposalResult{}, err
	}
	// Planner usage is written once by budget reconcile (outcome orchestrator_plan).
	proposal.Status = "started"
	proposal.SelectionBreakdown = selectionBreakdown
	if modelPlan != nil {
		proposal.PlanPreview = planStagePreview(modelPlan.Plan.Stages)
	}
	proposal.FlowID = flow.ID
	proposal.TeamAgentIDs = agentIDs
	if persistProposal {
		if err = a.store.SaveQuestProposal(ctx, proposal); err != nil {
			return QuestProposalResult{}, err
		}
	}

	if orchNote == "" {
		orchNote = partyLabel
	}
	result := QuestProposalResult{
		Proposal: proposal, Quest: &quest, Team: &team, Flow: &flow,
		OrchestratorNote: orchNote, OrchestratorMode: orchMode,
		OrchestratorModel: orchModel, PlannerFallback: plannerFallback,
	}
	if startFlow {
		runtime := flowruntime.Runtime{Store: a.store}
		flowRun, runErr := runtime.Start(ctx, flowruntime.StartRequest{
			FlowID: flow.ID, WorkspaceID: ws.ID, QuestID: quest.ID,
			Input: map[string]any{
				"title": quest.Title, "objectives": quest.Objectives, "importance": quest.Importance,
			},
		})
		if runErr != nil {
			return result, runErr
		}
		if _, childErr := a.ensureFlowNodeQuests(quest, flow, flowRun); childErr != nil {
			return result, childErr
		}
		quest.FlowRunID = flowRun.ID
		quest.UpdatedAt = time.Now().UTC()
		if saveErr := a.store.SaveQuest(ctx, quest); saveErr != nil {
			return result, saveErr
		}
		result.Quest = &quest
		result.FlowRun = &flowRun
		if schedErr := a.scheduleFlowAgentExecutions(quest, flow, flowRun, orchestratorAPIKey); schedErr != nil {
			return result, schedErr
		}
	}
	return result, nil
}

func planStagePreview(stages []orchestrator.PlanStage) []domain.PlanStagePreview {
	out := make([]domain.PlanStagePreview, 0, len(stages))
	for _, stage := range stages {
		out = append(out, domain.PlanStagePreview{
			Name: stage.Name, AgentID: stage.AgentID, Runtime: stage.Runtime, Model: stage.Model,
			ConnectionID: stage.ConnectionID, EstimatedCostCents: stage.EstimatedCostCents,
			OwnedPaths: append([]string(nil), stage.OwnedPaths...), CriterionIDs: append([]string(nil), stage.CriterionIDs...),
			MergePlan: stage.MergePlan, Reason: stage.ModelReason,
		})
	}
	return out
}

func questPartyName(title string) string {
	name := "Отряд · " + orchestrator.NormalizeQuestTitle(title)
	runes := []rune(name)
	if len(runes) <= 88 {
		return name
	}
	return strings.TrimSpace(string(runes[:87])) + "…"
}

func (a *App) scheduleFlowAgentExecutions(quest domain.Quest, flow domain.FlowGraph, flowRun domain.FlowRun, apiKey string) error {
	a.rememberFlowOrchestratorKey(flowRun.ID, apiKey)
	return a.scheduleWaitingAgentNodes(quest, flow, flowRun, apiKey)
}

func (a *App) rememberFlowOrchestratorKey(flowRunID, apiKey string) {
	flowRunID = strings.TrimSpace(flowRunID)
	apiKey = strings.TrimSpace(apiKey)
	if flowRunID == "" || apiKey == "" {
		return
	}
	a.flowOrchestratorKeysMu.Lock()
	defer a.flowOrchestratorKeysMu.Unlock()
	if a.flowOrchestratorKeys == nil {
		a.flowOrchestratorKeys = map[string]string{}
	}
	a.flowOrchestratorKeys[flowRunID] = apiKey
}

func (a *App) flowOrchestratorKey(flowRunID string) string {
	flowRunID = strings.TrimSpace(flowRunID)
	if flowRunID == "" {
		return ""
	}
	a.flowOrchestratorKeysMu.Lock()
	defer a.flowOrchestratorKeysMu.Unlock()
	return a.flowOrchestratorKeys[flowRunID]
}

func (a *App) clearFlowOrchestratorKey(flowRunID string) {
	flowRunID = strings.TrimSpace(flowRunID)
	if flowRunID == "" {
		return
	}
	a.flowOrchestratorKeysMu.Lock()
	defer a.flowOrchestratorKeysMu.Unlock()
	delete(a.flowOrchestratorKeys, flowRunID)
}

// ResumeActiveFlowRuns restores persisted orchestration after the local core restarts.
func (a *App) ResumeActiveFlowRuns(workspaceID string) error {
	ctx := context.Background()
	runs, err := a.store.ListFlowRuns(ctx, workspaceID, 500)
	if err != nil {
		return err
	}
	executions, err := a.store.ListExecutions(ctx, workspaceID, 500)
	if err != nil {
		return err
	}
	executionByID := make(map[string]domain.ExecutionInstance, len(executions))
	for _, execution := range executions {
		executionByID[execution.ID] = execution
	}
	runtime := flowruntime.Runtime{Store: a.store}
	for _, flowRun := range runs {
		if flowRun.Status != domain.RunRunning && flowRun.Status != domain.RunWaiting {
			continue
		}
		current := flowRun
		for nodeID, state := range current.NodeStates {
			if state.Status != "waiting_agent" {
				continue
			}
			executionID, _ := state.Output["executionId"].(string)
			execution := executionByID[executionID]
			if execution.ID == "" {
				continue
			}
			if execution.Status == domain.RunRunning && !a.engine.IsActiveRun(execution.RunID) {
				execution.Status = domain.RunInterrupted
				execution.Error = "local core restarted; execution is ready to resume"
				if saveErr := a.store.SaveExecution(ctx, execution); saveErr != nil {
					return saveErr
				}
				executionByID[execution.ID] = execution
			}
			switch execution.Status {
			case domain.RunCompleted, domain.RunFailed, domain.RunCancelled:
				if execution.Status == domain.RunFailed {
					if recoveredRun, recovered, recoveryErr := a.recoverFlowNodeFailure(current.ID, nodeID, execution); recoveryErr != nil {
						return recoveryErr
					} else if recovered {
						current = recoveredRun
						continue
					}
				}
				childStatus := domain.QuestFailed
				if execution.Status == domain.RunCompleted {
					childStatus = domain.QuestCompleted
				} else if execution.Status == domain.RunCancelled {
					childStatus = domain.QuestCancelled
				}
				a.setFlowChildQuestStatus(current.ID, nodeID, childStatus)
				output := map[string]any{
					"runId": execution.RunID, "result": execution.Result, "status": execution.Status,
				}
				a.attachCompletionEvidence(ctx, execution.RunID, output)
				current, err = runtime.CompleteAgentNode(ctx, current.ID, nodeID, execution.Status == domain.RunCompleted, output)
				if err != nil {
					return err
				}
			}
		}
		current, err = runtime.Tick(ctx, current.ID)
		if err != nil {
			return err
		}
		if current.Status == domain.RunRunning || current.Status == domain.RunWaiting {
			if err = a.scheduleFlowAgentExecutionsFromRun(current); err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *App) scheduleFlowAgentExecutionsFromRun(flowRun domain.FlowRun) error {
	if flowRun.Status == domain.RunFailed || flowRun.Status == domain.RunCancelled || flowRun.Status == domain.RunCompleted {
		a.clearFlowOrchestratorKey(flowRun.ID)
		return nil
	}
	flow, ok, err := flowruntime.FlowFromSnapshot(flowRun)
	if err != nil {
		return err
	}
	if !ok {
		flow, err = a.store.GetFlow(context.Background(), flowRun.FlowID)
		if err != nil {
			return err
		}
	}
	quest := domain.Quest{ID: flowRun.QuestID, Title: flow.Name}
	if flowRun.QuestID != "" {
		quests, qErr := a.store.ListQuests(context.Background(), flowRun.WorkspaceID)
		if qErr == nil {
			for _, item := range quests {
				if item.ID == flowRun.QuestID {
					quest = item
					break
				}
			}
		}
	}
	return a.scheduleFlowAgentExecutions(quest, flow, flowRun, a.flowOrchestratorKey(flowRun.ID))
}

// rootFlowSeedPath returns the immutable starting snapshot of the earliest
// root execution in this FlowRun. Every root branch is forked from that same
// snapshot, even when parallelism limits schedule the branches at different
// times or the user edits the live workspace between them.
func (a *App) rootFlowSeedPath(flowRun domain.FlowRun) string {
	executions, err := a.store.ListExecutions(context.Background(), flowRun.WorkspaceID, 500)
	if err != nil {
		return ""
	}
	var root *domain.ExecutionInstance
	var rootRecord domain.SandboxRecord
	for index := range executions {
		execution := &executions[index]
		if execution.FlowRunID != flowRun.ID {
			continue
		}
		record, loadErr := a.store.GetSandbox(context.Background(), execution.SandboxID)
		if loadErr != nil || len(sandboxParentExecutionIDs(record)) > 0 {
			continue
		}
		if root == nil || execution.StartedAt.Before(root.StartedAt) || (execution.StartedAt.Equal(root.StartedAt) && execution.ID < root.ID) {
			root = execution
			rootRecord = record
		}
	}
	if root == nil {
		return ""
	}
	path := strings.TrimSpace(rootRecord.BaselinePath)
	if info, statErr := os.Stat(path); statErr == nil && info.IsDir() {
		return path
	}
	return ""
}

// Отмена интерактивного исполнения хранится только в записи: по ней позднее
// завершение SDK распознаётся как уже отменённое. Незаписанная отмена
// возвращает исполнение к жизни задним числом.
func (a *App) saveCascadeCancellation(exec domain.ExecutionInstance) {
	if err := a.store.SaveExecution(context.Background(), exec); err != nil {
		slog.Warn("cascade cancellation not persisted", "execution_id", exec.ID, "quest_id", exec.QuestID, "error", err)
	}
}

// Причина ожидания узла живёт только в записи прогона: интерфейс читает
// waitReason оттуда и по нему объясняет человеку, почему агент ещё не начал.
// Отказ записи означает узел, «висящий без причины», — молчать здесь нельзя.
func (a *App) saveFlowRunWaitReason(flowRun domain.FlowRun, nodeID, reason string) {
	if err := a.store.SaveFlowRun(context.Background(), flowRun); err != nil {
		slog.Warn("flow node wait reason not persisted", "flow_run_id", flowRun.ID, "flow_node_id", nodeID, "wait_reason", reason, "error", err)
	}
}

func (a *App) scheduleWaitingAgentNodes(quest domain.Quest, flow domain.FlowGraph, flowRun domain.FlowRun, apiKey string) error {
	if flowRun.Status == domain.RunFailed || flowRun.Status == domain.RunCancelled || flowRun.Status == domain.RunCompleted {
		return nil
	}
	maxConcurrent := 4
	if cfg, ok := a.loadOrchestratorConfig(flowRun.WorkspaceID); ok {
		maxConcurrent = orchestrator.MaxConcurrentAgents(cfg)
	}
	if quest.Brief != nil && quest.Brief.Budget.MaxParallel < maxConcurrent {
		maxConcurrent = quest.Brief.Budget.MaxParallel
	}
	changeSets, _ := a.store.ListChangeSets(context.Background(), flowRun.WorkspaceID)
	childQuests, err := a.ensureFlowNodeQuests(quest, flow, flowRun)
	if err != nil {
		return err
	}
	runningOrStarting := 0
	for _, node := range flow.Nodes {
		if node.Kind != domain.FlowNodeAgent && node.Kind != domain.FlowNodeTool {
			continue
		}
		state := flowRun.NodeStates[node.ID]
		if state.Status != "waiting_agent" {
			continue
		}
		if executionID, _ := state.Output["executionId"].(string); executionID != "" {
			if exec, err := a.findExecution(executionID); err == nil && exec.Status == domain.RunRunning {
				runningOrStarting++
			}
		}
	}

	seen := map[string]bool{}
	for _, node := range flow.Nodes {
		if node.Kind != domain.FlowNodeAgent && node.Kind != domain.FlowNodeTool {
			continue
		}
		state := flowRun.NodeStates[node.ID]
		if state.Status != "waiting_agent" {
			continue
		}
		effectiveAgentID := node.AgentID
		if candidate, _ := state.Output["effectiveAgentId"].(string); strings.TrimSpace(candidate) != "" {
			effectiveAgentID = strings.TrimSpace(candidate)
		}
		if effectiveAgentID == "" {
			runtime := flowruntime.Runtime{Store: a.store}
			_, _ = runtime.CompleteAgentNode(context.Background(), flowRun.ID, node.ID, false, map[string]any{
				"error": "agentId is required for agent and tool nodes",
			})
			continue
		}
		needsSchedule, _ := state.Output["needsSchedule"].(bool)
		key := node.ID + ":" + effectiveAgentID
		if seen[key] {
			continue
		}
		seen[key] = true
		task := quest.Title
		if node.Name != "" {
			task = node.Name + ": " + quest.Title
		}
		if instruction, ok := node.Config["instruction"].(string); ok && strings.TrimSpace(instruction) != "" {
			task = strings.TrimSpace(instruction)
		} else if inst := orchestrator.DefaultStageInstruction(domain.FlowNodeStageRole(node)); inst != "" {
			task = inst
		}
		if role := domain.FlowNodeStageRole(node); role == domain.StageRoleImplement && quest.Title != "" && !strings.Contains(task, quest.Title) {
			task += "\n\nQuest goal: " + quest.Title
		}
		contract, contractErr := workContractFromNode(node)
		if contractErr != nil {
			return fmt.Errorf("flow node %s work contract: %w", node.ID, contractErr)
		}
		task += workContractInstructions(contract)
		projectAgent, agentErr := a.store.GetProjectAgent(context.Background(), effectiveAgentID)
		if agentErr != nil {
			return agentErr
		}
		if node.Kind == domain.FlowNodeTool {
			if strings.TrimSpace(node.ToolName) == "" || !policy.ProfileGrants(domain.ProfileFromProjectAgent(projectAgent)).Allows(node.ToolName) {
				runtime := flowruntime.Runtime{Store: a.store}
				_, _ = runtime.CompleteAgentNode(context.Background(), flowRun.ID, node.ID, false, map[string]any{
					"error": "tool node requires an allowed tool", "toolName": node.ToolName,
				})
				continue
			}
			updated, executeErr := a.executeDeterministicFlowTool(quest, flow, flowRun, node, projectAgent)
			if executeErr != nil {
				return executeErr
			}
			return a.scheduleFlowAgentExecutionsFromRun(updated)
		}
		childQuest := childQuests[node.ID]
		executionQuestID := quest.ID
		if childQuest.ID != "" {
			executionQuestID = childQuest.ID
			a.setFlowChildQuestStatus(flowRun.ID, node.ID, domain.QuestActive)
		}
		var exec domain.ExecutionInstance
		executionID, _ := state.Output["executionId"].(string)
		if executionID != "" {
			exec, _ = a.findExecution(executionID)
			if exec.Status == domain.RunRunning {
				continue
			}
			if exec.ID != "" && exec.Status != domain.RunPending && exec.Status != domain.RunInterrupted {
				continue
			}
		}
		if !reviewReadsIntegratedRevision(quest, node) {
			if state.Output == nil {
				state.Output = map[string]any{}
			}
			state.Output["waitReason"] = "await_integrated_revision"
			flowRun.NodeStates[node.ID] = state
			a.saveFlowRunWaitReason(flowRun, node.ID, "await_integrated_revision")
			continue
		}
		if domain.FlowNodeWriteFiles(node) && writerRootBusy(flow, flowRun, node) {
			if state.Output == nil {
				state.Output = map[string]any{}
			}
			state.Output["waitReason"] = "serial_writer_root"
			flowRun.NodeStates[node.ID] = state
			a.saveFlowRunWaitReason(flowRun, node.ID, "serial_writer_root")
			continue
		}
		if runningOrStarting >= maxConcurrent {
			if state.Output == nil {
				state.Output = map[string]any{}
			}
			state.Output["waitReason"] = "parallelism_limit"
			state.Output["maxConcurrent"] = maxConcurrent
			flowRun.NodeStates[node.ID] = state
			a.saveFlowRunWaitReason(flowRun, node.ID, "parallelism_limit")
			continue
		}
		if exec.ID == "" {
			if !needsSchedule && state.Output["executionId"] != nil {
				continue
			}
			upstreamExecutions := completedUpstreamExecutionIDs(flow, flowRun, node.ID)
			parentExecutionID := ""
			if len(upstreamExecutions) == 1 {
				parentExecutionID = upstreamExecutions[0]
			}
			var created domain.ExecutionInstance
			var createErr error
			var mergeSet *domain.ChangeSet
			if len(upstreamExecutions) > 1 {
				if waitReason, _ := state.Output["waitReason"].(string); waitReason == "sandbox_merge_conflict" {
					continue
				}
				var merged sandbox.MergeResult
				created, merged, mergeSet, createErr = a.startMergedSandboxedExecution(
					effectiveAgentID, task, executionQuestID, flowRun.ID, node.ID, upstreamExecutions, mergeResolutionsFromOutput(state.Output),
				)
				if createErr == nil && len(merged.Conflicts) > 0 {
					state.Output["needsSchedule"] = true
					state.Output["waitReason"] = "sandbox_merge_conflict"
					state.Output["sandboxLineage"] = "merge_conflict"
					state.Output["upstreamExecutionIds"] = append([]string(nil), upstreamExecutions...)
					state.Output["mergeConflicts"] = merged.Conflicts
					state.Output["mergeConflictCount"] = len(merged.Conflicts)
					flowRun.NodeStates[node.ID] = state
					if saveErr := a.store.SaveFlowRun(context.Background(), flowRun); saveErr != nil {
						return saveErr
					}
					a.setProjectControllerState(context.Background(), quest.ID, controllerNeedsUser)
					continue
				}
				a.setProjectControllerState(context.Background(), quest.ID, controllerWaitingMerge)
			} else {
				rootSeedPath := ""
				if parentExecutionID == "" {
					rootSeedPath = a.rootFlowSeedPath(flowRun)
				}
				created, createErr = a.startSandboxedExecutionWithSeed(effectiveAgentID, task, executionQuestID, parentExecutionID, rootSeedPath)
			}
			if createErr != nil {
				return createErr
			}
			exec = created
			exec.FlowRunID = flowRun.ID
			exec.FlowNodeID = node.ID
			if saveErr := a.store.SaveExecution(context.Background(), exec); saveErr != nil {
				return saveErr
			}
			state.Output["needsSchedule"] = false
			state.Output["executionId"] = exec.ID
			state.Output["effectiveAgentId"] = effectiveAgentID
			state.Output["waitReason"] = ""
			state.Output["handoffFrom"] = upstreamHandoffSummary(flow, flowRun, node.ID)
			if parentExecutionID != "" {
				state.Output["sandboxLineage"] = "inherited"
				state.Output["seedExecutionId"] = parentExecutionID
			} else if len(upstreamExecutions) > 1 {
				state.Output["sandboxLineage"] = "merged_parallel_join"
				state.Output["upstreamExecutionIds"] = append([]string(nil), upstreamExecutions...)
				delete(state.Output, "mergeConflicts")
				state.Output["mergeConflictCount"] = 0
				if mergeSet != nil {
					state.Output["mergeChangeSetId"] = mergeSet.ID
					state.Output["mergedChangeSetIds"] = append([]string(nil), mergeSet.Supersedes...)
					state.Output["mergedResultVerified"] = true
				}
			} else {
				state.Output["sandboxLineage"] = "fresh"
			}
			flowRun.NodeStates[node.ID] = state
			if saveErr := a.store.SaveFlowRun(context.Background(), flowRun); saveErr != nil {
				return saveErr
			}
		}

		role := domain.FlowNodeStageRole(node)
		lineage, _ := state.Output["sandboxLineage"].(string)
		if stageAllowsLLMBypass(role, lineage) {
			updated, passErr := a.completeStagePassthrough(flowRun, node, exec,
				"serial inherited tip already integrated; skipped redundant "+role+" LLM stage")
			if passErr != nil {
				return passErr
			}
			return a.continueAfterDeterministicStage(updated)
		}
		if stageUsesDeterministicBootstrap(role) {
			updated, bootErr := a.completeDeterministicBootstrap(flowRun, node, exec, projectAgent)
			if bootErr != nil {
				return bootErr
			}
			return a.continueAfterDeterministicStage(updated)
		}
		if role == domain.StageRoleAccept {
			if handled, updated, acceptErr := a.tryDeterministicAccept(quest, flowRun, node, exec, projectAgent); acceptErr != nil {
				return acceptErr
			} else if handled {
				return a.continueAfterDeterministicStage(updated)
			}
		}

		// Cursor without a local MCP address still uses the interactive IDE
		// adapter. Headless Cursor/Codex/Claude share the ordinary StartRun path.
		if projectAgent.Provider == domain.ProviderCursor && strings.TrimSpace(a.SelfURL()) == "" {
			if state.Output == nil {
				state.Output = map[string]any{}
			}
			state.Output["waitReason"] = "waiting_interactive_cursor"
			delete(state.Output, "startError")
			flowRun.NodeStates[node.ID] = state
			a.saveFlowRunWaitReason(flowRun, node.ID, "waiting_interactive_cursor")
			continue
		}

		// Auto-start local/no-key providers; otherwise leave pending for Launch with API key.
		if providerNeedsAPIKey(projectAgent.Provider, projectAgent.ProviderPreset) && strings.TrimSpace(apiKey) == "" {
			if state.Output == nil {
				state.Output = map[string]any{}
			}
			state.Output["waitReason"] = "waiting_api_key"
			flowRun.NodeStates[node.ID] = state
			a.saveFlowRunWaitReason(flowRun, node.ID, "waiting_api_key")
			slog.Warn("flow agent waiting for API key", "flow_run_id", flowRun.ID, "flow_node_id", node.ID, "execution_id", exec.ID, "provider", projectAgent.Provider)
			continue
		}
		checkKind := ""
		if lineage, _ := state.Output["sandboxLineage"].(string); lineage == "merged_parallel_join" {
			checkKind = "merged-result"
			a.syncIntakeStatus(context.Background(), flowRun.QuestID, domain.IntakeIntegrating, "")
		}
		modelBinding, bindingErr := modelBindingFromNode(node)
		if bindingErr != nil {
			return fmt.Errorf("flow node %s model binding: %w", node.ID, bindingErr)
		}
		_, startErr := a.StartRun(StartRunRequest{
			ProfileID: effectiveAgentID, Task: task, APIKey: apiKey,
			QuestID: executionQuestID, FlowRunID: flowRun.ID, FlowNodeID: node.ID, ExecutionID: exec.ID,
			StageRole:           domain.FlowNodeStageRole(node),
			CompletionCheckKind: checkKind,
			ContextItems:        flowNodeContext(quest, flow, flowRun, node.ID, changeSets),
			ModelBinding:        modelBinding,
			WorkContract:        &contract,
		})
		if startErr != nil {
			if state.Output == nil {
				state.Output = map[string]any{}
			}
			state.Output["waitReason"] = "start_failed"
			state.Output["startError"] = startErr.Error()
			flowRun.NodeStates[node.ID] = state
			a.saveFlowRunWaitReason(flowRun, node.ID, "start_failed")
			slog.Warn("flow agent start failed", "flow_run_id", flowRun.ID, "flow_node_id", node.ID, "execution_id", exec.ID, "agent_id", effectiveAgentID, "error", startErr)
			continue
		}
		if checkKind == "merged-result" || node.Kind == domain.FlowNodeVerifier {
			a.syncIntakeStatus(context.Background(), flowRun.QuestID, domain.IntakeVerifying, "")
		}
		runningOrStarting++
	}
	a.syncWorkOrderQuestStallV2(flowRun.ID)
	return nil
}

// syncWorkOrderQuestStallV2 доводит до карточки то, что увидел планировщик.
//
// Планировщик оставляет узел в waiting_agent и идёт дальше: ключа нет, запуск
// не состоялся, песочница разошлась. Статус квеста при этом никто не трогал —
// он пересчитывался только на запуске наряда. После перезапуска ядра и ручного
// «Продолжить» квест оставался `running` у работы, которая стоит: карточка
// писала «Квест выполняется», причины не называла, а кнопки возобновления не
// давала, потому что `running` не возобновляем. Выхода из этого не было вовсе.
//
// Место одно на всех вызывающих: планировщик — единственный, кто оставляет
// узлы ждать, и пересчёт принадлежит ему, а не каждому из четырнадцати мест,
// откуда его зовут.
func (a *App) syncWorkOrderQuestStallV2(flowRunID string) {
	ctx := context.Background()
	flowRun, err := a.store.GetFlowRun(ctx, flowRunID)
	if err != nil || strings.TrimSpace(flowRun.QuestID) == "" {
		return
	}
	status, message, _ := workOrderLaunchOutcomeV2(&flowRun, "")
	if status == domain.QuestRunning {
		return
	}
	// Наряд v2 узнаётся по утверждению: у остальных квестов свой жизненный цикл,
	// и трогать их статус отсюда нельзя.
	approval, approvalErr := a.store.WorkOrderApprovalByQuestV2(ctx, flowRun.QuestID)
	if approvalErr != nil {
		return
	}
	quest, questErr := a.workOrderQuestV2(ctx, approval.WorkOrder.WorkspaceID, flowRun.QuestID)
	if questErr != nil || quest.Status == status {
		return
	}
	// Решение человека старше наблюдения: паузу и отмену пересчёт не отменяет.
	switch quest.Status {
	case domain.QuestPreflight, domain.QuestRunning, domain.QuestVerifying, domain.QuestApplying:
	default:
		return
	}
	if _, saveErr := a.setWorkOrderQuestStatusV2(ctx, quest, status, message); saveErr != nil {
		slog.Warn("work order stall status not persisted", "quest_id", flowRun.QuestID, "status", status, "error", saveErr)
		return
	}
	slog.Info("work order quest stalled", "quest_id", flowRun.QuestID, "flow_run_id", flowRun.ID, "status", status)
}

func (a *App) executeDeterministicFlowTool(quest domain.Quest, flow domain.FlowGraph, flowRun domain.FlowRun, node domain.FlowNode, projectAgent domain.ProjectAgent) (domain.FlowRun, error) {
	profile := domain.ProfileFromProjectAgent(projectAgent)
	if err := a.enrichProjectAgentForRun(flowRun.WorkspaceID, projectAgent, &profile, nil); err != nil {
		return domain.FlowRun{}, err
	}
	decision := policy.Engine{TrustedCustomTool: a.trustedCustomTool}.Evaluate(profile, node.ToolName)
	if decision.Risk != domain.ToolRiskLow || decision.RequiresApproval || decision.Policy != domain.ToolPolicyAllow {
		return domain.FlowRun{}, fmt.Errorf("tool node %q cannot execute %q deterministically: only explicitly allowed low-risk tools are supported", node.ID, node.ToolName)
	}
	upstreamExecutions := completedUpstreamExecutionIDs(flow, flowRun, node.ID)
	parentExecutionID := ""
	if len(upstreamExecutions) == 1 {
		parentExecutionID = upstreamExecutions[0]
	}
	state := flowRun.NodeStates[node.ID]
	if waitReason, _ := state.Output["waitReason"].(string); waitReason == "sandbox_merge_conflict" {
		return flowRun, nil
	}
	var exec domain.ExecutionInstance
	var err error
	var mergeSet *domain.ChangeSet
	if len(upstreamExecutions) > 1 {
		var merged sandbox.MergeResult
		exec, merged, mergeSet, err = a.startMergedSandboxedExecution(
			projectAgent.ID, "Tool: "+node.ToolName, quest.ID, flowRun.ID, node.ID,
			upstreamExecutions, mergeResolutionsFromOutput(state.Output),
		)
		if err == nil && len(merged.Conflicts) > 0 {
			state.Output["needsSchedule"] = true
			state.Output["waitReason"] = "sandbox_merge_conflict"
			state.Output["sandboxLineage"] = "merge_conflict"
			state.Output["upstreamExecutionIds"] = append([]string(nil), upstreamExecutions...)
			state.Output["mergeConflicts"] = merged.Conflicts
			state.Output["mergeConflictCount"] = len(merged.Conflicts)
			flowRun.NodeStates[node.ID] = state
			a.syncIntakeStatus(context.Background(), quest.ID, domain.IntakeIntegrating, "merge conflict requires integration agent")
			if saveErr := a.store.SaveFlowRun(context.Background(), flowRun); saveErr != nil {
				return domain.FlowRun{}, saveErr
			}
			return flowRun, nil
		}
	} else {
		rootSeedPath := ""
		if parentExecutionID == "" {
			rootSeedPath = a.rootFlowSeedPath(flowRun)
		}
		exec, err = a.startSandboxedExecutionWithSeed(projectAgent.ID, "Tool: "+node.ToolName, quest.ID, parentExecutionID, rootSeedPath)
	}
	if err != nil {
		return domain.FlowRun{}, err
	}
	exec.FlowRunID = flowRun.ID
	exec.FlowNodeID = node.ID
	exec.Status = domain.RunRunning
	if err = a.store.SaveExecution(context.Background(), exec); err != nil {
		return domain.FlowRun{}, err
	}
	sandboxRecord, err := a.store.GetSandbox(context.Background(), exec.SandboxID)
	if err != nil {
		return domain.FlowRun{}, err
	}
	sandboxFS, err := workspace.Open(sandboxRecord.Path)
	if err != nil {
		return domain.FlowRun{}, err
	}
	customTools, err := a.store.ListCustomTools(context.Background())
	if err != nil {
		return domain.FlowRun{}, err
	}
	registry, _ := agent.BuildToolRegistryWithSources(sandboxFS, customTools, serverProfileBridge{app: a}, a.dbToolAccess(), profile)
	tool, ok := registry.Get(node.ToolName)
	if !ok {
		return domain.FlowRun{}, fmt.Errorf("tool node %q references unavailable tool %q", node.ID, node.ToolName)
	}
	arguments := node.Config["arguments"]
	if arguments == nil {
		arguments = map[string]any{}
	}
	raw, err := json.Marshal(arguments)
	if err != nil {
		return domain.FlowRun{}, fmt.Errorf("encode tool node %q arguments: %w", node.ID, err)
	}
	timeout := time.Duration(projectAgent.MaxDurationSeconds) * time.Second
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	result := tool.Execute(ctx, raw)
	cancel()
	encoded, _ := json.Marshal(result)
	now := time.Now().UTC()
	exec.FinishedAt = &now
	exec.DurationMs = now.Sub(exec.StartedAt).Milliseconds()
	exec.Result = string(encoded)
	exec.Status = domain.RunCompleted
	if !result.OK {
		exec.Status = domain.RunFailed
		if result.Error != nil {
			exec.Error = result.Error.Message
		}
	}
	if err = a.store.SaveExecution(context.Background(), exec); err != nil {
		return domain.FlowRun{}, err
	}
	runtime := flowruntime.Runtime{Store: a.store}
	output := map[string]any{"toolName": node.ToolName, "result": result, "executionId": exec.ID}
	if parentExecutionID != "" {
		output["sandboxLineage"] = "inherited"
		output["seedExecutionId"] = parentExecutionID
	} else if len(upstreamExecutions) > 1 {
		output["sandboxLineage"] = "merged_parallel_join"
		output["upstreamExecutionIds"] = append([]string(nil), upstreamExecutions...)
		if mergeSet != nil {
			output["mergeChangeSetId"] = mergeSet.ID
			output["mergedChangeSetIds"] = append([]string(nil), mergeSet.Supersedes...)
			output["mergedResultVerified"] = true
		}
	} else {
		output["sandboxLineage"] = "fresh"
	}
	return runtime.CompleteAgentNode(context.Background(), flowRun.ID, node.ID, result.OK, output)
}

func completedUpstreamAgentNodeIDs(flow domain.FlowGraph, flowRun domain.FlowRun, nodeID string) []string {
	nodeByID := make(map[string]domain.FlowNode, len(flow.Nodes))
	incoming := make(map[string][]string, len(flow.Nodes))
	for _, node := range flow.Nodes {
		nodeByID[node.ID] = node
	}
	for _, edge := range flow.Edges {
		incoming[edge.To] = append(incoming[edge.To], edge.From)
	}
	visited := map[string]bool{}
	found := map[string]bool{}
	result := make([]string, 0, 4)
	var walk func(string)
	walk = func(currentID string) {
		if visited[currentID] {
			return
		}
		visited[currentID] = true
		state, ok := flowRun.NodeStates[currentID]
		if !ok || state.Status != "completed" {
			return
		}
		node := nodeByID[currentID]
		if node.Kind == domain.FlowNodeAgent || node.Kind == domain.FlowNodeTool || (node.Kind == "" && strings.TrimSpace(node.AgentID) != "") {
			if executionID, _ := state.Output["executionId"].(string); strings.TrimSpace(executionID) != "" && !found[currentID] {
				found[currentID] = true
				result = append(result, currentID)
			}
			return
		}
		for _, parentID := range incoming[currentID] {
			walk(parentID)
		}
	}
	for _, parentID := range incoming[nodeID] {
		walk(parentID)
	}
	return result
}

func completedUpstreamExecutionIDs(flow domain.FlowGraph, flowRun domain.FlowRun, nodeID string) []string {
	result := make([]string, 0, 4)
	seen := map[string]bool{}
	for _, upstreamNodeID := range completedUpstreamAgentNodeIDs(flow, flowRun, nodeID) {
		executionID, _ := flowRun.NodeStates[upstreamNodeID].Output["executionId"].(string)
		executionID = strings.TrimSpace(executionID)
		if executionID != "" && !seen[executionID] {
			seen[executionID] = true
			result = append(result, executionID)
		}
	}
	return result
}

func flowNodeContext(quest domain.Quest, flow domain.FlowGraph, flowRun domain.FlowRun, nodeID string, changeSets []domain.ChangeSet) []domain.RunContextInput {
	inputs := make([]domain.RunContextInput, 0, 4)
	currentName := nodeID
	currentAgent := ""
	for _, node := range flow.Nodes {
		if node.ID == nodeID {
			if strings.TrimSpace(node.Name) != "" {
				currentName = node.Name
			}
			currentAgent = node.AgentID
			break
		}
	}
	if quest.ID != "" {
		questContext := map[string]any{
			"title": quest.Title, "description": quest.Description, "objectives": quest.Objectives,
			"constraints": quest.Constraints, "definitionOfDone": quest.DefinitionOfDone,
			"importance": quest.Importance,
			"coordination": map[string]any{
				"flowRunId": flowRun.ID, "currentNodeId": nodeID, "currentNode": currentName,
				"currentAgentId": currentAgent, "role": "active",
			},
		}
		if data, err := json.Marshal(questContext); err == nil {
			inputs = append(inputs, domain.RunContextInput{
				Kind: domain.ContextText, Label: "Quest brief", Content: string(data),
			})
		}
	}
	nodeByID := make(map[string]domain.FlowNode, len(flow.Nodes))
	for _, node := range flow.Nodes {
		nodeByID[node.ID] = node
	}
	for _, upstreamNodeID := range completedUpstreamAgentNodeIDs(flow, flowRun, nodeID) {
		state := flowRun.NodeStates[upstreamNodeID]
		if state.Status != "completed" || state.Output == nil {
			continue
		}
		from := nodeByID[upstreamNodeID]
		fromLabel := strings.TrimSpace(from.Name)
		if fromLabel == "" {
			fromLabel = upstreamNodeID
		}
		executionID, _ := state.Output["executionId"].(string)
		handoff := map[string]any{
			"kind":           "agent_handoff",
			"fromNodeId":     upstreamNodeID,
			"fromNode":       fromLabel,
			"fromAgentId":    from.AgentID,
			"toNodeId":       nodeID,
			"toNode":         currentName,
			"toAgentId":      currentAgent,
			"status":         state.Status,
			"executionId":    executionID,
			"result":         state.Output["result"],
			"runId":          state.Output["runId"],
			"changedFiles":   state.Output["changedFiles"],
			"upstreamStatus": state.Output["status"],
		}
		if reviewed := changeSetHandoff(changeSets, executionID); len(reviewed) > 0 {
			handoff["changeSets"] = reviewed
		}
		if summary := handoffResultSummary(state.Output); summary != "" {
			handoff["summary"] = summary
		}
		data, err := json.Marshal(handoff)
		if err != nil {
			continue
		}
		content := string(data)
		if len(content) > 64*1024 {
			content = content[:64*1024] + "\n[truncated]"
		}
		inputs = append(inputs, domain.RunContextInput{
			Kind: domain.ContextText, Label: "Передача от · " + fromLabel, Content: content,
		})
	}
	return inputs
}

func changeSetHandoff(changeSets []domain.ChangeSet, executionID string) []map[string]any {
	if strings.TrimSpace(executionID) == "" {
		return nil
	}
	const maxTotalDiffRunes = 48 * 1024
	const maxItemDiffRunes = 12 * 1024
	remaining := maxTotalDiffRunes
	result := make([]map[string]any, 0, 2)
	for _, set := range changeSets {
		if set.ExecutionID != executionID || len(result) >= 4 {
			continue
		}
		items := make([]map[string]any, 0, min(len(set.Items), 24))
		for _, item := range set.Items {
			if len(items) >= 24 || remaining <= 0 {
				break
			}
			diff := strings.TrimSpace(security.Redact(item.Diff))
			runes := []rune(diff)
			limit := min(maxItemDiffRunes, remaining)
			truncated := len(runes) > limit
			if truncated {
				runes = runes[:limit]
			}
			remaining -= len(runes)
			entry := map[string]any{"path": item.Path, "kind": item.Kind, "diff": string(runes)}
			if truncated {
				entry["truncated"] = true
			}
			items = append(items, entry)
		}
		result = append(result, map[string]any{
			"id": set.ID, "status": set.Status, "title": set.Title, "dependsOn": append([]string(nil), set.DependsOn...), "items": items,
			"truncated": len(items) < len(set.Items) || remaining <= 0,
		})
	}
	return result
}

func handoffResultSummary(output map[string]any) string {
	if output == nil {
		return ""
	}
	if raw, ok := output["result"]; ok {
		switch typed := raw.(type) {
		case string:
			msg := strings.TrimSpace(typed)
			if msg == "" {
				return ""
			}
			runes := []rune(msg)
			if len(runes) > 400 {
				return string(runes[:400]) + "…"
			}
			return msg
		}
	}
	return ""
}

func upstreamHandoffSummary(flow domain.FlowGraph, flowRun domain.FlowRun, nodeID string) []map[string]any {
	nodeByID := make(map[string]domain.FlowNode, len(flow.Nodes))
	for _, node := range flow.Nodes {
		nodeByID[node.ID] = node
	}
	out := make([]map[string]any, 0, 2)
	for _, edge := range flow.Edges {
		if edge.To != nodeID {
			continue
		}
		state := flowRun.NodeStates[edge.From]
		if state.Status != "completed" {
			continue
		}
		from := nodeByID[edge.From]
		label := strings.TrimSpace(from.Name)
		if label == "" {
			label = edge.From
		}
		out = append(out, map[string]any{
			"fromNodeId": edge.From, "fromNode": label, "fromAgentId": from.AgentID,
			"status": state.Status, "summary": handoffResultSummary(state.Output),
		})
	}
	return out
}

func (a *App) cancelSiblingFlowExecutions(flowRunID, exceptExecutionID string) {
	if flowRunID == "" {
		return
	}
	ws, err := a.requireWorkspace()
	if err != nil {
		return
	}
	executions, err := a.store.ListExecutions(context.Background(), ws.ID, 500)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	for _, exec := range executions {
		if exec.FlowRunID != flowRunID || exec.ID == exceptExecutionID {
			continue
		}
		switch exec.Status {
		case domain.RunRunning:
			if exec.RunID != "" {
				_ = a.engine.Cancel(exec.RunID)
			} else {
				// Interactive Cursor executions have no headless RunID. Persist
				// cancellation so a late SDK completion is idempotently ignored.
				exec.Status = domain.RunCancelled
				exec.Error = "отменено: другой агент Flow завершился с ошибкой"
				exec.FinishedAt = &now
				exec.DurationMs = now.Sub(exec.StartedAt).Milliseconds()
				a.saveCascadeCancellation(exec)
			}
		case domain.RunPending, domain.RunInterrupted, domain.RunWaiting:
			exec.Status = domain.RunCancelled
			exec.Error = "отменено: другой агент Flow завершился с ошибкой"
			exec.FinishedAt = &now
			exec.DurationMs = now.Sub(exec.StartedAt).Milliseconds()
			a.saveCascadeCancellation(exec)
		}
	}
}

func providerNeedsAPIKey(kind domain.ProviderKind, presetID string) bool {
	for _, preset := range domain.BuiltInProviderCatalog() {
		if preset.ID == presetID {
			return preset.RequiresAPIKey
		}
	}
	if kind == domain.ProviderOllama {
		return false
	}
	return true
}

func (a *App) findExecution(executionID string) (domain.ExecutionInstance, error) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return domain.ExecutionInstance{}, err
	}
	executions, err := a.store.ListExecutions(context.Background(), ws.ID, 500)
	if err != nil {
		return domain.ExecutionInstance{}, err
	}
	for _, exec := range executions {
		if exec.ID == executionID {
			return exec, nil
		}
	}
	return domain.ExecutionInstance{}, fmt.Errorf("execution %s not found", executionID)
}

func (a *App) BeginCursorExecution(ctx context.Context, executionID string) (CursorExecutionLaunch, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	exec, err := a.findExecution(executionID)
	if err != nil {
		return CursorExecutionLaunch{}, err
	}
	brief, err := a.taskBriefForQuest(ctx, exec.WorkspaceID, exec.QuestID)
	if err != nil {
		return CursorExecutionLaunch{}, err
	}
	if brief != nil {
		return CursorExecutionLaunch{}, errors.New("Cursor runtime не поддерживает контроль прав и критериев утверждённого задания; используйте встроенного исполнителя")
	}
	if exec.Status != domain.RunPending && exec.Status != domain.RunInterrupted {
		return CursorExecutionLaunch{}, fmt.Errorf("Cursor-исполнение %s не ожидает запуска", executionID)
	}
	agentItem, err := a.store.GetProjectAgent(ctx, exec.ProjectAgentID)
	if err != nil {
		return CursorExecutionLaunch{}, err
	}
	if agentItem.WorkspaceID != exec.WorkspaceID {
		return CursorExecutionLaunch{}, errors.New("Cursor-агент принадлежит другому проекту")
	}
	if agentItem.Provider != domain.ProviderCursor {
		return CursorExecutionLaunch{}, errors.New("интерактивный запуск доступен только для Cursor Agent")
	}
	profile := domain.ProfileFromProjectAgent(agentItem)
	if err = a.enrichProjectAgentForRun(exec.WorkspaceID, agentItem, &profile, nil); err != nil {
		return CursorExecutionLaunch{}, err
	}
	sandboxRecord, err := a.store.GetSandboxByExecution(ctx, exec.ID)
	if err != nil {
		return CursorExecutionLaunch{}, fmt.Errorf("не удалось открыть песочницу Cursor-исполнения: %w", err)
	}
	if info, statErr := os.Stat(sandboxRecord.Path); statErr != nil || !info.IsDir() {
		if statErr == nil {
			statErr = errors.New("путь не является папкой")
		}
		return CursorExecutionLaunch{}, fmt.Errorf("песочница Cursor-исполнения недоступна: %w", statErr)
	}
	now := time.Now().UTC()
	exec.Status = domain.RunRunning
	exec.Error = ""
	exec.Result = ""
	exec.RunID = ""
	exec.StartedAt = now
	exec.FinishedAt = nil
	exec.DurationMs = 0
	if err = a.store.SaveExecution(ctx, exec); err != nil {
		return CursorExecutionLaunch{}, err
	}
	slog.Info("interactive Cursor execution claimed", "execution_id", exec.ID, "quest_id", exec.QuestID, "project_agent_id", exec.ProjectAgentID)
	return CursorExecutionLaunch{Execution: exec, Profile: profile, SandboxPath: sandboxRecord.Path}, nil
}

func (a *App) CompleteCursorExecution(ctx context.Context, executionID string, completion CursorExecutionCompletion) (domain.ExecutionInstance, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if len([]rune(completion.Result)) > 64*1024 || len([]rune(completion.Error)) > 8*1024 {
		return domain.ExecutionInstance{}, errors.New("результат Cursor-исполнения слишком велик")
	}
	exec, err := a.findExecution(executionID)
	if err != nil {
		return domain.ExecutionInstance{}, err
	}
	ws, err := a.requireWorkspace()
	if err != nil {
		return domain.ExecutionInstance{}, err
	}
	if ws.ID != exec.WorkspaceID {
		return domain.ExecutionInstance{}, errors.New("Cursor-исполнение принадлежит другому проекту")
	}
	agentItem, err := a.store.GetProjectAgent(ctx, exec.ProjectAgentID)
	if err != nil {
		return domain.ExecutionInstance{}, err
	}
	if agentItem.Provider != domain.ProviderCursor {
		return domain.ExecutionInstance{}, errors.New("завершить через Cursor можно только Cursor-исполнение")
	}
	if exec.Status == domain.RunCompleted || exec.Status == domain.RunFailed || exec.Status == domain.RunCancelled {
		return exec, nil
	}
	if exec.Status != domain.RunRunning {
		return domain.ExecutionInstance{}, fmt.Errorf("Cursor-исполнение %s не запущено", executionID)
	}
	status := strings.ToLower(strings.TrimSpace(completion.Status))
	switch status {
	case "completed", "success", "succeeded":
		exec.Status = domain.RunCompleted
	case "cancelled", "canceled":
		exec.Status = domain.RunCancelled
	case "failed", "error":
		exec.Status = domain.RunFailed
	default:
		return domain.ExecutionInstance{}, fmt.Errorf("неизвестный статус Cursor-исполнения %q", completion.Status)
	}
	exec.Result = strings.TrimSpace(completion.Result)
	exec.Error = strings.TrimSpace(completion.Error)
	if exec.Status == domain.RunFailed && exec.Error == "" {
		exec.Error = "Cursor Agent завершился с ошибкой"
	}
	if exec.Status == domain.RunCancelled && exec.Error == "" {
		exec.Error = "Cursor Agent остановлен пользователем"
	}
	now := time.Now().UTC()
	exec.FinishedAt = &now
	exec.DurationMs = now.Sub(exec.StartedAt).Milliseconds()
	if err = a.store.SaveExecution(ctx, exec); err != nil {
		return domain.ExecutionInstance{}, err
	}

	success := exec.Status == domain.RunCompleted
	a.awardProjectAgentOutcome(exec.ProjectAgentID, success)
	if sandboxRecord, loadErr := a.store.GetSandboxByExecution(ctx, exec.ID); loadErr == nil {
		baselinePath, dependencies, lineageErr := a.changeSetLineage(exec.WorkspaceID, sandboxRecord)
		if lineageErr != nil {
			slog.Warn("Cursor execution change set lineage unavailable", "execution_id", exec.ID, "error", lineageErr)
		} else {
			applier := changesets.Applier{Store: a.store}
			if _, buildErr := applier.BuildFromSandbox(ctx, changesets.BuildRequest{
				WorkspaceID: exec.WorkspaceID, ExecutionID: exec.ID, QuestID: exec.QuestID,
				Title: "Changes from Cursor · " + exec.ID, WorkspacePath: ws.Path,
				BaselinePath: baselinePath, SandboxPath: sandboxRecord.Path, DependsOn: dependencies,
			}); buildErr != nil {
				slog.Warn("Cursor execution change set unavailable", "execution_id", exec.ID, "error", buildErr)
			}
		}
	}

	if exec.FlowRunID == "" || exec.FlowNodeID == "" {
		if exec.QuestID != "" {
			a.finalizeQuestAfterFlow(exec.QuestID, success)
		}
	} else {
		if !success {
			if recoveredRun, recovered, recoveryErr := a.recoverFlowNodeFailure(exec.FlowRunID, exec.FlowNodeID, exec); recoveryErr != nil {
				slog.Warn("Cursor flow node recovery unavailable", "execution_id", exec.ID, "error", recoveryErr)
			} else if recovered {
				if scheduleErr := a.scheduleFlowAgentExecutionsFromRun(recoveredRun); scheduleErr != nil {
					return exec, scheduleErr
				}
				slog.Info("interactive Cursor execution scheduled for recovery", "execution_id", exec.ID, "flow_run_id", exec.FlowRunID, "flow_node_id", exec.FlowNodeID)
				return exec, nil
			}
		}
		childStatus := domain.QuestFailed
		if success {
			childStatus = domain.QuestCompleted
		} else if exec.Status == domain.RunCancelled {
			childStatus = domain.QuestCancelled
		}
		a.setFlowChildQuestStatus(exec.FlowRunID, exec.FlowNodeID, childStatus)
		flowRuntime := flowruntime.Runtime{Store: a.store}
		completionOutput := a.flowAttemptOutput(exec.FlowRunID, exec.FlowNodeID, exec.ID, map[string]any{
			"executionId": exec.ID, "result": exec.Result, "status": exec.Status,
			"error": exec.Error, "externalRuntime": "cursor",
		})
		flowRun, completeErr := flowRuntime.CompleteAgentNode(ctx, exec.FlowRunID, exec.FlowNodeID, success, completionOutput)
		if completeErr != nil {
			return exec, completeErr
		}
		if flowRun.Status == domain.RunFailed || flowRun.Status == domain.RunCancelled {
			a.cancelSiblingFlowExecutions(flowRun.ID, exec.ID)
			a.closeUnfinishedFlowChildQuests(flowRun.ID, false)
			if flowRun.QuestID != "" {
				a.finalizeQuestAfterFlow(flowRun.QuestID, false)
			}
		} else if flowRun.Status == domain.RunCompleted {
			a.closeUnfinishedFlowChildQuests(flowRun.ID, true)
			if flowRun.QuestID != "" {
				a.finalizeQuestAfterFlow(flowRun.QuestID, success)
			}
		} else if scheduleErr := a.scheduleFlowAgentExecutionsFromRun(flowRun); scheduleErr != nil {
			return exec, scheduleErr
		}
	}
	slog.Info("interactive Cursor execution completed", "execution_id", exec.ID, "status", exec.Status, "duration_ms", exec.DurationMs)
	return exec, nil
}

func (a *App) LaunchPendingExecution(executionID, apiKey string) (domain.Run, error) {
	exec, err := a.findExecution(executionID)
	if err != nil {
		return domain.Run{}, err
	}
	if exec.Status != domain.RunPending && exec.Status != domain.RunInterrupted {
		return domain.Run{}, fmt.Errorf("execution %s is not pending", executionID)
	}
	if exec.FlowRunID != "" {
		a.rememberFlowOrchestratorKey(exec.FlowRunID, apiKey)
	}
	var contextItems []domain.RunContextInput
	checkKind := ""
	if exec.FlowRunID != "" && exec.FlowNodeID != "" {
		if flowRun, runErr := a.store.GetFlowRun(context.Background(), exec.FlowRunID); runErr == nil {
			checkKind = flowNodeCompletionCheckKind(flowRun, exec.FlowNodeID)
			if flow, ok, flowErr := flowruntime.FlowFromSnapshot(flowRun); flowErr == nil && ok {
				quest := domain.Quest{ID: flowRun.QuestID, Title: flow.Name}
				if flowRun.QuestID != "" {
					if quests, listErr := a.store.ListQuests(context.Background(), exec.WorkspaceID); listErr == nil {
						for _, item := range quests {
							if item.ID == flowRun.QuestID {
								quest = item
								break
							}
						}
					}
				}
				changeSets, _ := a.store.ListChangeSets(context.Background(), flowRun.WorkspaceID)
				contextItems = flowNodeContext(quest, flow, flowRun, exec.FlowNodeID, changeSets)
			}
		}
	}
	return a.StartRun(StartRunRequest{
		ProfileID: exec.ProjectAgentID, Task: exec.Task, APIKey: apiKey,
		QuestID: exec.QuestID, FlowRunID: exec.FlowRunID, FlowNodeID: exec.FlowNodeID, ExecutionID: exec.ID,
		CompletionCheckKind: checkKind,
		ContextItems:        contextItems,
	})
}

func flowNodeCompletionCheckKind(flowRun domain.FlowRun, nodeID string) string {
	if nodeID == "" || flowRun.NodeStates == nil {
		return ""
	}
	state := flowRun.NodeStates[nodeID]
	if lineage, _ := state.Output["sandboxLineage"].(string); lineage == "merged_parallel_join" {
		return "merged-result"
	}
	return ""
}

func (a *App) ResumeFlowApproval(flowRunID, nodeID string, approved bool) (domain.FlowRun, error) {
	runtime := flowruntime.Runtime{Store: a.store}
	flowRun, err := runtime.ResumeAfterApproval(context.Background(), flowRunID, nodeID, approved)
	if err != nil {
		return domain.FlowRun{}, err
	}
	if flowRun.Status == domain.RunFailed || flowRun.Status == domain.RunCancelled {
		a.cancelSiblingFlowExecutions(flowRun.ID, "")
		a.closeUnfinishedFlowChildQuests(flowRun.ID, false)
		if flowRun.QuestID != "" {
			a.finalizeQuestAfterFlow(flowRun.QuestID, false)
		}
		return flowRun, nil
	}
	if flowRun.Status == domain.RunCompleted && flowRun.QuestID != "" {
		a.closeUnfinishedFlowChildQuests(flowRun.ID, true)
		a.finalizeQuestAfterFlow(flowRun.QuestID, true)
		return flowRun, nil
	}
	_ = a.scheduleFlowAgentExecutionsFromRun(flowRun)
	return flowRun, nil
}

func (a *App) attachCompletionEvidence(ctx context.Context, runID string, output map[string]any) {
	if strings.TrimSpace(runID) == "" || output == nil {
		return
	}
	events, err := a.store.ListByRun(ctx, runID)
	if err != nil {
		return
	}
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type != domain.EventCompletionChecked {
			continue
		}
		var payload struct {
			Status   string          `json:"status"`
			Evidence json.RawMessage `json:"evidence"`
		}
		if json.Unmarshal(events[i].Data, &payload) != nil {
			return
		}
		output["completionStatus"] = payload.Status
		if len(payload.Evidence) > 0 {
			var evidence map[string]any
			if json.Unmarshal(payload.Evidence, &evidence) == nil {
				output["completionEvidence"] = evidence
				if status, _ := evidence["status"].(string); status == "needs_review" {
					output["needsReview"] = true
				}
			}
		}
		return
	}
}

func annotateFlowVerifier(flow *domain.FlowGraph, brief *domain.TaskBrief) {
	if flow == nil || brief == nil {
		return
	}
	criteria := make([]any, 0, len(brief.Criteria))
	for _, c := range brief.Criteria {
		criteria = append(criteria, map[string]any{"id": c.ID, "kind": c.Kind, "text": c.Text})
	}
	for i := range flow.Nodes {
		if flow.Nodes[i].Kind != domain.FlowNodeVerifier {
			continue
		}
		if flow.Nodes[i].Config == nil {
			flow.Nodes[i].Config = map[string]any{}
		}
		flow.Nodes[i].Config["requireResult"] = true
		if len(criteria) > 0 {
			flow.Nodes[i].Config["criteria"] = criteria
		}
		if brief.Permissions.WriteFiles && brief.Budget.MaxParallel > 1 {
			flow.Nodes[i].Config["requireMergedResult"] = true
		}
	}
}

func (a *App) finalizeQuestAfterFlow(questID string, success bool) {
	if approval, approvalErr := a.store.WorkOrderApprovalByQuestV2(context.Background(), questID); approvalErr == nil {
		if a.advanceWorkOrderMilestoneV2(approval, success) {
			return
		}
		a.finalizeWorkOrderQuestAfterFlowV2(approval, success)
		return
	}
	ws, err := a.requireWorkspace()
	if err != nil {
		return
	}
	quests, err := a.store.ListQuests(context.Background(), ws.ID)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	for _, quest := range quests {
		if quest.ID != questID {
			continue
		}
		quest.Status = domain.QuestFailed
		content := fmt.Sprintf("Quest «%s» finished with status %s. Objectives: %s", quest.Title, quest.Status, strings.Join(quest.Objectives, "; "))
		verified := success
		if quest.Brief != nil {
			verified = false
			content = "Execution ended for «" + quest.Title + "»; task criteria have not been verified."
			if outcome, outcomeErr := a.QuestOutcome(context.Background(), quest.ID); outcomeErr == nil {
				verified = success && outcome.Verified
				content = "Task «" + quest.Title + "»: " + outcome.Honest
				if success && !verified {
					if taskBriefHasManualCriteria(quest.Brief) {
						quest.Status = domain.QuestNeedsReview
					} else {
						quest.Status = domain.QuestBlocked
					}
				}
			}
		} else if success {
			quest.Status = domain.QuestCompleted
		}
		if quest.Kind == "project" {
			verified = a.projectControllerFinished(context.Background(), &quest, success)
		} else if verified {
			quest.Status = domain.QuestCompleted
		}
		if domain.IsTerminalQuestStatus(quest.Status) {
			quest.FinishedAt = &now
		} else {
			quest.FinishedAt = nil
		}
		quest.UpdatedAt = now
		if err := a.store.SaveQuest(context.Background(), quest); err != nil {
			slog.Warn("quest completion not persisted", "quest_id", quest.ID, "status", quest.Status, "error", err)
		}
		if _, err := a.SaveMemory(domain.MemoryRecord{
			WorkspaceID: ws.ID, Kind: domain.MemoryQuest, OwnerID: quest.ID,
			Content: content, Source: "quest-complete", Confidence: 0.8, Pinned: false,
		}); err != nil {
			slog.Warn("quest memory not saved", "quest_id", quest.ID, "kind", domain.MemoryQuest, "error", err)
		}
		if verified && len(quest.DefinitionOfDone) > 0 {
			_, _ = a.SaveMemory(domain.MemoryRecord{
				WorkspaceID: ws.ID, Kind: domain.MemoryProject,
				Content: "Completed DoD for «" + quest.Title + "»: " + strings.Join(quest.DefinitionOfDone, "; "),
				Source:  "quest-dod", Confidence: 0.7, Pinned: true,
			})
		}
		a.finalizeIntakeAfterQuest(quest.ID, verified)
		return
	}
}

func taskBriefHasManualCriteria(brief *domain.TaskBrief) bool {
	if brief == nil {
		return false
	}
	for _, criterion := range brief.Criteria {
		if criterion.Kind == "manual" {
			return true
		}
	}
	return false
}

func (a *App) awardProjectAgentOutcome(projectAgentID string, success bool) {
	if projectAgentID == "" {
		return
	}
	agent, err := a.store.GetProjectAgent(context.Background(), projectAgentID)
	if err != nil {
		return
	}
	agent.TasksCompleted++
	if success {
		agent.SuccessCount++
		agent.Experience += 25
	} else {
		agent.Experience += 5
	}
	agent.Level = 1 + agent.Experience/100
	agent.UpdatedAt = time.Now().UTC()
	if err := a.store.SaveProjectAgent(context.Background(), agent); err != nil {
		slog.Warn("agent outcome not persisted", "project_agent_id", agent.ID, "success", success, "error", err)
	}
}

func (a *App) recordUsageFromEvent(event domain.Event) {
	if event.Type != domain.EventModelUsage {
		return
	}
	var payload struct {
		BudgetReservationID string `json:"budgetReservationId"`
		Usage               struct {
			InputTokens  int `json:"inputTokens"`
			OutputTokens int `json:"outputTokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(event.Data, &payload); err != nil {
		return
	}
	// Agent-engine requests are reconciled once, after the provider stream
	// closes. Some providers emit partial usage more than once, so recording
	// each event here would double-count both tokens and cost.
	if payload.BudgetReservationID != "" {
		return
	}
	run, runErr := a.store.GetRun(context.Background(), event.RunID)
	if runErr != nil {
		return
	}
	workspaceID := event.WorkspaceID
	if workspaceID == "" {
		workspaceID = run.WorkspaceID
	}
	if workspaceID == "" {
		return
	}
	total := int64(payload.Usage.InputTokens + payload.Usage.OutputTokens)
	latencyMs := int64(0)
	if events, listErr := a.store.ListByRun(context.Background(), event.RunID); listErr == nil {
		for index := len(events) - 1; index >= 0; index-- {
			candidate := events[index]
			if candidate.Type != domain.EventModelRequested || candidate.Step != event.Step || candidate.CreatedAt.After(event.CreatedAt) {
				continue
			}
			latencyMs = event.CreatedAt.Sub(candidate.CreatedAt).Milliseconds()
			break
		}
	}
	record := domain.UsageRecord{
		ID: domain.NewID("usage"), WorkspaceID: workspaceID,
		ExecutionID: event.ExecutionID, QuestID: event.QuestID,
		ProjectAgentID: run.ProfileID, Provider: run.Provider, Model: run.Model,
		InputTokens: int64(payload.Usage.InputTokens), OutputTokens: int64(payload.Usage.OutputTokens),
		TotalTokens: total, LatencyMs: latencyMs, Outcome: "usage_reported", CreatedAt: event.CreatedAt,
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now().UTC()
	}
	if err := a.store.InsertUsageRecord(context.Background(), record); err != nil {
		slog.Warn("usage record not stored", "execution_id", record.ExecutionID, "quest_id", record.QuestID, "total_tokens", record.TotalTokens, "error", err)
	}
}

func (a *App) RevertExecution(executionID string) (map[string]any, error) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return nil, err
	}
	executions, err := a.store.ListExecutions(context.Background(), ws.ID, 500)
	if err != nil {
		return nil, err
	}
	var exec *domain.ExecutionInstance
	for index := range executions {
		if executions[index].ID == executionID {
			exec = &executions[index]
			break
		}
	}
	if exec == nil {
		return nil, fmt.Errorf("execution %s not found", executionID)
	}
	sets, err := a.store.ListChangeSets(context.Background(), ws.ID)
	if err != nil {
		return nil, err
	}
	reverted := 0
	hasChangeSet := false
	for _, set := range sets {
		if set.ExecutionID != exec.ID {
			continue
		}
		hasChangeSet = true
		if set.Status == domain.ChangeSetApplied {
			result, revertErr := a.RevertChangeSet(set.ID)
			if revertErr != nil {
				return nil, revertErr
			}
			reverted += len(result.Applied)
		} else if set.Status == domain.ChangeSetPending || set.Status == domain.ChangeSetApproved || set.Status == domain.ChangeSetConflict {
			if _, rejectErr := a.RejectChangeSet(set.ID); rejectErr != nil {
				return nil, rejectErr
			}
		}
	}
	// Compatibility fallback for pre-Hub executions that wrote directly to the
	// live workspace and therefore have no ChangeSet transaction.
	if !hasChangeSet && exec.RunID != "" {
		patches, patchErr := a.store.PatchesByRun(context.Background(), exec.RunID)
		if patchErr == nil {
			for _, patch := range patches {
				if patch.Status != "applied" {
					continue
				}
				if _, err = a.RevertPatch(patch.ID); err == nil {
					reverted++
				}
			}
		}
	}
	return map[string]any{"executionId": exec.ID, "revertedFiles": reverted, "revertedPatches": reverted}, nil
}

func (a *App) RevertFlowNode(flowRunID, nodeID string) (map[string]any, error) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return nil, err
	}
	executions, err := a.store.ListExecutions(context.Background(), ws.ID, 500)
	if err != nil {
		return nil, err
	}
	total := 0
	var executionIDs []string
	for _, exec := range executions {
		if exec.FlowRunID != flowRunID || exec.FlowNodeID != nodeID {
			continue
		}
		result, revertErr := a.RevertExecution(exec.ID)
		if revertErr != nil {
			continue
		}
		executionIDs = append(executionIDs, exec.ID)
		if count, ok := result["revertedPatches"].(int); ok {
			total += count
		}
	}
	return map[string]any{
		"flowRunId": flowRunID, "nodeId": nodeID, "executions": executionIDs, "revertedPatches": total,
	}, nil
}

func (a *App) RevertQuest(questID string) (map[string]any, error) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return nil, err
	}
	executions, err := a.store.ListExecutions(context.Background(), ws.ID, 500)
	if err != nil {
		return nil, err
	}
	total := 0
	var executionIDs []string
	for _, exec := range executions {
		if exec.QuestID != questID {
			continue
		}
		result, revertErr := a.RevertExecution(exec.ID)
		if revertErr != nil {
			continue
		}
		executionIDs = append(executionIDs, exec.ID)
		if count, ok := result["revertedPatches"].(int); ok {
			total += count
		}
	}
	quests, err := a.store.ListQuests(context.Background(), ws.ID)
	if err == nil {
		for _, quest := range quests {
			if quest.ID != questID {
				continue
			}
			quest.Status = domain.QuestCancelled
			now := time.Now().UTC()
			quest.FinishedAt = &now
			quest.UpdatedAt = now
			if err := a.store.SaveQuest(context.Background(), quest); err != nil {
				slog.Warn("quest cancellation not persisted", "quest_id", quest.ID, "error", err)
			}
		}
	}
	return map[string]any{"questId": questID, "executions": executionIDs, "revertedPatches": total}, nil
}

// questDescription — что станет описанием квеста и уйдёт в контекст агента.
//
// Задача словами человека, если она есть. Предложения, созданные до появления
// поля task, её не несут — для них остаётся прежнее поведение, иначе описание
// стало бы пустым у всего, что уже лежит в очереди решений.
func questDescription(proposal domain.QuestProposal) string {
	if task := strings.TrimSpace(proposal.Task); task != "" {
		return task
	}
	return proposal.Rationale
}
