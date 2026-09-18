package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/flowruntime"
)

type ReplanQuestRequest struct {
	QuestID      string                    `json:"questId"`
	Reason       string                    `json:"reason"`
	CriterionIDs []string                  `json:"criterionIds,omitempty"`
	Stages       []domain.ReplanStagePatch `json:"stages"`
}

type ReplanQuestResult struct {
	Replan domain.QuestReplan `json:"replan"`
	Flow   domain.FlowGraph   `json:"flow"`
}

// ReplanQuest revises unstarted or in-flight agent stages of an active structured quest.
// Replaced waiting stages are cancelled; the live graph is written into FlowRun.Snapshot["graph"].
func (a *App) ReplanQuest(ctx context.Context, req ReplanQuestRequest) (ReplanQuestResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	req.Reason = strings.TrimSpace(req.Reason)
	if req.QuestID == "" || req.Reason == "" || len(req.Stages) == 0 {
		return ReplanQuestResult{}, errors.New("replan requires questId, reason and at least one stage")
	}
	ws, err := a.requireWorkspace()
	if err != nil {
		return ReplanQuestResult{}, err
	}
	quests, err := a.store.ListQuests(ctx, ws.ID)
	if err != nil {
		return ReplanQuestResult{}, err
	}
	var quest domain.Quest
	found := false
	for _, item := range quests {
		if item.ID == req.QuestID {
			quest, found = item, true
			break
		}
	}
	if !found {
		return ReplanQuestResult{}, errors.New("quest not found")
	}
	if quest.Brief == nil || !domain.IsTaskBriefApproved(*quest.Brief) {
		return ReplanQuestResult{}, errors.New("replans require an approved structured task brief")
	}
	if quest.FlowID == "" {
		return ReplanQuestResult{}, errors.New("quest has no flow to replan")
	}
	count, err := a.store.CountQuestReplans(ctx, quest.ID)
	if err != nil {
		return ReplanQuestResult{}, err
	}
	maxReplans := quest.Brief.Budget.MaxReplans
	if maxReplans < 0 {
		return ReplanQuestResult{}, errors.New("replans are disabled for this task")
	}
	if count >= maxReplans {
		return ReplanQuestResult{}, fmt.Errorf("replan limit reached (%d/%d)", count, maxReplans)
	}

	flowRun, hasRun, err := a.resolveActiveQuestFlowRun(ctx, quest)
	if err != nil {
		return ReplanQuestResult{}, err
	}
	flow, err := a.store.GetFlow(ctx, quest.FlowID)
	if err != nil {
		return ReplanQuestResult{}, err
	}
	if hasRun {
		if snap, ok, snapErr := flowruntime.FlowFromSnapshot(flowRun); snapErr != nil {
			return ReplanQuestResult{}, snapErr
		} else if ok {
			flow = snap
		}
	}

	byID := map[string]*domain.FlowNode{}
	for i := range flow.Nodes {
		byID[flow.Nodes[i].ID] = &flow.Nodes[i]
	}
	criterionSet := map[string]bool{}
	for _, c := range quest.Brief.Criteria {
		criterionSet[c.ID] = true
	}
	for _, id := range req.CriterionIDs {
		if !criterionSet[id] {
			return ReplanQuestResult{}, fmt.Errorf("unknown criterion %q", id)
		}
	}
	digests := make([]string, 0, len(req.Stages))
	updated := make([]string, 0, len(req.Stages))
	stopped := make([]string, 0)
	seenNodes := map[string]bool{}
	if hasRun && flowRun.NodeStates == nil {
		flowRun.NodeStates = map[string]domain.FlowNodeState{}
	}
	for _, patch := range req.Stages {
		nodeID := strings.TrimSpace(patch.NodeID)
		if nodeID == "" {
			createdID, createErr := appendFutureWaveStage(&flow, patch)
			if createErr != nil {
				return ReplanQuestResult{}, createErr
			}
			nodeID = createdID
			byID = map[string]*domain.FlowNode{}
			for i := range flow.Nodes {
				byID[flow.Nodes[i].ID] = &flow.Nodes[i]
			}
			if hasRun {
				flowRun.NodeStates[nodeID] = domain.FlowNodeState{Status: "waiting_agent", Output: map[string]any{"needsSchedule": true}}
			}
		}
		if seenNodes[nodeID] {
			return ReplanQuestResult{}, fmt.Errorf("duplicate stage node %s", nodeID)
		}
		seenNodes[nodeID] = true
		node := byID[nodeID]
		if node == nil || node.Kind != domain.FlowNodeAgent {
			return ReplanQuestResult{}, fmt.Errorf("node %s is not an agent stage", nodeID)
		}
		state := domain.FlowNodeState{}
		if hasRun {
			state = flowRun.NodeStates[nodeID]
		}
		status := strings.TrimSpace(state.Status)
		switch status {
		case "completed", "failed", "skipped":
			return ReplanQuestResult{}, fmt.Errorf("node %s already finished with status %s", nodeID, status)
		case "waiting_join":
			return ReplanQuestResult{}, fmt.Errorf("node %s status %s cannot be replanned", nodeID, status)
		case "waiting_agent", "waiting_approval":
			executionID, _ := state.Output["executionId"].(string)
			if executionID != "" {
				runID, stopErr := a.cancelFlowNodeExecution(ctx, executionID, "отменено: этап заменён replanning")
				if stopErr != nil {
					return ReplanQuestResult{}, fmt.Errorf("stop replaced stage %s: %w", nodeID, stopErr)
				}
				if runID != "" {
					stopped = append(stopped, runID)
				} else {
					stopped = append(stopped, executionID)
				}
			}
			if hasRun {
				if state.Output == nil {
					state.Output = map[string]any{}
				}
				state.Status = "waiting_agent"
				state.Output["needsSchedule"] = true
				state.Output["replanStopped"] = true
				delete(state.Output, "executionId")
				delete(state.Output, "waitReason")
				delete(state.Output, "startError")
				flowRun.NodeStates[nodeID] = state
			}
		case "", "blocked", "ready":
			// Unstarted — patch graph only.
		default:
			return ReplanQuestResult{}, fmt.Errorf("node %s status %s cannot be replanned", nodeID, status)
		}
		if agentID := strings.TrimSpace(patch.AgentID); agentID != "" {
			if _, agentErr := a.store.GetProjectAgent(ctx, agentID); agentErr != nil {
				return ReplanQuestResult{}, fmt.Errorf("agent %s: %w", agentID, agentErr)
			}
			node.AgentID = agentID
		}
		if name := strings.TrimSpace(patch.Name); name != "" {
			node.Name = name
		}
		if instruction := strings.TrimSpace(patch.Instruction); instruction != "" {
			if node.Config == nil {
				node.Config = map[string]any{}
			}
			node.Config["instruction"] = instruction
		}
		if node.Config == nil {
			node.Config = map[string]any{}
		}
		node.Config["replanSeq"] = count + 1
		node.Config["replanReason"] = req.Reason
		if len(req.CriterionIDs) > 0 {
			node.Config["criterionIds"] = append([]string(nil), req.CriterionIDs...)
		}
		digest := stagePatchDigest(nodeID, node.AgentID, node.Name, node.Config)
		digests = append(digests, digest)
		updated = append(updated, nodeID)
	}
	sort.Strings(digests)
	if previous, prevErr := a.store.LatestQuestReplanDigests(ctx, quest.ID); prevErr == nil && len(previous) > 0 {
		prevCopy := append([]string(nil), previous...)
		sort.Strings(prevCopy)
		if strings.Join(prevCopy, ",") == strings.Join(digests, ",") {
			return ReplanQuestResult{}, errors.New("replan duplicates the previous stage set")
		}
	}
	flow.UpdatedAt = time.Now().UTC()
	// Persist definition via store (App.SaveFlow rejects active runs).
	if err = a.store.SaveFlow(ctx, flow); err != nil {
		return ReplanQuestResult{}, err
	}
	flowRunID := ""
	if hasRun {
		flowRunID = flowRun.ID
		if flowRun.Snapshot == nil {
			flowRun.Snapshot = map[string]any{}
		}
		flowRun.Snapshot["graph"] = flow
		if err = a.store.SaveFlowRun(ctx, flowRun); err != nil {
			return ReplanQuestResult{}, err
		}
	}
	replan := domain.QuestReplan{
		ID: domain.NewID("replan"), WorkspaceID: ws.ID, QuestID: quest.ID, FlowID: flow.ID, FlowRunID: flowRunID,
		Seq: count + 1, Reason: req.Reason, CriterionIDs: append([]string(nil), req.CriterionIDs...),
		StageDigests: digests, StoppedRuns: stopped, UpdatedNodes: updated, CreatedAt: time.Now().UTC(),
	}
	if err = a.store.SaveQuestReplan(ctx, replan); err != nil {
		return ReplanQuestResult{}, err
	}
	if quest.Kind == "project" {
		quest.ControllerState = controllerExecutingWave
		quest.Status = domain.QuestActive
		if quest.Controller == nil {
			quest.Controller = map[string]any{}
		}
		quest.Controller["lastReplanSeq"] = replan.Seq
		quest.Controller["lastReplanReason"] = replan.Reason
		invalidateDownstreamArtifacts(&quest, "")
		delete(quest.Controller, "pendingReplan")
		delete(quest.Controller, "pendingReplanReason")
		quest.UpdatedAt = time.Now().UTC()
		if err = a.store.SaveQuest(ctx, quest); err != nil {
			return ReplanQuestResult{}, err
		}
	}
	if hasRun {
		if schedErr := a.scheduleFlowAgentExecutionsFromRun(flowRun); schedErr != nil {
			return ReplanQuestResult{Replan: replan, Flow: flow}, schedErr
		}
	}
	return ReplanQuestResult{Replan: replan, Flow: flow}, nil
}

