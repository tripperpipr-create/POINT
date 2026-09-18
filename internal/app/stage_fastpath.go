package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"local-agent-workbench/internal/agent"
	"local-agent-workbench/internal/domain"
	projectenv "local-agent-workbench/internal/environment"
	"local-agent-workbench/internal/flowruntime"
	"local-agent-workbench/internal/tools"
	"local-agent-workbench/internal/workspace"
)

// stageAllowsLLMBypass is true when launching an LLM cannot improve the tip and
// often degrades it (e.g. integrate inventing bin/console outside ownership).
// Serial inheritance already carries one integrated sandbox tip; accept still
// runs machine verification afterward.
func stageAllowsLLMBypass(role, sandboxLineage string) bool {
	if sandboxLineage != "inherited" {
		return false
	}
	switch role {
	case domain.StageRoleIntegrate, domain.StageRoleImplReview:
		return true
	default:
		return false
	}
}

func stageUsesDeterministicBootstrap(role string) bool {
	return role == domain.StageRoleBootstrap
}

// continueAfterDeterministicStage finalizes the quest when a non-LLM stage ends
// the flow. LLM OnFinished does this; deterministic paths must mirror it or the
// intake poll waits until timeout.
func (a *App) continueAfterDeterministicStage(flowRun domain.FlowRun) error {
	switch flowRun.Status {
	case domain.RunFailed, domain.RunCancelled:
		a.cancelSiblingFlowExecutions(flowRun.ID, "")
		a.closeUnfinishedFlowChildQuests(flowRun.ID, false)
		if flowRun.QuestID != "" {
			a.finalizeQuestAfterFlow(flowRun.QuestID, false)
		}
		a.clearFlowOrchestratorKey(flowRun.ID)
		return nil
	case domain.RunCompleted:
		a.closeUnfinishedFlowChildQuests(flowRun.ID, true)
		if flowRun.QuestID != "" {
			a.finalizeQuestAfterFlow(flowRun.QuestID, true)
		}
		a.clearFlowOrchestratorKey(flowRun.ID)
		return nil
	default:
		return a.scheduleFlowAgentExecutionsFromRun(flowRun)
	}
}

// applyStageExecutionBudget keeps implement/accept capable while capping
// completion budgets that Qwen3.x otherwise spends entirely on reasoning
// (finish_reason=length, no tool call). This raises effective quality and speed.
func applyStageExecutionBudget(profile *domain.AgentProfile, stageRole string) {
	if profile == nil {
		return
	}
	if profile.MaxOutputTokens <= 0 || profile.MaxOutputTokens > 16384 {
		profile.MaxOutputTokens = 16384
	}
	switch strings.ToLower(strings.TrimSpace(profile.ReasoningEffort)) {
	case "", "medium", "high", "xhigh":
		profile.ReasoningEffort = "low"
	}
	switch stageRole {
	case domain.StageRoleBootstrap, domain.StageRoleIntegrate, domain.StageRoleImplReview:
		if profile.MaxSteps <= 0 || profile.MaxSteps > 16 {
			profile.MaxSteps = 16
		}
		if profile.MaxOutputTokens > 4096 {
			profile.MaxOutputTokens = 4096
		}
	}
}

func (a *App) completeStagePassthrough(flowRun domain.FlowRun, node domain.FlowNode, exec domain.ExecutionInstance, reason string) (domain.FlowRun, error) {
	now := time.Now().UTC()
	exec.Status = domain.RunCompleted
	exec.Result = reason
	exec.FinishedAt = &now
	if !exec.StartedAt.IsZero() {
		exec.DurationMs = now.Sub(exec.StartedAt).Milliseconds()
	}
	if err := a.store.SaveExecution(context.Background(), exec); err != nil {
		return domain.FlowRun{}, err
	}
	a.setFlowChildQuestStatus(flowRun.ID, node.ID, domain.QuestCompleted)
	output := map[string]any{
		"executionId": exec.ID,
		"result":      reason,
		"status":      string(domain.RunCompleted),
		"fastPath":    domain.FlowNodeStageRole(node),
	}
	if state := flowRun.NodeStates[node.ID]; state.Output != nil {
		if lineage, _ := state.Output["sandboxLineage"].(string); lineage != "" {
			output["sandboxLineage"] = lineage
		}
		if seed, _ := state.Output["seedExecutionId"].(string); seed != "" {
			output["seedExecutionId"] = seed
		}
	}
	runtime := flowruntime.Runtime{Store: a.store}
	updated, err := runtime.CompleteAgentNode(context.Background(), flowRun.ID, node.ID, true, output)
	if err != nil {
		return domain.FlowRun{}, err
	}
	a.recordFlowNodeArtifact(updated, node, true, exec.ID)
	return updated, nil
}

