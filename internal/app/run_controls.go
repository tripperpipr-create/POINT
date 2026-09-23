package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"local-agent-workbench/internal/agent"
	"local-agent-workbench/internal/attachments"
	"local-agent-workbench/internal/changesets"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/sandbox"
	"local-agent-workbench/internal/storage"
)

// HubBudgetSettings is the project-scoped spending policy configured by the user.
// Zero limits disable the corresponding period. HardStop turns the limits from
// warnings into a launch guard enforced by the runtime.
type HubBudgetSettings struct {
	WorkspaceID  string `json:"workspaceId"`
	DailyCents   int64  `json:"dailyCents"`
	MonthlyCents int64  `json:"monthlyCents"`
	HardStop     bool   `json:"hardStop"`
}

func (a *App) resumeStructuredExecutionIfSafe(previous *domain.ExecutionInstance, apiKey string) (domain.Run, error) {
	if previous == nil || strings.TrimSpace(previous.RunID) == "" {
		return domain.Run{}, nil
	}
	checkpoint, err := a.store.LatestRunCheckpoint(context.Background(), previous.RunID)
	if err != nil {
		return domain.Run{}, errors.New("unknown_outcome: исполнение уже запускалось; сохранённая песочница требует восстановления из проверенной точки, повтор задания с начала запрещён")
	}
	if !checkpoint.Resumable() {
		return domain.Run{}, errors.New("unknown_outcome: run stopped mid-action and cannot be safely continued")
	}
	if previous.Status != domain.RunPaused && previous.Status != domain.RunInterrupted {
		run, runErr := a.store.GetRun(context.Background(), previous.RunID)
		if runErr != nil {
			return domain.Run{}, errors.New("unknown_outcome: исполнение уже запускалось; сохранённая песочница требует восстановления из проверенной точки, повтор задания с начала запрещён")
		}
		if run.Status != domain.RunPaused && run.Status != domain.RunInterrupted {
			return domain.Run{}, errors.New("unknown_outcome: исполнение уже запускалось; сохранённая песочница требует восстановления из проверенной точки, повтор задания с начала запрещён")
		}
	}
	return a.ResumeRun(previous.RunID, ResumeRunRequest{APIKey: apiKey})
}

func (a *App) PauseRun(runID string) (domain.Run, error) {
	run, err := a.store.GetRun(context.Background(), runID)
	if err != nil {
		return domain.Run{}, err
	}
	if err = a.guardWorld(run.WorkspaceID); err != nil {
		return domain.Run{}, err
	}
	if err = a.engine.Pause(runID); err != nil {
		return domain.Run{}, err
	}
	run, err = a.store.GetRun(context.Background(), runID)
	if err != nil {
		return domain.Run{}, err
	}
	return publicRun(run), nil
}

type ResumeRunRequest struct {
	APIKey string `json:"apiKey,omitempty"`
	Extend bool   `json:"extend,omitempty"`
}

