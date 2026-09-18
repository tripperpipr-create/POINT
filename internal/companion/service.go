package companion

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/observability"
	"local-agent-workbench/internal/providers"
	"local-agent-workbench/internal/security"
	"local-agent-workbench/internal/workspace"
)

type Store interface {
	GetCompanionConfig(ctx context.Context, workspaceID string) (domain.CompanionConfig, error)
	SaveCompanionConfig(ctx context.Context, cfg domain.CompanionConfig) error
	SaveQuestProposal(ctx context.Context, proposal domain.QuestProposal) error
	ListQuestProposals(ctx context.Context, workspaceID string) ([]domain.QuestProposal, error)
	ListCompanionActionProposals(ctx context.Context, workspaceID string) ([]domain.CompanionActionProposal, error)
	SaveCompanionActionProposal(ctx context.Context, proposal domain.CompanionActionProposal) error
	ListIDEObservations(ctx context.Context, workspaceID string, limit int) ([]domain.IDEObservation, error)
	ListBlueprints(ctx context.Context) ([]domain.AgentBlueprint, error)
	ListSkills(ctx context.Context) ([]domain.SkillDefinition, error)
	ListProjectAgents(ctx context.Context, workspaceID string) ([]domain.ProjectAgent, error)
	ListQuests(ctx context.Context, workspaceID string) ([]domain.Quest, error)
	ListFlows(ctx context.Context, workspaceID string) ([]domain.FlowGraph, error)
	ListExecutions(ctx context.Context, workspaceID string, limit int) ([]domain.ExecutionInstance, error)
	ListChangeSets(ctx context.Context, workspaceID string) ([]domain.ChangeSet, error)
	ListConnections(ctx context.Context) ([]domain.Connection, error)
	ListMemories(ctx context.Context, workspaceID string) ([]domain.MemoryRecord, error)
	ListUsageRecords(ctx context.Context, workspaceID string, limit int) ([]domain.UsageRecord, error)
	SaveMemory(ctx context.Context, memory domain.MemoryRecord) error
	DeleteMemory(ctx context.Context, workspaceID, id string) error
	SaveCompanionMessage(ctx context.Context, message domain.CompanionMessage) error
	ListCompanionMessages(ctx context.Context, workspaceID string, limit int) ([]domain.CompanionMessage, error)
	ListChatMessages(ctx context.Context, workspaceID, speaker string, limit int) ([]domain.CompanionMessage, error)
	InsertUsageRecord(ctx context.Context, record domain.UsageRecord) error
}

type ProjectContext interface {
	ProjectMap(ctx context.Context, maxSymbols int) (workspace.ProjectMap, error)
	SearchContextWithRelations(ctx context.Context, query string, maxChunks, maxChars int, includeRelated bool) (workspace.ContextSearchResult, error)
}

type ModelFactory func(providers.Config) (providers.Model, error)
type ConfigResolver func(domain.CompanionConfig) (domain.CompanionConfig, error)

// companionUsageRecordLimit caps usage history pulled into each Companion gather.
// Monthly rollups only need recent rows; 5000 forced large SQLite scans every turn.
const companionUsageRecordLimit = 500

const pointIDECapabilities = `Authoritative Point IDE product capabilities (this is trusted product knowledge, not project context):
- Code and files: editor tabs, navigation, usages, refactoring, formatting, search everywhere, recent locations, bookmarks, scratch files, file history and multi-root projects.
- Terminal and run: native terminal channels, shell/REPL selection, Run Anything, tasks, launch configurations, run and debug.
- Git: clone, working tree, diff of uncommitted changes or of a named commit, commit, rollback, pull, push, blame and the Point Git Chronicle/history.
- Databases: saved SQLite, PostgreSQL and MySQL profiles; connection probe, schema browsing and SQL queries with confirmation for risky operations.
- SSH: saved server profiles, connection probe, SSH terminal, remote directory browser and safe file preview.
- Tools: Agent Hub, project index, Docker, connections, skills, workflows, approvals and local core chronicle.
- This companion chat: reads the project, its index and git history; wears its own skills; and keeps project-scoped memory. The person can say "запомни: …" to store a fact, "что ты помнишь" to list what is stored, and "забудь …" to drop one. A stored request is pinned and returns in every later conversation, so answer honestly that you can remember things for this project. You still never edit files, run commands, or start work on your own.
Point is a standalone IDE built on the Code-OSS editor base. When the user asks what Point can do, answer from this list even when the current project context contains no documentation about Point itself.`

