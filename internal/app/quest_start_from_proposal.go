// Запуск квеста из утверждённого предложения.
//
// Между «человек нажал Старт» и «агент начал» лежит вся сборка: отряд, схема
// потока, договоры этапов, первые узлы. Здесь она целиком.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/flowruntime"
	"local-agent-workbench/internal/orchestrator"
)

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
	agentIDs := proposal.TeamAgentIDs
	availableAgents := make(map[string]bool, len(runnableAgents))
	for _, agent := range agents {
		if capability, blocked := blockedAgents[agent.ID]; blocked {
			if userPickedTeam && slices.Contains(agentIDs, agent.ID) {
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
				if !slices.Contains(agentIDs, agentID) {
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
	if selectedFlow == nil && proposal.Brief != nil && proposal.Brief.Mode == domain.TaskModeProject && !useModelPlanner {
		return QuestProposalResult{}, errors.New("для проектного задания нужна настроенная модель планировщика или явно выбранный Flow")
	}
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
		a.updateWorkOrderLaunchProgressV2(ctx, &quest, "planning", "Модель "+cfg.Model+" строит план; ждём первый фрагмент ответа (лимит "+plannerBudgetText(orchestrator.PlannerBudget(cfg))+")")
		skills, skillErr := a.newMasterSkillSession(ctx, ws.ID, "planning", "", proposal.ID, quest.ID)
		if skillErr != nil {
			return QuestProposalResult{}, skillErr
		}
		defer a.finishMasterOperation(skills, cfg, orchestratorAPIKey)
		planner := orchestrator.Planner{NewModel: a.budgetedModelFactory(modelBudgetScope{
			WorkspaceID: ws.ID, QuestID: quest.ID, ProjectAgentID: "master", Outcome: "orchestrator_plan",
		})}
		planRequest := orchestrator.PlanRequest{
			Skills: skills,
			Config: cfg, Proposal: proposal, Agents: runnableAgents, LockedAgentIDs: locked, APIKey: orchestratorAPIKey,
			Project: a.masterProjectFacts(ctx), Signals: selectionSignals, ModelCandidates: modelCandidates,
			Environment: a.plannerExecutionEnvironment(proposal.Brief),
			Progress: func(progress orchestrator.PlanProgress) {
				a.updateWorkOrderLaunchProgressV2(ctx, &quest, "planning", progress.Message)
			},
		}
		planned, planErr := planner.Plan(ctx, planRequest)
		attempts := 1
		if planErr != nil && ctx.Err() == nil && !plannerBudgetBlocked(planErr) {
			a.updateWorkOrderLaunchProgressV2(ctx, &quest, "planning", "Первая попытка планирования не удалась; повторяем запрос модели")
			firstErr := planErr
			attempts = 2
			planRequest.RetryFeedback = plannerFailureText(firstErr)
			planned, planErr = planner.Plan(ctx, planRequest)
			if planErr != nil {
				planErr = fmt.Errorf("план модели не получен после двух попыток: первая — %s; вторая — %s", plannerFailureText(firstErr), plannerFailureText(planErr))
			}
		}
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
				if errors.Is(ctxErr, context.DeadlineExceeded) {
					if attempts == 2 {
						return QuestProposalResult{}, planErr
					}
					return QuestProposalResult{}, fmt.Errorf("план модели не получен: %s", plannerFailureText(planErr))
				}
				return QuestProposalResult{}, ctxErr
			}
			return QuestProposalResult{}, planErr
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
	if modelPlan != nil {
		a.updateWorkOrderLaunchProgressV2(ctx, draftQuest, "compiling", "План модели проверен; движок Point собирает Flow")
	} else {
		a.updateWorkOrderLaunchProgressV2(ctx, draftQuest, "compiling", "Движок Point собирает Flow из утверждённого задания")
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
		flow = orchestrator.EnsureProjectPipeline(flow, agentIDs)
		flow.UpdatedAt = time.Now().UTC()
		if err = a.store.SaveFlow(ctx, flow); err != nil {
			return QuestProposalResult{}, err
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
		OrchestratorModel: orchModel,
	}
	if startFlow {
		a.updateWorkOrderLaunchProgressV2(ctx, &quest, "launching", "Flow готов; запускаем первого исполнителя")
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

func plannerBudgetBlocked(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "budget blocked") ||
		strings.Contains(message, "reserve model budget") ||
		strings.Contains(message, "quest token budget") ||
		strings.Contains(message, "quest cost budget")
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
