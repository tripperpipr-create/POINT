package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
)

const masterWatchInterval = 45 * time.Second

// StartMasterWatch begins periodic Orchestrator inspection of active runs.
func (a *App) StartMasterWatch() {
	a.masterWatchMu.Lock()
	defer a.masterWatchMu.Unlock()
	if a.masterWatchCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.masterWatchCancel = cancel
	a.masterWatchWG.Add(1)
	go func() {
		defer a.masterWatchWG.Done()
		ticker := time.NewTicker(masterWatchInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				a.inspectActiveRuns(ctx)
			}
		}
	}()
}

func (a *App) stopMasterWatch() {
	a.masterWatchMu.Lock()
	cancel := a.masterWatchCancel
	a.masterWatchCancel = nil
	a.masterWatchMu.Unlock()
	if cancel != nil {
		cancel()
	}
	a.masterWatchWG.Wait()
}

func (a *App) inspectActiveRuns(ctx context.Context) {
	workspaceID := a.currentWorldID()
	if workspaceID == "" {
		return
	}
	runs, err := a.store.ListRunsForWorkspace(ctx, workspaceID, 50)
	if err != nil {
		return
	}
	for _, run := range runs {
		switch run.Status {
		case domain.RunRunning, domain.RunWaiting, domain.RunPaused:
		default:
			continue
		}
		a.inspectRun(ctx, run)
	}
}

func (a *App) inspectRun(ctx context.Context, run domain.Run) {
	if run.Status == domain.RunWaiting || run.Status == domain.RunPaused {
		return
	}
	checkpoint, err := a.store.LatestRunCheckpoint(ctx, run.ID)
	if err != nil {
		return
	}
	if watchShouldSkipStall(checkpoint, run) {
		return
	}
	if a.runAwaitingUserDecision(ctx, run) {
		return
	}
	if a.watchRepeatDeniedEgress(ctx, run, checkpoint) {
		return
	}
	if checkpoint.IdenticalToolPlans >= 2 && run.Status == domain.RunRunning {
		a.interveneDuplicatePlan(ctx, run, checkpoint.IdenticalToolPlans)
	}
}

const masterWatchOperationTimeout = 15 * time.Minute

func watchShouldSkipStall(checkpoint domain.RunCheckpoint, run domain.Run) bool {
	if run.Status == domain.RunWaiting || run.Status == domain.RunPaused {
		return true
	}
	if checkpoint.PauseReason == domain.PauseReasonMasterSupervision {
		return true
	}
	opAge := time.Since(checkpoint.HeartbeatAt)
	if checkpoint.HeartbeatAt.IsZero() {
		opAge = 0
	}
	if checkpoint.InFlightCallID != "" {
		return checkpoint.HeartbeatAt.IsZero() || opAge < masterWatchOperationTimeout
	}
	tool := strings.ToLower(checkpoint.ActiveToolName + " " + checkpoint.LastToolPlan)
	for _, marker := range []string{"run_command", "composer", "phpunit", "npm ", "go test", "pytest"} {
		if strings.Contains(tool, marker) {
			return checkpoint.HeartbeatAt.IsZero() || opAge < masterWatchOperationTimeout
		}
	}
	return false
}

func (a *App) runAwaitingUserDecision(ctx context.Context, run domain.Run) bool {
	asks, err := a.store.ListPendingEgressAsks(ctx, run.WorkspaceID)
	if err != nil {
		return false
	}
	for _, ask := range asks {
		if ask.RunID == run.ID {
			return true
		}
	}
	return false
}

func (a *App) watchRepeatDeniedEgress(ctx context.Context, run domain.Run, checkpoint domain.RunCheckpoint) bool {
	if checkpoint.QuestID == "" {
		return false
	}
	quests, err := a.store.ListQuests(ctx, run.WorkspaceID)
	if err != nil {
		return false
	}
	haystack := strings.ToLower(checkpoint.ActiveToolName + " " + checkpoint.LastToolPlan)
	for _, quest := range quests {
		if quest.ID != checkpoint.QuestID {
			continue
		}
		raw, _ := json.Marshal(quest.Controller["deniedEgressHosts"])
		var hosts []string
		_ = json.Unmarshal(raw, &hosts)
		for _, host := range hosts {
			if host != "" && strings.Contains(haystack, strings.ToLower(host)) {
				return true
			}
		}
	}
	return false
}

