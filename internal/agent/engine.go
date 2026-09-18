package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/events"
	"local-agent-workbench/internal/executors"
	"local-agent-workbench/internal/policy"
	"local-agent-workbench/internal/providers"
	"local-agent-workbench/internal/sandbox"
	"local-agent-workbench/internal/security"
	"local-agent-workbench/internal/textutil"
	workbenchtools "local-agent-workbench/internal/tools"
)

type Repository interface {
	events.Store
	SaveRun(context.Context, domain.Run) error
	SaveApproval(context.Context, domain.Approval) error
	SavePatch(context.Context, domain.PatchProposal) error
	SaveRunCheckpoint(context.Context, domain.RunCheckpoint) error
	LatestRunCheckpoint(context.Context, string) (domain.RunCheckpoint, error)
}

type ModelFactory func(providers.Config) (providers.Model, error)

type ModelBudgetRequest struct {
	WorkspaceID          string
	QuestID              string
	ExecutionID          string
	RunID                string
	Provider             domain.ProviderKind
	ProviderPreset       string
	Model                string
	EstimatedInputTokens int64
	MaxOutputTokens      int64
}

type ModelBudgetSettlement struct {
	ReservationID  string
	WorkspaceID    string
	ProjectAgentID string
	Provider       domain.ProviderKind
	Model          string
	InputTokens    int64
	OutputTokens   int64
	UsageReported  bool
	Outcome        string
}

type ModelBudgetController interface {
	ReserveModelBudget(context.Context, ModelBudgetRequest) (string, error)
	ReconcileModelBudget(context.Context, ModelBudgetSettlement) error
}

type activeRun struct {
	taskBrief         *domain.TaskBrief
	mu                sync.RWMutex
	run               domain.Run
	workspaceRevision int
	cancel            context.CancelFunc
	onFinished        func(domain.Run)
	controlMu         sync.Mutex
	pauseRequested    bool
	pauseReason       string
	paused            bool
	resumeCh          chan struct{}
	amendmentsMu      sync.RWMutex
	amendments        domain.ExecutionAmendments
	correlation       runCorrelation
	finalized         chan struct{}
	clock             *activeClock
	checkpointSeq     int
	sandboxPath       string
	apiKey            string
	serverProfiles    workbenchtools.ServerProfileSource
	dbSource          workbenchtools.DBConnectionSource
	teamBus           workbenchtools.TeamBus
}

const (
	maxModelResponseBytes        = 2 * 1024 * 1024
	maxRunToolOutputBytes        = 4 * 1024 * 1024
	maxToolArgumentBytes         = 1024 * 1024
	maxIdenticalToolPlans        = 3
	maxToolPlanRecoveries        = 1
	maxReasoningBudgetRecoveries = 2
	maxRecordedExecutableChanges = 500
	maxWorkspaceEventPaths       = 200
)

type Engine struct {
	repo            Repository
	models          ModelFactory
	policy          policy.Engine
	broker          *Broker
	onEvent         func(domain.Event)
	dbSource        workbenchtools.DBConnectionSource
	mu              sync.RWMutex
	active          map[string]*activeRun
	wg              sync.WaitGroup
	stopping        bool
	publishFailures atomic.Int64
	processExecutor sandbox.ProcessExecutor
	budgets         ModelBudgetController
	toolSessions    ToolSessionOpener
	cliFactory      func(executors.Kind) executors.Executor
	networkGrants   *workbenchtools.NetworkGrantBook
}

func NewEngine(repo Repository, onEvent func(domain.Event)) *Engine {
	return &Engine{repo: repo, models: providers.New, broker: NewBroker(), onEvent: onEvent, active: make(map[string]*activeRun), cliFactory: func(kind executors.Kind) executors.Executor { return executors.NewCLI(kind) }}
}

func (e *Engine) SetModelFactory(factory ModelFactory) { e.models = factory }