func (a *App) ResumeRun(runID string, request ...ResumeRunRequest) (domain.Run, error) {
	var req ResumeRunRequest
	if len(request) > 0 {
		req = request[0]
	}
	run, err := a.store.GetRun(context.Background(), runID)
	if err != nil {
		return domain.Run{}, err
	}
	if err = a.guardWorld(run.WorkspaceID); err != nil {
		return domain.Run{}, err
	}
	if a.engine.IsActiveRun(runID) {
		if req.Extend {
			if err = a.engine.ExtendActiveTime(runID); err != nil {
				return domain.Run{}, err
			}
		}
		if err = a.engine.Resume(runID); err != nil {
			return domain.Run{}, err
		}
		run, err = a.store.GetRun(context.Background(), runID)
		if err != nil {
			return domain.Run{}, err
		}
		return publicRun(run), nil
	}
	checkpoint, err := a.store.LatestRunCheckpoint(context.Background(), runID)
	if err != nil {
		return domain.Run{}, fmt.Errorf("run is not active and has no resumable checkpoint: %w", err)
	}
	if !checkpoint.Resumable() {
		return domain.Run{}, errors.New("unknown_outcome: run stopped mid-action and cannot be safely continued")
	}
	if run.Status != domain.RunPaused && run.Status != domain.RunInterrupted {
		return domain.Run{}, fmt.Errorf("run status %s cannot be resumed from checkpoint", run.Status)
	}
	ws, err := a.requireWorkspace()
	if err != nil {
		return domain.Run{}, err
	}
	if run.WorkspaceID != ws.ID {
		return domain.Run{}, errors.New("run does not belong to the open workspace")
	}
	snapshot := run.ConfigurationSnapshot
	var brief *domain.TaskBrief
	if len(checkpoint.TaskBriefJSON) > 0 {
		var stored domain.TaskBrief
		if unmarshalErr := json.Unmarshal(checkpoint.TaskBriefJSON, &stored); unmarshalErr == nil {
			brief = &stored
		}
	}
	execID := checkpoint.ExecutionID
	questID := checkpoint.QuestID
	flowRunID := checkpoint.FlowRunID
	flowNodeID := checkpoint.FlowNodeID
	sandboxPath := checkpoint.SandboxPath
	sandboxImage := ""
	if execution, execErr := a.store.GetExecutionByRunID(context.Background(), runID); execErr == nil {
		if snapshot.SchemaVersion == 0 {
			snapshot = execution.Snapshot
		}
		if brief == nil && execution.QuestID != "" {
			brief, _ = a.taskBriefForQuest(context.Background(), execution.WorkspaceID, execution.QuestID)
		}
		execID = execution.ID
		questID = execution.QuestID
		flowRunID = execution.FlowRunID
		flowNodeID = execution.FlowNodeID
		if sandboxRecord, sandboxErr := a.store.GetSandboxByExecution(context.Background(), execution.ID); sandboxErr == nil {
			sandboxPath = sandboxRecord.Path
			sandboxImage = sandbox.ExecutionImageForRecord(sandboxRecord)
		}
	}
	if snapshot.SchemaVersion != 3 {
		return domain.Run{}, errors.New("resumed runs require an immutable schema v3 configuration snapshot")
	}
	if req.Extend {
		if checkpoint.ActiveSecondsBudget <= 0 {
			return domain.Run{}, errors.New("active time budget is not enabled for this run")
		}
		if checkpoint.ActiveTimeExtensions >= 1 {
			return domain.Run{}, errors.New("active time may be extended only once without a new task approval")
		}
		checkpoint.ActiveTimeExtensions = 1
		if saveErr := a.store.SaveRunCheckpoint(context.Background(), checkpoint); saveErr != nil {
			return domain.Run{}, saveErr
		}
	}
	projectAgentID := ""
	onFinished := func(finished domain.Run) {
		var sandboxRecord domain.SandboxRecord
		var questID string
		if execution, execErr := a.store.GetExecutionByRunID(context.Background(), finished.ID); execErr == nil {
			execution.Status = finished.Status
			execution.Error = finished.Error
			execution.Result = finished.Result
			execution.FinishedAt = finished.FinishedAt
			execution.DurationMs = finished.DurationMs
			_ = a.store.SaveExecution(context.Background(), execution)
			projectAgentID = execution.ProjectAgentID
			questID = execution.QuestID
			if sandbox, sandboxErr := a.store.GetSandboxByExecution(context.Background(), execution.ID); sandboxErr == nil {
				sandboxRecord = sandbox
			}
		}
		if learningErr := a.recordRunLearningEvidence(context.Background(), finished, projectAgentID); learningErr != nil {
			_ = learningErr
		}
		a.queueAgentImprovement(finished, projectAgentID, req.APIKey)
		if sandboxRecord.ID == "" || (finished.Status != domain.RunCompleted && finished.Status != domain.RunFailed && finished.Status != domain.RunCancelled && finished.Status != domain.RunInterrupted) {
			return
		}
		baselinePath, dependencies, lineageErr := a.changeSetLineage(ws.ID, sandboxRecord)
		if lineageErr != nil {
			return
		}
		brief, _ := a.taskBriefForQuest(context.Background(), ws.ID, questID)
		if brief != nil && !brief.Permissions.WriteFiles {
			return
		}
		applier := changesets.Applier{Store: a.store}
		built, buildErr := applier.BuildFromSandbox(context.Background(), changesets.BuildRequest{
			WorkspaceID: ws.ID, ExecutionID: execID, QuestID: questID,
			Title: "Changes from " + finished.ID, WorkspacePath: ws.Path,
			BaselinePath: baselinePath, SandboxPath: sandboxRecord.Path, DependsOn: dependencies,
		})
		if buildErr != nil || built.ID == "" || len(built.Items) == 0 {
			return
		}
		if sandboxRecord.Kind == "live" && built.Status == domain.ChangeSetPending {
			_, _ = applier.Apply(context.Background(), ws.Path, built.ID)
		}
	}
	continued, err := a.engine.ContinueFromCheckpoint(agent.StartInput{
		TaskBrief: brief, Configuration: snapshot, Workspace: ws, SandboxPath: sandboxPath,
		SandboxImage: sandboxImage,
		ExecutionID:  execID, QuestID: questID, FlowRunID: flowRunID, FlowNodeID: flowNodeID,
		Task: run.Task, APIKey: req.APIKey, ContextItems: run.ContextItems,
		ServerProfiles: serverProfileBridge{app: a}, DBSource: a.dbToolAccess(),
		TeamBus:    a,
		OnFinished: onFinished,
	}, run, checkpoint)
	if err != nil {
		return domain.Run{}, err
	}
	return publicRun(continued), nil
}

