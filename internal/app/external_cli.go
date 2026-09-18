package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"local-agent-workbench/internal/agent"
	"local-agent-workbench/internal/changesets"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/executors"
	"local-agent-workbench/internal/flowruntime"
	"local-agent-workbench/internal/mcp"
	workbenchtools "local-agent-workbench/internal/tools"
	"local-agent-workbench/internal/workspace"
)

type cliMCPExecutor struct {
	registry *workbenchtools.Registry
	allowed  map[string]bool
}

func (e cliMCPExecutor) Definitions() []domain.ToolDefinition {
	names := make([]string, 0, len(e.allowed))
	for name := range e.allowed {
		names = append(names, name)
	}
	return e.registry.Definitions(names)
}

func (e cliMCPExecutor) Execute(ctx context.Context, name string, arguments json.RawMessage) domain.ToolResult {
	if !e.allowed[name] {
		return workbenchtools.Fail("tool_denied", "tool is outside the CLI execution contract")
	}
	tool, ok := e.registry.Get(name)
	if !ok {
		return workbenchtools.Fail("tool_unavailable", "tool is unavailable")
	}
	return tool.Execute(ctx, arguments)
}

func (a *App) launchExternalCLIExecution(execution domain.ExecutionInstance, projectAgent domain.ProjectAgent, brief *domain.TaskBrief, resume bool) error {
	kind := executors.KindForProvider(projectAgent.Provider)
	if kind == executors.KindPoint {
		return errors.New("project agent is not an external CLI runtime")
	}
	sandboxRecord, err := a.store.GetSandbox(context.Background(), execution.SandboxID)
	if err != nil {
		return err
	}
	if sandboxRecord.Kind == "live" {
		return errors.New("external CLI execution requires an isolated workspace")
	}
	profile := domain.ProfileFromProjectAgent(projectAgent)
	if err = a.enrichProjectAgentForRun(execution.WorkspaceID, projectAgent, &profile, nil); err != nil {
		return err
	}
	fs, err := workspace.Open(sandboxRecord.Path)
	if err != nil {
		return err
	}
	registry, _ := agent.BuildToolRegistryForFlow(fs, nil, serverProfileBridge{app: a}, a.dbToolAccess(), a,
		execution.WorkspaceID, execution.QuestID, execution.FlowRunID, execution.FlowNodeID, profile)
	safe := map[string]bool{
		"project_map": true, "search_code": true, "list_files": true, "read_file": true,
		"search_text": true, "git_diff": true, "git_log": true, "git_branches": true,
		"git_tags": true, "read_skill": true, "team_inbox": true, "team_publish": true, "permission_prompt": true,
	}
	allowed := map[string]bool{}
	for _, name := range profile.AllowedTools {
		if safe[name] {
			allowed[name] = true
		}
	}
	allowed["team_inbox"], allowed["team_publish"] = true, true
	bridge := cliMCPExecutor{registry: registry, allowed: allowed}
	baseURL := a.SelfURL()
	if strings.TrimSpace(baseURL) == "" {
		return errors.New("Point MCP address is unavailable")
	}
	key, closeSession, err := a.OpenMCPSession("execution/"+execution.ID, bridge)
	if err != nil {
		return err
	}
	config, err := mcp.ClientConfig(baseURL, key)
	if err != nil {
		closeSession()
		return err
	}
	write, commands := false, false
	if brief != nil {
		write, commands = brief.Permissions.WriteFiles, brief.Permissions.ExecuteCommands
	}
	capabilities := executors.NewCLI(kind).Capabilities()
	if brief != nil && !capabilities.NetworkIsolation &&
		(commands || len(brief.Permissions.NetworkHosts) > 0) {
		closeSession()
		return errors.New("selected CLI cannot enforce the approved network boundary")
	}
	reservationID, err := a.ReserveModelBudget(context.Background(), agent.ModelBudgetRequest{
		WorkspaceID: execution.WorkspaceID, QuestID: execution.QuestID, ExecutionID: execution.ID,
		Provider: profile.Provider, ProviderPreset: profile.ProviderPreset, Model: profile.Model, EstimatedInputTokens: 10000,
		MaxOutputTokens: int64(max(1024, profile.MaxOutputTokens)),
	})
	if err != nil {
		closeSession()
		return fmt.Errorf("reserve external CLI budget: %w", err)
	}
	ctx, cancel := context.WithCancel(a.ctx)
	a.externalMu.Lock()
	if a.externalCancels == nil {
		a.externalCancels = map[string]context.CancelFunc{}
	}
	if _, exists := a.externalCancels[execution.ID]; exists {
		a.externalMu.Unlock()
		cancel()
		closeSession()
		_ = a.ReconcileModelBudget(context.Background(), agent.ModelBudgetSettlement{ReservationID: reservationID, WorkspaceID: execution.WorkspaceID})
		return errors.New("external CLI execution is already active")
	}
	a.externalCancels[execution.ID] = cancel
	a.externalMu.Unlock()
	execution.Status = domain.RunRunning
	execution.Runtime = string(kind)
	execution.Snapshot.Profile = profile
	if err = a.store.SaveExecution(context.Background(), execution); err != nil {
		cancel()
		closeSession()
		_ = a.ReconcileModelBudget(context.Background(), agent.ModelBudgetSettlement{ReservationID: reservationID, WorkspaceID: execution.WorkspaceID})
		return err
	}
	a.externalWG.Add(1)
	go func() {
		defer a.externalWG.Done()
		defer closeSession()
		defer func() {
			a.externalMu.Lock()
			delete(a.externalCancels, execution.ID)
			a.externalMu.Unlock()
			cancel()
		}()
		started := time.Now()
		runtime := executors.NewCLI(kind)
		request := executors.Request{
			Provider: profile.Provider, Model: profile.Model, Prompt: execution.Task,
			SystemPrompt:  profile.SystemPrompt + "\n" + agent.TaskContractInstructions(brief),
			WorkspacePath: sandboxRecord.Path, MCPConfigJSON: config,
			WriteFiles: write, ExecuteCommands: commands, SessionID: execution.RuntimeSessionID,
		}
		onEvent := func(event executors.Event) error {
			if event.SessionID != "" && execution.RuntimeSessionID == "" {
				execution.RuntimeSessionID = event.SessionID
				_ = a.store.SaveExecution(context.Background(), execution)
			}
			if event.Kind == "error" {
				slog.Warn("external CLI event", "execution_id", execution.ID, "runtime", kind, "data", string(event.Data))
			}
			return nil
		}
		var result executors.Result
		var runErr error
		if resume {
			result, runErr = runtime.Resume(ctx, request, onEvent)
		} else {
			result, runErr = runtime.Run(ctx, request, onEvent)
		}
		_ = a.ReconcileModelBudget(context.Background(), agent.ModelBudgetSettlement{
			ReservationID: reservationID, WorkspaceID: execution.WorkspaceID,
			ProjectAgentID: execution.ProjectAgentID, Outcome: "external_cli_usage_unreported",
		})
		now := time.Now().UTC()
		execution.DurationMs = time.Since(started).Milliseconds()
		execution.FinishedAt = &now
		execution.Result = result.Text
		if result.SessionID != "" {
			execution.RuntimeSessionID = result.SessionID
		}
		success := runErr == nil && result.ExitCode == 0
		if success {
			execution.Status = domain.RunCompleted
		} else if errors.Is(ctx.Err(), context.Canceled) {
			execution.Status = domain.RunCancelled
			execution.Error = "external CLI execution cancelled"
		} else {
			execution.Status = domain.RunFailed
			if runErr != nil {
				execution.Error = runErr.Error()
			}
		}
		_ = a.store.SaveExecution(context.Background(), execution)
		a.finishExternalCLIExecution(execution, sandboxRecord, success, brief)
	}()
	return nil
}

