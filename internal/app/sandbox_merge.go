package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"local-agent-workbench/internal/changesets"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/observability"
	"local-agent-workbench/internal/sandbox"
	"local-agent-workbench/internal/security"
)

type parallelMergePlan struct {
	BasePath      string
	Seeds         []sandbox.MergeSeed
	DependsOn     []string
	SupersedeSets []domain.ChangeSet
}

func sandboxParentExecutionIDs(record domain.SandboxRecord) []string {
	result := make([]string, 0, len(record.ParentExecutionIDs)+1)
	seen := map[string]bool{}
	for _, id := range record.ParentExecutionIDs {
		id = strings.TrimSpace(id)
		if id != "" && !seen[id] {
			seen[id] = true
			result = append(result, id)
		}
	}
	if id := strings.TrimSpace(record.ParentExecutionID); id != "" && !seen[id] {
		result = append(result, id)
	}
	return result
}

func (a *App) executionAncestorDepths(executionID string) (map[string]int, error) {
	type queued struct {
		id    string
		depth int
	}
	depths := map[string]int{}
	queue := []queued{{id: strings.TrimSpace(executionID)}}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current.id == "" {
			continue
		}
		if prior, exists := depths[current.id]; exists && prior <= current.depth {
			continue
		}
		depths[current.id] = current.depth
		record, err := a.store.GetSandboxByExecution(context.Background(), current.id)
		if err != nil {
			return nil, fmt.Errorf("load sandbox lineage for %s: %w", current.id, err)
		}
		for _, parentID := range sandboxParentExecutionIDs(record) {
			queue = append(queue, queued{id: parentID, depth: current.depth + 1})
		}
	}
	return depths, nil
}

func (a *App) parallelMergeBase(parentRecords []domain.SandboxRecord) (string, error) {
	if len(parentRecords) < 2 {
		return "", fmt.Errorf("parallel merge requires at least two parent sandboxes")
	}
	lineages := make([]map[string]int, 0, len(parentRecords))
	for _, record := range parentRecords {
		depths, err := a.executionAncestorDepths(record.ExecutionID)
		if err != nil {
			return "", err
		}
		lineages = append(lineages, depths)
	}
	bestID := ""
	bestMaxDepth := int(^uint(0) >> 1)
	bestSumDepth := bestMaxDepth
	for candidate, firstDepth := range lineages[0] {
		maxDepth := firstDepth
		sumDepth := firstDepth
		common := true
		for _, lineage := range lineages[1:] {
			depth, exists := lineage[candidate]
			if !exists {
				common = false
				break
			}
			if depth > maxDepth {
				maxDepth = depth
			}
			sumDepth += depth
		}
		if !common {
			continue
		}
		if maxDepth < bestMaxDepth || (maxDepth == bestMaxDepth && (sumDepth < bestSumDepth || (sumDepth == bestSumDepth && candidate < bestID))) {
			bestID, bestMaxDepth, bestSumDepth = candidate, maxDepth, sumDepth
		}
	}
	if bestID != "" {
		record, err := a.store.GetSandboxByExecution(context.Background(), bestID)
		if err != nil {
			return "", err
		}
		if info, statErr := os.Stat(record.Path); statErr != nil || !info.IsDir() {
			return "", fmt.Errorf("common ancestor sandbox for %s is unavailable", bestID)
		}
		return record.Path, nil
	}

	// Root branches have no execution ancestor, but every root now owns an
	// immutable baseline. They may be merged only when those snapshots are
	// byte-equivalent in the text domain managed by Change Sets.
	base := strings.TrimSpace(parentRecords[0].BaselinePath)
	if base == "" {
		return "", fmt.Errorf("parallel branch %s has no immutable baseline", parentRecords[0].ExecutionID)
	}
	for _, record := range parentRecords[1:] {
		candidate := strings.TrimSpace(record.BaselinePath)
		if candidate == "" {
			return "", fmt.Errorf("parallel branch %s has no immutable baseline", record.ExecutionID)
		}
		diffs, err := a.sandboxBackend.Diff(context.Background(), base, candidate)
		if err != nil {
			return "", err
		}
		if len(diffs) != 0 {
			return "", fmt.Errorf("parallel branches do not share the same immutable starting snapshot")
		}
	}
	return base, nil
}