func (a *App) ExtendActiveTime(runID string, apiKey string) (domain.Run, error) {
	run, err := a.store.GetRun(context.Background(), runID)
	if err != nil {
		return domain.Run{}, err
	}
	if err = a.guardWorld(run.WorkspaceID); err != nil {
		return domain.Run{}, err
	}
	if a.engine.IsActiveRun(runID) {
		if err = a.engine.ExtendActiveTime(runID); err != nil {
			return domain.Run{}, err
		}
		if err = a.engine.Resume(runID); err != nil && !strings.Contains(err.Error(), "not paused") {
			return domain.Run{}, err
		}
		run, err = a.store.GetRun(context.Background(), runID)
		if err != nil {
			return domain.Run{}, err
		}
		return publicRun(run), nil
	}
	return a.ResumeRun(runID, ResumeRunRequest{APIKey: apiKey, Extend: true})
}

func (a *App) InjectRunMessage(runID, message, learningIntent string) error {
	if err := a.guardRunControl(runID); err != nil {
		return err
	}
	return a.engine.InjectRunMessage(runID, message, learningIntent)
}

func (a *App) ForbidRunFile(runID, path string) error {
	if err := a.guardRunControl(runID); err != nil {
		return err
	}
	return a.engine.ForbidRunFile(runID, path)
}

func (a *App) AmendRunContext(runID string, action domain.ContextAmendAction, itemID string) error {
	if err := a.guardRunControl(runID); err != nil {
		return err
	}
	return a.engine.AmendRunContext(runID, action, itemID)
}

func (a *App) guardRunControl(runID string) error {
	run, err := a.store.GetRun(context.Background(), runID)
	if err != nil {
		return err
	}
	return a.guardWorld(run.WorkspaceID)
}

// AddRunContext resolves IDE file references on the backend and queues the
// resulting immutable snapshots for the next safe checkpoint of an active run.
func (a *App) AddRunContext(runID string, inputs []domain.RunContextInput) (domain.ContextPreview, error) {
	if !a.engine.IsActiveRun(runID) {
		return domain.ContextPreview{}, errors.New("run is not active")
	}
	workspaceView, err := a.requireWorkspace()
	if err != nil {
		return domain.ContextPreview{}, err
	}
	run, err := a.store.GetRun(context.Background(), runID)
	if err != nil {
		return domain.ContextPreview{}, err
	}
	if run.WorkspaceID != workspaceView.ID {
		return domain.ContextPreview{}, errors.New("run does not belong to the open workspace")
	}
	fs, err := a.fs()
	if err != nil {
		return domain.ContextPreview{}, err
	}
	preview, err := attachments.Resolve(fs, inputs)
	if err != nil {
		return domain.ContextPreview{}, err
	}
	for index := range preview.Items {
		item := &preview.Items[index]
		if strings.TrimSpace(item.Category) == "" {
			item.Category = "Live context"
		}
		if strings.TrimSpace(item.AddedBy) == "" {
			item.AddedBy = "user"
		}
		if strings.TrimSpace(item.Reason) == "" {
			item.Reason = "Added to the active execution from the IDE"
		}
		if strings.TrimSpace(item.Source) == "" {
			item.Source = run.ID
		}
		if item.Relevance == 0 {
			item.Relevance = 1
		}
		item.Pending = true
	}
	queued := append([]domain.RunContextItem(nil), preview.Items...)
	for index := range queued {
		queued[index].Pending = false
	}
	if err = a.engine.AddRunContext(runID, queued); err != nil {
		return domain.ContextPreview{}, err
	}
	preview.Active = true
	preview.PendingItems = len(preview.Items)
	preview.Items = publicContextItems(preview.Items)
	return preview, nil
}