type runStore interface {
	ListRunsForWorkspace(ctx context.Context, workspaceID string, limit int) ([]domain.Run, error)
}

type Service struct {
	Store          Store
	ProjectContext ProjectContext
	ModelFactory   ModelFactory
	ConfigResolver ConfigResolver
	// Tools — читающие инструменты рабочей папки. Без них компаньон отвечает
	// только по собранному заранее контексту, и это законный режим: рабочей
	// папки может не быть вовсе.
	Tools ReadTools
	// Skills — навыки, надетые на помощника. Практики у него свои: он отвечает
	// в боковой панели, а не правит файлы по квесту.
	Skills []domain.SkillRuntime
	// UsageManagedExternally is set by Point Core when the model is wrapped in
	// an atomic budget reservation that also writes the canonical usage row.
	UsageManagedExternally bool
	// WorkspaceRoot нужен провайдерам, отвечающим локальным процессом: Claude
	// Code читает проект относительно рабочей папки, и без неё он смотрел бы
	// туда, откуда запущено ядро.
	WorkspaceRoot string
	// ToolBridge выдаёт инструменты помощника исполнителю, который принимает их
	// только по MCP. Без моста такой исполнитель работает своими руками —
	// читать и искать он умеет сам, а вот индекс Point и его память доступны
	// только отсюда.
	ToolBridge ToolBridge
}

// ToolBridge открывает доступ сущности для внешнего исполнителя и закрывает его
// вместе с работой. Возвращает конфигурацию для исполнителя и список имён,
// которые ему разрешено звать: права принадлежат сущности, и выдаются они
// поимённо.
type ToolBridge interface {
	OpenToolSession(subject string, tools ReadTools) (config string, allowed []string, closeSession func(), err error)
}

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ProgressFn reports system-level companion work to the IDE UI.
// Labels are applied by the client — never put these into the model prompt.
type ProgressFn func(step, status string)

// DeltaFn reports a growing assistant reply extracted from a partial model JSON envelope.
// Only called when the decoded reply text grew since the previous emission.
type DeltaFn func(reply string)

type ChatRequest struct {
	WorkspaceID string `json:"workspaceId"`
	// Speaker isolates purpose-specific conversations (for example log
	// diagnostics) from the everyday IDE assistant.
	Speaker string    `json:"speaker,omitempty"`
	Message string    `json:"message"`
	APIKey  string    `json:"apiKey,omitempty"`
	Focus   ChatFocus `json:"focus,omitempty"`
	// PreviousAnswerRejected — человек отметил прошлый ответ как не помогший.
	// Отметка стояла в интерфейсе и никуда не шла: кнопка обещала «учтём», а
	// учитывать её было некому. Здесь она становится указанием на один круг —
	// не повторять тот же ход, — и в память не превращается: недовольство одним
	// ответом не должно менять поведение навсегда.
	PreviousAnswerRejected bool       `json:"previousAnswerRejected,omitempty"`
	OnProgress             ProgressFn `json:"-"`
	OnDelta                DeltaFn    `json:"-"`
}

// ChatFocus is live, untrusted IDE attention for one Companion turn.
// It is not persisted as an observation and never starts a Quest.
type ChatFocus struct {
	File        string `json:"file,omitempty"`
	Line        int    `json:"line,omitempty"`
	Language    string `json:"language,omitempty"`
	Dirty       bool   `json:"dirty,omitempty"`
	Snippet     string `json:"snippet,omitempty"`
	Run         string `json:"run,omitempty"`
	Debug       string `json:"debug,omitempty"`
	Failure     string `json:"failure,omitempty"`
	Selection   bool   `json:"selection,omitempty"`
	Diagnostics int    `json:"diagnostics,omitempty"`
}