func changeSetClosure(id string, byID map[string]domain.ChangeSet) (map[string]bool, error) {
	closure := map[string]bool{}
	var visit func(string) error
	visit = func(current string) error {
		current = strings.TrimSpace(current)
		if current == "" || closure[current] {
			return nil
		}
		set, exists := byID[current]
		if !exists {
			return fmt.Errorf("change set lineage references missing set %s", current)
		}
		closure[current] = true
		for _, dependencyID := range set.DependsOn {
			if err := visit(dependencyID); err != nil {
				return err
			}
		}
		return nil
	}
	return closure, visit(id)
}

func (a *App) nearestExecutionChangeSet(executionID string, latestByExecution map[string]domain.ChangeSet) (domain.ChangeSet, bool, error) {
	type queued struct {
		id    string
		depth int
	}
	queue := []queued{{id: executionID}}
	visited := map[string]bool{}
	bestDepth := -1
	var candidates []domain.ChangeSet
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current.id == "" || visited[current.id] || (bestDepth >= 0 && current.depth > bestDepth) {
			continue
		}
		visited[current.id] = true
		if set, exists := latestByExecution[current.id]; exists {
			bestDepth = current.depth
			candidates = append(candidates, set)
			continue
		}
		record, err := a.store.GetSandboxByExecution(context.Background(), current.id)
		if err != nil {
			return domain.ChangeSet{}, false, err
		}
		for _, parentID := range sandboxParentExecutionIDs(record) {
			queue = append(queue, queued{id: parentID, depth: current.depth + 1})
		}
	}
	if len(candidates) == 0 {
		return domain.ChangeSet{}, false, nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].CreatedAt.Equal(candidates[j].CreatedAt) {
			return candidates[i].ID < candidates[j].ID
		}
		return candidates[i].CreatedAt.After(candidates[j].CreatedAt)
	})
	return candidates[0], true, nil
}

func (a *App) parallelMergeChangeSets(workspaceID string, parentExecutionIDs []string) ([]string, []domain.ChangeSet, error) {
	sets, err := a.store.ListChangeSets(context.Background(), workspaceID)
	if err != nil {
		return nil, nil, err
	}
	byID := make(map[string]domain.ChangeSet, len(sets))
	latestByExecution := map[string]domain.ChangeSet{}
	for _, set := range sets {
		byID[set.ID] = set
		current, exists := latestByExecution[set.ExecutionID]
		if !exists || set.CreatedAt.After(current.CreatedAt) {
			latestByExecution[set.ExecutionID] = set
		}
	}
	closures := make([]map[string]bool, 0, len(parentExecutionIDs))
	union := map[string]bool{}
	for _, executionID := range parentExecutionIDs {
		head, exists, findErr := a.nearestExecutionChangeSet(executionID, latestByExecution)
		if findErr != nil {
			return nil, nil, findErr
		}
		closure := map[string]bool{}
		if exists {
			closure, findErr = changeSetClosure(head.ID, byID)
			if findErr != nil {
				return nil, nil, findErr
			}
		}
		closures = append(closures, closure)
		for id := range closure {
			union[id] = true
		}
	}
	shared := map[string]bool{}
	if len(closures) > 0 {
		for id := range closures[0] {
			shared[id] = true
		}
		for _, closure := range closures[1:] {
			for id := range shared {
				if !closure[id] {
					delete(shared, id)
				}
			}
		}
	}
	olderShared := map[string]bool{}
	for id := range shared {
		closure, closureErr := changeSetClosure(id, byID)
		if closureErr != nil {
			return nil, nil, closureErr
		}
		for dependencyID := range closure {
			if dependencyID != id && shared[dependencyID] {
				olderShared[dependencyID] = true
			}
		}
	}
	dependsOn := make([]string, 0, len(shared))
	for id := range shared {
		if !olderShared[id] {
			dependsOn = append(dependsOn, id)
		}
	}
	sort.Strings(dependsOn)

	supersede := make([]domain.ChangeSet, 0, len(union))
	for id := range union {
		if shared[id] {
			continue
		}
		set := byID[id]
		if set.Status != domain.ChangeSetPending && set.Status != domain.ChangeSetApproved {
			return nil, nil, fmt.Errorf("parallel branch change set %s cannot be merged from status %s", set.ID, set.Status)
		}
		supersede = append(supersede, set)
	}
	sort.Slice(supersede, func(i, j int) bool { return supersede[i].ID < supersede[j].ID })
	return dependsOn, supersede, nil
}

