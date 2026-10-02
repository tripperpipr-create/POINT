package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/events"
	"local-agent-workbench/internal/executors"
	"local-agent-workbench/internal/policy"
	"local-agent-workbench/internal/providers"
	"local-agent-workbench/internal/sandbox"
	"local-agent-workbench/internal/security"
	"local-agent-workbench/internal/textutil"
	workbenchtools "local-agent-workbench/internal/tools"
	"local-agent-workbench/internal/workspace"
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
	processExecutor   sandbox.ProcessExecutor
	taskBrief         *domain.TaskBrief
	mu                sync.RWMutex
	run               domain.Run
	workspaceRevision int
	cancel            context.CancelFunc
	onFinished        func(domain.Run)
	controlMu         sync.Mutex
	pauseRequested    bool
	pauseReason       string
	// autoResumeAfter — пауза снимается сама через этот срок (ноль — только
	// человеком). Нужна паузе provider_unavailable.
	autoResumeAfter time.Duration
	paused          bool
	resumeCh        chan struct{}
	amendmentsMu    sync.RWMutex
	amendments      domain.ExecutionAmendments
	correlation     runCorrelation
	finalized       chan struct{}
	clock           *activeClock
	steps           *stepBudget
	checkpointSeq   int
	// checkpointCost — во что обошлись контрольные точки прогона: каждая
	// сериализует всю историю разговора и пишет её в SQLite. Число нужно,
	// чтобы решать о дельтах по замеру, а не по догадке.
	checkpointCost             checkpointCost
	sandboxPath                string
	sandboxImage               string
	apiKey                     string
	serverProfiles             workbenchtools.ServerProfileSource
	dbSource                   workbenchtools.DBConnectionSource
	teamBus                    workbenchtools.TeamBus
	initialBudgetReservationID string
	// teamInboxSeen — события ящика команды, уже вставленные в этот прогон.
	// Широковещательное событие хранилище доставленным не отмечает (у него
	// нет одного адресата), и без этой памяти оно приходило на каждом шаге.
	teamInboxSeen map[string]bool
	// lastSnapshot — снимок рабочей области после последней команды. Следующий
	// снимок берёт из него неизменённые файлы, а не читает их заново.
	lastSnapshot *workspace.TextSnapshot
}

func (a *activeRun) takeInitialBudgetReservation() string {
	a.controlMu.Lock()
	defer a.controlMu.Unlock()
	id := a.initialBudgetReservationID
	a.initialBudgetReservationID = ""
	return id
}

const (
	maxModelResponseBytes = 2 * 1024 * 1024
	maxRunToolOutputBytes = 4 * 1024 * 1024
	// После maxRunToolOutputBytes каждый вывод сокращается до этого размера,
	// а прогон падает только на жёстком пределе.
	overBudgetToolOutputBytes    = 8 * 1024
	hardRunToolOutputBytes       = 16 * 1024 * 1024
	maxToolArgumentBytes         = 1024 * 1024
	maxIdenticalToolPlans        = 3
	maxToolPlanRecoveries        = 1
	maxReasoningBudgetRecoveries = 2
	// Сколько раз прогон переживает пустой ход модели. Пустой ответ — ни
	// слова, ни вызова инструмента — прежде убивал прогон сразу; теперь он
	// стоит хода, как и два соседних затыка, и только третий подряд
	// считается отказом: модель, молчащая трижды, молчит не случайно.
	maxEmptyResponseRecoveries = 2
	// Сколько раз прогон переживает обрыв потока провайдера, когда запасной
	// модели не объявлено. Ошибка установления обращения повторяется ниже,
	// в HTTP-слое; сюда доходит поток, умерший на середине, и его прежде
	// никто не повторял.
	maxTransientModelRetries = 8
	// Повторы хода, где весь вывод снова ушёл в размышление, после того как
	// рост предела и гашение размышления уже не помогли.
	maxTruncatedReasoningRetries = 2
	maxRecordedExecutableChanges = 500
	maxWorkspaceEventPaths       = 200
)

