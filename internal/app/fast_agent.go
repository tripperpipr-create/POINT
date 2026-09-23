package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"local-agent-workbench/internal/agent"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/environment"
	"local-agent-workbench/internal/sandbox"
	"local-agent-workbench/internal/security"
	"local-agent-workbench/internal/storage"
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

func (a *App) StartFastAgent(request FastAgentRequest) (domain.Run, error) {
	if databaseFileForRuntime() == legacyDatabaseFile {
		return a.startLegacyFastAgent(request)
	}
	return a.startFastAgentV2(request)
}

type preparedFastAgentV2 struct {
	run       preparedAgentRun
	order     domain.WorkOrder
	brief     domain.TaskBrief
	snapshots []domain.SourceSnapshot
}

func (a *App) startFastAgentV2(request FastAgentRequest) (domain.Run, error) {
	ctx := context.Background()
	prepared, err := a.prepareFastAgentV2(ctx, request)
	if err != nil {
		return domain.Run{}, err
	}
	order, err := a.prepareWorkOrderWorkspaceV2(ctx, prepared.order)
	if err != nil {
		return domain.Run{}, fmt.Errorf("prepare fast-agent work order: %w", err)
	}
	if err = a.requireRosterRunnableV2(ctx, order); err != nil {
		return domain.Run{}, err
	}
	brief, err := taskBriefFromWorkOrderV2(order)
	if err != nil {
		return domain.Run{}, fmt.Errorf("fast agent work order brief: %w", err)
	}
	brief.FastAgent = true
	brief, err = domain.ApproveTaskBrief(brief)
	if err != nil {
		return domain.Run{}, fmt.Errorf("approve fast agent brief: %w", err)
	}
	prepared.brief = brief
	launch, err := a.prepareFastAgentLaunchCommitV2(ctx, prepared, order)
	if err != nil {
		return domain.Run{}, err
	}
	createRequest := sandbox.CreateRequest{
		WorkspaceID: order.WorkspaceID, WorkspacePath: order.Workspace.Path, ExecutionID: launch.Execution.ID,
		Runtime:        environment.RuntimeRequirementsForWorkOrder(&order),
		PreferWorktree: order.Workspace.Isolation == "git_worktree", LiveWorkspace: false,
	}
	sandboxRecord, err := a.sandboxBackend.Create(ctx, createRequest)
	if err != nil {
		return domain.Run{}, fmt.Errorf("create fast-agent sandbox: %w", err)
	}
	if sandboxRecord.Kind == "live" {
		_ = a.sandboxBackend.Close(ctx, sandboxRecord, order.Workspace.Path)
		return domain.Run{}, errors.New("FastAgent v2 requires a separate execution workspace")
	}
	launch.Sandbox = sandboxRecord
	launch.Execution.SandboxID = sandboxRecord.ID
	approval, err := a.store.CommitFastAgentLaunchV2(ctx, launch)
	if err != nil {
		_ = a.sandboxBackend.Close(context.Background(), sandboxRecord, order.Workspace.Path)
		return domain.Run{}, fmt.Errorf("commit fast-agent launch: %w", err)
	}
	run, err := a.StartRun(StartRunRequest{
		ProfileID: prepared.run.projectAgentID, Task: strings.TrimSpace(request.Task), APIKey: request.APIKey,
		ContextItems: request.ContextItems, PreflightFingerprint: request.PreflightFingerprint,
		QuestID: approval.QuestID, ExecutionID: launch.Execution.ID,
		preparedRunID: launch.Run.ID, preparedAgentID: launch.Run.AgentID, preparedStartedAt: launch.Run.StartedAt,
		preparedConfiguration: &launch.Run.ConfigurationSnapshot, initialBudgetReservationID: launch.Reservation.ID,
		preparedAgent: &prepared.run,
	})
	if err != nil {
		quest, _ := a.workOrderQuestV2(ctx, order.WorkspaceID, approval.QuestID)
		return domain.Run{}, a.failFastAgentLaunchV2(ctx, approval, quest, launch.Execution, launch.Run, err)
	}
	slog.Info("fast agent v2 launched", "launch_mode", "fast_agent_v2", "work_order_id", order.ID,
		"quest_id", approval.QuestID, "execution_id", launch.Execution.ID, "run_id", run.ID)
	return run, nil
}

