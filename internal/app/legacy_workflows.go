package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"local-agent-workbench/internal/changesets"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/sandbox"
	"local-agent-workbench/internal/storage"
	"local-agent-workbench/internal/workflows"
)

type StartWorkflowRequest struct {
	WorkflowID   string                   `json:"workflowId"`
	Task         string                   `json:"task"`
	APIKeys      map[string]string        `json:"apiKeys,omitempty"`
	ContextItems []domain.RunContextInput `json:"contextItems,omitempty"`
}

func (a *App) SaveWorkflow(workflow domain.AgentWorkflow) (domain.AgentWorkflow, error) {
	now := time.Now().UTC()
	if workflow.ID == "" {
		workflow.ID = domain.NewID("workflow")
		workflow.CreatedAt = now
	}
	if workflow.CreatedAt.IsZero() {
		workflow.CreatedAt = now
	}
	workflow.UpdatedAt = now
	workflow.Name = strings.TrimSpace(workflow.Name)
	workflow.Description = strings.TrimSpace(workflow.Description)
	for index := range workflow.Steps {
		if workflow.Steps[index].ID == "" {
			workflow.Steps[index].ID = domain.NewID("step")
		}
		workflow.Steps[index].Name = strings.TrimSpace(workflow.Steps[index].Name)
		workflow.Steps[index].ProfileID = strings.TrimSpace(workflow.Steps[index].ProfileID)
		workflow.Steps[index].Instruction = strings.TrimSpace(workflow.Steps[index].Instruction)
	}
	profiles, err := a.storedProfiles(context.Background())
	if err != nil {
		return domain.AgentWorkflow{}, err
	}
	profileIDs := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		profileIDs = append(profileIDs, profile.ID)
	}
	if err = storage.ValidateWorkflow(workflow, profileIDs...); err != nil {
		return domain.AgentWorkflow{}, err
	}
	if err = a.recordCompatibilityUsage(context.Background(), a.currentWorldID(), domain.CompatibilityWorkflowSave, legacyWorkflowVersion); err != nil {
		return domain.AgentWorkflow{}, err
	}
	if err = a.store.SaveWorkflow(context.Background(), workflow); err != nil {
		return domain.AgentWorkflow{}, err
	}
	return workflow, nil
}

func (a *App) DeleteWorkflow(id string) error {
	if a.workflowManager.IsWorkflowActive(id) {
		return errors.New("workflow is currently running")
	}
	if err := a.recordCompatibilityUsage(context.Background(), a.currentWorldID(), domain.CompatibilityWorkflowDelete, legacyWorkflowVersion); err != nil {
		return err
	}
	return a.store.DeleteWorkflow(context.Background(), id)
}

