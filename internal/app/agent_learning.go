package app

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/security"
	"local-agent-workbench/internal/textutil"
)

const (
	agentLearningMinToolCalls         = 5
	agentLearningFeedbackMinToolCalls = 2
	agentLearningTimeout              = 4 * time.Minute
	agentLearningManagedBy            = "agent-hub-self-improvement"
	learningTriggerSuccess            = "successful_complex_run"
	learningTriggerFeedback           = "user_feedback_after_recovery"
)

var (
	errAgentLearningIneligible      = errors.New("run is not eligible for autonomous learning")
	errSubagentEvaluatorUnavailable = errors.New("temporary subagent evaluator unavailable")
)

type learningReview struct {
	Decision            string `json:"decision"`
	Name                string `json:"name"`
	Description         string `json:"description"`
	Instructions        string `json:"instructions"`
	MemoryDecision      string `json:"memoryDecision"`
	MemoryKey           string `json:"memoryKey"`
	Memory              string `json:"memory"`
	InstructionDecision string `json:"instructionDecision"`
	InstructionKey      string `json:"instructionKey"`
	Instruction         string `json:"instruction"`
}

type learningTrajectory struct {
	Tools       []string
	ToolCalls   int
	ExecutionID string
	Evidence    []string
	// Feedback contains only messages that the user explicitly marked as a
	// reusable correction. Ordinary mid-run guidance is never sent to the
	// background reviewer.
	Feedback             []string
	Steps                []learningToolStep
	VerificationRequired bool
	VerificationRecorded bool
	VerificationGap      string
	ApprovalsDenied      int
	CompletionRevisions  int
	CompletionRejected   bool
	ChangedFileCount     int
	Health               string
	RunStatus            domain.RunStatus
	StopReason           string
	QuestID              string
}

func (a *App) queueAgentImprovement(run domain.Run, projectAgentID, apiKey string) {
	if strings.TrimSpace(projectAgentID) == "" {
		return
	}
	switch run.Status {
	case domain.RunCompleted, domain.RunFailed, domain.RunInterrupted:
	default:
		return
	}
	agent, agentErr := a.store.GetProjectAgent(context.Background(), projectAgentID)
	temporary := agentErr == nil && agent.Temporary && strings.TrimSpace(agent.ParentAgentID) != ""
	if temporary {
		if !a.questIsTerminal(context.Background(), agent.WorkspaceID, agent.OwnerQuestID) {
			return
		}
		switch agent.Status {
		case domain.ProjectAgentActive:
			if err := a.store.SetProjectAgentStatus(context.Background(), agent.ID, domain.ProjectAgentActive, domain.ProjectAgentEvaluationPending); err != nil {
				return
			}
			agent.Status = domain.ProjectAgentEvaluationPending
			_ = a.store.SaveAgentLifecycleEvent(context.Background(), domain.AgentLifecycleEvent{
				ID: domain.NewID("agentlife"), WorkspaceID: agent.WorkspaceID, AgentID: agent.ID,
				Kind: "subagent_evaluation_started", Detail: map[string]any{"questId": agent.OwnerQuestID, "runId": run.ID}, CreatedAt: time.Now().UTC(),
			})
		case domain.ProjectAgentEvaluationPending:
			// A previous evaluator outage is retryable. Keep the specialist
			// non-runnable and repeat the same evidence review.
		default:
			return
		}
	}
	// A no-tools answer cannot satisfy the complex-trajectory threshold. For a
	// permanent agent there is nothing to review; for a temporary specialist it
	// is a completed negative evaluation and the quest-scoped record is removed.
	if len(run.ToolsUsed) == 0 {
		if temporary {
			a.finishTemporarySubagentEvaluation(context.Background(), agent, domain.AgentImprovement{}, errAgentLearningIneligible)
		}
		return
	}
	a.learningMu.Lock()
	if a.learningStopping {
		a.learningMu.Unlock()
		return
	}
	a.learningWG.Add(1)
	ctx := a.learningCtx
	a.learningMu.Unlock()
	go func() {
		defer a.learningWG.Done()
		ctx, cancel := context.WithTimeout(ctx, agentLearningTimeout)
		defer cancel()
		item, err := a.reviewAgentRun(ctx, run, projectAgentID, apiKey)
		if temporary {
			a.finishTemporarySubagentEvaluation(ctx, agent, item, err)
		}
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, errAgentLearningIneligible) {
			slog.Warn("agent self-improvement review failed", "run_id", run.ID, "agent_id", projectAgentID, "error", security.Redact(err.Error()))
		}
	}()
}