func (a *App) RunContextInspector(runID string) (domain.ContextPreview, error) {
	run, err := a.store.GetRun(context.Background(), runID)
	if err != nil {
		return domain.ContextPreview{}, err
	}
	if err = a.guardWorld(run.WorkspaceID); err != nil {
		return domain.ContextPreview{}, err
	}
	profile := run.ConfigurationSnapshot.Profile
	systemMessage := agent.SystemMessage(profile, run.ConfigurationSnapshot.CustomTools)
	configuration, _ := json.MarshalIndent(map[string]any{
		"agentId": profile.ID, "name": profile.Name, "role": profile.RoleDescription,
		"provider": profile.Provider, "model": profile.Model, "fallbackModels": profile.FallbackModels,
		"allowedTools": profile.AllowedTools, "toolPolicies": profile.ToolPolicies,
		"maxSteps": profile.MaxSteps, "maxDurationSeconds": profile.MaxDurationSeconds,
		"contextWindowTokens": profile.ContextWindowTokens, "maxOutputTokens": profile.MaxOutputTokens,
	}, "", "  ")
	items := []domain.RunContextItem{
		{ID: "system:" + run.ID, Kind: domain.ContextText, Label: "Compiled system prompt", Content: systemMessage, Category: "System", AddedBy: "prompt-compiler", Source: "configuration-snapshot", Reason: "Immutable runtime instruction"},
		{ID: "agent:" + run.ID, Kind: domain.ContextText, Label: "Agent configuration", Content: string(configuration), Category: "Agent", AddedBy: "runtime", Source: "configuration-snapshot", Reason: "Model, tools, policies and limits captured at start"},
		{ID: "task:" + run.ID, Kind: domain.ContextText, Label: "Quest task", Content: run.Task, Category: "Quest", AddedBy: "user-or-orchestrator", Source: run.ID, Reason: "Requested outcome and acceptance criteria"},
	}
	attached := publicContextItems(run.ContextItems)
	for index := range attached {
		attached[index].Amendable = true
		if attached[index].Category == "" {
			attached[index].Category = "Retrieved context"
		}
		if attached[index].AddedBy == "" {
			attached[index].AddedBy = "run-input"
		}
	}
	items = append(items, attached...)
	pending := a.engine.RunAmendments(runID)
	pendingCount := 0
	for _, amendment := range pending.ContextAmends {
		if amendment.Action != domain.ContextAmendAdd || amendment.Item == nil {
			continue
		}
		item := publicContextItems([]domain.RunContextItem{*amendment.Item})[0]
		item.Category = "Live context · queued"
		item.Reason = "Will be applied before the next model turn"
		item.Amendable = false
		item.Pending = true
		items = append(items, item)
		pendingCount++
	}
	events, eventErr := a.store.ListByRun(context.Background(), runID)
	if eventErr == nil {
		toolItems := make([]domain.RunContextItem, 0, 20)
		for _, event := range events {
			if event.Type != domain.EventToolFinished {
				continue
			}
			content := string(event.Data)
			if len(content) > 32*1024 {
				content = content[:32*1024] + "\n[truncated by Context Inspector]"
			}
			toolItems = append(toolItems, domain.RunContextItem{
				ID: event.ID, Kind: domain.ContextText, Label: fmt.Sprintf("Tool result · step %d", event.Step),
				Content: content, Category: "Tool results", AddedBy: "tool-runtime", Source: string(event.Type),
				Reason: "Evidence returned to the model during execution",
			})
			if len(toolItems) > 20 {
				toolItems = toolItems[len(toolItems)-20:]
			}
		}
		items = append(items, toolItems...)
	}
	preview := domain.ContextPreview{Items: items, Active: a.engine.IsActiveRun(runID), PendingItems: pendingCount}
	for index := range preview.Items {
		preview.Items[index].Size = int64(len(preview.Items[index].Content))
		if preview.Items[index].SourceSize == 0 {
			preview.Items[index].SourceSize = preview.Items[index].Size
		}
		preview.Items[index].TokenEstimate = agent.EstimateContextItemTokens(preview.Items[index])
		preview.TotalContextBytes += preview.Items[index].Size
		preview.TotalSourceBytes += preview.Items[index].SourceSize
		preview.EstimatedTokens += preview.Items[index].TokenEstimate
	}
	if pendingCount > 0 {
		preview.Warnings = append(preview.Warnings, fmt.Sprintf("Новый контекст (%d) будет подключён на следующем безопасном шаге", pendingCount))
	}
	return preview, nil
}