func (a *App) StartWorkflow(request StartWorkflowRequest) (domain.WorkflowRun, error) {
	a.mu.RLock()
	current := a.currentWorkspace
	currentFS := a.currentFS
	a.mu.RUnlock()
	if current == nil || currentFS == nil {
		return domain.WorkflowRun{}, errors.New("open a workspace before starting a workflow")
	}
	workflowDefinitions, err := a.store.ListWorkflows(context.Background())
	if err != nil {
		return domain.WorkflowRun{}, err
	}
	var selected *domain.AgentWorkflow
	for index := range workflowDefinitions {
		if workflowDefinitions[index].ID == request.WorkflowID {
			selected = &workflowDefinitions[index]
			break
		}
	}
	if selected == nil {
		return domain.WorkflowRun{}, errors.New("workflow not found")
	}
	profiles, err := a.storedProfiles(context.Background())
	if err != nil {
		return domain.WorkflowRun{}, err
	}
	profileIDs := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		profileIDs = append(profileIDs, profile.ID)
	}
	if err = storage.ValidateWorkflow(*selected, profileIDs...); err != nil {
		return domain.WorkflowRun{}, err
	}
	if len(request.APIKeys) > 12 {
		return domain.WorkflowRun{}, errors.New("too many workflow API keys")
	}
	for profileID, key := range request.APIKeys {
		if len(profileID) > 200 || len(key) > 64*1024 {
			return domain.WorkflowRun{}, errors.New("workflow API key input exceeds its limit")
		}
	}
	contextItems, err := resolveRunContext(currentFS, request.ContextItems)
	if err != nil {
		return domain.WorkflowRun{}, err
	}
	customTools, err := a.store.ListCustomTools(context.Background())
	if err != nil {
		return domain.WorkflowRun{}, err
	}
	if err = a.recordCompatibilityUsage(context.Background(), current.ID, domain.CompatibilityWorkflowRun, legacyWorkflowVersion); err != nil {
		return domain.WorkflowRun{}, err
	}
	workflowExecutionID := domain.NewID("workflow-execution")
	sandboxRecord, err := a.sandboxBackend.Create(context.Background(), sandbox.CreateRequest{
		WorkspaceID: current.ID, WorkspacePath: current.Path, ExecutionID: workflowExecutionID,
		LiveWorkspace: sandbox.LiveFileMutationEnabled(),
	})
	if err != nil {
		return domain.WorkflowRun{}, fmt.Errorf("create legacy workflow sandbox: %w", err)
	}
	if err = a.store.SaveSandbox(context.Background(), sandboxRecord); err != nil {
		_ = a.sandboxBackend.Close(context.Background(), sandboxRecord, current.Path)
		return domain.WorkflowRun{}, err
	}
	isolatedWorkspace := *current
	isolatedWorkspace.Path = sandboxRecord.Path
	workspaceID := current.ID
	workspacePath := current.Path
	run, err := a.workflowManager.Start(workflows.StartInput{
		ApplicationVersion: Version, Workflow: *selected, Profiles: profiles, CustomTools: customTools,
		Workspace: isolatedWorkspace, Task: request.Task, APIKeys: request.APIKeys, ContextItems: contextItems,
		OnFinished: func(finished domain.WorkflowRun) error {
			applier := changesets.Applier{Store: a.store}
			_, finalizeErr := applier.BuildFromSandbox(context.Background(), changesets.BuildRequest{
				WorkspaceID: workspaceID, ExecutionID: workflowExecutionID,
				Title:         "Changes from legacy workflow " + finished.ID,
				WorkspacePath: workspacePath, BaselinePath: sandboxRecord.BaselinePath, SandboxPath: sandboxRecord.Path,
			})
			return finalizeErr
		},
	})
	if err != nil {
		_ = a.sandboxBackend.Close(context.Background(), sandboxRecord, workspacePath)
	}
	return publicWorkflowRun(run), err
}

func (a *App) WorkflowRuns() ([]domain.WorkflowRun, error) {
	runs, err := a.store.ListWorkflowRunsForWorkspace(context.Background(), a.currentWorldID(), 100)
	for index := range runs {
		runs[index] = publicWorkflowRun(runs[index])
	}
	return runs, err
}

func (a *App) WorkflowRunDetails(id string) (domain.WorkflowRun, error) {
	run, err := a.store.GetWorkflowRun(context.Background(), id)
	if err != nil {
		return domain.WorkflowRun{}, err
	}
	if err = a.guardWorld(run.WorkspaceID); err != nil {
		return domain.WorkflowRun{}, err
	}
	for index := range run.StepRuns {
		if run.StepRuns[index].RunID == "" {
			continue
		}
		child, childErr := a.store.GetRun(context.Background(), run.StepRuns[index].RunID)
		if childErr != nil {
			continue
		}
		run.StepRuns[index].Status = child.Status
	}
	return publicWorkflowRun(run), nil
}

func (a *App) CancelWorkflowRun(id string) error {
	run, err := a.store.GetWorkflowRun(context.Background(), id)
	if err != nil {
		return err
	}
	if err = a.guardWorld(run.WorkspaceID); err != nil {
		return err
	}
	return a.workflowManager.Cancel(id)
}

type WorkflowStepCompletion struct {
	ClaimToken string           `json:"claimToken"`
	Status     domain.RunStatus `json:"status"`
	Result     string           `json:"result,omitempty"`
	Error      string           `json:"error,omitempty"`
}

func (a *App) ClaimWorkflowStep(runID, stepID string) (map[string]string, error) {
	token, err := a.workflowManager.Claim(runID, stepID)
	if err != nil {
		return nil, err
	}
	return map[string]string{"claimToken": token}, nil
}
func (a *App) HeartbeatWorkflowStep(runID, stepID, claimToken string) error {
	return a.workflowManager.Heartbeat(runID, stepID, claimToken)
}
func (a *App) CompleteWorkflowStep(runID, stepID string, completion WorkflowStepCompletion) error {
	return a.workflowManager.Complete(runID, stepID, completion.ClaimToken, completion.Status, completion.Result, completion.Error)
}

func (a *App) ValidateWorkflow(workflow domain.AgentWorkflow) error {
	profiles, err := a.storedProfiles(context.Background())
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		ids = append(ids, profile.ID)
	}
	return storage.ValidateWorkflow(workflow, ids...)
}