func (f ChatFocus) Empty() bool {
	return strings.TrimSpace(f.File) == "" && strings.TrimSpace(f.Run) == "" && strings.TrimSpace(f.Debug) == "" && strings.TrimSpace(f.Failure) == "" && strings.TrimSpace(f.Snippet) == ""
}

func (f ChatFocus) Label() string {
	file := strings.TrimSpace(f.File)
	if file == "" {
		return ""
	}
	if f.Line > 0 {
		return fmt.Sprintf("%s:%d", file, f.Line)
	}
	return file
}

func hasParentDirSegment(value string) bool {
	return workspace.HasParentDirSegment(value)
}

func sanitizeChatFocus(focus ChatFocus) ChatFocus {
	focus.File = filepath.ToSlash(strings.TrimSpace(focus.File))
	if hasParentDirSegment(focus.File) {
		focus.File = ""
	}
	focus.File = trim(focus.File, 240)
	if focus.Line < 0 || focus.Line > 1_000_000 {
		focus.Line = 0
	}
	focus.Language = trim(focus.Language, 40)
	focus.Snippet = trim(focus.Snippet, 2500)
	focus.Run = trim(focus.Run, 200)
	focus.Debug = trim(focus.Debug, 200)
	focus.Failure = trim(focus.Failure, 200)
	if focus.Diagnostics < 0 {
		focus.Diagnostics = 0
	}
	return focus
}

type ChatResponse struct {
	Reply          string                          `json:"reply"`
	Level          string                          `json:"level"` // suggestion | warning | critical
	Mode           string                          `json:"mode"`  // model | deterministic
	Provider       string                          `json:"provider,omitempty"`
	Model          string                          `json:"model,omitempty"`
	FallbackReason string                          `json:"fallbackReason,omitempty"`
	Proposal       *domain.QuestProposal           `json:"proposal,omitempty"`
	ActionProposal *domain.CompanionActionProposal `json:"actionProposal,omitempty"`
	Questions      []string                        `json:"questions,omitempty"`
	FactsUsed      []string                        `json:"factsUsed,omitempty"`
	Usage          *domain.UsageRecord             `json:"usage,omitempty"`
}

type RecommendRequest struct {
	WorkspaceID string `json:"workspaceId"`
	Goal        string `json:"goal"`
	Context     string `json:"context,omitempty"`
}

type gatheredContext struct {
	Facts  []string
	Prompt string
	Usage  usageAnalysis
	IDE    []domain.IDEObservation
	Focus  ChatFocus
}

type usagePeriod struct {
	Records            int
	Tokens             int64
	KnownCostCents     int64
	UnknownCostRecords int
	FailedOutcomes     int
}

type usageRank struct {
	Name    string
	Records int
	Tokens  int64
}

type usageAnalysis struct {
	Records       int
	Bounded       bool
	CurrentMonth  usagePeriod
	PreviousMonth usagePeriod
	TopModels     []usageRank
	TopAgents     []usageRank
}