func (a *App) resolveActiveQuestFlowRun(ctx context.Context, quest domain.Quest) (domain.FlowRun, bool, error) {
	if quest.FlowRunID != "" {
		run, err := a.store.GetFlowRun(ctx, quest.FlowRunID)
		if err == nil {
			return run, true, nil
		}
	}
	runs, err := a.store.ListFlowRunsByFlowID(ctx, quest.FlowID)
	if err != nil {
		return domain.FlowRun{}, false, err
	}
	var latest domain.FlowRun
	var latestActive domain.FlowRun
	found, foundActive := false, false
	for _, run := range runs {
		if quest.ID != "" && run.QuestID != "" && run.QuestID != quest.ID {
			continue
		}
		if !found || run.StartedAt.After(latest.StartedAt) {
			latest, found = run, true
		}
		switch run.Status {
		case domain.RunRunning, domain.RunWaiting, domain.RunPaused, domain.RunInterrupted:
			if !foundActive || run.StartedAt.After(latestActive.StartedAt) {
				latestActive, foundActive = run, true
			}
		}
	}
	if foundActive {
		return latestActive, true, nil
	}
	if found {
		return latest, true, nil
	}
	return domain.FlowRun{}, false, nil
}

// cancelFlowNodeExecution stops a flow-node execution, including Cursor runs without a headless RunID.
func (a *App) cancelFlowNodeExecution(ctx context.Context, executionID, reason string) (string, error) {
	exec, err := a.store.GetExecution(ctx, executionID)
	if err != nil {
		return "", err
	}
	switch exec.Status {
	case domain.RunCompleted, domain.RunFailed, domain.RunCancelled:
		return exec.RunID, nil
	}
	if exec.RunID != "" {
		if cancelErr := a.CancelRun(exec.RunID); cancelErr != nil {
			return "", cancelErr
		}
		return exec.RunID, nil
	}
	now := time.Now().UTC()
	exec.Status = domain.RunCancelled
	exec.Error = reason
	exec.FinishedAt = &now
	if !exec.StartedAt.IsZero() {
		exec.DurationMs = now.Sub(exec.StartedAt).Milliseconds()
	}
	if saveErr := a.store.SaveExecution(ctx, exec); saveErr != nil {
		return "", saveErr
	}
	return "", nil
}