func (a *App) prepareParallelMerge(workspaceID string, parentExecutionIDs []string) (parallelMergePlan, error) {
	if len(parentExecutionIDs) < 2 {
		return parallelMergePlan{}, fmt.Errorf("parallel merge requires at least two executions")
	}
	parentRecords := make([]domain.SandboxRecord, 0, len(parentExecutionIDs))
	seeds := make([]sandbox.MergeSeed, 0, len(parentExecutionIDs))
	for _, executionID := range parentExecutionIDs {
		execution, err := a.findExecution(executionID)
		if err != nil {
			return parallelMergePlan{}, err
		}
		if execution.WorkspaceID != workspaceID {
			return parallelMergePlan{}, fmt.Errorf("parallel parent execution belongs to another workspace")
		}
		if execution.Status != domain.RunCompleted {
			return parallelMergePlan{}, fmt.Errorf("parallel parent execution %s is not completed", execution.ID)
		}
		record, err := a.store.GetSandbox(context.Background(), execution.SandboxID)
		if err != nil {
			return parallelMergePlan{}, err
		}
		parentRecords = append(parentRecords, record)
		seeds = append(seeds, sandbox.MergeSeed{ExecutionID: execution.ID, SandboxID: record.ID, Path: record.Path})
	}
	basePath, err := a.parallelMergeBase(parentRecords)
	if err != nil {
		return parallelMergePlan{}, err
	}
	dependsOn, supersedeSets, err := a.parallelMergeChangeSets(workspaceID, parentExecutionIDs)
	if err != nil {
		return parallelMergePlan{}, err
	}
	return parallelMergePlan{BasePath: basePath, Seeds: seeds, DependsOn: dependsOn, SupersedeSets: supersedeSets}, nil
}

func mergeResolutionsFromOutput(output map[string]any) []sandbox.MergeResolution {
	if output == nil || output["mergeResolutions"] == nil {
		return nil
	}
	encoded, err := json.Marshal(output["mergeResolutions"])
	if err != nil {
		return nil
	}
	var resolutions []sandbox.MergeResolution
	if json.Unmarshal(encoded, &resolutions) != nil {
		return nil
	}
	return resolutions
}

func mergeConflictsFromOutput(output map[string]any) []sandbox.MergeConflict {
	if output == nil || output["mergeConflicts"] == nil {
		return nil
	}
	encoded, err := json.Marshal(output["mergeConflicts"])
	if err != nil {
		return nil
	}
	var conflicts []sandbox.MergeConflict
	if json.Unmarshal(encoded, &conflicts) != nil {
		return nil
	}
	return conflicts
}