// BudgetSnapshot is read-only evidence for Companion. Hard enforcement stays
// in the application's budget policy and is never performed by Companion.
func (s Service) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	started := time.Now()
	req.Message = strings.TrimSpace(req.Message)
	if req.Message == "" {
		return ChatResponse{}, errors.New("companion message is required")
	}
	if len(req.Message) > 32*1024 {
		return ChatResponse{}, errors.New("companion message exceeds 32 KiB")
	}
	if len(req.APIKey) > 64*1024 {
		return ChatResponse{}, errors.New("companion API key exceeds 64 KiB")
	}
	req.Focus = sanitizeChatFocus(req.Focus)
	req.Speaker = normalizeCompanionSpeaker(req.Speaker)
	cfg, err := s.EnsureConfig(ctx, req.WorkspaceID)
	if err != nil {
		return ChatResponse{}, err
	}
	if s.ConfigResolver != nil {
		cfg, err = s.ConfigResolver(cfg)
		if err != nil {
			return ChatResponse{}, err
		}
	}
	if observability.RequestID(ctx) == "" {
		ctx = observability.WithRequestID(ctx, observability.NewRequestID())
	}
	log := observability.From(ctx)
	log.Info("companion chat start",
		"workspace_id", req.WorkspaceID,
		"message_bytes", len(req.Message),
		"message_preview", observability.Snippet(security.Redact(req.Message), 160),
		"focus_file", req.Focus.File,
		"focus_line", req.Focus.Line,
		"focus_language", req.Focus.Language,
		"focus_dirty", req.Focus.Dirty,
		"focus_selection", req.Focus.Selection,
		"focus_run", observability.Snippet(req.Focus.Run, 80),
		"focus_failure", observability.Snippet(security.Redact(req.Focus.Failure), 120),
		"focus_diagnostics", req.Focus.Diagnostics,
		"has_api_key", strings.TrimSpace(req.APIKey) != "",
		"provider", string(cfg.Provider),
		"provider_preset", cfg.ProviderPreset,
		"model", cfg.Model,
		"base_url_host", observability.HostOnly(cfg.BaseURL),
		"preset", cfg.Preset,
	)
	history, err := s.Store.ListChatMessages(ctx, req.WorkspaceID, req.Speaker, 48)
	if err != nil {
		return ChatResponse{}, err
	}
	// Вопрос сохраняется до работы, а не вместе с ответом в конце круга.
	//
	// Прежде пара уходила в базу одним куском после ответа, и оборванный круг —
	// таймаут ядра, отмена, отвалившийся клиент — не оставлял ничего: человек
	// отправлял сообщение и видел пустую ленту, не понимая, дошло ли оно вообще.
	// Читается история до этой записи, иначе свежий вопрос пришёл бы модели
	// дважды: в истории и отдельной репликой.
	if err = s.Store.SaveCompanionMessage(ctx, domain.CompanionMessage{
		ID: domain.NewID("companionmsg"), WorkspaceID: req.WorkspaceID, Speaker: req.Speaker, Role: "user",
		Content: req.Message, CreatedAt: time.Now().UTC(),
	}); err != nil {
		return ChatResponse{}, fmt.Errorf("save companion user message: %w", err)
	}
	// Ранняя часть разговора уходит из окна: в запрос она попадает выжимкой и
	// исчезает вместе с ним. Та же выжимка ложится в память помощника, чтобы
	// после перезапуска или нового чата знание не начиналось с чистого листа.
	s.rememberHistoryDigest(ctx, req.WorkspaceID, req.Speaker, history)
	emitProgress(req.OnProgress, "gather", "running")
	projectContext, err := s.gatherContext(ctx, req.WorkspaceID, req.Message, req.Focus, req.OnProgress)
	if err != nil {
		emitProgress(req.OnProgress, "gather", "error")
		log.Error("companion gather context failed", "workspace_id", req.WorkspaceID, "error", security.Redact(err.Error()))
		return ChatResponse{}, err
	}
	emitProgress(req.OnProgress, "gather", "done")
	logCompanionContext(ctx, req.WorkspaceID, projectContext)
	var response ChatResponse
	path := "deterministic"
	lowerMessage := strings.ToLower(req.Message)
	finish := func(resp ChatResponse, pathName string, callErr error) (ChatResponse, error) {
		if callErr != nil {
			log.Error("companion chat failed",
				"workspace_id", req.WorkspaceID,
				"path", pathName,
				"duration_ms", time.Since(started).Milliseconds(),
				"error", security.Redact(callErr.Error()),
			)
			return ChatResponse{}, callErr
		}
		log.Info("companion chat done",
			"workspace_id", req.WorkspaceID,
			"path", pathName,
			"mode", resp.Mode,
			"level", resp.Level,
			"provider", resp.Provider,
			"model", resp.Model,
			"reply_bytes", len(resp.Reply),
			"reply_preview", observability.Snippet(security.Redact(resp.Reply), 200),
			"questions", len(resp.Questions),
			"has_proposal", resp.Proposal != nil,
			"has_action", resp.ActionProposal != nil,
			"fallback_reason", observability.Snippet(security.Redact(resp.FallbackReason), 300),
			"facts", len(resp.FactsUsed),
			"duration_ms", time.Since(started).Milliseconds(),
		)
		return resp, nil
	}
	if kind := companionMemoryIntent(req.Message); kind != "" {
		path = "memory"
		log.Info("companion path selected", "path", path, "reason", "message is about the companion memory", "intent", kind)
		response, err = s.handleMemoryIntent(ctx, req.WorkspaceID, kind, req.Message)
		if err != nil {
			return finish(response, path, err)
		}
		if err = s.persistTurn(ctx, req, response); err != nil {
			return finish(response, path, err)
		}
		return finish(response, path, nil)
	}
	if isToolCreationRequest(lowerMessage) {
		path = "tool"
		log.Info("companion path selected", "path", path, "reason", "message matched tool creation")
		response, err = s.proposeCustomToolAction(ctx, cfg, req.Message, projectContext)
		if err != nil {
			return finish(response, path, err)
		}
		if err = s.persistTurn(ctx, req, response); err != nil {
			return finish(response, path, err)
		}
		return finish(response, path, nil)
	}
	if isSkillCreationRequest(lowerMessage) {
		path = "skill"
		log.Info("companion path selected", "path", path, "reason", "message matched skill creation")
		response, err = s.proposeSkillAction(ctx, cfg, req.Message, projectContext)
		if err != nil {
			return finish(response, path, err)
		}
		if err = s.persistTurn(ctx, req, response); err != nil {
			return finish(response, path, err)
		}
		return finish(response, path, nil)
	}
	if isTeamCreationRequest(lowerMessage) {
		path = "team"
		log.Info("companion path selected", "path", path, "reason", "message matched team creation")
		response, err = s.proposeTeamAction(ctx, cfg, req.Message, projectContext)
		if err != nil {
			return finish(response, path, err)
		}
		if err = s.persistTurn(ctx, req, response); err != nil {
			return finish(response, path, err)
		}
		return finish(response, path, nil)
	}
	if isAgentCreationRequest(lowerMessage) {
		path = "agent"
		log.Info("companion path selected", "path", path, "reason", "message matched agent creation")
		response, err = s.proposeAgentAction(ctx, cfg, req.Message, projectContext)
		if err != nil {
			return finish(response, path, err)
		}
		if err = s.persistTurn(ctx, req, response); err != nil {
			return finish(response, path, err)
		}
		return finish(response, path, nil)
	}
	if isFlowCreationRequest(lowerMessage) {
		path = "flow"
		log.Info("companion path selected", "path", path, "reason", "message matched flow creation")
		response, err = s.proposeFlowAction(ctx, cfg, req.Message, projectContext)
		if err != nil {
			return finish(response, path, err)
		}
		if err = s.persistTurn(ctx, req, response); err != nil {
			return finish(response, path, err)
		}
		return finish(response, path, nil)
	}
	if cfg.Provider != "" && strings.TrimSpace(cfg.Model) != "" {
		path = "model"
		log.Info("companion path selected", "path", path, "reason", "provider and model configured")
		emitProgress(req.OnProgress, "model", "running")
		response, modelErr := s.chatWithModel(ctx, cfg, req, projectContext, history)
		if modelErr == nil {
			emitProgress(req.OnProgress, "model", "done")
			if err = s.persistTurn(ctx, req, response); err != nil {
				return finish(response, path, err)
			}
			return finish(response, path, nil)
		}
		emitProgress(req.OnProgress, "model", "error")
		log.Warn("companion model failed; using deterministic fallback",
			"workspace_id", req.WorkspaceID,
			"provider", string(cfg.Provider),
			"model", cfg.Model,
			"error", security.Redact(modelErr.Error()),
		)
		path = "model_fallback"
		log.Info("companion path selected", "path", path, "reason", "model call failed")
		emitProgress(req.OnProgress, "local", "running")
		// Откат переживает обрыв ожидания. Тот, кто ждал ответа, мог уже уйти, но
		// разбор, написанный Point вместо молчащей модели, принадлежит разговору:
		// человек увидит его в истории, когда вернётся.
		fallbackCtx, cancelFallback := context.WithTimeout(context.WithoutCancel(ctx), companionFallbackBudget)
		defer cancelFallback()
		response, err = s.deterministicChat(fallbackCtx, cfg, req.Message, projectContext, history)
		if err != nil {
			emitProgress(req.OnProgress, "local", "error")
			return finish(response, path, err)
		}
		emitProgress(req.OnProgress, "local", "done")
		response.FallbackReason = trim(modelErr.Error(), 500)
		if err = s.persistTurn(fallbackCtx, req, response); err != nil {
			return finish(response, path, err)
		}
		return finish(response, path, nil)
	}
	log.Info("companion path selected", "path", path, "reason", "no provider or model configured")
	emitProgress(req.OnProgress, "local", "running")
	response, err = s.deterministicChat(ctx, cfg, req.Message, projectContext, history)
	if err != nil {
		emitProgress(req.OnProgress, "local", "error")
		return finish(response, path, err)
	}
	emitProgress(req.OnProgress, "local", "done")
	if err = s.persistTurn(ctx, req, response); err != nil {
		return finish(response, path, err)
	}
	return finish(response, path, nil)
}

