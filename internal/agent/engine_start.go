// Начало прогона и продолжение с чекпоинта.
package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/executors"
	"local-agent-workbench/internal/observability"
	"local-agent-workbench/internal/providers"
	"local-agent-workbench/internal/security"
	"local-agent-workbench/internal/textutil"
	workbenchtools "local-agent-workbench/internal/tools"
	"local-agent-workbench/internal/workspace"
)

type StartInput struct {
	TaskBrief     *domain.TaskBrief
	Configuration domain.RunConfigurationSnapshot
	Workspace     domain.Workspace
	// SandboxPath, when set, is the isolated FS root for tools. Live workspace stays read-only until Change Set apply.
	SandboxPath string
	// SandboxImage is selected by trusted stack policy, never by the model.
	SandboxImage string
	// RunID and InitialBudgetReservationID are set only by an atomic launch
	// commit. The worker reuses those durable records instead of creating a
	// visibility gap between persistence and the first model request.
	RunID                      string
	AgentID                    string
	StartedAt                  time.Time
	InitialBudgetReservationID string
	ExecutionID                string
	QuestID                    string
	FlowRunID                  string
	FlowNodeID                 string
	// CompletionCheckKind, when set (e.g. "merged-result"), is stamped on EventCompletionChecked.
	CompletionCheckKind string
	// StageRole is recorded on EventRunStarted for QuestOutcome stage preference.
	StageRole string
	// ForbiddenPaths seed mid-run path denies from the stage work contract.
	ForbiddenPaths []string
	Task           string
	APIKey         string
	ContextItems   []domain.RunContextItem
	ServerProfiles workbenchtools.ServerProfileSource
	DBSource       workbenchtools.DBConnectionSource
	TeamBus        workbenchtools.TeamBus
	OnFinished     func(domain.Run)
}

