// Песочницы и наборы изменений: сборка, родословная, применение и откат.
package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
	"time"

	"local-agent-workbench/internal/changesets"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/observability"
	"local-agent-workbench/internal/sandbox"
	"local-agent-workbench/internal/security"
)

func (a *App) StartSandboxedExecution(projectAgentID, task, questID string) (domain.ExecutionInstance, error) {
	return a.startSandboxedExecution(projectAgentID, task, questID, "")
}

func (a *App) startSandboxedExecution(projectAgentID, task, questID, parentExecutionID string) (domain.ExecutionInstance, error) {
	return a.startSandboxedExecutionWithSeed(projectAgentID, task, questID, parentExecutionID, "")
}

func (a *App) startSandboxedExecutionWithSeed(projectAgentID, task, questID, parentExecutionID, rootSeedPath string) (domain.ExecutionInstance, error) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return domain.ExecutionInstance{}, err
	}
	agent, err := a.store.GetProjectAgent(context.Background(), projectAgentID)
	if err != nil {
		// «sql: no rows in result set» — это не объяснение. Запуск без
		// заведённого исполнителя отвечал человеку строкой из драйвера базы,
		// по которой не видно ни того, что искали, ни что делать дальше.
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ExecutionInstance{}, fmt.Errorf("исполнитель %q не найден в этом проекте: заведите агента в Гильдии или попросите Мастера собрать его", projectAgentID)
		}
		return domain.ExecutionInstance{}, err
	}
	now := time.Now().UTC()
	execID := domain.NewID("execution")
	snapshot, err := a.runtimeSnapshotForProjectAgent(ws.ID, agent, now)
	if err != nil {
		return domain.ExecutionInstance{}, err
	}
	createRequest := sandbox.CreateRequest{
		WorkspaceID: ws.ID, WorkspacePath: ws.Path, ExecutionID: execID,
		PreferWorktree: true, LiveWorkspace: sandbox.LiveFileMutationEnabled() && !a.questRequiresIsolatedWorkspace(context.Background(), ws.ID, questID),
	}
	if brief, briefErr := a.taskBriefForQuest(context.Background(), ws.ID, questID); briefErr == nil {
		createRequest.Runtime = managedSandboxRuntimeForBrief(brief)
		if len(createRequest.Runtime.RequiredCommands) > 0 {
			createRequest.Runtime.Progress = func(phase, message string) {
				a.updateRuntimeProvisioningProgressV2(context.Background(), ws.ID, questID, phase, message)
			}
			a.updateRuntimeProvisioningProgressV2(context.Background(), ws.ID, questID, "runtime_provisioning",
				"Проверяем системные инструменты sandbox: "+strings.Join(createRequest.Runtime.RequiredCommands, ", "))
		}
	}
	if strings.TrimSpace(rootSeedPath) != "" {
		createRequest.SeedPath = rootSeedPath
		createRequest.PreferWorktree = false
	}
	if strings.TrimSpace(parentExecutionID) != "" {
		parent, parentErr := a.findExecution(parentExecutionID)
		if parentErr != nil {
			return domain.ExecutionInstance{}, fmt.Errorf("load parent execution: %w", parentErr)
		}
		if parent.WorkspaceID != ws.ID {
			return domain.ExecutionInstance{}, fmt.Errorf("parent execution belongs to another workspace")
		}
		if parent.Status != domain.RunCompleted {
			return domain.ExecutionInstance{}, fmt.Errorf("parent execution %s is not completed", parent.ID)
		}
		parentSandbox, sandboxErr := a.store.GetSandbox(context.Background(), parent.SandboxID)
		if sandboxErr != nil {
			return domain.ExecutionInstance{}, fmt.Errorf("load parent sandbox: %w", sandboxErr)
		}
		createRequest.SeedPath = parentSandbox.Path
		createRequest.ParentSandboxID = parentSandbox.ID
		createRequest.ParentExecutionID = parent.ID
		createRequest.BaselineChangeSetIDs = a.changeSetDependencyIDs(ws.ID, parent.ID)
	}
	sandboxRecord, err := a.sandboxBackend.Create(context.Background(), createRequest)
	if err != nil {
		return domain.ExecutionInstance{}, err
	}
	if len(createRequest.Runtime.RequiredCommands) > 0 {
		a.updateRuntimeProvisioningProgressV2(context.Background(), ws.ID, questID, "runtime_ready",
			"Runtime sandbox готов: "+strings.TrimSpace(sandboxRecord.BackendImage))
	}
	exec := domain.ExecutionInstance{
		ID: execID, WorkspaceID: ws.ID, ProjectAgentID: agent.ID, QuestID: questID,
		SandboxID: sandboxRecord.ID, Task: task, Status: domain.RunPending,
		Snapshot:  snapshot,
		StartedAt: now,
	}
	if err := a.store.SaveSandbox(context.Background(), sandboxRecord); err != nil {
		_ = a.sandboxBackend.Close(context.Background(), sandboxRecord, ws.Path)
		return domain.ExecutionInstance{}, err
	}
	if err := a.store.SaveExecution(context.Background(), exec); err != nil {
		_ = a.sandboxBackend.Close(context.Background(), sandboxRecord, ws.Path)
		if cleanupErr := a.store.DeleteOrphanSandbox(context.Background(), sandboxRecord.ID, exec.ID); cleanupErr != nil {
			slog.Warn("orphan sandbox metadata cleanup failed", "sandbox_id", sandboxRecord.ID, "execution_id", exec.ID, "error", cleanupErr)
		}
		return domain.ExecutionInstance{}, err
	}
	slog.Info("hub execution created",
		"execution_id", exec.ID,
		"project_agent_id", agent.ID,
		"quest_id", questID,
		"sandbox_id", sandboxRecord.ID,
		"parent_execution_id", sandboxRecord.ParentExecutionID,
		"task_preview", observability.Snippet(security.Redact(task), 160),
	)
	return exec, nil
}