// ResolveFlowSandboxMerge records one explicit branch choice (or bounded
// manual result) for a conflicted parallel Join. Scheduling resumes only after
// every reported path has a resolution; partial decisions remain durable.
func (a *App) ResolveFlowSandboxMerge(flowRunID, nodeID string, resolution sandbox.MergeResolution) (domain.FlowRun, error) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return domain.FlowRun{}, err
	}
	// Resolution requests can arrive back-to-back from the IDE. Serialize the
	// read/modify/write cycle so a choice for one file cannot overwrite a
	// choice for another file in the same merge.
	a.flowMergeMu.Lock()
	defer a.flowMergeMu.Unlock()
	run, err := a.store.GetFlowRun(context.Background(), strings.TrimSpace(flowRunID))
	if err != nil {
		return domain.FlowRun{}, err
	}
	if run.WorkspaceID != ws.ID {
		return domain.FlowRun{}, fmt.Errorf("flow run belongs to another workspace")
	}
	state, exists := run.NodeStates[strings.TrimSpace(nodeID)]
	if !exists || state.Output == nil {
		return domain.FlowRun{}, fmt.Errorf("flow node %s not found", nodeID)
	}
	waitReason, _ := state.Output["waitReason"].(string)
	if waitReason != "sandbox_merge_conflict" {
		return domain.FlowRun{}, fmt.Errorf("flow node %s is not waiting for a sandbox merge resolution", nodeID)
	}
	resolution.Path = filepath.ToSlash(strings.TrimSpace(resolution.Path))
	resolution.Strategy = strings.TrimSpace(resolution.Strategy)
	if resolution.Path == "" {
		return domain.FlowRun{}, fmt.Errorf("merge resolution path is required")
	}
	conflicts := mergeConflictsFromOutput(state.Output)
	var conflict *sandbox.MergeConflict
	for index := range conflicts {
		if conflicts[index].Path == resolution.Path {
			conflict = &conflicts[index]
			break
		}
	}
	if conflict == nil {
		return domain.FlowRun{}, fmt.Errorf("path %s is not an unresolved merge conflict", resolution.Path)
	}
	switch resolution.Strategy {
	case "use_parent":
		valid := false
		for _, candidate := range conflict.Candidates {
			if candidate.ExecutionID == strings.TrimSpace(resolution.ExecutionID) {
				valid = true
				break
			}
		}
		if !valid {
			return domain.FlowRun{}, fmt.Errorf("execution %s is not a candidate for %s", resolution.ExecutionID, resolution.Path)
		}
	case "manual":
		if !resolution.Delete && resolution.Content == nil {
			return domain.FlowRun{}, fmt.Errorf("manual merge resolution content is required")
		}
		if resolution.Content != nil && len(*resolution.Content) > 1024*1024 {
			return domain.FlowRun{}, fmt.Errorf("manual merge resolution exceeds 1 MiB")
		}
	default:
		return domain.FlowRun{}, fmt.Errorf("unsupported merge resolution strategy %q", resolution.Strategy)
	}
	resolutions := mergeResolutionsFromOutput(state.Output)
	replaced := false
	for index := range resolutions {
		if resolutions[index].Path == resolution.Path {
			resolutions[index] = resolution
			replaced = true
			break
		}
	}
	if !replaced {
		resolutions = append(resolutions, resolution)
	}
	sort.Slice(resolutions, func(i, j int) bool { return resolutions[i].Path < resolutions[j].Path })
	state.Output["mergeResolutions"] = resolutions
	resolvedPaths := map[string]bool{}
	for _, item := range resolutions {
		resolvedPaths[item.Path] = true
	}
	allResolved := true
	for _, item := range conflicts {
		if !resolvedPaths[item.Path] {
			allResolved = false
			break
		}
	}
	if allResolved {
		state.Output["waitReason"] = ""
		state.Output["needsSchedule"] = true
	}
	run.NodeStates[nodeID] = state
	if err = a.store.SaveFlowRun(context.Background(), run); err != nil {
		return domain.FlowRun{}, err
	}
	if allResolved {
		if err = a.scheduleFlowAgentExecutionsFromRun(run); err != nil {
			return domain.FlowRun{}, err
		}
		return a.store.GetFlowRun(context.Background(), run.ID)
	}
	return run, nil
}