func (e *Engine) Start(input StartInput) (domain.Run, error) {
	if strings.TrimSpace(input.Task) == "" {
		return domain.Run{}, errors.New("task is required")
	}
	if len(input.Task) > 64*1024 {
		return domain.Run{}, errors.New("task exceeds 64 KiB")
	}
	fsRoot := input.Workspace.Path
	if strings.TrimSpace(input.SandboxPath) != "" {
		fsRoot = input.SandboxPath
	}
	fs, err := workspace.Open(fsRoot)
	if err != nil {
		return domain.Run{}, err
	}
	if err := refuseQuarantinedBrief(input.TaskBrief); err != nil {
		return domain.Run{}, err
	}
	input.TaskBrief = copyExecutionBrief(input.TaskBrief)
	profile, restrictErr := RestrictTaskProfile(input.Configuration.Profile, input.TaskBrief, input.Configuration.CustomTools)
	if restrictErr != nil {
		return domain.Run{}, restrictErr
	}
	if err := ValidateTaskVerification(profile, input.TaskBrief, input.Configuration.CustomTools); err != nil {
		return domain.Run{}, err
	}
	if input.TaskBrief != nil && input.TaskBrief.Mode == domain.TaskModeProject && (input.SandboxPath == "" || !e.strongTaskSandbox()) {
		return domain.Run{}, errors.New("autonomous execution requires a strong Docker process boundary")
	}
	if input.TaskBrief != nil {
		input.Configuration = domain.NewRunConfigurationSnapshot(input.Configuration.ApplicationVersion, profile, input.Configuration.CustomTools, input.Configuration.CapturedAt)
	}
	// v1/v2 remain readable from storage, but Start always creates a new Run.
	// Accepting a legacy snapshot here would silently reintroduce mutable or
	// incomplete execution evidence through an alternate caller.
	if input.Configuration.SchemaVersion != 3 || profile.ID == "" {
		return domain.Run{}, errors.New("new runs require an immutable schema v3 configuration snapshot")
	}
	runID := strings.TrimSpace(input.RunID)
	if runID == "" {
		runID = domain.NewID("run")
	}
	agentID := strings.TrimSpace(input.AgentID)
	if agentID == "" {
		agentID = domain.NewID("agent")
	}
	startedAt := input.StartedAt.UTC()
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}
	run := domain.Run{ID: runID, AgentID: agentID, ProfileID: profile.ID, WorkspaceID: input.Workspace.ID, Task: input.Task, ContextItems: input.ContextItems, ConfigurationSnapshot: input.Configuration, Provider: string(profile.Provider), Model: profile.Model, Status: domain.RunRunning, ToolsUsed: []string{}, ChangedFiles: []string{}, StartedAt: startedAt}
	activeBudget := 0
	if input.TaskBrief != nil && input.TaskBrief.Budget.ActiveSeconds > 0 {
		activeBudget = input.TaskBrief.Budget.ActiveSeconds
		run.Controller = domain.RunControllerState{ActiveSecondsBudget: activeBudget}
	}
	maxDuration := wallClockLimit(profile.MaxDurationSeconds, activeBudget)
	ctx, cancel := context.WithTimeout(context.Background(), maxDuration)
	active := &activeRun{
		run: run, cancel: cancel, onFinished: input.OnFinished, finalized: make(chan struct{}), taskBrief: input.TaskBrief,
		clock: newActiveClock(activeBudget), sandboxPath: input.SandboxPath, sandboxImage: input.SandboxImage, apiKey: input.APIKey,
		initialBudgetReservationID: strings.TrimSpace(input.InitialBudgetReservationID),
		serverProfiles:             input.ServerProfiles, dbSource: input.DBSource, teamBus: input.TeamBus,
		correlation: runCorrelation{
			WorkspaceID: input.Workspace.ID,
			ExecutionID: input.ExecutionID, QuestID: input.QuestID,
			FlowRunID: input.FlowRunID, FlowNodeID: input.FlowNodeID,
			CompletionCheckKind: strings.TrimSpace(input.CompletionCheckKind),
		},
	}
	if len(input.ForbiddenPaths) > 0 {
		active.amendments.ForbiddenPaths = append([]string(nil), input.ForbiddenPaths...)
	}
	e.mu.Lock()
	if e.stopping {
		e.mu.Unlock()
		cancel()
		return domain.Run{}, errors.New("agent engine is stopping")
	}
	e.active[run.ID] = active
	e.wg.Add(1)
	e.mu.Unlock()
	if err = e.saveRun(run); err != nil {
		cancel()
		e.discardUnstarted(run.ID)
		return domain.Run{}, err
	}
	contextSummary := make([]map[string]any, 0, len(input.ContextItems))
	for _, item := range input.ContextItems {
		contextSummary = append(contextSummary, map[string]any{
			"id": item.ID, "kind": item.Kind, "label": item.Label, "path": item.Path,
			"format": item.Format, "mediaType": item.MediaType, "digest": item.Digest,
			"size": item.Size, "sourceSize": item.SourceSize, "extractedSize": item.ExtractedSize,
			"width": item.Width, "height": item.Height, "truncated": item.Truncated,
		})
	}
	startData := map[string]any{
		"task": input.Task, "profileId": profile.ID, "configurationSchemaVersion": input.Configuration.SchemaVersion,
		"workspace": input.Workspace.Path, "contextItems": contextSummary, "taskBrief": input.TaskBrief,
	}
	if role := strings.TrimSpace(input.StageRole); role != "" {
		startData["stageRole"] = role
	}
	if input.SandboxPath != "" {
		startData["sandboxPath"] = input.SandboxPath
		startData["executionId"] = input.ExecutionID
		startData["writesTarget"] = "sandbox"
	} else {
		startData["writesTarget"] = "workspace"
	}
	if err = e.publish(context.Background(), run, domain.EventRunStarted, "user", startData); err != nil {
		cancel()
		e.discardUnstarted(run.ID)
		return domain.Run{}, err
	}
	slog.Info("agent run started",
		"run_id", run.ID,
		"workspace_id", run.WorkspaceID,
		"profile_id", profile.ID,
		"provider", string(profile.Provider),
		"model", profile.Model,
		"task_preview", observability.Snippet(security.Redact(input.Task), 200),
		"tools", profile.AllowedTools,
		"max_steps", profile.MaxSteps,
		"context_items", len(input.ContextItems),
		"sandbox", input.SandboxPath != "",
		"execution_id", input.ExecutionID,
		"quest_id", input.QuestID,
	)
	registry, patches := buildToolRegistryWithExecution(fs, input.Configuration.CustomTools, input.ServerProfiles, input.DBSource, e.processExecutor, run.ID, input.SandboxImage, confirmedRemotesFromBrief(input.TaskBrief), e.networkGrants, input.TeamBus, active.correlation, profile)
	var model providers.Model
	var modelErr error
	if executors.KindForProvider(profile.Provider) == executors.KindPoint {
		model, modelErr = e.models(providers.Config{Kind: profile.Provider, Preset: profile.ProviderPreset, BaseURL: profile.BaseURL, APIKey: input.APIKey, APIVersion: profile.APIVersion, TimeoutSeconds: profile.MaxDurationSeconds, HeaderTimeoutSeconds: agentProviderHeaderTimeoutSeconds})
	} else if e.toolSessions == nil {
		modelErr = errors.New("headless CLI requires Point MCP runtime")
	}
	if modelErr != nil {
		cancel()
		now := time.Now().UTC()
		run.Status = domain.RunFailed
		run.Error = security.Redact(modelErr.Error())
		run.FinishedAt = &now
		if saveErr := e.saveRun(run); saveErr != nil {
			slog.Error("persist failed run after model setup error", "run_id", run.ID, "error", saveErr)
		}
		_ = e.publish(context.Background(), run, domain.EventRunFailed, "agent", map[string]any{"error": run.Error})
		e.discardUnstarted(run.ID)
		return run, modelErr
	}
	go func() {
		defer e.wg.Done()
		runCtx := observability.With(ctx, "run_id", run.ID, "model", profile.Model)
		e.execute(runCtx, active, profile, input.Configuration.CustomTools, model, registry, patches)
	}()
	return run, nil
}