func (a *App) prepareFastAgentV2(ctx context.Context, request FastAgentRequest) (preparedFastAgentV2, error) {
	task := strings.TrimSpace(request.Task)
	if task == "" {
		return preparedFastAgentV2{}, errors.New("task is required")
	}
	if len(task) > 64*1024 {
		return preparedFastAgentV2{}, errors.New("task exceeds 64 KiB")
	}
	profileID := strings.TrimSpace(request.ProfileID)
	if profileID == "" {
		return preparedFastAgentV2{}, errors.New("profileId is required")
	}
	ws, err := a.requireWorkspace()
	if err != nil {
		return preparedFastAgentV2{}, err
	}
	if err = a.ensureCodingModelRouteReady(ws.ID); err != nil {
		return preparedFastAgentV2{}, err
	}
	prepared, err := a.prepareAgentRun(profileID, task, request.ContextItems, true)
	if err != nil {
		return preparedFastAgentV2{}, err
	}
	if prepared.projectAgentID == "" {
		return preparedFastAgentV2{}, errors.New("FastAgent v2 requires a project agent")
	}
	if request.PreflightFingerprint != "" && request.PreflightFingerprint != prepared.fingerprint {
		return preparedFastAgentV2{}, errors.New("agent preflight is stale; preview the updated task, profile, tools or context before launch")
	}
	if err = a.enforceRunnableSystemLifecycle(ctx); err != nil {
		return preparedFastAgentV2{}, err
	}
	if err = a.enforceHubBudget(ws.ID); err != nil {
		return preparedFastAgentV2{}, err
	}
	if strings.TrimSpace(prepared.profile.ConnectionID) == "" || strings.TrimSpace(prepared.profile.Model) == "" {
		return preparedFastAgentV2{}, errors.New("FastAgent v2 requires a configured model connection and model")
	}

	plan := environment.Analyze(ws.Path, ws.ID)
	verificationCommand := ""
	for _, command := range plan.Commands {
		if command.ProvidesVerification {
			verificationCommand = environmentCommandTextV2(command)
			break
		}
	}
	exitCode := 0
	criterion := domain.AcceptanceCriterion{ID: "done", Text: "Запрошенные изменения внесены в проект", Kind: "manual"}
	completion := domain.CompletionProfile{ID: "fast-agent-manual", Version: "1", Checks: []domain.CompletionCheck{{Kind: domain.CompletionCheckAcceptance}}}
	if verificationCommand != "" {
		arguments, _ := json.Marshal(map[string]any{"command": verificationCommand})
		criterion = domain.AcceptanceCriterion{
			ID: "done", Text: "Запрошенные изменения внесены, и проверка проекта проходит", Kind: "verification",
			Tool: "run_command", Arguments: arguments, ExpectedExitCode: &exitCode,
		}
		completion = domain.CompletionProfile{ID: "fast-agent-verified", Version: "1", Checks: []domain.CompletionCheck{
			{Kind: domain.CompletionCheckAcceptance}, {Kind: "automated_tests", Command: verificationCommand},
		}}
	}
	snapshots, refs := fastAgentSourceSnapshotsV2(ws.ID, prepared.context.Items)
	maxSteps := prepared.profile.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 64
	}
	order := domain.NormalizeWorkOrder(domain.WorkOrder{
		State: "ready", Goal: task, Scope: []string{"Изменения по запросу пользователя"},
		Criteria: []domain.AcceptanceCriterion{criterion}, Sources: refs,
		WorkspaceID: ws.ID, Workspace: domain.WorkspacePlan{Mode: "existing", Path: ws.Path},
		Stack:  fastAgentStackV2(plan),
		Roster: domain.AgentRosterPlan{Permanent: []domain.AgentDraft{{ID: prepared.projectAgentID, Existing: true}}},
		Routing: domain.ModelRoutingPolicy{
			Mode: "fixed", FixedConnectionID: prepared.profile.ConnectionID, FixedModel: prepared.profile.Model,
			FallbackMode: "wait", CostKnown: false, Certification: "experimental",
			Adapter: domain.AdapterCapabilityManifest{Tools: true, StructuredOutput: true, ContextTokens: prepared.profile.ContextWindowTokens},
		},
		Budget: domain.BudgetEnvelope{
			Preset: "medium", Tokens: 200000, ActiveSeconds: 3600, MaxParallel: 1,
			MaxReplans: 2, MaxAttempts: 3, MaxSteps: maxSteps, MaxProjectAgents: 1,
		},
		Completion: completion,
		Delivery:   domain.DeliveryPolicy{ApplyMode: "automatic", CommitMode: "none", KeepPartialDays: 30},
	})
	if isCleanGitWorkspace(ws.Path) {
		order.Workspace.Isolation = "git_worktree"
	} else {
		order.Workspace.Isolation = "snapshot"
	}
	brief, err := taskBriefFromWorkOrderV2(order)
	if err != nil {
		return preparedFastAgentV2{}, fmt.Errorf("fast agent work order brief: %w", err)
	}
	brief.State, brief.ApprovedVersion, brief.ApprovedDigest, brief.FastAgent = "ready", 0, "", true
	brief, err = domain.ApproveTaskBrief(brief)
	if err != nil {
		return preparedFastAgentV2{}, fmt.Errorf("fast agent brief: %w", err)
	}
	if err = a.validateTaskEnvironment(&brief); err != nil {
		return preparedFastAgentV2{}, err
	}
	restricted, err := agent.RestrictTaskProfile(prepared.profile, &brief, prepared.customTools)
	if err != nil {
		return preparedFastAgentV2{}, err
	}
	if err = agent.ValidateTaskVerification(restricted, &brief, prepared.customTools); err != nil {
		return preparedFastAgentV2{}, err
	}
	return preparedFastAgentV2{run: prepared, order: order, brief: brief, snapshots: snapshots}, nil
}