func (a *App) GetFlow(flowID string) (domain.FlowGraph, error) {
	return a.store.GetFlow(context.Background(), flowID)
}

func (a *App) ResolveChangeSetConflict(changeSetID string, req changesets.ResolveRequest) (domain.ChangeSet, error) {
	applier := changesets.Applier{Store: a.store}
	set, err := applier.ResolveConflict(context.Background(), changeSetID, req)
	if err != nil {
		return domain.ChangeSet{}, err
	}
	ws, err := a.requireWorkspace()
	if err != nil {
		return set, err
	}
	result, err := applier.Apply(context.Background(), ws.Path, changeSetID)
	if err != nil {
		return set, err
	}
	return result.ChangeSet, nil
}

func (a *App) SaveHubBudget(settings HubBudgetSettings) (HubBudgetSettings, error) {
	workspace, err := a.requireWorkspace()
	if err != nil {
		return HubBudgetSettings{}, err
	}
	if settings.WorkspaceID != "" && settings.WorkspaceID != workspace.ID {
		return HubBudgetSettings{}, errors.New("budget workspace does not match the open project")
	}
	settings.WorkspaceID = workspace.ID
	if err := validateHubBudgetSettings(settings); err != nil {
		return HubBudgetSettings{}, err
	}
	payload, err := json.Marshal(settings)
	if err != nil {
		return HubBudgetSettings{}, err
	}
	if err := a.store.SaveSetting(context.Background(), hubBudgetSettingKey(workspace.ID), string(payload)); err != nil {
		return HubBudgetSettings{}, err
	}
	return settings, nil
}

func (a *App) loadHubBudgetSettings(ctx context.Context, workspaceID string) (HubBudgetSettings, error) {
	settings := HubBudgetSettings{WorkspaceID: workspaceID}
	if workspaceID != "" {
		value, err := a.store.Setting(ctx, hubBudgetSettingKey(workspaceID))
		if err == nil {
			if err := json.Unmarshal([]byte(value), &settings); err != nil {
				return HubBudgetSettings{}, fmt.Errorf("decode project budget: %w", err)
			}
			settings.WorkspaceID = workspaceID
			if err := validateHubBudgetSettings(settings); err != nil {
				return HubBudgetSettings{}, err
			}
			return settings, nil
		}
		if !storage.IsNotFound(err) {
			return HubBudgetSettings{}, err
		}
	}

	// Read the pre-project-scoping settings as a compatibility fallback. A new
	// save writes only the workspace-specific record and no longer leaks a
	// project's budget into other worlds.
	if value, err := a.store.Setting(ctx, "hub.budgetDailyCents"); err == nil {
		settings.DailyCents = parseInt64Setting(value)
	}
	if value, err := a.store.Setting(ctx, "hub.budgetMonthlyCents"); err == nil {
		settings.MonthlyCents = parseInt64Setting(value)
	}
	if value, err := a.store.Setting(ctx, "hub.budgetHardStop"); err == nil {
		settings.HardStop = strings.EqualFold(strings.TrimSpace(value), "true") || value == "1"
	}
	if value, err := a.store.Setting(ctx, "hub.preferences"); err == nil && strings.TrimSpace(value) != "" {
		var preferences map[string]any
		if json.Unmarshal([]byte(value), &preferences) == nil {
			if settings.DailyCents == 0 {
				settings.DailyCents = int64FromAny(preferences["budgetDailyCents"])
			}
			if settings.MonthlyCents == 0 {
				settings.MonthlyCents = int64FromAny(preferences["budgetMonthlyCents"])
			}
			if !settings.HardStop {
				settings.HardStop = boolFromAny(preferences["budgetHardStop"])
			}
		}
	}
	if err := validateHubBudgetSettings(settings); err != nil {
		return HubBudgetSettings{}, err
	}
	return settings, nil
}

