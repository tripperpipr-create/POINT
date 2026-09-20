package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/executors"
	"local-agent-workbench/internal/mcp"
	"local-agent-workbench/internal/policy"
	"local-agent-workbench/internal/providers"
	workbenchtools "local-agent-workbench/internal/tools"
)

type ToolSessionOpener interface {
	OpenRunToolSession(subject string, executor mcp.Executor) (configJSON string, close func(), err error)
}

type runMCPExecutor struct {
	mu              sync.Mutex
	engine          *Engine
	active          *activeRun
	profile         domain.AgentProfile
	registry        *workbenchtools.Registry
	patches         *workbenchtools.PatchManager
	history         *conversationHistory
	completion      *completionTracker
	observations    *observationTracker
	completed       map[string]struct{}
	model           string
	step            int
	toolOutputBytes int
}

func (x *runMCPExecutor) Definitions() []domain.ToolDefinition {
	return x.registry.Definitions(policy.ProfileGrants(x.profile).ToolNames())
}

func (x *runMCPExecutor) Execute(ctx context.Context, name string, arguments json.RawMessage) domain.ToolResult {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.step++
	call := prepareToolCall(providers.ToolCall{ID: domain.NewID("mcp_call"), Name: name, Arguments: arguments}, x.profile.AllowedTools)
	revision := x.engine.currentWorkspaceRevision(x.active)
	key := toolExecutionKey(call, revision)
	if _, duplicate := x.completed[key]; duplicate {
		return x.engine.rejectDuplicateTool(ctx, x.active, call)
	}
	result, err := x.engine.executeTool(ctx, x.active, x.profile, x.registry, x.patches, x.observations, call)
	if err != nil {
		return workbenchtools.Fail("tool_failed", err.Error())
	}
	revision = x.engine.currentWorkspaceRevision(x.active)
	if toolCallCompletedSuccessfully(result) {
		x.completed[key] = struct{}{}
		x.observations.Observe(call, result, revision, x.step, key)
	}
	x.completion.ObserveTool(call.Name, call.Arguments, result, revision)
	payload, _ := json.Marshal(result)
	x.toolOutputBytes += len(payload)
	if x.toolOutputBytes > maxRunToolOutputBytes {
		x.engine.fail(x.active, errors.New("cumulative tool output exceeds 4 MiB"))
		return workbenchtools.Fail("tool_output_limit", "cumulative tool output exceeds 4 MiB")
	}
	if err = x.engine.persistRoundCheckpoint(x.active, x.history, x.completion, x.observations, x.completed,
		x.model, nil, x.step+1, 0, 0, 0, x.toolOutputBytes, "", "", ""); err != nil {
		x.engine.fail(x.active, fmt.Errorf("persist CLI tool checkpoint: %w", err))
		return workbenchtools.Fail("checkpoint_failed", err.Error())
	}
	return result
}