func (a *App) prepareFastAgentLaunchCommitV2(ctx context.Context, prepared preparedFastAgentV2, order domain.WorkOrder) (storage.FastAgentLaunchV2, error) {
	profile := prepared.run.profile
	if hasTool(profile.AllowedTools, "propose_patch") {
		profile.AllowedTools = appendUniqueStrings(profile.AllowedTools, "list_files")
	}
	restricted, err := agent.RestrictTaskProfile(profile, &prepared.brief, prepared.run.customTools)
	if err != nil {
		return storage.FastAgentLaunchV2{}, err
	}
	if err = agent.ValidateTaskVerification(restricted, &prepared.brief, prepared.run.customTools); err != nil {
		return storage.FastAgentLaunchV2{}, err
	}
	registry, _ := agent.BuildToolRegistryWithSources(prepared.run.fs, prepared.run.customTools, serverProfileBridge{app: a}, a.dbToolAccess(), restricted)
	definitions := registry.Definitions(restricted.AllowedTools)
	messages := agent.BuildStableMessages(restricted, prepared.run.context.Items, prepared.run.task, prepared.run.customTools)
	if instruction := agent.TaskContractInstructions(&prepared.brief); instruction != "" {
		messages[0].Content += "\n" + instruction
	}
	initialTokens := agent.EstimateModelInputTokens(messages, definitions)
	inputBudget := agent.ModelInputBudgetTokens(restricted)
	if initialTokens > inputBudget {
		return storage.FastAgentLaunchV2{}, fmt.Errorf("initial context needs about %d tokens, exceeding the %d-token input budget", initialTokens, inputBudget)
	}

	now := time.Now().UTC()
	questID := domain.NewID("quest")
	executionID := domain.NewID("execution")
	runID := domain.NewID("run")
	snapshot := domain.NewRunConfigurationSnapshot(Version, restricted, prepared.run.customTools, now)
	reservation, limits, err := a.prepareModelBudgetReservation(ctx, agent.ModelBudgetRequest{
		WorkspaceID: order.WorkspaceID, QuestID: questID, ExecutionID: executionID, RunID: runID,
		Provider: restricted.Provider, ProviderPreset: restricted.ProviderPreset, Model: restricted.Model,
		EstimatedInputTokens: int64(initialTokens), MaxOutputTokens: int64(restricted.MaxOutputTokens),
	})
	if err != nil {
		return storage.FastAgentLaunchV2{}, err
	}
	run := domain.Run{
		ID: runID, AgentID: domain.NewID("agent"), ProfileID: restricted.ID, WorkspaceID: order.WorkspaceID,
		Task: prepared.run.task, ContextItems: prepared.run.context.Items, ConfigurationSnapshot: snapshot,
		Provider: string(restricted.Provider), Model: restricted.Model, Status: domain.RunPending,
		ToolsUsed: []string{}, ChangedFiles: []string{}, StartedAt: now,
	}
	if prepared.brief.Budget.ActiveSeconds > 0 {
		run.Controller.ActiveSecondsBudget = prepared.brief.Budget.ActiveSeconds
	}
	execution := domain.ExecutionInstance{
		ID: executionID, WorkspaceID: order.WorkspaceID, ProjectAgentID: prepared.run.projectAgentID,
		QuestID: questID, RunID: runID, Task: prepared.run.task, Status: domain.RunPending,
		Snapshot: snapshot, StartedAt: now,
	}
	return storage.FastAgentLaunchV2{
		Order: order, Snapshots: prepared.snapshots, Brief: &prepared.brief, QuestID: questID,
		Execution: execution, Run: run, Reservation: reservation, BudgetLimits: limits,
		IdempotencyKey: "fast-agent-" + order.ID,
	}, nil
}

