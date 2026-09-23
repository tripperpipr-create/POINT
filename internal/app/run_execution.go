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
	"local-agent-workbench/internal/attachments"
	"local-agent-workbench/internal/changesets"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/egress"
	"local-agent-workbench/internal/environment"
	"local-agent-workbench/internal/flowruntime"
	"local-agent-workbench/internal/observability"
	"local-agent-workbench/internal/policy"
	"local-agent-workbench/internal/sandbox"
	"local-agent-workbench/internal/security"
	"local-agent-workbench/internal/workspace"
)

type preparedAgentRun struct {
	workspace      domain.Workspace
	fs             *workspace.FS
	profile        domain.AgentProfile
	projectAgentID string
	legacyProfile  bool
	customTools    []domain.CustomTool
	task           string
	context        domain.ContextPreview
	fingerprint    string
}

func (a *App) prepareAgentRun(profileID, task string, inputs []domain.RunContextInput, allowCursor bool) (preparedAgentRun, error) {
	return a.prepareAgentRunWithRoute(profileID, task, inputs, allowCursor, true)
}

func (a *App) prepareAgentRunWithRoute(profileID, task string, inputs []domain.RunContextInput, allowCursor, applyCoding bool) (preparedAgentRun, error) {
	a.mu.RLock()
	current := a.currentWorkspace
	currentFS := a.currentFS
	a.mu.RUnlock()
	if current == nil || currentFS == nil {
		return preparedAgentRun{}, errors.New("open a workspace before starting a run")
	}
	task = strings.TrimSpace(task)
	if task == "" {
		return preparedAgentRun{}, errors.New("task is required")
	}
	if len(task) > 64*1024 {
		return preparedAgentRun{}, errors.New("task exceeds 64 KiB")
	}
	var selected *domain.AgentProfile
	projectAgentID := ""
	legacyProfile := false
	// ProjectAgent is the canonical runnable identity in Hub mode. Resolve it
	// before the temporary legacy profile bridge, including requests that still
	// carry a blueprint/profile ID from an older client.
	agents, err := a.store.ListProjectAgents(context.Background(), current.ID)
	if err != nil {
		return preparedAgentRun{}, err
	}
	var matchedAgent *domain.ProjectAgent
	for index := range agents {
		if agents[index].ID == profileID {
			matchedAgent = &agents[index]
			break
		}
	}
	if matchedAgent == nil {
		for index := range agents {
			if agents[index].BlueprintID == profileID {
				matchedAgent = &agents[index]
				break
			}
		}
	}
	if matchedAgent != nil {
		profile := domain.ProfileFromProjectAgent(*matchedAgent)
		selected = &profile
		projectAgentID = matchedAgent.ID
		if err := a.enrichProjectAgentForRun(current.ID, *matchedAgent, selected, &inputs); err != nil {
			return preparedAgentRun{}, fmt.Errorf("prepare project agent %q: %w", matchedAgent.Name, err)
		}
	}
	if selected == nil {
		profiles, listErr := a.storedProfiles(context.Background())
		if listErr != nil {
			return preparedAgentRun{}, listErr
		}
		for index := range profiles {
			if profiles[index].ID == profileID {
				selected = &profiles[index]
				legacyProfile = true
				break
			}
		}
	}
	if selected == nil {
		return preparedAgentRun{}, errors.New("agent profile not found")
	}
	// Workspace coding route действует только на этот запуск: сохранённый
	// персонаж остаётся со своей моделью, а неизменяемый снимок прогона
	// запоминает фактически выбранный маршрут. Stage model binding
	// перезаписывает маршрут после prepare — не применяем coding, чтобы
	// дешёвый coding-host не остался на CLI-провайдере без connection.
	if applyCoding {
		if err := a.applyCodingModelRoute(current.ID, selected); err != nil {
			return preparedAgentRun{}, err
		}
	}
	if _, err := egress.CompileToolPolicies(selected.ToolPolicies); err != nil {
		return preparedAgentRun{}, fmt.Errorf("invalid controlled egress policy: %w", err)
	}
	// Адрес и версия API принадлежат подключению, а не агенту: иначе правка
	// шлюза требовала бы обойти каждый профиль, а Azure вовсе не заработал бы —
	// его api-version в профиле не задаётся.
	if err := a.applyConnectionEndpoint(selected); err != nil {
		return preparedAgentRun{}, err
	}
	if selected.Provider == domain.ProviderCursor && !allowCursor && strings.TrimSpace(a.SelfURL()) == "" {
		return preparedAgentRun{}, errors.New("Cursor Agent CLI запускается интерактивно из Point, пока ядру не задан локальный MCP-адрес")
	}
	customTools, err := a.store.ListCustomTools(context.Background())
	if err != nil {
		return preparedAgentRun{}, err
	}
	sort.Slice(customTools, func(i, j int) bool { return customTools[i].ID < customTools[j].ID })
	contextPreview, err := attachments.Resolve(currentFS, inputs)
	if err != nil {
		return preparedAgentRun{}, err
	}
	prepared := preparedAgentRun{
		workspace: *current, fs: currentFS, profile: *selected, projectAgentID: projectAgentID, legacyProfile: legacyProfile,
		customTools: customTools, task: task, context: contextPreview,
	}
	prepared.fingerprint, err = agentRunFingerprint(prepared)
	if err != nil {
		return preparedAgentRun{}, err
	}
	return prepared, nil
}

