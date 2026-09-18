package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
)

// FastAgentRequest launches a Cursor-style precise write run: one Send creates
// an approved FastAgent brief + Quest and starts the agent without Master interview.
type FastAgentRequest struct {
	ProfileID            string                   `json:"profileId"`
	Task                 string                   `json:"task"`
	APIKey               string                   `json:"apiKey"`
	ContextItems         []domain.RunContextInput `json:"contextItems,omitempty"`
	PreflightFingerprint string                   `json:"preflightFingerprint,omitempty"`
}

// StartFastAgent builds an approved precise FastAgent brief, persists a Quest,
// creates a pending execution, and starts the run. propose_patch is auto-approved;
// run_command stays ASK.
func (a *App) StartFastAgent(request FastAgentRequest) (domain.Run, error) {
	task := strings.TrimSpace(request.Task)
	if task == "" {
		return domain.Run{}, errors.New("task is required")
	}
	if len(task) > 64*1024 {
		return domain.Run{}, errors.New("task exceeds 64 KiB")
	}
	profileID := strings.TrimSpace(request.ProfileID)
	if profileID == "" {
		return domain.Run{}, errors.New("profileId is required")
	}
	title := task
	if first, _, ok := strings.Cut(title, "\n"); ok {
		title = first
	}
	runes := []rune(title)
	if len(runes) > 120 {
		title = string(runes[:120])
	}
	brief, err := domain.ApproveTaskBrief(domain.NormalizeTaskBrief(domain.TaskBrief{
		SourceRequest: task,
		Mode:          domain.TaskModePrecise,
		State:         "ready",
		Goal:          title,
		ResultKind:    "workspace_change",
		Scope:         []string{"Изменения по запросу пользователя"},
		Criteria: []domain.AcceptanceCriterion{{
			ID: "done", Text: "Запрошенные изменения внесены в проект", Kind: "manual",
		}},
		Permissions: domain.TaskPermissions{WriteFiles: true, ExecuteCommands: true},
		Budget:      domain.TaskBudget{Tokens: 200000, ActiveSeconds: 3600, MaxParallel: 1, MaxReplans: 2, MaxAttempts: 3},
		FastAgent:   true,
	}))
	if err != nil {
		return domain.Run{}, fmt.Errorf("fast agent brief: %w", err)
	}
	ws, err := a.requireWorkspace()
	if err != nil {
		return domain.Run{}, err
	}
	// FastAgent создаёт квест до StartRun, поэтому обязательный coding route
	// проверяется заранее: отказ подключения не должен оставлять фантомную
	// активную работу.
	if err = a.ensureCodingModelRouteReady(ws.ID); err != nil {
		return domain.Run{}, err
	}
	now := time.Now().UTC()
	quest := domain.Quest{
		ID: domain.NewID("quest"), WorkspaceID: ws.ID, Title: title, Description: task,
		Objectives: []string{brief.Goal}, DefinitionOfDone: []string{brief.Criteria[0].Text},
		Importance: domain.QuestNormal, Status: domain.QuestActive, Brief: &brief,
		BudgetTokens: brief.Budget.Tokens, CreatedAt: now, UpdatedAt: now,
	}
	if err = a.store.SaveQuest(context.Background(), quest); err != nil {
		return domain.Run{}, err
	}
	exec, err := a.startSandboxedExecution(profileID, task, quest.ID, "")
	if err != nil {
		return domain.Run{}, fmt.Errorf("fast agent execution: %w", err)
	}
	// Keep Task raw so ComposeQuestTask matches the execution.Task snapshot.
	return a.StartRun(StartRunRequest{
		ProfileID: profileID, Task: task, APIKey: request.APIKey,
		ContextItems:         request.ContextItems,
		PreflightFingerprint: request.PreflightFingerprint,
		QuestID:              quest.ID, ExecutionID: exec.ID,
	})
}