func (a *App) questIsTerminal(ctx context.Context, workspaceID, questID string) bool {
	if strings.TrimSpace(questID) == "" {
		return false
	}
	quests, err := a.store.ListQuests(ctx, workspaceID)
	if err != nil {
		return false
	}
	for _, quest := range quests {
		if quest.ID == questID {
			return domain.IsTerminalQuestStatus(quest.Status)
		}
	}
	return false
}

// queueQuestSubagentEvaluations catches specialists that completed before the
// last Flow node. Their individual run ended while the owner quest was still
// active, so evaluation is scheduled only after the quest becomes terminal.
func (a *App) queueQuestSubagentEvaluations(questID string) {
	ws, err := a.requireWorkspace()
	if err != nil || !a.questIsTerminal(context.Background(), ws.ID, questID) {
		return
	}
	agents, err := a.store.ListProjectAgents(context.Background(), ws.ID)
	if err != nil {
		return
	}
	executions, err := a.store.ListExecutions(context.Background(), ws.ID, 0)
	if err != nil {
		return
	}
	for _, candidate := range agents {
		if !candidate.Temporary || candidate.OwnerQuestID != questID || (candidate.Status != domain.ProjectAgentActive && candidate.Status != domain.ProjectAgentEvaluationPending) {
			continue
		}
		var latest *domain.Run
		for _, execution := range executions {
			if execution.ProjectAgentID != candidate.ID || strings.TrimSpace(execution.RunID) == "" {
				continue
			}
			run, runErr := a.store.GetRun(context.Background(), execution.RunID)
			if runErr != nil {
				continue
			}
			if latest == nil || run.StartedAt.After(latest.StartedAt) {
				copy := run
				latest = &copy
			}
		}
		if latest != nil {
			a.queueAgentImprovement(*latest, candidate.ID, "")
			continue
		}
		if setErr := a.store.SetProjectAgentStatus(context.Background(), candidate.ID, domain.ProjectAgentActive, domain.ProjectAgentEvaluationPending); setErr == nil {
			_ = a.store.SaveAgentLifecycleEvent(context.Background(), domain.AgentLifecycleEvent{
				ID: domain.NewID("agentlife"), WorkspaceID: candidate.WorkspaceID, AgentID: candidate.ID,
				Kind: "subagent_evaluation_unavailable", Detail: map[string]any{"questId": questID, "reason": "no terminal run"}, CreatedAt: time.Now().UTC(),
			})
		}
	}
}

func (a *App) finishTemporarySubagentEvaluation(ctx context.Context, agent domain.ProjectAgent, item domain.AgentImprovement, reviewErr error) {
	detail := map[string]any{"questId": agent.OwnerQuestID}
	if item.ID != "" {
		detail["improvementId"] = item.ID
	}
	if reviewErr != nil && !errors.Is(reviewErr, errAgentLearningIneligible) {
		detail["reason"] = truncateRunes(security.Redact(reviewErr.Error()), 500)
		_ = a.store.SaveAgentLifecycleEvent(ctx, domain.AgentLifecycleEvent{
			ID: domain.NewID("agentlife"), WorkspaceID: agent.WorkspaceID, AgentID: agent.ID,
			Kind: "subagent_evaluation_unavailable", Detail: detail, CreatedAt: time.Now().UTC(),
		})
		return
	}
	if reviewErr == nil && item.Kind == "subagent_specialization" && item.PromotionStatus == "candidate" && improvementIsApplied(item.Status) {
		_ = a.store.SaveAgentLifecycleEvent(ctx, domain.AgentLifecycleEvent{
			ID: domain.NewID("agentlife"), WorkspaceID: agent.WorkspaceID, AgentID: agent.ID,
			Kind: "subagent_blueprint_proposed", Detail: detail, CreatedAt: time.Now().UTC(),
		})
		return
	}
	detail["reason"] = "evaluation rejected reusable specialization"
	if reviewErr != nil {
		detail["reason"] = security.Redact(reviewErr.Error())
	}
	if err := a.store.DeleteTemporaryProjectAgent(ctx, agent.ID, domain.AgentLifecycleEvent{
		Kind: "subagent_evaluation_rejected", Detail: detail,
	}); err != nil {
		slog.Warn("temporary subagent cleanup failed", "agent_id", agent.ID, "error", security.Redact(err.Error()))
	}
}

// truncateRunes — имя, под которым обрезка известна в этом пакете; правило
// одно на всё ядро и живёт в textutil.
func truncateRunes(value string, limit int) string {
	return textutil.Bounded(value, limit)
}