func (e *Engine) executeHeadlessCLI(ctx context.Context, active *activeRun, profile domain.AgentProfile, registry *workbenchtools.Registry, patches *workbenchtools.PatchManager, history *conversationHistory, completion *completionTracker, observations *observationTracker, completed map[string]struct{}) {
	if e.toolSessions == nil {
		e.fail(active, errors.New("headless CLI requires the Point MCP tool-session bridge"))
		return
	}
	if e.cliFactory == nil {
		e.fail(active, errors.New("headless CLI factory is not configured"))
		return
	}
	runtime := executors.KindForProvider(profile.Provider)
	if runtime == executors.KindPoint {
		e.fail(active, errors.New("provider is not a headless CLI runtime"))
		return
	}
	mcpExecutor := &runMCPExecutor{
		engine: e, active: active, profile: profile, registry: registry, patches: patches,
		history: history, completion: completion, observations: observations, completed: completed, model: profile.Model,
	}
	config, closeSession, err := e.toolSessions.OpenRunToolSession("run/"+active.run.ID, mcpExecutor)
	if err != nil {
		e.fail(active, err)
		return
	}
	defer closeSession()
	profile.AllowedTools = appendUniqueToolName(profile.AllowedTools, "permission_prompt")
	mcpExecutor.profile = profile
	e.injectTeamInbox(active, history, profile.ID)
	definitions := mcpExecutor.Definitions()
	estimatedInput := int64(EstimateModelInputTokens(history.stable, definitions))
	reservationID := ""
	if e.budgets != nil {
		reservationID = active.takeInitialBudgetReservation()
	}
	if e.budgets != nil && reservationID == "" {
		reservationID, err = e.budgets.ReserveModelBudget(ctx, ModelBudgetRequest{
			WorkspaceID: active.run.WorkspaceID, QuestID: active.correlation.QuestID,
			ExecutionID: active.correlation.ExecutionID, RunID: active.run.ID,
			Provider: profile.Provider, ProviderPreset: profile.ProviderPreset, Model: profile.Model, EstimatedInputTokens: estimatedInput,
			MaxOutputTokens: int64(profile.MaxOutputTokens),
		})
		if err != nil {
			e.fail(active, fmt.Errorf("reserve CLI model budget: %w", err))
			return
		}
	}
	e.update(active, func(run *domain.Run) {
		run.Step = 1
		run.RequestCount++
	})
	e.publishOrLog(ctx, e.snapshot(active), domain.EventModelRequested, "agent", map[string]any{
		"model": profile.Model, "runtime": runtime, "estimatedInputTokens": estimatedInput,
		"budgetReservationId": reservationID,
	})
	system, prompt := renderCLIPrompt(history.stable)
	request := executors.Request{
		Provider: profile.Provider, Model: profile.Model, SystemPrompt: system +
			"\nUse Point MCP tools for every mutation, command, verification and team message. Native write, shell, web and subagent tools are outside the approved contract.",
		Prompt: prompt, WorkspacePath: active.sandboxPath, MCPConfigJSON: config,
		MCPTools: policy.ProfileGrants(profile).ToolNames(), MCPOnly: true,
		WriteFiles: false, ExecuteCommands: false,
	}
	active.clock.start()
	result, runErr := e.cliFactory(runtime).Run(ctx, request, func(event executors.Event) error {
		if event.Text != "" {
			return e.publish(ctx, e.snapshot(active), domain.EventModelStreamed, "model", map[string]any{"delta": event.Text, "runtime": runtime})
		}
		return nil
	})
	if reservationID != "" {
		reconcileErr := e.budgets.ReconcileModelBudget(context.Background(), ModelBudgetSettlement{
			ReservationID: reservationID, WorkspaceID: active.run.WorkspaceID,
			Provider: profile.Provider, Model: profile.Model, UsageReported: false,
			Outcome: "external_cli_usage_unreported",
		})
		if runErr == nil && reconcileErr != nil {
			runErr = reconcileErr
		}
	}
	if runErr != nil {
		if ctx.Err() != nil {
			e.finishContext(active, ctx.Err())
		} else {
			e.fail(active, runErr)
		}
		return
	}
	if len(result.Text) > maxModelResponseBytes {
		e.fail(active, errors.New("CLI response exceeds 2 MiB"))
		return
	}
	e.publishOrLog(ctx, e.snapshot(active), domain.EventModelResponded, "model", map[string]any{"content": result.Text, "runtime": runtime})
	current := e.snapshot(active)
	revision := e.currentWorkspaceRevision(active)
	missing := completion.Missing(revision, current.ChangedFiles)
	status := "accepted"
	if len(missing) > 0 {
		status = "rejected"
	}
	e.publishOrLog(ctx, current, domain.EventCompletionChecked, "agent", withCompletionCheckKind(active, map[string]any{
		"status": status, "requirements": missing, "workspaceRevision": revision,
		"evidence": completion.Evidence(revision), "changedFiles": current.ChangedFiles,
	}))
	if len(missing) > 0 {
		e.fail(active, completionRequirementError(missing))
		return
	}
	e.complete(active, strings.TrimSpace(result.Text))
}

func renderCLIPrompt(messages []providers.Message) (string, string) {
	var system, prompt strings.Builder
	for _, message := range messages {
		switch message.Role {
		case "system":
			if system.Len() > 0 {
				system.WriteString("\n\n")
			}
			system.WriteString(message.Content)
		default:
			if prompt.Len() > 0 {
				prompt.WriteString("\n\n")
			}
			prompt.WriteString(message.Content)
		}
	}
	return system.String(), prompt.String()
}

func appendUniqueToolName(names []string, extra string) []string {
	if slices.Contains(names, extra) {
		return names
	}
	return append(names, extra)
}