func (a *App) startMergedSandboxedExecution(projectAgentID, task, questID, flowRunID, flowNodeID string, parentExecutionIDs []string, resolutions []sandbox.MergeResolution) (domain.ExecutionInstance, sandbox.MergeResult, *domain.ChangeSet, error) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return domain.ExecutionInstance{}, sandbox.MergeResult{}, nil, err
	}
	agent, err := a.store.GetProjectAgent(context.Background(), projectAgentID)
	if err != nil {
		return domain.ExecutionInstance{}, sandbox.MergeResult{}, nil, err
	}
	plan, err := a.prepareParallelMerge(ws.ID, parentExecutionIDs)
	if err != nil {
		return domain.ExecutionInstance{}, sandbox.MergeResult{}, nil, err
	}
	now := time.Now().UTC()
	snapshot, err := a.runtimeSnapshotForProjectAgent(ws.ID, agent, now)
	if err != nil {
		return domain.ExecutionInstance{}, sandbox.MergeResult{}, nil, err
	}
	execID := domain.NewID("execution")
	manager, err := a.sandboxMergeBackend()
	if err != nil {
		return domain.ExecutionInstance{}, sandbox.MergeResult{}, nil, err
	}
	brief, err := a.taskBriefForQuest(context.Background(), ws.ID, questID)
	if err != nil {
		return domain.ExecutionInstance{}, sandbox.MergeResult{}, nil, err
	}
	merged, err := manager.Merge(context.Background(), sandbox.MergeRequest{
		WorkspaceID: ws.ID, ExecutionID: execID, BasePath: plan.BasePath, Seeds: plan.Seeds,
		Runtime:     managedSandboxRuntimeForBrief(brief),
		Resolutions: resolutions, BaselineChangeSetIDs: plan.DependsOn,
	})
	if err != nil || len(merged.Conflicts) > 0 {
		return domain.ExecutionInstance{}, merged, nil, err
	}
	exec := domain.ExecutionInstance{
		ID: execID, WorkspaceID: ws.ID, ProjectAgentID: agent.ID, QuestID: questID,
		FlowRunID: flowRunID, FlowNodeID: flowNodeID,
		SandboxID: merged.Record.ID, Task: task, Status: domain.RunPending,
		Snapshot:  snapshot,
		StartedAt: now,
	}
	diffs, err := manager.Diff(context.Background(), plan.BasePath, merged.Record.BaselinePath)
	if err != nil {
		_ = manager.Close(context.Background(), merged.Record, ws.Path)
		return domain.ExecutionInstance{}, sandbox.MergeResult{}, nil, err
	}
	var mergeSet *domain.ChangeSet
	if len(diffs) > 0 || len(plan.SupersedeSets) > 0 {
		built := changesets.BuildWithContents(changesets.BuildRequest{
			WorkspaceID: ws.ID, ExecutionID: exec.ID, QuestID: questID,
			Title: "Merged parallel branches", DependsOn: plan.DependsOn,
		}, diffs)
		built.Kind = "merge"
		for _, source := range plan.SupersedeSets {
			built.Supersedes = append(built.Supersedes, source.ID)
		}
		mergeSet = &built
		merged.Record.BaselineChangeSetIDs = []string{built.ID}
		for index := range plan.SupersedeSets {
			plan.SupersedeSets[index].Status = domain.ChangeSetSuperseded
			plan.SupersedeSets[index].SupersededBy = built.ID
			plan.SupersedeSets[index].UpdatedAt = now
		}
	} else {
		merged.Record.BaselineChangeSetIDs = append([]string(nil), plan.DependsOn...)
	}
	if err = a.store.SaveParallelMergeExecution(context.Background(), exec, merged.Record, mergeSet, plan.SupersedeSets); err != nil {
		_ = manager.Close(context.Background(), merged.Record, ws.Path)
		return domain.ExecutionInstance{}, sandbox.MergeResult{}, nil, err
	}
	slog.Info("parallel sandbox merged",
		"execution_id", exec.ID,
		"project_agent_id", agent.ID,
		"quest_id", questID,
		"parent_execution_ids", parentExecutionIDs,
		"paths", len(merged.Paths),
		"merge_change_set_id", func() string {
			if mergeSet != nil {
				return mergeSet.ID
			}
			return ""
		}(),
		"task_preview", observability.Snippet(security.Redact(task), 160),
	)
	return exec, merged, mergeSet, nil
}