func logCompanionContext(ctx context.Context, workspaceID string, projectContext gatheredContext) {
	ideKinds := map[string]int{}
	diagErrors, diagWarnings, failedCmds := 0, 0, 0
	for _, item := range projectContext.IDE {
		ideKinds[item.Kind]++
		if item.Kind == "diagnostic" {
			if item.Level == "error" {
				diagErrors++
			} else if item.Level == "warning" {
				diagWarnings++
			}
		}
		if (item.Kind == "terminal" || item.Kind == "task") && item.ExitCode != nil && *item.ExitCode != 0 {
			failedCmds++
		}
	}
	codePaths := make([]string, 0, 8)
	indexState := ""
	for _, fact := range projectContext.Facts {
		if strings.HasPrefix(fact, "codeContext=") {
			codePaths = append(codePaths, strings.TrimPrefix(fact, "codeContext="))
		}
		if strings.HasPrefix(fact, "projectIndexFiles=") || strings.HasPrefix(fact, "projectIndex=") {
			indexState = fact
		}
	}
	observability.From(ctx).Info("companion context gathered",
		"workspace_id", workspaceID,
		"facts", len(projectContext.Facts),
		"prompt_bytes", len(projectContext.Prompt),
		"ide_observations", len(projectContext.IDE),
		"ide_kinds", ideKinds,
		"ide_diagnostic_errors", diagErrors,
		"ide_diagnostic_warnings", diagWarnings,
		"ide_failed_commands", failedCmds,
		"focus", projectContext.Focus.Label(),
		"usage_records", projectContext.Usage.Records,
		"index", indexState,
		"code_context", observability.Snippet(strings.Join(codePaths, "; "), 400),
		"facts_preview", observability.Snippet(strings.Join(projectContext.Facts, "; "), 400),
	)
	observability.From(ctx).Debug("companion context prompt",
		"workspace_id", workspaceID,
		"prompt_preview", observability.Snippet(projectContext.Prompt, 1200),
	)
}