func (a *App) ResumeExternalCLIExecution(ctx context.Context, executionID string) (domain.ExecutionInstance, error) {
	execution, err := a.store.GetExecution(ctx, executionID)
	if err != nil {
		return domain.ExecutionInstance{}, err
	}
	if execution.RuntimeSessionID == "" {
		return domain.ExecutionInstance{}, errors.New("external execution has no resumable runtime session")
	}
	projectAgent, err := a.store.GetProjectAgent(ctx, execution.ProjectAgentID)
	if err != nil {
		return domain.ExecutionInstance{}, err
	}
	var brief *domain.TaskBrief
	if execution.QuestID != "" {
		quests, _ := a.store.ListQuests(ctx, execution.WorkspaceID)
		for _, quest := range quests {
			if quest.ID == execution.QuestID {
				brief = quest.Brief
				break
			}
		}
	}
	execution.FinishedAt, execution.Error = nil, ""
	if err = a.launchExternalCLIExecution(execution, projectAgent, brief, true); err != nil {
		return domain.ExecutionInstance{}, err
	}
	return a.store.GetExecution(ctx, executionID)
}

func (a *App) finishExternalCLIExecution(execution domain.ExecutionInstance, sandboxRecord domain.SandboxRecord, success bool, brief *domain.TaskBrief) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return
	}
	if brief == nil || brief.Permissions.WriteFiles {
		baseline, dependencies, lineageErr := a.changeSetLineage(ws.ID, sandboxRecord)
		if lineageErr == nil {
			applier := changesets.Applier{Store: a.store}
			built, buildErr := applier.BuildFromSandbox(context.Background(), changesets.BuildRequest{
				WorkspaceID: ws.ID, ExecutionID: execution.ID, QuestID: execution.QuestID,
				Title: "Changes from " + execution.ID, WorkspacePath: ws.Path,
				BaselinePath: baseline, SandboxPath: sandboxRecord.Path, DependsOn: dependencies,
			})
			if buildErr == nil && execution.FlowRunID != "" {
				if run, runErr := a.store.GetFlowRun(context.Background(), execution.FlowRunID); runErr == nil {
					if flow, flowErr := a.store.GetFlow(context.Background(), run.FlowID); flowErr == nil {
						for _, node := range flow.Nodes {
							if node.ID == execution.FlowNodeID {
								if contract, contractErr := workContractFromNode(node); contractErr == nil {
									if contractErr = validateWorkContractChanges(contract, built); contractErr != nil {
										success = false
										execution.Status, execution.Error = domain.RunFailed, contractErr.Error()
										_ = a.store.SaveExecution(context.Background(), execution)
									}
								}
								break
							}
						}
					}
				}
			}
		}
	}
	a.awardProjectAgentOutcome(execution.ProjectAgentID, success)
	if execution.FlowRunID == "" {
		a.finalizeQuestAfterFlow(execution.QuestID, success)
		return
	}
	childStatus := domain.QuestFailed
	if success {
		childStatus = domain.QuestCompleted
	}
	a.setFlowChildQuestStatus(execution.FlowRunID, execution.FlowNodeID, childStatus)
	runtime := flowruntime.Runtime{Store: a.store}
	output := a.flowAttemptOutput(execution.FlowRunID, execution.FlowNodeID, execution.ID, map[string]any{
		"executionId": execution.ID, "result": execution.Result, "status": execution.Status, "error": execution.Error,
		"runtime": string(executors.KindForProvider(execution.Snapshot.Profile.Provider)),
	})
	run, completeErr := runtime.CompleteAgentNode(context.Background(), execution.FlowRunID, execution.FlowNodeID, success, output)
	if completeErr != nil {
		slog.Warn("complete external CLI node failed", "execution_id", execution.ID, "error", completeErr)
		return
	}
	switch run.Status {
	case domain.RunCompleted:
		a.closeUnfinishedFlowChildQuests(run.ID, true)
		a.finalizeQuestAfterFlow(run.QuestID, success)
	case domain.RunFailed, domain.RunCancelled:
		a.cancelSiblingFlowExecutions(run.ID, execution.ID)
		a.closeUnfinishedFlowChildQuests(run.ID, false)
		a.finalizeQuestAfterFlow(run.QuestID, false)
	default:
		_ = a.scheduleFlowAgentExecutionsFromRun(run)
	}
}

func (a *App) StopExternalCLIExecution(executionID string) error {
	a.externalMu.Lock()
	cancel := a.externalCancels[executionID]
	a.externalMu.Unlock()
	if cancel == nil {
		return fmt.Errorf("external execution %s is not active", executionID)
	}
	cancel()
	return nil
}