func (a *App) updateRuntimeProvisioningProgressV2(ctx context.Context, workspaceID, questID, phase, message string) {
	quests, err := a.store.ListQuests(ctx, workspaceID)
	if err != nil {
		return
	}
	byID := make(map[string]domain.Quest, len(quests))
	for _, quest := range quests {
		byID[quest.ID] = quest
	}
	seen := map[string]bool{}
	for questID != "" && !seen[questID] {
		seen[questID] = true
		quest, ok := byID[questID]
		if !ok {
			return
		}
		if quest.Controller != nil && quest.Controller["source"] == "work_order_v2" {
			if !domain.IsTerminalQuestStatus(quest.Status) {
				a.updateWorkOrderLaunchProgressV2(ctx, &quest, phase, message)
			}
			return
		}
		questID = quest.ParentID
	}
}

func (a *App) BuildChangeSet(executionID string) (domain.ChangeSet, error) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return domain.ChangeSet{}, err
	}
	executions, err := a.store.ListExecutions(context.Background(), ws.ID, 200)
	if err != nil {
		return domain.ChangeSet{}, err
	}
	var exec domain.ExecutionInstance
	found := false
	for _, item := range executions {
		if item.ID == executionID {
			exec = item
			found = true
			break
		}
	}
	if !found {
		return domain.ChangeSet{}, fmt.Errorf("execution %s not found", executionID)
	}
	sandboxRecord, err := a.store.GetSandbox(context.Background(), exec.SandboxID)
	if err != nil {
		return domain.ChangeSet{}, err
	}
	baselinePath, dependencies, err := a.changeSetLineage(ws.ID, sandboxRecord)
	if err != nil {
		return domain.ChangeSet{}, err
	}
	applier := changesets.Applier{Store: a.store}
	return applier.BuildFromSandbox(context.Background(), changesets.BuildRequest{
		WorkspaceID: ws.ID, ExecutionID: exec.ID, QuestID: exec.QuestID, Title: "Changes from " + exec.ID,
		WorkspacePath: ws.Path, BaselinePath: baselinePath, SandboxPath: sandboxRecord.Path, DependsOn: dependencies,
	})
}