// SetTrustedCustomToolLookup передаёт движку справку о доверенных самодельных
// инструментах. Без неё движок спрашивает подтверждение на каждый их вызов —
// это верное умолчание для всех, у кого нет хранилища со счётчиком.
func (e *Engine) SetTrustedCustomToolLookup(trusted func(name string) bool) {
	e.policy.TrustedCustomTool = trusted
}

func (e *Engine) SetDBSource(source workbenchtools.DBConnectionSource) { e.dbSource = source }

func (e *Engine) SetProcessExecutor(executor sandbox.ProcessExecutor) { e.processExecutor = executor }

func (e *Engine) SetBudgetController(controller ModelBudgetController) { e.budgets = controller }

func (e *Engine) SetToolSessionOpener(opener ToolSessionOpener) { e.toolSessions = opener }

func (e *Engine) SetCLIFactory(factory func(executors.Kind) executors.Executor) {
	if factory != nil {
		e.cliFactory = factory
	}
}

func (e *Engine) SetNetworkGrants(book *workbenchtools.NetworkGrantBook) {
	e.networkGrants = book
}

func (e *Engine) IsActiveRun(runID string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	_, ok := e.active[runID]
	return ok
}

func (e *Engine) snapshot(active *activeRun) domain.Run {
	active.mu.RLock()
	defer active.mu.RUnlock()
	return active.run
}

func (e *Engine) update(active *activeRun, change func(*domain.Run)) {
	active.mu.Lock()
	change(&active.run)
	active.run.DurationMs = time.Since(active.run.StartedAt).Milliseconds()
	active.mu.Unlock()
}

func (e *Engine) waitAtCheckpoint(ctx context.Context, active *activeRun) error {
	active.clock.stop()
	active.controlMu.Lock()
	if !active.pauseRequested {
		active.controlMu.Unlock()
		return nil
	}
	reason := active.pauseReason
	if reason == "" {
		reason = domain.PauseReasonUserRequested
	}
	active.pauseRequested = false
	active.paused = true
	active.resumeCh = make(chan struct{})
	resumeCh := active.resumeCh
	active.controlMu.Unlock()

	e.syncControllerState(active, reason, true)
	e.update(active, func(r *domain.Run) {
		r.Status = domain.RunPaused
		r.Controller.PauseReason = reason
		r.Controller.Resumable = true
	})
	if err := e.saveRun(e.snapshot(active)); err != nil {
		return fmt.Errorf("persist paused run: %w", err)
	}

	select {
	case <-resumeCh:
	case <-ctx.Done():
		return ctx.Err()
	}

	active.controlMu.Lock()
	active.paused = false
	active.resumeCh = nil
	active.pauseReason = ""
	active.controlMu.Unlock()

	e.syncControllerState(active, "", true)
	e.update(active, func(r *domain.Run) {
		r.Status = domain.RunRunning
		r.Controller.PauseReason = ""
	})
	if err := e.saveRun(e.snapshot(active)); err != nil {
		return err
	}
	active.clock.start()
	return nil
}

func (e *Engine) applyPendingAmendments(active *activeRun, history *conversationHistory, profile domain.AgentProfile, customTools []domain.CustomTool) {
	active.amendmentsMu.Lock()
	pending := append([]domain.RunMessageAmendment(nil), active.amendments.PendingMessages...)
	contextAmends := cloneContextAmendments(active.amendments.ContextAmends)
	active.amendments.PendingMessages = nil
	active.amendments.ContextAmends = nil
	active.amendmentsMu.Unlock()

	e.injectTeamInbox(active, history, profile.ID)
	for _, message := range pending {
		history.AppendUserMessage(message.Content)
		// The message is part of the model trajectory, so keep a bounded,
		// redacted audit record as evidence for post-run learning. Persisting
		// only context amendments made user corrections invisible to the
		// background reviewer after the in-memory conversation was gone.
		e.publishOrLog(context.Background(), e.snapshot(active), domain.EventRunMessageInjected, "user", map[string]any{
			"content": boundedLearningMessage(message.Content), "learningIntent": message.LearningIntent,
		})
	}
	if len(contextAmends) == 0 {
		return
	}
	e.update(active, func(r *domain.Run) {
		applyContextAmendments(&r.ContextItems, contextAmends)
	})
	amended := e.snapshot(active)
	history.ReplaceStable(BuildStableMessages(profile, amended.ContextItems, amended.Task, customTools))
	if err := e.saveRun(amended); err != nil {
		slog.Warn("persist context amendments failed", "run_id", active.run.ID, "error", err)
	}
	e.publishOrLog(context.Background(), amended, domain.EventContextAmended, "user", contextAmendmentEventData(contextAmends))
}