func fastAgentSourceSnapshotsV2(workspaceID string, items []domain.RunContextItem) ([]domain.SourceSnapshot, []domain.SourceSnapshotRef) {
	now := time.Now().UTC()
	snapshots := make([]domain.SourceSnapshot, 0, len(items))
	refs := make([]domain.SourceSnapshotRef, 0, len(items))
	for _, item := range items {
		digest := strings.TrimSpace(item.Digest)
		if digest == "" {
			sum := sha256.Sum256([]byte(item.Content + "\x00" + item.DataBase64))
			digest = "sha256:" + hex.EncodeToString(sum[:])
		}
		kind := string(item.Kind)
		if kind != "workspace_file" && kind != "image" {
			kind = "text"
		}
		snapshot := domain.SourceSnapshot{
			ID: domain.NewID("source"), WorkspaceID: workspaceID, Kind: kind, Label: item.Label,
			Locator: item.Path, Format: item.Format, MediaType: item.MediaType,
			ExtractedText: security.Redact(item.Content), Digest: digest,
			SourceBytes: item.SourceSize, ExtractedBytes: item.ExtractedSize, Warnings: []string{}, CreatedAt: now,
		}
		snapshots = append(snapshots, snapshot)
		refs = append(refs, sourceSnapshotRef(snapshot))
	}
	return snapshots, refs
}

func fastAgentStackV2(plan domain.EnvironmentPlan) domain.StackPresetRef {
	toolchains := make([]string, 0, len(plan.Runtime.Toolchains))
	for name := range plan.Runtime.Toolchains {
		toolchains = append(toolchains, strings.ToLower(strings.TrimSpace(name)))
	}
	sort.Strings(toolchains)
	category := "general"
	if len(toolchains) > 0 && toolchains[0] != "" {
		category = toolchains[0]
	}
	return domain.StackPresetRef{ID: "fast-" + category, Version: "1", Category: category, Source: "benchmark"}
}

func (a *App) markFastAgentMilestoneV2(ctx context.Context, approval domain.WorkOrderApproval, status domain.QuestStatus) error {
	runtimes, err := a.store.ListMilestoneRuntimesV2(ctx, approval.QuestID, approval.WorkOrder.Version)
	if err != nil {
		return err
	}
	if len(runtimes) != 1 {
		return fmt.Errorf("fast-agent work order requires exactly one milestone runtime, got %d", len(runtimes))
	}
	return a.markWorkOrderMilestoneV2(ctx, approval, runtimes[0], status, "", "")
}

func (a *App) failFastAgentLaunchV2(ctx context.Context, approval domain.WorkOrderApproval, quest domain.Quest, execution domain.ExecutionInstance, run domain.Run, cause error) error {
	now := time.Now().UTC()
	reason := security.Redact(cause.Error())
	if execution.ID != "" {
		if record, err := a.store.GetSandbox(ctx, execution.SandboxID); err == nil {
			if ws, wsErr := a.requireWorkspace(); wsErr == nil {
				_ = a.sandboxBackend.Close(ctx, record, ws.Path)
			}
			record.ClosedAt = &now
			if err = a.store.SaveSandbox(ctx, record); err != nil {
				slog.Warn("fast-agent sandbox close persistence failed", "sandbox_id", record.ID, "error", security.Redact(err.Error()))
			}
		}
		execution.Status = domain.RunFailed
		execution.Error = reason
		execution.FinishedAt = &now
		execution.DurationMs = now.Sub(execution.StartedAt).Milliseconds()
		if err := a.store.SaveExecution(ctx, execution); err != nil {
			slog.Warn("fast-agent execution failure persistence failed", "execution_id", execution.ID, "error", security.Redact(err.Error()))
		}
	}
	if run.ID != "" {
		run.Status = domain.RunFailed
		run.Error = reason
		run.FinishedAt = &now
		run.DurationMs = now.Sub(run.StartedAt).Milliseconds()
		if err := a.store.SaveRun(ctx, run); err != nil {
			slog.Warn("fast-agent run failure persistence failed", "run_id", run.ID, "error", security.Redact(err.Error()))
		}
		if err := a.store.ReleaseBudgetReservations(ctx, run.ID, now); err != nil {
			slog.Warn("fast-agent budget release failed", "run_id", run.ID, "error", security.Redact(err.Error()))
		}
	}
	if quest.ID != "" {
		a.blockWorkOrderFinalizationV2(ctx, quest, "FastAgent не запущен: "+reason, cause)
		_ = a.markFastAgentMilestoneV2(ctx, approval, domain.QuestBlocked)
	}
	return fmt.Errorf("fast agent v2 launch: %w", cause)
}

// startLegacyFastAgent is the rollback-only v1 path selected by
// POINT_AGENT_HUB_V2=0.
func (a *App) startLegacyFastAgent(request FastAgentRequest) (domain.Run, error) {
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