func (a *App) interveneDuplicatePlan(ctx context.Context, run domain.Run, identical int) {
	a.masterWatchMu.Lock()
	if a.masterWatchIntervened == nil {
		a.masterWatchIntervened = map[string]time.Time{}
	}
	if when, ok := a.masterWatchIntervened[run.ID]; ok && time.Since(when) < 10*time.Minute {
		a.masterWatchMu.Unlock()
		return
	}
	a.masterWatchIntervened[run.ID] = time.Now().UTC()
	a.masterWatchMu.Unlock()

	questID := ""
	if checkpoint, err := a.store.LatestRunCheckpoint(ctx, run.ID); err == nil {
		questID = checkpoint.QuestID
	}
	reason := fmt.Sprintf("agent repeated an identical tool plan %d times; paused before hard stall", identical)
	a.emitOrchestratorEvent(run.WorkspaceID, run.ID, questID, map[string]any{
		"code": "duplicate_tool_plan_intervention", "identicalPlans": identical, "action": "pause_ask", "reason": reason,
	})
	_ = a.EnsureSupervisionAsk(ctx, run, questID, reason)
	if _, err := a.PauseRun(run.ID); err == nil {
		if stored, err := a.store.GetRun(ctx, run.ID); err == nil {
			stored.Controller.PauseReason = domain.PauseReasonMasterSupervision
			_ = a.store.SaveRun(ctx, stored)
		}
	}
}

// EnsureSupervisionAsk creates a blocking decision so the user can continue or stop.
func (a *App) EnsureSupervisionAsk(ctx context.Context, run domain.Run, questID, reason string) error {
	ask, err := a.EnsureEgressAsk(ctx, run.WorkspaceID, questID, run.ID, domain.EgressAskSupervision, "duplicate_tool_plan:"+run.ID, reason)
	if err != nil {
		return err
	}
	_ = ask
	return nil
}

func (a *App) handleMasterWatchEvent(event domain.Event) {
	switch event.Type {
	case domain.EventAgentGuardrail:
		a.handleGuardrailEvent(event)
	case domain.EventToolFinished:
		a.handleToolFinishedEgress(event)
	}
}

func (a *App) handleGuardrailEvent(event domain.Event) {
	var payload map[string]any
	if err := json.Unmarshal(event.Data, &payload); err != nil {
		return
	}
	code, _ := payload["code"].(string)
	if code != "duplicate_tool_plan" {
		return
	}
	identical := 0
	switch v := payload["identicalPlans"].(type) {
	case float64:
		identical = int(v)
	case string:
		identical, _ = strconv.Atoi(v)
	}
	if identical < 2 {
		return
	}
	run, err := a.store.GetRun(context.Background(), event.RunID)
	if err != nil || run.Status != domain.RunRunning {
		return
	}
	a.interveneDuplicatePlan(context.Background(), run, identical)
}

func (a *App) handleToolFinishedEgress(event domain.Event) {
	var payload map[string]any
	if err := json.Unmarshal(event.Data, &payload); err != nil {
		return
	}
	code, message := toolFinishedEgressHint(payload)
	kind, target := egressTargetFromToolError(code, message)
	if kind == "" || target == "" || target == "unspecified-remote" {
		return
	}
	questID := event.QuestID
	if questID == "" {
		if checkpoint, err := a.store.LatestRunCheckpoint(context.Background(), event.RunID); err == nil {
			questID = checkpoint.QuestID
		}
	}
	workspaceID := event.WorkspaceID
	if workspaceID == "" {
		if run, err := a.store.GetRun(context.Background(), event.RunID); err == nil {
			workspaceID = run.WorkspaceID
		}
	}
	_, _ = a.EnsureEgressAsk(context.Background(), workspaceID, questID, event.RunID, kind, target, message)
}

// ResolveSupervisionContinue resumes a run paused by Master watch.
func (a *App) ResolveSupervisionContinue(ctx context.Context, askID string, continueRun bool) error {
	ask, err := a.store.GetEgressAsk(ctx, askID)
	if err != nil {
		return err
	}
	if ask.Kind != domain.EgressAskSupervision {
		return fmt.Errorf("ask %q is not a supervision decision", askID)
	}
	if ask.Status != domain.EgressAskPending {
		return fmt.Errorf("supervision ask already resolved")
	}
	now := time.Now().UTC()
	if continueRun {
		ask.Status = domain.EgressAskAllowedOnce
		if ask.QuestID != "" && ask.WorkspaceID != "" {
			if quests, qErr := a.store.ListQuests(ctx, ask.WorkspaceID); qErr == nil {
				for _, quest := range quests {
					if quest.ID != ask.QuestID {
						continue
					}
					bumpQuestCounter(&quest, "supervisionContinues")
					_ = a.store.SaveQuest(ctx, quest)
					break
				}
			}
		}
		if ask.RunID != "" {
			_ = a.InjectRunMessage(ask.RunID, "Master/user asked you to change strategy: do not repeat the identical tool plan. Inspect fresh evidence, try a different approach, then continue.", "")
			if _, err = a.ResumeRun(ask.RunID); err != nil {
				return err
			}
		}
	} else {
		ask.Status = domain.EgressAskDenied
		if ask.RunID != "" {
			_ = a.CancelRun(ask.RunID)
		}
	}
	ask.ResolvedAt = now
	return a.store.SaveEgressAsk(ctx, ask)
}