func (a *App) completeDeterministicBootstrap(flowRun domain.FlowRun, node domain.FlowNode, exec domain.ExecutionInstance, projectAgent domain.ProjectAgent) (domain.FlowRun, error) {
	sandboxRecord, err := a.store.GetSandbox(context.Background(), exec.SandboxID)
	if err != nil {
		return domain.FlowRun{}, err
	}
	root := sandboxRecord.Path
	composerPath := filepath.Join(root, "composer.json")
	vendorPath := filepath.Join(root, "vendor")
	summary := "bootstrap: no composer.json; tip left unchanged"
	ok := true
	if _, statErr := os.Stat(composerPath); statErr == nil {
		if _, vendorErr := os.Stat(vendorPath); vendorErr == nil {
			summary = "bootstrap: vendor already present; skipped composer install"
		} else {
			sandboxFS, openErr := workspace.Open(root)
			if openErr != nil {
				return domain.FlowRun{}, openErr
			}
			hosts := bootstrapNetworkHosts(a, flowRun, projectAgent)
			policy := "DENY"
			if len(hosts) > 0 {
				policy = "ALLOWLIST"
			}
			runID := exec.RunID
			if strings.TrimSpace(runID) == "" {
				runID = exec.ID
			}
			composerCmd := "composer install --no-interaction --prefer-dist"
			if _, lockErr := os.Stat(filepath.Join(root, "composer.lock")); lockErr != nil {
				composerCmd = "composer update --no-interaction --prefer-dist"
			}
			tool := tools.RunCommand{
				FS: sandboxFS, NetworkPolicy: policy, AllowedNetworkHosts: hosts,
				Executor: a.sandboxProcessExecutor(), RunID: runID, QuestID: flowRun.QuestID,
				DefaultTimeout: 10 * time.Minute, MaxOutput: 256 * 1024,
			}
			args, _ := json.Marshal(map[string]any{
				"command":        composerCmd,
				"reason":         "deterministic bootstrap dependency install",
				"timeoutSeconds": 600,
			})
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
			result := tool.Execute(ctx, args)
			cancel()
			autoload := filepath.Join(root, "vendor", "autoload.php")
			_, hasAutoload := os.Stat(autoload)
			if !result.OK {
				ok = false
				summary = "bootstrap: composer failed"
				if result.Error != nil && strings.TrimSpace(result.Error.Message) != "" {
					summary += ": " + result.Error.Message
				}
			} else if code := toolResultExitCode(result); code != 0 {
				if hasAutoload == nil {
					// Post-scripts / optional packages often exit non-zero after a usable vendor tree.
					ok = true
					summary = fmt.Sprintf("bootstrap: %s exit %d but vendor/autoload.php present; continuing", composerCmd, code)
				} else {
					ok = false
					summary = fmt.Sprintf("bootstrap: %s exit %d (vendor/autoload.php missing)", composerCmd, code)
				}
			} else if hasAutoload != nil {
				ok = false
				summary = "bootstrap: composer reported success but vendor/autoload.php is missing"
			} else {
				summary = "bootstrap: " + composerCmd + " completed"
			}
		}
	}

	now := time.Now().UTC()
	exec.FinishedAt = &now
	if !exec.StartedAt.IsZero() {
		exec.DurationMs = now.Sub(exec.StartedAt).Milliseconds()
	}
	exec.Result = summary
	if ok {
		exec.Status = domain.RunCompleted
	} else {
		exec.Status = domain.RunFailed
		exec.Error = summary
	}
	if err = a.store.SaveExecution(context.Background(), exec); err != nil {
		return domain.FlowRun{}, err
	}
	childStatus := domain.QuestFailed
	if ok {
		childStatus = domain.QuestCompleted
	}
	a.setFlowChildQuestStatus(flowRun.ID, node.ID, childStatus)
	output := map[string]any{
		"executionId":    exec.ID,
		"result":         summary,
		"status":         string(exec.Status),
		"fastPath":       domain.StageRoleBootstrap,
		"sandboxLineage": "fresh",
	}
	runtime := flowruntime.Runtime{Store: a.store}
	updated, completeErr := runtime.CompleteAgentNode(context.Background(), flowRun.ID, node.ID, ok, output)
	if completeErr != nil {
		return domain.FlowRun{}, completeErr
	}
	a.recordFlowNodeArtifact(updated, node, ok, exec.ID)
	return updated, nil
}