// ListQuestReplans returns governed replan history for a quest (newest last).
func (a *App) ListQuestReplans(ctx context.Context, questID string) ([]domain.QuestReplan, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	questID = strings.TrimSpace(questID)
	if questID == "" {
		return nil, errors.New("questId is required")
	}
	if _, err := a.requireWorkspace(); err != nil {
		return nil, err
	}
	return a.store.ListQuestReplans(ctx, questID)
}

func appendFutureWaveStage(flow *domain.FlowGraph, patch domain.ReplanStagePatch) (string, error) {
	if flow == nil {
		return "", errors.New("flow is required")
	}
	agentID := strings.TrimSpace(patch.AgentID)
	if agentID == "" {
		return "", errors.New("new wave stage requires agentId")
	}
	name := strings.TrimSpace(patch.Name)
	if name == "" {
		name = "Future wave"
	}
	instruction := strings.TrimSpace(patch.Instruction)
	if instruction == "" {
		instruction = "Continue the next bounded wave from current evidence."
	}
	node := domain.FlowNode{
		ID: domain.NewID("node"), Kind: domain.FlowNodeAgent, Name: name, AgentID: agentID,
		Config: map[string]any{"instruction": instruction, "planner": "replan-wave"},
	}
	anchor := ""
	for _, existing := range flow.Nodes {
		if existing.Kind == domain.FlowNodeVerifier || existing.Kind == domain.FlowNodeOutput {
			anchor = existing.ID
			break
		}
	}
	flow.Nodes = append(flow.Nodes, node)
	if anchor != "" {
		for index, edge := range flow.Edges {
			if edge.To == anchor {
				flow.Edges[index].To = node.ID
			}
		}
		flow.Edges = append(flow.Edges, domain.FlowEdge{ID: domain.NewID("edge"), From: node.ID, To: anchor})
	}
	return node.ID, nil
}

func stagePatchDigest(nodeID, agentID, name string, config map[string]any) string {
	payload, _ := json.Marshal(struct {
		NodeID      string `json:"nodeId"`
		AgentID     string `json:"agentId"`
		Name        string `json:"name"`
		Instruction any    `json:"instruction"`
		Criteria    any    `json:"criterionIds"`
	}{NodeID: nodeID, AgentID: agentID, Name: name, Instruction: config["instruction"], Criteria: config["criterionIds"]})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