func (a *App) changeSetLineage(workspaceID string, record domain.SandboxRecord) (string, []string, error) {
	baselinePath := strings.TrimSpace(record.BaselinePath)
	if baselinePath != "" {
		if info, err := os.Stat(baselinePath); err == nil && info.IsDir() {
			if len(record.BaselineChangeSetIDs) > 0 {
				return baselinePath, append([]string(nil), record.BaselineChangeSetIDs...), nil
			}
			if strings.TrimSpace(record.ParentExecutionID) == "" {
				return baselinePath, nil, nil
			}
			return baselinePath, a.changeSetDependencyIDs(workspaceID, record.ParentExecutionID), nil
		}
	}
	if strings.TrimSpace(record.ParentExecutionID) == "" {
		return "", nil, fmt.Errorf("sandbox baseline for execution %s is unavailable", record.ExecutionID)
	}
	// Baselines live in the same managed temp root as the execution sandbox.
	// If only that snapshot was cleaned up, the completed parent sandbox is an
	// equivalent immutable source. Never fall back to the live workspace: that
	// would silently turn an incremental stage into a cumulative Change Set.
	if strings.TrimSpace(record.ParentSandboxID) != "" {
		parent, err := a.store.GetSandbox(context.Background(), record.ParentSandboxID)
		if err == nil {
			if info, statErr := os.Stat(parent.Path); statErr == nil && info.IsDir() {
				return parent.Path, a.changeSetDependencyIDs(workspaceID, record.ParentExecutionID), nil
			}
		}
	}
	return "", nil, fmt.Errorf("sandbox lineage baseline for execution %s is unavailable", record.ExecutionID)
}

func (a *App) changeSetDependencyIDs(workspaceID, parentExecutionID string) []string {
	sets, err := a.store.ListChangeSets(context.Background(), workspaceID)
	if err != nil {
		return nil
	}
	latestByExecution := map[string]domain.ChangeSet{}
	for _, set := range sets {
		current, exists := latestByExecution[set.ExecutionID]
		if !exists || set.CreatedAt.After(current.CreatedAt) {
			latestByExecution[set.ExecutionID] = set
		}
	}
	visited := map[string]bool{}
	queue := []string{strings.TrimSpace(parentExecutionID)}
	for len(queue) > 0 {
		currentExecutionID := queue[0]
		queue = queue[1:]
		if currentExecutionID == "" || visited[currentExecutionID] {
			continue
		}
		visited[currentExecutionID] = true
		if set, ok := latestByExecution[currentExecutionID]; ok {
			return []string{set.ID}
		}
		record, getErr := a.store.GetSandboxByExecution(context.Background(), currentExecutionID)
		if getErr != nil {
			continue
		}
		if len(record.BaselineChangeSetIDs) > 0 {
			return append([]string(nil), record.BaselineChangeSetIDs...)
		}
		queue = append(queue, sandboxParentExecutionIDs(record)...)
	}
	return nil
}

func (a *App) ApplyChangeSet(changeSetID string) (changesets.ApplyResult, error) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return changesets.ApplyResult{}, err
	}
	set, err := a.store.GetChangeSet(context.Background(), changeSetID)
	if err != nil {
		return changesets.ApplyResult{}, err
	}
	if err = a.guardWorld(set.WorkspaceID); err != nil {
		return changesets.ApplyResult{}, err
	}
	for _, dependencyID := range set.DependsOn {
		dependency, getErr := a.store.GetChangeSet(context.Background(), dependencyID)
		if getErr != nil {
			return changesets.ApplyResult{}, fmt.Errorf("load change set dependency %s: %w", dependencyID, getErr)
		}
		if dependency.WorkspaceID != set.WorkspaceID {
			return changesets.ApplyResult{}, fmt.Errorf("change set dependency %s belongs to another workspace", dependencyID)
		}
		if dependency.Status != domain.ChangeSetApplied {
			return changesets.ApplyResult{}, fmt.Errorf("apply prerequisite change set %s first (status=%s)", dependencyID, dependency.Status)
		}
	}
	applier := changesets.Applier{Store: a.store}
	result, err := applier.Apply(context.Background(), ws.Path, changeSetID)
	if err != nil {
		return changesets.ApplyResult{}, err
	}
	if len(result.Applied) > 0 {
		a.InvalidateProjectIndex()
		_ = a.cache.DeletePrefix(context.Background(), a.cachePrefix())
		_, _ = a.SaveMemory(domain.MemoryRecord{
			WorkspaceID: ws.ID, Kind: domain.MemoryProject,
			Content: fmt.Sprintf("Applied change set %s (%d files): %s", result.ChangeSet.Title, len(result.Applied), strings.Join(result.Applied, ", ")),
			Source:  "changeset-apply", Confidence: 0.75, Pinned: false,
		})
	}
	return result, nil
}