func hubBudgetSettingKey(workspaceID string) string {
	return "hub.budget." + workspaceID
}

func validateHubBudgetSettings(settings HubBudgetSettings) error {
	if settings.DailyCents < 0 || settings.MonthlyCents < 0 {
		return errors.New("budget limits cannot be negative")
	}
	return nil
}

func (a *App) usageCostCents(records []domain.UsageRecord, since time.Time) int64 {
	var total int64
	for _, record := range records {
		if record.CreatedAt.Before(since) {
			continue
		}
		if record.CostCents != nil {
			total += *record.CostCents
		}
	}
	return total
}

func (a *App) enforceHubBudget(workspaceID string) error {
	ctx := context.Background()
	settings, err := a.loadHubBudgetSettings(ctx, workspaceID)
	if err != nil {
		return err
	}
	if !settings.HardStop || (settings.DailyCents <= 0 && settings.MonthlyCents <= 0) {
		return nil
	}
	usage, err := a.store.ListUsageRecords(ctx, workspaceID, 5000)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	if settings.DailyCents > 0 && a.usageCostCents(usage, dayStart) >= settings.DailyCents {
		return errors.New("daily hub budget exceeded; new runs are blocked until budget resets or hard stop is disabled")
	}
	if settings.MonthlyCents > 0 && a.usageCostCents(usage, monthStart) >= settings.MonthlyCents {
		return errors.New("monthly hub budget exceeded; new runs are blocked until budget resets or hard stop is disabled")
	}
	return nil
}

func (a *App) enforceQuestBudget(workspaceID, questID string, expectedTokens int64, profile domain.AgentProfile) error {
	if strings.TrimSpace(questID) == "" {
		return nil
	}
	// Preflight повторяет правило резервации: токены бесплатного рантайма не
	// расходуют токеновый потолок квеста, поэтому и предстартовая проверка не
	// должна закрывать перед ними дверь.
	if !domain.RuntimeChargesForTokens(profile.Provider, profile.ProviderPreset) {
		return nil
	}
	return a.store.CheckQuestBudget(context.Background(), workspaceID, questID, expectedTokens)
}

func questDescendsFrom(questID, ancestorID string, byID map[string]*domain.Quest) bool {
	visited := map[string]bool{}
	for questID != "" && !visited[questID] {
		if questID == ancestorID {
			return true
		}
		visited[questID] = true
		quest := byID[questID]
		if quest == nil {
			return false
		}
		questID = quest.ParentID
	}
	return false
}

func parseInt64Setting(value string) int64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0
	}
	return parsed
}

func int64FromAny(value any) int64 {
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case int:
		return int64(typed)
	case int64:
		return typed
	case string:
		return parseInt64Setting(typed)
	default:
		return 0
	}
}

func boolFromAny(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "true") || typed == "1"
	case float64:
		return typed != 0
	default:
		return false
	}
}

func budgetWarning(used, limit int64) string {
	if limit <= 0 || used*100 < limit*80 {
		return ""
	}
	return fmt.Sprintf("использовано не менее 80%% бюджета (%d/%d центов)", used, limit)
}

func isActiveFlowRunStatus(status domain.RunStatus) bool {
	switch status {
	case domain.RunRunning, domain.RunWaiting, domain.RunPaused, domain.RunPending:
		return true
	default:
		return false
	}
}

func flowHasActiveRun(store *storage.SQLite, flowID string) error {
	runs, err := store.ListFlowRunsByFlowID(context.Background(), flowID)
	if err != nil {
		return err
	}
	for _, run := range runs {
		if isActiveFlowRunStatus(run.Status) {
			return fmt.Errorf("flow %s has active run %s with status %s; graph is immutable while running", flowID, run.ID, run.Status)
		}
	}
	return nil
}
