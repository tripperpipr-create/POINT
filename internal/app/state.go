package app

import (
	"context"
	"time"

	"local-agent-workbench/internal/diagnostics"
	"local-agent-workbench/internal/domain"
)

// RuntimeState is the coherent execution slice used after orchestration,
// approval, patch and flow transitions. It deliberately excludes catalog,
// connection, memory and companion-history reads from the hot path.
type RuntimeState struct {
	Runs                     []domain.Run                     `json:"runs"`
	RunDiagnostics           []diagnostics.RunDiagnostics     `json:"runDiagnostics"`
	WorkflowRuns             []domain.WorkflowRun             `json:"workflowRuns"`
	Changes                  []domain.PatchProposal           `json:"changes"`
	Quests                   []domain.Quest                   `json:"quests"`
	Flows                    []domain.FlowGraph               `json:"flows"`
	FlowRuns                 []domain.FlowRun                 `json:"flowRuns"`
	Executions               []domain.ExecutionInstance       `json:"executions"`
	ChangeSets               []domain.ChangeSet               `json:"changeSets"`
	QuestProposals           []domain.QuestProposal           `json:"questProposals"`
	CompanionActionProposals []domain.CompanionActionProposal `json:"companionActionProposals"`
	LearningSignals          []domain.LearningSignal          `json:"learningSignals"`
	SkillOutcomes            []domain.SkillOutcome            `json:"skillOutcomes"`
}

// GuildState is the coherent agent-definition slice. Companion actions can
// change several of these collections together, so patching only the returned
// action would leave the roster and blueprints disagreeing.
type GuildState struct {
	Blueprints    []domain.AgentBlueprint          `json:"blueprints"`
	ProjectAgents []domain.ProjectAgent            `json:"projectAgents"`
	Skills        []domain.SkillDefinition         `json:"skills"`
	ProjectSkills []domain.ProjectSkillInstance    `json:"projectSkills"`
	Teams         []domain.Team                    `json:"teams"`
	SkillCuration []domain.SkillCurationSuggestion `json:"skillCuration"`
}

func nonNilSlice[T any](items []T) []T {
	if items == nil {
		return []T{}
	}
	return items
}

func (a *App) RuntimeState() (RuntimeState, error) {
	ctx := context.Background()
	workspaceID := a.currentWorldID()
	state := RuntimeState{}
	var err error

	state.Runs, err = a.store.ListRunsForWorkspace(ctx, workspaceID, 100)
	if err != nil {
		return RuntimeState{}, err
	}
	for index := range state.Runs {
		state.Runs[index] = publicRun(state.Runs[index])
	}
	state.Runs = nonNilSlice(state.Runs)
	state.RunDiagnostics, err = a.recentRunDiagnostics(ctx, state.Runs, 20)
	if err != nil {
		return RuntimeState{}, err
	}
	state.RunDiagnostics = nonNilSlice(state.RunDiagnostics)

	state.WorkflowRuns, err = a.store.ListWorkflowRunsForWorkspace(ctx, workspaceID, 100)
	if err != nil {
		return RuntimeState{}, err
	}
	for index := range state.WorkflowRuns {
		state.WorkflowRuns[index] = publicWorkflowRun(state.WorkflowRuns[index])
	}
	state.WorkflowRuns = nonNilSlice(state.WorkflowRuns)

	state.Changes, err = a.store.ListPatchesForWorkspace(ctx, workspaceID, 200)
	if err != nil {
		return RuntimeState{}, err
	}
	state.Changes = nonNilSlice(publicPatches(state.Changes))
	state.Quests, err = a.store.ListQuests(ctx, workspaceID)
	if err != nil {
		return RuntimeState{}, err
	}
	state.Flows, err = a.store.ListFlows(ctx, workspaceID)
	if err != nil {
		return RuntimeState{}, err
	}
	state.FlowRuns, err = a.store.ListFlowRuns(ctx, workspaceID, 50)
	if err != nil {
		return RuntimeState{}, err
	}
	state.Executions, err = a.store.ListExecutions(ctx, workspaceID, 50)
	if err != nil {
		return RuntimeState{}, err
	}
	state.ChangeSets, err = a.store.ListChangeSets(ctx, workspaceID)
	if err != nil {
		return RuntimeState{}, err
	}
	state.QuestProposals, err = a.store.ListQuestProposals(ctx, workspaceID)
	if err != nil {
		return RuntimeState{}, err
	}
	state.QuestProposals = trimResolvedProposals(state.QuestProposals)
	state.CompanionActionProposals, err = a.store.ListCompanionActionProposals(ctx, workspaceID)
	if err != nil {
		return RuntimeState{}, err
	}
	state.LearningSignals, err = a.store.ListLearningSignals(ctx, workspaceID, 200)
	if err != nil {
		return RuntimeState{}, err
	}
	state.SkillOutcomes, err = a.store.ListSkillOutcomes(ctx, workspaceID, 500)
	if err != nil {
		return RuntimeState{}, err
	}

	state.Quests = nonNilSlice(state.Quests)
	state.Flows = nonNilSlice(state.Flows)
	state.FlowRuns = nonNilSlice(state.FlowRuns)
	state.Executions = nonNilSlice(state.Executions)
	state.ChangeSets = nonNilSlice(state.ChangeSets)
	state.QuestProposals = nonNilSlice(state.QuestProposals)
	state.CompanionActionProposals = nonNilSlice(state.CompanionActionProposals)
	state.LearningSignals = nonNilSlice(state.LearningSignals)
	state.SkillOutcomes = nonNilSlice(state.SkillOutcomes)
	return state, nil
}

func (a *App) GuildState() (GuildState, error) {
	ctx := context.Background()
	workspaceID := a.currentWorldID()
	state := GuildState{}
	var err error
	state.Blueprints, err = a.store.ListBlueprints(ctx)
	if err != nil {
		return GuildState{}, err
	}
	state.Skills, err = a.store.ListSkills(ctx)
	if err != nil {
		return GuildState{}, err
	}
	if len(state.Skills) == 0 {
		if err = a.seedDefaultSkills(ctx); err != nil {
			return GuildState{}, err
		}
		state.Skills, err = a.store.ListSkills(ctx)
		if err != nil {
			return GuildState{}, err
		}
	}
	if workspaceID != "" {
		state.ProjectAgents, err = a.store.ListProjectAgents(ctx, workspaceID)
		if err != nil {
			return GuildState{}, err
		}
		state.ProjectSkills, err = a.store.ListProjectSkills(ctx, workspaceID)
		if err != nil {
			return GuildState{}, err
		}
		state.Teams, err = a.store.ListTeams(ctx, workspaceID)
		if err != nil {
			return GuildState{}, err
		}
		outcomes, outcomesErr := a.store.ListAllSkillOutcomes(ctx, 1000)
		if outcomesErr != nil {
			return GuildState{}, outcomesErr
		}
		state.SkillCuration = buildSkillCuration(state.Skills, state.ProjectAgents, state.Blueprints, state.ProjectSkills, outcomes, time.Now().UTC())
	}
	state.Blueprints = nonNilSlice(state.Blueprints)
	state.ProjectAgents = nonNilSlice(state.ProjectAgents)
	state.Skills = nonNilSlice(state.Skills)
	state.ProjectSkills = nonNilSlice(state.ProjectSkills)
	state.Teams = nonNilSlice(state.Teams)
	state.SkillCuration = nonNilSlice(state.SkillCuration)
	return state, nil
}