func (a *App) RejectChangeSet(changeSetID string) (domain.ChangeSet, error) {
	set, err := a.store.GetChangeSet(context.Background(), changeSetID)
	if err != nil {
		return domain.ChangeSet{}, err
	}
	if err = a.guardWorld(set.WorkspaceID); err != nil {
		return domain.ChangeSet{}, err
	}
	if err = a.ensureNoChangeSetDependents(set, "reject"); err != nil {
		return domain.ChangeSet{}, err
	}
	applier := changesets.Applier{Store: a.store}
	return applier.Reject(context.Background(), changeSetID)
}

func (a *App) RevertChangeSet(changeSetID string) (changesets.ApplyResult, error) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return changesets.ApplyResult{}, err
	}
	set, err := a.store.GetChangeSet(context.Background(), changeSetID)
	if err != nil {
		return changesets.ApplyResult{}, err
	}
	if err = a.guardWorld(set.WorkspaceID); err != nil {
		return changesets.ApplyResult{}, err
	}
	if err = a.ensureNoChangeSetDependents(set, "revert"); err != nil {
		return changesets.ApplyResult{}, err
	}
	applier := changesets.Applier{Store: a.store}
	result, err := applier.Revert(context.Background(), ws.Path, changeSetID)
	if err != nil {
		return changesets.ApplyResult{}, err
	}
	if len(result.Applied) > 0 {
		a.InvalidateProjectIndex()
		_ = a.cache.DeletePrefix(context.Background(), a.cachePrefix())
	}
	return result, nil
}

func (a *App) ensureNoChangeSetDependents(set domain.ChangeSet, action string) error {
	sets, err := a.store.ListChangeSets(context.Background(), set.WorkspaceID)
	if err != nil {
		return err
	}
	for _, candidate := range sets {
		if candidate.ID == set.ID || candidate.Status == domain.ChangeSetRejected || candidate.Status == domain.ChangeSetReverted {
			continue
		}
		if slices.Contains(candidate.DependsOn, set.ID) {
			return fmt.Errorf("cannot %s change set %s while dependent change set %s is %s", action, set.ID, candidate.ID, candidate.Status)
		}
	}
	executions, err := a.store.ListExecutions(context.Background(), set.WorkspaceID, 500)
	if err != nil {
		return err
	}
	for _, execution := range executions {
		if execution.Status != domain.RunPending && execution.Status != domain.RunRunning && execution.Status != domain.RunInterrupted && execution.Status != domain.RunPaused && execution.Status != domain.RunWaiting {
			continue
		}
		record, getErr := a.store.GetSandboxByExecution(context.Background(), execution.ID)
		if getErr != nil {
			continue
		}
		dependsOnExecution := record.ParentExecutionID == set.ExecutionID || slices.Contains(record.ParentExecutionIDs, set.ExecutionID)
		dependsOnChangeSet := slices.Contains(record.BaselineChangeSetIDs, set.ID)
		if dependsOnExecution || dependsOnChangeSet {
			// The refusal is the whole message the card shows. A bare "while
			// dependent execution … is interrupted" was clicked 15 times in a
			// row: an interrupted execution never resumes by itself, and nothing
			// said that stopping it is the way out.
			verb := map[string]string{"reject": "отклонить", "revert": "откатить"}[action]
			if verb == "" {
				verb = action
			}
			return fmt.Errorf("нельзя %s набор правок %s: от него зависит исполнение %s (%s). Остановите это исполнение или отмените его квест, затем повторите", verb, set.ID, execution.ID, execution.Status)
		}
	}
	return nil
}