// ContinueFromCheckpoint restarts a paused run after process restart using a
// durable round-boundary checkpoint. In-flight mutations are rejected.
func (e *Engine) ContinueFromCheckpoint(input StartInput, existing domain.Run, checkpoint domain.RunCheckpoint) (domain.Run, error) {
	if existing.ID == "" || checkpoint.RunID != existing.ID {
		return domain.Run{}, errors.New("checkpoint does not belong to the run")
	}
	if !checkpoint.Resumable() {
		return domain.Run{}, fmt.Errorf("%w: unknown_outcome: run stopped mid-action and cannot be safely continued", errToolJournalIntegrity)
	}
	if existing.Status != domain.RunPaused && existing.Status != domain.RunInterrupted {
		return domain.Run{}, fmt.Errorf("run status %s is not resumable from checkpoint", existing.Status)
	}
	if e.IsActiveRun(existing.ID) {
		return domain.Run{}, errors.New("run is already active")
	}
	fsRoot := input.Workspace.Path
	if strings.TrimSpace(checkpoint.SandboxPath) != "" {
		fsRoot = checkpoint.SandboxPath
	} else if strings.TrimSpace(input.SandboxPath) != "" {
		fsRoot = input.SandboxPath
	}
	fs, err := workspace.Open(fsRoot)
	if err != nil {
		return domain.Run{}, err
	}
	if err := refuseQuarantinedBrief(input.TaskBrief); err != nil {
		return domain.Run{}, err
	}
	input.TaskBrief = copyExecutionBrief(input.TaskBrief)
	profile, restrictErr := RestrictTaskProfile(input.Configuration.Profile, input.TaskBrief, input.Configuration.CustomTools)
	if restrictErr != nil {
		return domain.Run{}, restrictErr
	}
	if input.Configuration.SchemaVersion != 3 || profile.ID == "" {
		return domain.Run{}, errors.New("resumed runs require an immutable schema v3 configuration snapshot")
	}
	activeBudget := checkpoint.ActiveSecondsBudget
	if activeBudget <= 0 && input.TaskBrief != nil {
		activeBudget = input.TaskBrief.Budget.ActiveSeconds
	}
	maxDuration := wallClockLimit(profile.MaxDurationSeconds, activeBudget)
	ctx, cancel := context.WithTimeout(context.Background(), maxDuration)
	existing.Status = domain.RunRunning
	existing.FinishedAt = nil
	existing.Error = ""
	existing.Controller.PauseReason = ""
	existing.Controller.Resumable = true
	existing.Controller.CheckpointSeq = checkpoint.Seq
	existing.Controller.ActiveSecondsBudget = activeBudget
	existing.Controller.ActiveElapsedMs = checkpoint.ActiveElapsedMs
	existing.Controller.ActiveTimeExtensions = checkpoint.ActiveTimeExtensions
	active := &activeRun{
		run: existing, cancel: cancel, onFinished: input.OnFinished, finalized: make(chan struct{}), taskBrief: input.TaskBrief,
		clock: newActiveClock(activeBudget), sandboxPath: fsRoot, sandboxImage: input.SandboxImage, apiKey: input.APIKey,
		serverProfiles: input.ServerProfiles, dbSource: input.DBSource, teamBus: input.TeamBus, checkpointSeq: checkpoint.Seq,
		workspaceRevision: checkpoint.WorkspaceRevision,
		correlation: runCorrelation{
			WorkspaceID:         input.Workspace.ID,
			ExecutionID:         textutil.FirstNonEmpty(checkpoint.ExecutionID, input.ExecutionID),
			QuestID:             textutil.FirstNonEmpty(checkpoint.QuestID, input.QuestID),
			FlowRunID:           textutil.FirstNonEmpty(checkpoint.FlowRunID, input.FlowRunID),
			FlowNodeID:          textutil.FirstNonEmpty(checkpoint.FlowNodeID, input.FlowNodeID),
			CompletionCheckKind: strings.TrimSpace(input.CompletionCheckKind),
		},
	}
	active.clock.restore(checkpoint.ActiveElapsedMs, checkpoint.ActiveTimeExtensions, activeBudget)
	e.mu.Lock()
	if e.stopping {
		e.mu.Unlock()
		cancel()
		return domain.Run{}, errors.New("agent engine is stopping")
	}
	e.active[existing.ID] = active
	e.wg.Add(1)
	e.mu.Unlock()
	if err = e.saveRun(existing); err != nil {
		cancel()
		e.discardUnstarted(existing.ID)
		return domain.Run{}, err
	}
	registry, patches := buildToolRegistryWithExecution(fs, input.Configuration.CustomTools, input.ServerProfiles, input.DBSource, e.processExecutor, existing.ID, input.SandboxImage, confirmedRemotesFromBrief(input.TaskBrief), e.networkGrants, input.TeamBus, active.correlation, profile)
	modelTimeout := profile.MaxDurationSeconds
	if modelTimeout <= 0 {
		modelTimeout = 600
	}
	var model providers.Model
	var modelErr error
	if executors.KindForProvider(profile.Provider) == executors.KindPoint {
		model, modelErr = e.models(providers.Config{Kind: profile.Provider, Preset: profile.ProviderPreset, BaseURL: profile.BaseURL, APIKey: input.APIKey, APIVersion: profile.APIVersion, TimeoutSeconds: modelTimeout, HeaderTimeoutSeconds: agentProviderHeaderTimeoutSeconds})
	} else {
		modelErr = fmt.Errorf("%w: unknown_outcome: interrupted CLI process cannot be safely continued", errToolJournalIntegrity)
	}
	if modelErr != nil {
		cancel()
		now := time.Now().UTC()
		existing.Status = domain.RunFailed
		existing.Error = security.Redact(modelErr.Error())
		existing.FinishedAt = &now
		_ = e.saveRun(existing)
		e.discardUnstarted(existing.ID)
		return existing, modelErr
	}
	checkpointCopy := checkpoint
	go func() {
		defer e.wg.Done()
		runCtx := observability.With(ctx, "run_id", existing.ID, "model", profile.Model)
		e.executeWithCheckpoint(runCtx, active, profile, input.Configuration.CustomTools, model, registry, patches, &checkpointCopy)
	}()
	return existing, nil
}

func withCompletionCheckKind(active *activeRun, data map[string]any) map[string]any {
	if active == nil || strings.TrimSpace(active.correlation.CompletionCheckKind) == "" {
		return data
	}
	data["checkKind"] = active.correlation.CompletionCheckKind
	return data
}

func (e *Engine) execute(ctx context.Context, active *activeRun, profile domain.AgentProfile, customTools []domain.CustomTool, model providers.Model, registry *workbenchtools.Registry, patches *workbenchtools.PatchManager) {
	e.executeWithCheckpoint(ctx, active, profile, customTools, model, registry, patches, nil)
}