func bootstrapNetworkHosts(a *App, flowRun domain.FlowRun, projectAgent domain.ProjectAgent) []string {
	hosts := make([]string, 0, 8)
	seen := map[string]bool{}
	add := func(host string) {
		host = strings.TrimSpace(host)
		if host == "" || seen[host] {
			return
		}
		seen[host] = true
		hosts = append(hosts, host)
	}
	if a != nil && flowRun.WorkspaceID != "" && flowRun.QuestID != "" {
		if quests, err := a.store.ListQuests(context.Background(), flowRun.WorkspaceID); err == nil {
			for _, quest := range quests {
				if quest.ID != flowRun.QuestID || quest.Brief == nil {
					continue
				}
				for _, host := range quest.Brief.Permissions.NetworkHosts {
					add(host)
				}
				break
			}
		}
	}
	for key, value := range projectAgent.ToolPolicies {
		if !strings.EqualFold(strings.TrimSpace(value), "ALLOW") || !strings.HasPrefix(strings.ToLower(key), "network:") {
			continue
		}
		add(strings.TrimSpace(key[len("network:"):]))
	}
	// Composer always needs Packagist when installing; keep deterministic bootstrap usable.
	add("repo.packagist.org")
	add("packagist.org")
	add("github.com")
	return hosts
}

func toolResultExitCode(result domain.ToolResult) int {
	if len(result.Output) == 0 {
		return 0
	}
	var payload struct {
		ExitCode int `json:"exitCode"`
	}
	if json.Unmarshal(result.Output, &payload) != nil {
		return 0
	}
	return payload.ExitCode
}

// criteriaSupportDeterministicAccept is true when every parent criterion is a
// declared machine check (tool + arguments). Manual/ambiguous criteria keep LLM accept.
func criteriaSupportDeterministicAccept(brief *domain.TaskBrief) bool {
	if brief == nil || len(brief.Criteria) == 0 {
		return false
	}
	for _, c := range brief.Criteria {
		kind := strings.ToLower(strings.TrimSpace(c.Kind))
		if kind == "manual" || kind == "reproduction" {
			return false
		}
		if strings.TrimSpace(c.Tool) == "" || len(c.Arguments) == 0 {
			return false
		}
		if !strings.EqualFold(strings.TrimSpace(c.Tool), "run_command") {
			return false
		}
	}
	return true
}

