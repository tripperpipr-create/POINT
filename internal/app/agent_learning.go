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

var errAgentLearningIneligible = errors.New("run is not eligible for autonomous learning")

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
	// A no-tools answer cannot satisfy the complex-trajectory threshold. Avoid
	// starting a DB-backed reviewer after such runs; this also keeps shutdown
	// independent from a background job that is guaranteed to be ineligible.
	if strings.TrimSpace(projectAgentID) == "" || len(run.ToolsUsed) == 0 {
		return
	}
	switch run.Status {
	case domain.RunCompleted, domain.RunFailed, domain.RunInterrupted:
	default:
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
		if _, err := a.reviewAgentRun(ctx, run, projectAgentID, apiKey); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, errAgentLearningIneligible) {
			slog.Warn("agent self-improvement review failed", "run_id", run.ID, "agent_id", projectAgentID, "error", security.Redact(err.Error()))
		}
	}()
}

// truncateRunes — имя, под которым обрезка известна в этом пакете; правило
// одно на всё ядро и живёт в textutil.
func truncateRunes(value string, limit int) string {
	return textutil.Bounded(value, limit)
}