// Сколько ждём заголовков ответа провайдера.
//
// Провайдер, принявший запрос, присылает заголовки сразу; молчание дольше
// этого срока — это молчание, а не долгий ответ. Прежде агент не задавал
// отдельного срока вовсе, и молчащий шлюз держал прогон весь
// MaxDurationSeconds — до часа на профиле исполнителя, — тогда как местный
// разбор был готов в первые же секунды. У компаньона это решено верно
// (companionProviderHeaderTimeoutSeconds = 40).
//
// Шестьдесят, а не сорок: у агента перед первым словом стоит размышление над
// системным промптом, контрактом задания и определениями двух десятков
// инструментов, и на медленном рантайме заголовки приходят позже, чем у
// короткой реплики помощника.
const agentProviderHeaderTimeoutSeconds = 60

// Пауза перед повтором оборванного потока растёт вдвое от двух секунд до
// минуты: восемь повторов ждут около четырёх минут. Этого хватает, чтобы
// llmux перезапустился или поднял модель; прежние 1 и 2 с кончались раньше,
// чем шлюз успевал ожить. Джиттер ±20% разводит повторы параллельных этапов.
// Как часто поток ответа модели пишется в журнал событий.
const (
	streamFlushInterval = 100 * time.Millisecond
	streamFlushBytes    = 1024
)

const (
	transientModelRetryBackoff = 2 * time.Second
	transientModelRetryCeiling = time.Minute
)

// Переменные, а не константы, только ради тестов: минуты ожидания в них
// заменяются миллисекундами.
var (
	// Через сколько прогон на паузе provider_unavailable пробует снова сам.
	providerUnavailableAutoResume = 5 * time.Minute
	transientDelay                = transientRetryDelay
)

func transientRetryDelay(retry int) time.Duration {
	delay := transientModelRetryBackoff
	for i := 1; i < retry && delay < transientModelRetryCeiling; i++ {
		delay *= 2
	}
	if delay > transientModelRetryCeiling {
		delay = transientModelRetryCeiling
	}
	jitter := time.Duration(rand.Int64N(int64(delay)/5+1)) * 2
	return delay - delay/5 + jitter
}

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
	stageVerifier   StageVerifier
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
	autoResume := active.autoResumeAfter
	active.autoResumeAfter = 0
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

	var autoResumeCh <-chan time.Time
	if autoResume > 0 {
		timer := time.NewTimer(autoResume)
		defer timer.Stop()
		autoResumeCh = timer.C
	}
	select {
	case <-resumeCh:
	case <-autoResumeCh:
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
	if err != nil {
		return
	}
	events = unseenTeamEvents(active, events)
	if len(events) == 0 {
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

// unseenTeamEvents оставляет события, которых этот прогон ещё не видел, и
// не возвращает этапу его собственные сообщения: статус, опубликованный этим
// же узлом, агенту уже известен.
func unseenTeamEvents(active *activeRun, events []domain.TeamEvent) []domain.TeamEvent {
	active.amendmentsMu.Lock()
	defer active.amendmentsMu.Unlock()
	if active.teamInboxSeen == nil {
		active.teamInboxSeen = map[string]bool{}
	}
	fresh := make([]domain.TeamEvent, 0, len(events))
	for _, event := range events {
		if active.teamInboxSeen[event.ID] {
			continue
		}
		active.teamInboxSeen[event.ID] = true
		if node := strings.TrimSpace(active.correlation.FlowNodeID); node != "" && event.FlowNodeID == node {
			continue
		}
		fresh = append(fresh, event)
	}
	return fresh
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

// shrinkToolResult сокращает вывод инструмента до limit байт, когда суммарный
// бюджет вывода прогона исчерпан. Модель видит начало и подсказку сузить
// запрос; ошибка инструмента сохраняется целиком.
func shrinkToolResult(result domain.ToolResult, limit int) domain.ToolResult {
	if len(result.Output) <= limit {
		return result
	}
	text := string(result.Output)
	var decoded string
	if json.Unmarshal(result.Output, &decoded) == nil {
		text = decoded
	}
	if len(text) > limit {
		cut := limit
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		text = text[:cut]
	}
	text += "\n…[output shortened: the run's total tool-output budget is spent; request narrower ranges, filters or summaries]"
	shrunk := result
	shrunk.Output, _ = json.Marshal(text)
	shrunk.Truncated = true
	return shrunk
}