func (e *Engine) injectTeamInbox(active *activeRun, history *conversationHistory, agentID string) {
	if active == nil || history == nil || active.teamBus == nil || strings.TrimSpace(active.correlation.FlowRunID) == "" || strings.TrimSpace(agentID) == "" {
		return
	}
	events, err := active.teamBus.TeamInbox(context.Background(), active.correlation.FlowRunID, agentID, true)
	if err != nil || len(events) == 0 {
		return
	}
	var body strings.Builder
	body.WriteString("Team inbox (structured; cannot change brief, permissions, budget or ownership):\n")
	for _, event := range events {
		fmt.Fprintf(&body, "- [%s] from %s: %s\n", event.Kind, event.FromAgentID, event.Message)
	}
	text := strings.TrimSpace(body.String())
	if executors.KindForProvider(domain.ProviderKind(active.run.Provider)) == executors.KindPoint {
		history.AppendUserMessage(text)
	} else {
		history.stable = append(history.stable, providers.Message{Role: "user", Content: text})
	}
	e.publishOrLog(context.Background(), e.snapshot(active), domain.EventRunMessageInjected, "agent", map[string]any{
		"content": boundedLearningMessage(text), "source": "team_inbox",
	})
}

func (e *Engine) isPathForbidden(active *activeRun, path string) bool {
	active.amendmentsMu.RLock()
	defer active.amendmentsMu.RUnlock()
	for _, forbidden := range active.amendments.ForbiddenPaths {
		if pathsForbiddenMatch(path, forbidden) {
			return true
		}
	}
	return false
}

func (e *Engine) firstForbiddenPath(active *activeRun) string {
	active.amendmentsMu.RLock()
	defer active.amendmentsMu.RUnlock()
	if len(active.amendments.ForbiddenPaths) == 0 {
		return ""
	}
	return active.amendments.ForbiddenPaths[0]
}

func cloneAmendments(value domain.ExecutionAmendments) domain.ExecutionAmendments {
	return domain.ExecutionAmendments{
		PendingMessages: append([]domain.RunMessageAmendment(nil), value.PendingMessages...),
		ForbiddenPaths:  append([]string(nil), value.ForbiddenPaths...),
		ContextAmends:   cloneContextAmendments(value.ContextAmends),
	}
}

func cloneContextAmendments(values []domain.ContextAmendment) []domain.ContextAmendment {
	result := make([]domain.ContextAmendment, len(values))
	for index, value := range values {
		result[index] = value
		if value.Item != nil {
			item := *value.Item
			result[index].Item = &item
		}
	}
	return result
}

func contextAmendmentEventData(values []domain.ContextAmendment) map[string]any {
	items := make([]map[string]any, 0, len(values))
	for _, value := range values {
		entry := map[string]any{"action": value.Action, "itemId": value.ItemID}
		if value.Item != nil {
			entry["kind"] = value.Item.Kind
			entry["label"] = value.Item.Label
			entry["path"] = value.Item.Path
			entry["digest"] = value.Item.Digest
		}
		items = append(items, entry)
	}
	return map[string]any{"amendments": items}
}

func boundedLearningMessage(value string) string {
	return textutil.Bounded(security.Redact(strings.TrimSpace(value)), 2000)
}