func agentRunFingerprint(prepared preparedAgentRun) (string, error) {
	contextItems := append([]domain.RunContextItem(nil), prepared.context.Items...)
	for index := range contextItems {
		contextItems[index].ID = ""
		contextItems[index].Content = ""
		contextItems[index].DataBase64 = ""
	}
	canonical := struct {
		Version     string                  `json:"version"`
		WorkspaceID string                  `json:"workspaceId"`
		Workspace   string                  `json:"workspace"`
		Task        string                  `json:"task"`
		Profile     domain.AgentProfile     `json:"profile"`
		CustomTools []domain.CustomTool     `json:"customTools"`
		Context     []domain.RunContextItem `json:"context"`
	}{Version: Version, WorkspaceID: prepared.workspace.ID, Workspace: prepared.workspace.Path, Task: prepared.task, Profile: prepared.profile, CustomTools: prepared.customTools, Context: contextItems}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func tokenEstimate(bytes int) int {
	if bytes <= 0 {
		return 0
	}
	return (bytes + 3) / 4
}

func hasTool(items []string, name string) bool {
	for _, item := range items {
		if item == name {
			return true
		}
	}
	return false
}

// PreviewAgentRun returns the exact immutable inputs prepared for a run without
// calling a provider, creating a run, emitting events or writing to storage.
func (a *App) PreviewAgentRun(request AgentRunPreviewRequest) (AgentRunPreview, error) {
	task := agent.ComposeQuestTask(request.Task, request.Goal, request.AcceptanceCriteria, request.Constraints)
	prepared, err := a.prepareAgentRun(request.ProfileID, task, request.ContextItems, true)
	if err != nil {
		return AgentRunPreview{}, err
	}
	registry, _ := agent.BuildToolRegistryWithSources(prepared.fs, prepared.customTools, serverProfileBridge{app: a}, a.dbToolAccess(), prepared.profile)
	definitions := registry.Definitions(prepared.profile.AllowedTools)
	catalog := make(map[string]domain.ToolCatalogItem)
	for _, item := range domain.BuiltInToolCatalog() {
		catalog[item.Name] = item
	}
	for _, customTool := range prepared.customTools {
		catalog[customTool.ID] = domain.ToolCatalogItem{
			Name: customTool.ID, DisplayName: customTool.DisplayName, Description: customTool.Description,
			Category: "execute", Risk: "CRITICAL", RequiresApproval: true, ProvidesVerification: customTool.ProvidesVerification,
		}
	}
	customToolsByID := make(map[string]domain.CustomTool, len(prepared.customTools))
	for _, customTool := range prepared.customTools {
		customToolsByID[customTool.ID] = customTool
	}
	toolsPreview := make([]AgentToolPreview, 0, len(definitions))
	approvalCount := 0
	policyEngine := policy.Engine{TrustedCustomTool: a.trustedCustomTool}
	for _, definition := range definitions {
		item := catalog[definition.Name]
		decision := policyEngine.Evaluate(prepared.profile, definition.Name)
		if decision.RequiresApproval {
			approvalCount++
		}
		toolsPreview = append(toolsPreview, AgentToolPreview{Definition: definition, DisplayName: item.DisplayName, Category: item.Category, Risk: item.Risk, RequiresApproval: decision.RequiresApproval, ApprovalReason: decision.Reason, ProvidesVerification: customToolsByID[definition.Name].ProvidesVerification || definition.Name == "run_command"})
	}
	systemMessage := agent.SystemMessage(prepared.profile, prepared.customTools)
	encodedTools, err := json.Marshal(definitions)
	if err != nil {
		return AgentRunPreview{}, err
	}
	stableMessages := agent.BuildStableMessages(prepared.profile, prepared.context.Items, prepared.task, prepared.customTools)
	tokens := AgentTokenEstimate{SystemPrompt: tokenEstimate(len(systemMessage)), Task: tokenEstimate(len(prepared.task)), ToolSchemas: tokenEstimate(len(encodedTools)), Context: prepared.context.EstimatedTokens, ContextWindow: prepared.profile.ContextWindowTokens, AvailableInput: agent.ModelInputBudgetTokens(prepared.profile), ReservedOutput: prepared.profile.MaxOutputTokens}
	tokens.Total = agent.EstimateModelInputTokens(stableMessages, definitions)
	completionPolicy := agent.DescribeCompletionPolicy(prepared.profile, prepared.task, prepared.customTools)
	warnings := append([]string(nil), prepared.context.Warnings...)
	if hasTool(prepared.profile.AllowedTools, "propose_patch") && !hasTool(prepared.profile.AllowedTools, "read_file") && !hasTool(prepared.profile.AllowedTools, "search_code") {
		warnings = append(warnings, "Агенту разрешены правки, но запрещены read_file и search_code: Point остановит изменение существующего файла без предварительного изучения кода.")
	} else if hasTool(prepared.profile.AllowedTools, "propose_patch") && !hasTool(prepared.profile.AllowedTools, "read_file") {
		warnings = append(warnings, "У агента нет read_file: он сможет делать только точечные правки по фрагментам search_code, но не полнофайловые переписывания.")
	}
	if hasTool(prepared.profile.AllowedTools, "propose_patch") && !hasTool(prepared.profile.AllowedTools, "list_files") {
		warnings = append(warnings, "У агента нет list_files: перед созданием нового файла ему потребуется прочитать соседний файл.")
	}
	if len(definitions) == 0 {
		warnings = append(warnings, "У профиля нет доступных инструментов: агент сможет отвечать только текстом.")
	}
	if approvalCount > 0 {
		warnings = append(warnings, fmt.Sprintf("Инструментов с обязательным подтверждением: %d.", approvalCount))
	}
	if completionPolicy.BlockingConfigurationIssue {
		if hasTool(prepared.profile.AllowedTools, "propose_patch") && !completionPolicy.VerificationToolAvailable {
			warnings = append(warnings, "Разрешены правки файлов без умения-доказательства (Запуск команд или verifier). Такой квест не сможет завершиться успешно после изменений.")
		} else {
			warnings = append(warnings, "Критерии требуют запуска тестов или сборки, но у агента нет разрешённого инструмента, отмеченного как доказательство проверки. Такой квест не сможет завершиться успешно.")
		}
	}
	if tokens.Total > tokens.AvailableInput {
		warnings = append(warnings, fmt.Sprintf("Начальный контекст требует около %d токенов при доступном входном бюджете %d; сократите задачу или увеличьте окно модели.", tokens.Total, tokens.AvailableInput))
	}
	publicContext := prepared.context
	publicContext.Items = publicContextItems(publicContext.Items)
	return AgentRunPreview{Fingerprint: prepared.fingerprint, Version: Version, Workspace: prepared.workspace, Profile: prepared.profile, SystemMessage: systemMessage, Task: prepared.task, Tools: toolsPreview, Context: publicContext, Tokens: tokens, Completion: completionPolicy, Warnings: warnings}, nil
}

func (a *App) StartRun(request StartRunRequest) (domain.Run, error) {
	if request.ExecutionID != "" {
		unlock := lockTaskProposal("execution/" + request.ExecutionID)
		defer unlock()
	}
	slog.Info("agent start requested",
		"profile_id", request.ProfileID,
		"execution_id", request.ExecutionID,
		"quest_id", request.QuestID,
		"flow_run_id", request.FlowRunID,
		"context_items", len(request.ContextItems),
		"task_preview", observability.Snippet(security.Redact(request.Task), 160),
	)
	task := agent.ComposeQuestTask(request.Task, request.Goal, request.AcceptanceCriteria, request.Constraints)
	slog.Info("agent start progress", "execution_id", request.ExecutionID, "phase", "prepare")
	var prepared preparedAgentRun
	var err error
	if request.preparedAgent != nil {
		prepared = *request.preparedAgent
		if prepared.task != task || prepared.projectAgentID != request.ProfileID {
			return domain.Run{}, errors.New("atomic launch preparation does not match the requested task and agent")
		}
	} else {
		prepared, err = a.prepareAgentRunWithRoute(request.ProfileID, task, request.ContextItems, true, request.ModelBinding == nil)
		if err != nil {
			return domain.Run{}, err
		}
	}
	if request.FlowRunID != "" {
		prepared.profile.AllowedTools = appendUniqueStrings(prepared.profile.AllowedTools, "team_inbox", "team_publish")
	}
	// New-file propose_patch requires a prior list_files (or neighbor read). Writers
	// without list_files burn the step budget looping on inspection_required.
	if hasTool(prepared.profile.AllowedTools, "propose_patch") {
		prepared.profile.AllowedTools = appendUniqueStrings(prepared.profile.AllowedTools, "list_files")
	}
	slog.Info("agent start progress", "execution_id", request.ExecutionID, "phase", "model_binding")
	if err = a.applyModelBinding(&prepared.profile, request.ModelBinding); err != nil {
		return domain.Run{}, fmt.Errorf("apply stage model binding: %w", err)
	}
	applyStageExecutionBudget(&prepared.profile, request.StageRole)
	slog.Info("agent start progress", "execution_id", request.ExecutionID, "phase", "lifecycle")
	if err := a.enforceRunnableSystemLifecycle(context.Background()); err != nil {
		return domain.Run{}, err
	}
	slog.Info("agent start progress", "execution_id", request.ExecutionID, "phase", "hub_budget")
	if err := a.enforceHubBudget(prepared.workspace.ID); err != nil {
		return domain.Run{}, err
	}
	execID := request.ExecutionID
	projectAgentID := prepared.projectAgentID
	if projectAgentID == "" {
		projectAgentID = prepared.profile.ID
	}
	questID := request.QuestID
	flowRunID := request.FlowRunID
	flowNodeID := request.FlowNodeID
	var priorExecution *domain.ExecutionInstance
	if request.ExecutionID != "" {
		existingExec, listErr := a.findExecution(request.ExecutionID)
		if listErr != nil {
			return domain.Run{}, listErr
		}
		if (questID != "" && questID != existingExec.QuestID) || (flowRunID != "" && flowRunID != existingExec.FlowRunID) || (flowNodeID != "" && flowNodeID != existingExec.FlowNodeID) {
			return domain.Run{}, errors.New("execution does not belong to the requested task or plan stage")
		}
		questID, flowRunID, flowNodeID = existingExec.QuestID, existingExec.FlowRunID, existingExec.FlowNodeID
		priorExecution = &existingExec
	}
	brief, briefErr := a.taskBriefForQuest(context.Background(), prepared.workspace.ID, questID)
	if briefErr != nil {
		return domain.Run{}, briefErr
	}
	slog.Info("agent start progress", "execution_id", execID, "phase", "stage_brief", "stage_role", request.StageRole)
	brief = stageScopedBrief(brief, request.StageRole)
	runtimeRequirements := managedSandboxRuntimeForBrief(brief)
	if err := a.validateTaskEnvironment(brief); err != nil {
		return domain.Run{}, err
	}
	slog.Info("agent start progress", "execution_id", execID, "phase", "validated_brief")
	// Project assignments freeze the model after a real turn. Declared
	// fallbacks stay on the profile so an empty reasoning-only response can
	// still switch to the listed backup (engine rejects 429-style fallbacks).
	precommittedRun := request.preparedRunID != "" && priorExecution != nil && priorExecution.RunID == request.preparedRunID
	if priorExecution != nil && brief != nil && !precommittedRun && (priorExecution.Status != domain.RunPending || priorExecution.RunID != "") {
		if resumed, resumeErr := a.resumeStructuredExecutionIfSafe(priorExecution, request.APIKey); resumeErr != nil {
			return domain.Run{}, resumeErr
		} else if resumed.ID != "" {
			return resumed, nil
		}
	}
	if err = validateTaskExecutionLaunch(priorExecution, brief, projectAgentID, prepared.task, request.preparedRunID); err != nil {
		return domain.Run{}, err
	}
	prepared.profile, err = agent.RestrictTaskProfile(prepared.profile, brief, prepared.customTools)
	if err != nil {
		return domain.Run{}, err
	}
	if brief != nil && brief.Permissions.ProvisionProjectAgents && projectAgentID != "" {
		if owner, ownerErr := a.store.GetProjectAgent(context.Background(), projectAgentID); ownerErr == nil && !owner.Temporary && (owner.Status == "" || owner.Status == domain.ProjectAgentActive) {
			prepared.profile.AllowedTools = appendUniqueStrings(prepared.profile.AllowedTools, "request_subagent")
		}
	}
	prepared.profile = a.applyFlowNodeWritePolicy(prepared.profile, questID, flowNodeID)
	if questID != "" {
		if lease, leaseErr := a.store.GetLatestQuestToolLease(context.Background(), questID); leaseErr == nil {
			if time.Now().UTC().After(lease.ExpiresAt) {
				return domain.Run{}, errors.New("quest tool lease expired; expand or re-approve the intake contract")
			}
			prepared.profile = restrictProfileToLease(prepared.profile, lease)
		}
	}
	if err = agent.ValidateTaskVerification(prepared.profile, brief, prepared.customTools); err != nil {
		return domain.Run{}, err
	}
	completionPolicy := agent.DescribeCompletionPolicy(prepared.profile, prepared.task, prepared.customTools)
	if brief == nil && completionPolicy.BlockingConfigurationIssue {
		if hasTool(prepared.profile.AllowedTools, "propose_patch") && !completionPolicy.VerificationToolAvailable {
			return domain.Run{}, errors.New("propose_patch is enabled without a verification-capable tool; enable run_command or a custom tool with providesVerification")
		}
		return domain.Run{}, errors.New("task explicitly requires test, build, lint, or static-analysis verification, but no verification-capable tool is enabled for this agent")
	}
	if request.PreflightFingerprint != "" && request.PreflightFingerprint != prepared.fingerprint {
		return domain.Run{}, errors.New("agent preflight is stale; preview the updated task, profile, tools or context before launch")
	}
	registry, _ := agent.BuildToolRegistryWithSources(prepared.fs, prepared.customTools, serverProfileBridge{app: a}, a.dbToolAccess(), prepared.profile)
	definitions := registry.Definitions(prepared.profile.AllowedTools)
	initialMessages := agent.BuildStableMessages(prepared.profile, prepared.context.Items, prepared.task, prepared.customTools)
	if instruction := agent.TaskContractInstructions(brief); instruction != "" {
		initialMessages[0].Content += "\n" + instruction
	}
	initialTokens := agent.EstimateModelInputTokens(initialMessages, definitions)
	inputBudget := agent.ModelInputBudgetTokens(prepared.profile)
	if initialTokens > inputBudget {
		return domain.Run{}, fmt.Errorf("initial context needs about %d tokens, exceeding the %d-token input budget", initialTokens, inputBudget)
	}
	snapshot := domain.NewRunConfigurationSnapshot(Version, prepared.profile, prepared.customTools, time.Now().UTC())
	if request.preparedConfiguration != nil {
		if request.preparedConfiguration.ConfigurationDigest != snapshot.ConfigurationDigest {
			return domain.Run{}, errors.New("atomic launch configuration no longer matches the prepared agent route")
		}
		snapshot = *request.preparedConfiguration
	}
	if request.initialBudgetReservationID == "" {
		if err := a.enforceQuestBudget(prepared.workspace.ID, questID, int64(initialTokens+prepared.profile.MaxOutputTokens), prepared.profile); err != nil {
			return domain.Run{}, err
		}
	}
	if prepared.legacyProfile {
		if err := a.recordCompatibilityUsage(context.Background(), prepared.workspace.ID, domain.CompatibilityProfileRunFallback, legacyProfileRunVersion); err != nil {
			return domain.Run{}, err
		}
	}
	var sandboxRecord domain.SandboxRecord
	reuseSandbox := false
	if execID != "" {
		if existing, getErr := a.store.GetSandboxByExecution(context.Background(), execID); getErr == nil && existing.Path != "" {
			sandboxRecord = existing
			reuseSandbox = true
		}
	}
	if execID == "" {
		execID = domain.NewID("execution")
	}
	if !reuseSandbox {
		liveWorkspace := sandbox.LiveFileMutationEnabled()
		if brief != nil && brief.Mode == domain.TaskModeProject {
			liveWorkspace = false
		}
		created, sandboxErr := a.sandboxBackend.Create(context.Background(), sandbox.CreateRequest{
			WorkspaceID: prepared.workspace.ID, WorkspacePath: prepared.workspace.Path, ExecutionID: execID,
			Runtime: runtimeRequirements, PreferWorktree: true, LiveWorkspace: liveWorkspace,
		})
		if sandboxErr != nil {
			return domain.Run{}, fmt.Errorf("create execution sandbox: %w", sandboxErr)
		}
		if brief != nil && brief.Mode == domain.TaskModeProject && created.Kind == "live" {
			_ = a.sandboxBackend.Close(context.Background(), created, prepared.workspace.Path)
			return domain.Run{}, errors.New("project task requires a separate execution workspace")
		}
		sandboxRecord = created
		if err := a.store.SaveSandbox(context.Background(), sandboxRecord); err != nil {
			return domain.Run{}, err
		}
	}
	if brief != nil && brief.Mode == domain.TaskModeProject && sandboxRecord.Kind == "live" {
		// Hub may have created a live sandbox before the parent project brief
		// was visible on a milestone quest. Replace it with an isolated copy.
		_ = a.sandboxBackend.Close(context.Background(), sandboxRecord, prepared.workspace.Path)
		created, sandboxErr := a.sandboxBackend.Create(context.Background(), sandbox.CreateRequest{
			WorkspaceID: prepared.workspace.ID, WorkspacePath: prepared.workspace.Path, ExecutionID: execID,
			Runtime: runtimeRequirements, PreferWorktree: true, LiveWorkspace: false,
		})
		if sandboxErr != nil {
			return domain.Run{}, fmt.Errorf("create isolated project sandbox: %w", sandboxErr)
		}
		if created.Kind == "live" {
			_ = a.sandboxBackend.Close(context.Background(), created, prepared.workspace.Path)
			return domain.Run{}, errors.New("project task requires a separate execution workspace")
		}
		sandboxRecord = created
		if err := a.store.SaveSandbox(context.Background(), sandboxRecord); err != nil {
			return domain.Run{}, err
		}
	}
	sandboxImage := sandbox.ExecutionImageForRecord(sandboxRecord)
	createdDirectQuest := false
	if questID == "" && flowRunID == "" {
		title := strings.TrimSpace(request.Task)
		if title == "" {
			title = prepared.task
		}
		if firstLine, _, ok := strings.Cut(title, "\n"); ok {
			title = firstLine
		}
		if len([]rune(title)) > 160 {
			title = string([]rune(title)[:160])
		}
		objectives := []string{}
		if strings.TrimSpace(request.Goal) != "" {
			objectives = append(objectives, strings.TrimSpace(request.Goal))
		}
		quest, saveErr := a.SaveQuest(domain.Quest{
			WorkspaceID: prepared.workspace.ID, Title: title, Description: prepared.task,
			Objectives: objectives, Constraints: append([]string(nil), request.Constraints...),
			DefinitionOfDone: append([]string(nil), request.AcceptanceCriteria...),
			Importance:       domain.QuestNormal, Status: domain.QuestActive,
		})
		if saveErr != nil {
			return domain.Run{}, saveErr
		}
		questID = quest.ID
		createdDirectQuest = true
	}
	execution := domain.ExecutionInstance{
		ID: execID, WorkspaceID: prepared.workspace.ID, ProjectAgentID: projectAgentID,
		QuestID: questID, FlowRunID: flowRunID, FlowNodeID: flowNodeID,
		RunID: request.preparedRunID, SandboxID: sandboxRecord.ID, Task: prepared.task, Status: domain.RunRunning, Snapshot: snapshot,
		StartedAt: time.Now().UTC(),
	}
	if err := a.store.SaveExecution(context.Background(), execution); err != nil {
		return domain.Run{}, err
	}
	run, err := a.engine.Start(agent.StartInput{
		TaskBrief:     brief,
		Configuration: snapshot, Workspace: prepared.workspace, SandboxPath: sandboxRecord.Path,
		SandboxImage: sandboxImage,
		RunID:        request.preparedRunID, AgentID: request.preparedAgentID, StartedAt: request.preparedStartedAt,
		InitialBudgetReservationID: request.initialBudgetReservationID,
		ExecutionID:                execID, QuestID: questID, FlowRunID: flowRunID, FlowNodeID: flowNodeID,
		CompletionCheckKind: request.CompletionCheckKind,
		StageRole:           request.StageRole,
		ForbiddenPaths:      workContractMidRunForbiddenPaths(request.WorkContract),
		Task:                prepared.task, APIKey: request.APIKey, ContextItems: prepared.context.Items,
		ServerProfiles: serverProfileBridge{app: a},
		DBSource:       a.dbToolAccess(),
		TeamBus:        a,
		OnFinished: func(finished domain.Run) {
			execution.RunID = finished.ID
			execution.Status = finished.Status
			execution.Error = finished.Error
			execution.Result = finished.Result
			execution.FinishedAt = finished.FinishedAt
			execution.DurationMs = finished.DurationMs
			success := finished.Status == domain.RunCompleted
			_ = a.store.SaveExecution(context.Background(), execution)
			if releaseErr := a.store.ReleaseBudgetReservations(context.Background(), finished.ID, time.Now().UTC()); releaseErr != nil {
				slog.Warn("terminal run budget release unavailable", "run_id", finished.ID, "error", security.Redact(releaseErr.Error()))
			}
			if finished.Status == domain.RunCompleted || finished.Status == domain.RunFailed || finished.Status == domain.RunCancelled || finished.Status == domain.RunInterrupted {
				baselinePath, dependencies, lineageErr := a.changeSetLineage(prepared.workspace.ID, sandboxRecord)
				if lineageErr != nil {
					slog.Warn("execution change set lineage unavailable", "execution_id", execID, "error", lineageErr)
				} else if brief == nil || brief.Permissions.WriteFiles {
					applier := changesets.Applier{Store: a.store}
					built, buildErr := applier.BuildFromSandbox(context.Background(), changesets.BuildRequest{
						WorkspaceID: prepared.workspace.ID, ExecutionID: execID, QuestID: questID,
						Title: "Changes from " + finished.ID, WorkspacePath: prepared.workspace.Path,
						BaselinePath: baselinePath, SandboxPath: sandboxRecord.Path, DependsOn: dependencies,
					})
					if buildErr != nil {
						slog.Warn("execution change set build unavailable", "execution_id", execID, "error", buildErr)
					} else if request.WorkContract != nil {
						if contractErr := validateWorkContractChanges(*request.WorkContract, built); contractErr != nil {
							success = false
							execution.Status = domain.RunFailed
							execution.Error = contractErr.Error()
							slog.Warn("execution rejected by work contract", "execution_id", execID, "error", contractErr)
							_ = a.store.SaveExecution(context.Background(), execution)
						}
					}
					if buildErr == nil && sandboxRecord.Kind == "live" && built.ID != "" && len(built.Items) > 0 && built.Status == domain.ChangeSetPending && success {
						if _, applyErr := applier.Apply(context.Background(), prepared.workspace.Path, built.ID); applyErr != nil {
							slog.Warn("live change set auto-apply unavailable", "change_set_id", built.ID, "error", applyErr)
						}
					}
				}
				a.awardProjectAgentOutcome(projectAgentID, success)
			}
			// Direct WorkOrder runs need their ChangeSet before finalization: the
			// evidence gate applies that isolated result to the approved workspace.
			// Finalizing first produced an empty delivery receipt and only built the
			// actual ChangeSet afterwards.
			if flowRunID == "" && questID != "" {
				a.finalizeQuestAfterFlow(questID, success)
			}
			if flowRunID != "" && flowNodeID != "" {
				recovered := false
				var flowRun domain.FlowRun
				if !success {
					var recoveryErr error
					flowRun, recovered, recoveryErr = a.recoverFlowNodeFailure(flowRunID, flowNodeID, execution)
					if recoveryErr != nil {
						slog.Warn("flow node recovery unavailable", "flow_run_id", flowRunID, "node_id", flowNodeID, "execution_id", execID, "error", recoveryErr)
					}
				}
				if recovered {
					_ = a.scheduleFlowAgentExecutionsFromRun(flowRun)
				} else {
					childStatus := domain.QuestFailed
					if success {
						childStatus = domain.QuestCompleted
					} else if finished.Status == domain.RunCancelled || finished.Status == domain.RunInterrupted {
						childStatus = domain.QuestCancelled
					}
					a.setFlowChildQuestStatus(flowRunID, flowNodeID, childStatus)
					runtime := flowruntime.Runtime{Store: a.store}
					var completeErr error
					completionOutput := a.flowAttemptOutput(flowRunID, flowNodeID, execID, map[string]any{
						"executionId": execID, "runId": finished.ID, "result": finished.Result, "status": finished.Status,
						"error": execution.Error, "changedFiles": append([]string(nil), finished.ChangedFiles...),
					})
					a.attachCompletionEvidence(context.Background(), finished.ID, completionOutput)
					flowRun, completeErr = runtime.CompleteAgentNode(context.Background(), flowRunID, flowNodeID, success, completionOutput)
					if completeErr == nil {
						if flow, flowErr := a.store.GetFlow(context.Background(), flowRun.FlowID); flowErr == nil {
							for _, node := range flow.Nodes {
								if node.ID == flowNodeID {
									a.recordFlowNodeArtifact(flowRun, node, success, execID)
									break
								}
							}
						}
						if flowRun.Status == domain.RunFailed || flowRun.Status == domain.RunCancelled {
							a.cancelSiblingFlowExecutions(flowRun.ID, execID)
							a.closeUnfinishedFlowChildQuests(flowRun.ID, false)
							if flowRun.QuestID != "" {
								a.finalizeQuestAfterFlow(flowRun.QuestID, false)
							}
						} else if flowRun.Status == domain.RunCompleted {
							a.closeUnfinishedFlowChildQuests(flowRun.ID, true)
							if flowRun.QuestID != "" {
								a.finalizeQuestAfterFlow(flowRun.QuestID, success)
							}
						} else {
							_ = a.scheduleFlowAgentExecutionsFromRun(flowRun)
						}
					}
				}
			}
			if learningErr := a.recordRunLearningEvidence(context.Background(), finished, projectAgentID); learningErr != nil {
				slog.Warn("run learning evidence unavailable", "run_id", finished.ID, "agent_id", projectAgentID, "error", security.Redact(learningErr.Error()))
			}
			a.queueAgentImprovement(finished, projectAgentID, request.APIKey)
		},
	})
	if err != nil {
		now := time.Now().UTC()
		execution.Status = domain.RunFailed
		execution.Error = err.Error()
		execution.FinishedAt = &now
		execution.DurationMs = now.Sub(execution.StartedAt).Milliseconds()
		_ = a.store.SaveExecution(context.Background(), execution)
		if createdDirectQuest {
			a.finalizeQuestAfterFlow(questID, false)
		}
		slog.Error("agent start failed", "execution_id", execID, "quest_id", questID, "error", security.Redact(err.Error()))
		return domain.Run{}, err
	}
	execution.RunID = run.ID
	_ = a.store.SaveExecution(context.Background(), execution)
	slog.Info("agent start launched",
		"run_id", run.ID,
		"execution_id", execID,
		"quest_id", questID,
		"profile_id", prepared.profile.ID,
		"model", prepared.profile.Model,
		"tokens", initialTokens,
		"budget", inputBudget,
		"tools", prepared.profile.AllowedTools,
	)
	return publicRun(run), nil
}

func managedSandboxRuntimeForBrief(brief *domain.TaskBrief) sandbox.RuntimeRequirements {
	if brief == nil || brief.WorkOrder == nil {
		return sandbox.RuntimeRequirements{}
	}
	return environment.RuntimeRequirementsForExecutionContract(brief.WorkOrder)
}