// Ответ помощника. Вопрос человека сюда не входит: он сохранён раньше, до
// обращения к модели, — иначе оборванный круг уносил бы и его.
func (s Service) persistTurn(ctx context.Context, req ChatRequest, response ChatResponse) error {
	now := time.Now().UTC()
	assistant := domain.CompanionMessage{
		ID: domain.NewID("companionmsg"), WorkspaceID: req.WorkspaceID, Speaker: req.Speaker, Role: "assistant",
		Content: response.Reply, Level: response.Level, Mode: response.Mode, Provider: response.Provider,
		Model: response.Model, FactsUsed: append([]string(nil), response.FactsUsed...), Questions: append([]string(nil), response.Questions...), FallbackReason: response.FallbackReason,
		CreatedAt: now,
	}
	if response.Proposal != nil {
		assistant.ProposalID = response.Proposal.ID
	}
	if response.ActionProposal != nil {
		assistant.ActionProposalID = response.ActionProposal.ID
	}
	if response.Usage != nil {
		assistant.UsageRecordID = response.Usage.ID
		assistant.InputTokens = response.Usage.InputTokens
		assistant.OutputTokens = response.Usage.OutputTokens
		assistant.TotalTokens = response.Usage.TotalTokens
		assistant.LatencyMs = response.Usage.LatencyMs
	}
	if err := s.Store.SaveCompanionMessage(ctx, assistant); err != nil {
		return fmt.Errorf("save companion assistant message: %w", err)
	}
	return nil
}