// tryDeterministicAccept runs declared run_command criteria on the integrated tip
// without an LLM. Returns handled=false when criteria are not fully declarative.
func (a *App) tryDeterministicAccept(quest domain.Quest, flowRun domain.FlowRun, node domain.FlowNode, exec domain.ExecutionInstance, projectAgent domain.ProjectAgent) (bool, domain.FlowRun, error) {
	brief := quest.Brief
	if brief == nil {
		return false, flowRun, nil
	}
	if !criteriaSupportDeterministicAccept(brief) {
		return false, flowRun, nil
	}
	sandboxRecord, err := a.store.GetSandbox(context.Background(), exec.SandboxID)
	if err != nil {
		return true, flowRun, err
	}
	sandboxFS, err := workspace.Open(sandboxRecord.Path)
	if err != nil {
		return true, flowRun, err
	}
	hosts := bootstrapNetworkHosts(a, flowRun, projectAgent)
	policy := "DENY"
	if len(hosts) > 0 {
		policy = "ALLOWLIST"
	}
	runID := domain.NewID("run")
	now := time.Now().UTC()
	run := domain.Run{
		ID: runID, AgentID: projectAgent.ID, ProfileID: projectAgent.ID,
		WorkspaceID: flowRun.WorkspaceID, Task: "Deterministic accept: declared verification criteria",
		Status: domain.RunRunning, StartedAt: now,
	}
	if err = a.store.SaveRun(context.Background(), run); err != nil {
		return true, flowRun, err
	}
	exec.RunID = runID
	exec.Status = domain.RunRunning
	if err = a.store.SaveExecution(context.Background(), exec); err != nil {
		return true, flowRun, err
	}
	startPayload, _ := json.Marshal(map[string]any{
		"task": run.Task, "taskBrief": brief, "stageRole": domain.StageRoleAccept,
		"fastPath": domain.StageRoleAccept, "executionId": exec.ID,
	})
	if err = a.store.Append(context.Background(), domain.Event{
		ID: domain.NewID("event"), RunID: runID, Type: domain.EventRunStarted, Actor: "system",
		Data: startPayload, CreatedAt: now,
	}); err != nil {
		return true, flowRun, err
	}

	tool := tools.RunCommand{
		FS: sandboxFS, NetworkPolicy: policy, AllowedNetworkHosts: hosts,
		Executor: a.sandboxProcessExecutor(), RunID: runID, QuestID: flowRun.QuestID,
		DefaultTimeout: 10 * time.Minute, MaxOutput: 256 * 1024,
	}
	evidence := agent.CompletionEvidence{
		BriefVersion: brief.Version, WorkspaceRevision: 0, Status: "verified",
		Criteria: make([]agent.CriterionEvidence, 0, len(brief.Criteria)),
	}
	allOK := true
	summaries := make([]string, 0, len(brief.Criteria))
	for _, criterion := range brief.Criteria {
		var args map[string]any
		_ = json.Unmarshal(criterion.Arguments, &args)
		if args == nil {
			args = map[string]any{}
		}
		if _, ok := args["timeoutSeconds"]; !ok {
			args["timeoutSeconds"] = 300
		}
		if _, ok := args["reason"]; !ok {
			args["reason"] = "deterministic accept: " + criterion.ID
		}
		if cmd, _ := args["command"].(string); strings.TrimSpace(cmd) != "" {
			args["command"] = projectenv.ResolvePHPVerificationCommand(sandboxRecord.Path, cmd)
		}
		raw, _ := json.Marshal(args)
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
		result := tool.Execute(ctx, raw)
		cancel()
		exit := toolResultExitCode(result)
		expected := 0
		if criterion.ExpectedExitCode != nil {
			expected = *criterion.ExpectedExitCode
		}
		ce := agent.CriterionEvidence{
			CriterionID: criterion.ID, Text: criterion.Text, Kind: criterion.Kind,
			ExpectedExitCode: criterion.ExpectedExitCode,
			Check: &agent.CheckEvidence{
				Tool: criterion.Tool, Arguments: criterion.Arguments, ExitCode: &exit,
				Detail: string(result.Output), Status: "passed",
			},
		}
		if !result.OK || exit != expected {
			allOK = false
			ce.Status = "failed"
			if ce.Check != nil {
				ce.Check.Status = "unresolved"
			}
			if result.Error != nil {
				ce.Check.Detail = result.Error.Message
			}
			summaries = append(summaries, criterion.ID+": failed")
		} else {
			ce.Status = "satisfied"
			summaries = append(summaries, criterion.ID+": ok")
		}
		evidence.Criteria = append(evidence.Criteria, ce)
	}
	if allOK {
		evidence.Status = "verified"
	} else {
		evidence.Status = "blocked"
	}
	checkStatus := "accepted_after_revision"
	if !allOK {
		checkStatus = "rejected"
	}
	checkPayload, _ := json.Marshal(map[string]any{
		"status": checkStatus, "evidence": evidence, "checkKind": "accept",
	})
	if err = a.store.Append(context.Background(), domain.Event{
		ID: domain.NewID("event"), RunID: runID, Type: domain.EventCompletionChecked, Actor: "system",
		Data: checkPayload, CreatedAt: time.Now().UTC(),
	}); err != nil {
		return true, flowRun, err
	}
	finished := time.Now().UTC()
	run.FinishedAt = &finished
	run.DurationMs = finished.Sub(now).Milliseconds()
	run.Result = "deterministic accept: " + strings.Join(summaries, "; ")
	if allOK {
		run.Status = domain.RunCompleted
	} else {
		run.Status = domain.RunFailed
		run.Error = run.Result
	}
	if err = a.store.SaveRun(context.Background(), run); err != nil {
		return true, flowRun, err
	}
	exec.FinishedAt = &finished
	exec.DurationMs = run.DurationMs
	exec.Result = run.Result
	exec.Status = run.Status
	exec.Error = run.Error
	if err = a.store.SaveExecution(context.Background(), exec); err != nil {
		return true, flowRun, err
	}
	childStatus := domain.QuestFailed
	if allOK {
		childStatus = domain.QuestCompleted
	}
	a.setFlowChildQuestStatus(flowRun.ID, node.ID, childStatus)
	output := map[string]any{
		"executionId":        exec.ID,
		"runId":              runID,
		"result":             run.Result,
		"status":             string(exec.Status),
		"fastPath":           domain.StageRoleAccept,
		"completionStatus":   checkStatus,
		"completionEvidence": evidence,
	}
	if state := flowRun.NodeStates[node.ID]; state.Output != nil {
		if lineage, _ := state.Output["sandboxLineage"].(string); lineage != "" {
			output["sandboxLineage"] = lineage
		}
	}
	runtime := flowruntime.Runtime{Store: a.store}
	updated, completeErr := runtime.CompleteAgentNode(context.Background(), flowRun.ID, node.ID, allOK, output)
	if completeErr != nil {
		return true, flowRun, completeErr
	}
	a.recordFlowNodeArtifact(updated, node, allOK, exec.ID)
	return true, updated, nil
}
