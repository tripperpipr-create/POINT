package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/orchestrator"
	"local-agent-workbench/internal/textutil"
	"local-agent-workbench/internal/workspace"
)

// Разговор с Мастером — отдельная поверхность, а не переиспользование чата
// компаньона. Раньше раздел «Мастер» открывал буквально диалог помощника IDE:
// одно окно, два разных собеседника и ни одного способа их различить.

type MasterChatRequest struct {
	TurnID         string                    `json:"turnId,omitempty"`
	Attachments    []domain.MasterAttachment `json:"attachments,omitempty"`
	Model          string                    `json:"model,omitempty"`
	ConversationID string                    `json:"conversationId,omitempty"`
	TaskIntake     bool                      `json:"taskIntake,omitempty"`
	ProposalID     string                    `json:"proposalId,omitempty"`
	Message        string                    `json:"message"`
	// PreviousAnswerRejected — реплика повторена кнопкой «Ответить иначе».
	PreviousAnswerRejected bool `json:"previousAnswerRejected,omitempty"`
	// APIKey живёт только в памяти запроса: разговор Мастера ведёт его модель,
	// а ключ хранит SecretStorage расширения и присылает с каждой репликой.
	APIKey string `json:"apiKey,omitempty"`
}

// MasterTurnV2Request references immutable snapshots instead of accepting
// caller-supplied attachment bytes. This keeps the conversation and the
// approved WorkOrder bound to the same digests.
type MasterTurnV2Request struct {
	TurnID         string                     `json:"turnId,omitempty"`
	ConversationID string                     `json:"conversationId,omitempty"`
	WorkspaceID    string                     `json:"workspaceId,omitempty"`
	Message        string                     `json:"message"`
	Sources        []domain.SourceSnapshotRef `json:"sources,omitempty"`
	Model          string                     `json:"model,omitempty"`
	TaskIntake     bool                       `json:"taskIntake,omitempty"`
	ProposalID     string                     `json:"proposalId,omitempty"`
	APIKey         string                     `json:"apiKey,omitempty"`
	// PreviousAnswerRejected preserves the deterministic "answer differently"
	// action when the UI uses the v2 transport.
	PreviousAnswerRejected bool `json:"previousAnswerRejected,omitempty"`
}

type MasterChatView struct {
	ContextBudgetChars int                 `json:"contextBudgetChars"`
	SupportsImages     bool                `json:"supportsImages"`
	Before             int64               `json:"before,omitempty"`
	ActiveTurns        []domain.MasterTurn `json:"activeTurns,omitempty"`
	WorkOrders         []domain.WorkOrder  `json:"workOrders,omitempty"`
	// Hiring — карточка найма: кандидаты, их готовность, чертежи и пробелы.
	// Считается на чтении и не хранится: готовность меняется от внешних причин
	// (человек починил модель, отвалилось подключение), а под digest наряда ей
	// нельзя — иначе карточка «изменилась» без единого хода Мастера.
	Hiring   []RosterCardView `json:"hiring,omitempty"`
	Sessions MasterSessions   `json:"sessions"`
	// Configured=false — не ошибка, а состояние: клиент показывает приглашение
	// настроить Мастера вместо пустого окна с непонятным сбоем.
	Configured bool                      `json:"configured"`
	Response   orchestrator.ChatResponse `json:"response"`
	History    []domain.CompanionMessage `json:"history"`
	Config     domain.OrchestratorConfig `json:"config"`
	// Truncated — показаны не все реплики, разговор начался раньше.
	//
	// История отдаётся хвостом, и на длинном разговоре он начинался с середины
	// без единого знака: экран выглядел как весь диалог целиком. Разница между
	// «этого не было» и «этого здесь не показано» — то же самое различие, ради
	// которого в остальном интерфейсе разведены пустота и незнание.
	Truncated bool `json:"truncated,omitempty"`
	// Briefing is the exact bounded, read-only evidence supplied to Master.
	Briefing orchestrator.ProjectFacts `json:"briefing"`
}

// masterHistoryLimit — сколько реплик Мастера показывается за раз.
const masterHistoryLimit = 60

// masterHistoryFullLimit — весь хвост, какой хранилище отдаёт за один запрос.
// Строка «разговор начался раньше» была тупиком: она сообщала, что начало
// осталось за кадром, и не давала туда попасть.
const masterHistoryFullLimit = 200

// masterHistory отдаёт хвост разговора и признак того, что он именно хвост.
//
// Запрашивается на одну реплику больше предела: пришла лишняя — значит начало
// разговора осталось за кадром, и об этом нужно сказать. Считать по «пришло
// ровно столько, сколько просили» было бы гаданием: ровно шестьдесят реплик —
// это и полный разговор тоже.
func (a *App) masterHistory(ctx context.Context, service orchestrator.ChatService, workspaceID string, full bool) ([]domain.CompanionMessage, bool, error) {
	limit := masterHistoryLimit
	if full {
		limit = masterHistoryFullLimit
	}
	history, err := service.History(ctx, workspaceID, limit+1)
	if err != nil {
		return nil, false, err
	}
	if len(history) > limit {
		return history[len(history)-limit:], true, nil
	}
	if history == nil {
		history = []domain.CompanionMessage{}
	}
	return history, false, nil
}

func (a *App) masterChatService(ctx context.Context, briefing orchestrator.ProjectFacts) orchestrator.ChatService {
	a.mu.RLock()
	fs := a.currentFS
	a.mu.RUnlock()
	workspaceID := a.currentWorldID()
	reading := newMasterReadTools(fs, a.store, workspaceID, nil)
	return orchestrator.ChatService{
		ReadTools: reading,
		Store:     a.store,
		NewID:     domain.NewID,
		ModelFactory: a.budgetedModelFactory(modelBudgetScope{
			WorkspaceID: workspaceID, ProjectAgentID: "master", Outcome: "master_model",
		}),
		SelectionSignals: a.projectAgentSelectionSignals,
		// Мастер судит о годности агента теми же правилами, что и его карточка.
		// Иначе диспетчер собирал бы отряд из тех, кого форма настройки уже
		// признала неработоспособными, и человек узнавал бы об этом из провала.
		Capability: func(agent domain.ProjectAgent) []string {
			return a.projectAgentReadiness(ctx, agent).Blocking
		},
		// Числа происходящего Мастер берёт у того же кода, который наполняет
		// очередь решений: своя арифметика поссорила бы его с экраном.
		Situation: func(ctx context.Context, workspaceID string) (orchestrator.Situation, error) {
			queue, err := a.Decisions(ctx)
			if err != nil {
				return orchestrator.Situation{}, err
			}
			sets, err := a.store.ListChangeSets(ctx, workspaceID)
			if err != nil {
				return orchestrator.Situation{}, err
			}
			pending := 0
			for _, set := range sets {
				if set.Status == domain.ChangeSetPending {
					pending++
				}
			}
			executions, err := a.store.ListExecutions(ctx, workspaceID, 200)
			if err != nil {
				return orchestrator.Situation{}, err
			}
			running := 0
			for _, execution := range executions {
				switch execution.Status {
				case "running", "waiting_approval", "pending":
					running++
				}
			}
			quests, err := a.store.ListQuests(ctx, workspaceID)
			if err != nil {
				return orchestrator.Situation{}, err
			}
			active := 0
			for _, quest := range quests {
				if quest.Status == "active" || quest.Status == "running" {
					active++
				}
			}
			for _, quest := range quests {
				if len(briefing.ActiveQuests) >= 5 {
					break
				}
				if quest.ParentID == "" && (quest.Status == domain.QuestActive || quest.Status == domain.QuestPaused) {
					briefing.ActiveQuests = append(briefing.ActiveQuests, boundedMasterFact(quest.Title, 120)+" ["+string(quest.Status)+"]")
				}
			}
			for _, execution := range executions {
				if len(briefing.RecentChecks) >= 5 {
					break
				}
				if execution.Status == domain.RunCompleted || execution.Status == domain.RunFailed || execution.Status == domain.RunCancelled {
					briefing.RecentChecks = append(briefing.RecentChecks, boundedMasterFact(execution.Task, 100)+" ["+string(execution.Status)+"]")
				}
			}
			for _, set := range sets {
				if len(briefing.RecentChangeSets) >= 5 {
					break
				}
				briefing.RecentChangeSets = append(briefing.RecentChangeSets, boundedMasterFact(set.Title, 100)+" ["+string(set.Status)+"]")
			}
			return orchestrator.Situation{
				WaitingDecisions:  queue.Total,
				PendingChangeSets: pending,
				RunningExecutions: running,
				ActiveQuests:      active,
				Project:           briefing,
			}, nil
		},
	}
}

// masterProjectFacts — те же сведения о проекте, что человек видит в заголовке
// Чертога и в карточке индекса. Без них Мастер отвечал «не понял вопроса» на
// «расскажи про проект»: в снимке мира не было ни имени папки, ни языков.
func (a *App) masterProjectFacts(ctx context.Context) orchestrator.ProjectFacts {
	a.mu.RLock()
	current := a.currentWorkspace
	currentFS := a.currentFS
	a.mu.RUnlock()
	facts := orchestrator.ProjectFacts{IndexState: "no_workspace"}
	if current != nil {
		facts.Name = current.Name
	}
	if currentFS == nil {
		return facts
	}
	status := currentFS.IndexStatus()
	if projectMap, err := currentFS.ProjectMap(ctx, 16); err == nil {
		status = projectMap.Status
		facts.Modules = boundedStrings(projectMap.TopDirectories, 8)
		facts.KeySymbols = boundedStrings(projectMap.Symbols, 12)
		facts.Sources = append(facts.Sources, "project_index")
	}
	facts.IndexState = status.State
	facts.IndexUpdatedAt = status.BuiltAt
	facts.Files = status.Files
	facts.Symbols = status.Symbols
	// Языки — по убыванию доли, не больше пяти: список из тридцати расширений
	// не помогает понять, на чём написан проект.
	type langCount struct {
		name  string
		count int
	}
	counts := make([]langCount, 0, len(status.Languages))
	for name, count := range status.Languages {
		counts = append(counts, langCount{name: name, count: count})
	}
	sort.Slice(counts, func(i, j int) bool {
		if counts[i].count != counts[j].count {
			return counts[i].count > counts[j].count
		}
		return counts[i].name < counts[j].name
	})
	if len(counts) > 5 {
		counts = counts[:5]
	}
	for _, item := range counts {
		facts.Languages = append(facts.Languages, item.name)
	}
	if tree, err := currentFS.List(ctx, 3); err == nil {
		paths := flattenMasterPaths(tree, nil)
		facts.Entrypoints = masterEntrypoints(paths)
		facts.BuildCommands, facts.TestCommands = masterCommands(currentFS, paths)
		facts.Sources = append(facts.Sources, "workspace_tree", "build_manifests")
		// Пустота — факт, а не отсутствие фактов. Дерево спрашивается здесь же:
		// индекс мог не успеть построиться, а «ни одного файла на диске» знает
		// только обход папки.
		facts.Empty = len(paths) == 0 && facts.Files == 0 && len(facts.Modules) == 0
	}
	return facts
}

func flattenMasterPaths(nodes []domain.FileNode, result []string) []string {
	for _, node := range nodes {
		if !node.IsDir {
			result = append(result, node.Path)
		}
		result = flattenMasterPaths(node.Children, result)
	}
	return result
}

func masterEntrypoints(paths []string) []string {
	result := make([]string, 0, 8)
	for _, path := range paths {
		lower := strings.ToLower(strings.ReplaceAll(path, "\\", "/"))
		base := lower
		if index := strings.LastIndex(lower, "/"); index >= 0 {
			base = lower[index+1:]
		}
		entry := base == "main.go" || base == "main.py" || base == "manage.py" || base == "app.py" ||
			base == "index.ts" || base == "index.js" || base == "main.ts" || base == "main.js" ||
			strings.HasPrefix(lower, "cmd/") && base == "main.go"
		if entry {
			result = append(result, path)
			if len(result) == 8 {
				break
			}
		}
	}
	return result
}

func masterCommands(projectFS interface {
	Read(string, bool) (workspace.FileContent, error)
}, paths []string) ([]string, []string) {
	present := map[string]string{}
	for _, path := range paths {
		present[strings.ToLower(strings.ReplaceAll(path, "\\", "/"))] = path
	}
	build, tests := []string{}, []string{}
	if _, ok := present["go.mod"]; ok {
		build, tests = append(build, "go build ./..."), append(tests, "go test ./...")
	}
	if _, ok := present["cargo.toml"]; ok {
		build, tests = append(build, "cargo build"), append(tests, "cargo test")
	}
	if _, ok := present["pyproject.toml"]; ok {
		tests = append(tests, "python -m pytest")
	}
	if path, ok := present["package.json"]; ok {
		if file, readErr := projectFS.Read(path, false); readErr == nil && len(file.Content) <= 256*1024 {
			var manifest struct {
				Scripts map[string]string `json:"scripts"`
			}
			if json.Unmarshal([]byte(file.Content), &manifest) == nil {
				for _, name := range []string{"build", "check", "lint"} {
					if strings.TrimSpace(manifest.Scripts[name]) != "" {
						build = append(build, "npm run "+name)
					}
				}
				if strings.TrimSpace(manifest.Scripts["test"]) != "" {
					tests = append(tests, "npm test")
				}
			}
		}
	}
	return boundedStrings(build, 5), boundedStrings(tests, 5)
}

func boundedStrings(values []string, limit int) []string {
	seen, result := map[string]bool{}, make([]string, 0, min(limit, len(values)))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
		if len(result) >= limit {
			break
		}
	}
	return result
}

func boundedMasterFact(value string, limit int) string {
	return textutil.Bounded(strings.Join(strings.Fields(value), " "), limit)
}

// ErrMasterNotConfigured — разговор невозможен, пока диспетчер не настроен.
//
// Чат целиком принадлежит Мастеру: размер отряда, строгость подтверждений и
// автостарт Flow задаёт его политика. Подставлять пресет по умолчанию значило бы
// вести разговор от имени роли, которой человек ещё не задал ни характера, ни
// границ, и первое же предложение квеста опиралось бы на выдуманные настройки.
//
// Речь именно о политике, а не о модели: сам разговор ведёт движок Point, а
// модель Мастера — планировщик, который вступает при запуске квеста. Пресет без
// модели — рабочая настройка, и «не настроен» о ней не говорится.
var ErrMasterNotConfigured = errors.New("мастер не настроен")

// masterConfig возвращает конфигурацию диспетчера. Отсутствие записи — не сбой,
// а состояние: клиент показывает приглашение настроить Мастера, а не ошибку.
func (a *App) masterConfig(ctx context.Context, workspaceID string) (domain.OrchestratorConfig, error) {
	cfg, err := a.store.GetOrchestratorConfig(ctx, workspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.OrchestratorConfig{}, ErrMasterNotConfigured
	}
	if err != nil {
		return domain.OrchestratorConfig{}, err
	}
	cfg, err = a.resolveOrchestratorConnection(cfg)
	if err != nil {
		return domain.OrchestratorConfig{}, err
	}
	return a.applyCheapModelRoute(ctx, workspaceID, cfg)
}

// MasterChat проводит один ход разговора и возвращает обновлённую историю,
// чтобы клиенту не приходилось делать второй запрос ради собственной реплики.
func (a *App) MasterChat(ctx context.Context, req MasterChatRequest) (MasterChatView, error) {
	workspaceID := a.currentWorldID()
	cfg, err := a.masterConfig(ctx, workspaceID)
	// Ненастроенный Мастер — это состояние раздела, а не сбой запроса. Ошибка
	// показалась бы красным баннером «request_failed», хотя человеку нужно не
	// сообщение о поломке, а приглашение настроить диспетчера.
	if errors.Is(err, ErrMasterNotConfigured) {
		return MasterChatView{Configured: false, History: []domain.CompanionMessage{}}, nil
	}
	if err != nil {
		return MasterChatView{}, err
	}
	briefing := a.masterProjectFacts(ctx)
	service, sessions, err := a.sessionMasterService(ctx, a.masterChatService(ctx, briefing), req.ConversationID)
	if err != nil {
		return MasterChatView{}, err
	}
	return a.masterChatPrepared(ctx, req, workspaceID, cfg, briefing, service, sessions)
}
func (a *App) masterChatPrepared(ctx context.Context, req MasterChatRequest, workspaceID string, cfg domain.OrchestratorConfig, briefing orchestrator.ProjectFacts, service orchestrator.ChatService, sessions MasterSessions) (MasterChatView, error) {
	if req.Model == "" {
		req.Model = sessions.Model
	}
	if req.Model != "" {
		cfg.Model = req.Model
	}
	contextText, images, attachments, err := masterAttachments(req.Attachments, cfg, a.masterAttachmentReference(ctx, cfg))
	if err != nil {
		return MasterChatView{}, err
	}
	if scoped, ok := service.Store.(masterSessionStore); ok {
		scoped.turnID = req.TurnID
		scoped.attachments = attachments
		for _, entry := range sessions.MemoryEntries {
			if entry.Status == "accepted" {
				scoped.memoryIDs = append(scoped.memoryIDs, entry.ID)
			}
		}
		service.Store = scoped
	}
	if req.Model != "" {
		cfg.Model = req.Model
	}
	summary := ""
	for _, v := range sessions.Items {
		if v.ID == sessions.Active {
			summary = v.Summary
		}
	}
	phase := "explanation"
	if req.TaskIntake || sessions.WorkMode == "discuss" {
		phase = "intake"
	}
	service.Skills, err = a.newMasterSkillSession(ctx, workspaceID, phase, req.TurnID, req.ProposalID, "")
	if err != nil {
		return MasterChatView{}, err
	}
	temporary := false
	for _, conversation := range sessions.Items {
		if conversation.ID == sessions.Active {
			temporary = conversation.Temporary
		}
	}
	if !temporary {
		defer a.finishMasterOperation(service.Skills, cfg, req.APIKey)
	}
	response, err := service.Chat(ctx, orchestrator.ChatRequest{
		AutoRunReadOnly: sessions.AutoRunReadOnly,
		Summary:         summary,
		TaskIntake:      req.TaskIntake, ProposalID: req.ProposalID,
		ResponseMode: sessions.Mode, Memory: sessions.Memory, WorkMode: sessions.WorkMode, Context: contextText, Images: images,
		WorkspaceID: workspaceID,
		Message:     strings.TrimSpace(req.Message),
		Config:      cfg,
		APIKey:      req.APIKey,

		PreviousAnswerRejected: req.PreviousAnswerRejected,
	})
	if response.Proposal != nil {
		service.Skills.Operation.ProposalID = response.Proposal.ID
	}
	if response.FallbackReason != "" && !service.Skills.Operation.ProviderError {
		service.Skills.Operation.ContractError = true
	}
	if err != nil {
		return MasterChatView{}, err
	}
	history, truncated, err := a.masterHistory(ctx, service, workspaceID, false)
	if err != nil {
		return MasterChatView{}, err
	}
	if sessions.WorkMode == "execute" && sessions.AutoRunReadOnly && response.Proposal != nil {
		if started, startErr := a.tryMasterAutoRun(ctx, workspaceID, response.Proposal, req.APIKey); startErr != nil {
			if service.OnProgress != nil {
				service.OnProgress("tools", "Нужно подтверждение: "+startErr.Error(), "")
			}
		} else if started && service.OnProgress != nil {
			service.OnProgress("tools", "Задание передано исполнителю в разрешённых пределах", "")
		}
	}
	for i := range sessions.Items {
		item := sessions.Items[i]
		if item.ID != sessions.Active {
			continue
		}
		if item.Title == "Новый разговор" {
			title := []rune(strings.Join(strings.Fields(req.Message), " "))
			if len(title) > 60 {
				title = append(title[:60], '…')
			}
			item.Title = string(title)
		}
		if summary := strings.TrimSpace(response.ConversationSummary); summary != "" {
			runes := []rune(summary)
			if len(runes) > 6000 {
				runes = runes[:6000]
			}
			item.Summary = string(runes)
		}
		item.UpdatedAt = ""
		item.WorkspaceID = workspaceID
		if err = a.store.SaveMasterConversation(ctx, item); err != nil {
			return MasterChatView{}, err
		}
		sessions.Items[i] = item
	}
	for _, conversation := range sessions.Items {
		if conversation.ID == sessions.Active && conversation.Temporary {
			response.MemorySuggestions = nil
		}
	}
	for _, suggestion := range response.MemorySuggestions {
		content := strings.TrimSpace(suggestion)
		if content == "" || len([]rune(content)) > 4000 {
			continue
		}
		duplicate := false
		for _, entry := range sessions.MemoryEntries {
			if strings.EqualFold(entry.Content, content) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			entry := domain.MasterMemoryEntry{ID: domain.NewID("memory"), Content: content, SourceID: req.TurnID, Status: "proposed"}
			if err = a.store.SaveMasterMemory(ctx, workspaceID, entry); err != nil {
				return MasterChatView{}, err
			}
			sessions.MemoryEntries = append(sessions.MemoryEntries, entry)
		}
	}
	workOrders, err := a.store.ListWorkOrdersForConversationV2(ctx, sessions.Active)
	if err != nil {
		return MasterChatView{}, err
	}
	return MasterChatView{Configured: true, Sessions: sessions, Response: response, History: history, Config: cfg, Truncated: truncated, Briefing: briefing, WorkOrders: workOrders, Hiring: a.hiringCardsForConversation(ctx, workOrders)}, nil
}

// MasterMessageFeedback ставит или снимает оценку одной реплики Мастера.
//
// Оценка не меняет ни ответа, ни хода: она остаётся при реплике, чтобы человек
// видел, что уже разбирал, а обучение — какие постановки задач принимают, а
// какие переделывают. Повторное нажатие снимает отметку: передумать можно.
func (a *App) MasterMessageFeedback(ctx context.Context, messageID, value string) error {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return errors.New("не указана реплика")
	}
	switch value {
	case "up", "down", "":
	default:
		return errors.New("оценка бывает только «up», «down» или пустой")
	}
	if err := a.store.SetChatMessageFeedback(ctx, a.currentWorldID(), messageID, value); err != nil {
		return err
	}
	if err := a.store.SetMasterOperationFeedback(ctx, a.currentWorldID(), messageID, value); err != nil {
		return err
	}
	return a.updateMasterCanaries(ctx, a.currentWorldID())
}

// MasterHistory отдаёт разговор без нового хода — для открытия раздела.
// Ненастроенный Мастер возвращается состоянием, а не ошибкой: раздел должен
// открыться и объяснить, чего не хватает.
func (a *App) MasterHistory(ctx context.Context) (MasterChatView, error) {
	return a.MasterSessionHistory(ctx, "", false)
}

// full=true отдаёт весь хвост, какой хранит ядро: это кнопка «Показать раньше»
// под строкой о том, что разговор начался раньше показанного.
func (a *App) MasterSessionHistory(ctx context.Context, id string, full bool) (MasterChatView, error) {
	workspaceID := a.currentWorldID()
	cfg, err := a.masterConfig(ctx, workspaceID)
	configured := true
	if errors.Is(err, ErrMasterNotConfigured) {
		configured = false
		err = nil
	}
	if err != nil {
		return MasterChatView{}, err
	}
	briefing := a.masterProjectFacts(ctx)
	service, sessions, err := a.sessionMasterService(ctx, a.masterChatService(ctx, briefing), id)
	if err != nil {
		return MasterChatView{}, err
	}
	history, truncated, err := a.masterHistory(ctx, service, workspaceID, full)
	if err != nil {
		return MasterChatView{}, err
	}
	page, pageErr := a.store.MasterMessagePage(ctx, workspaceID, sessions.Active, 0, "", masterHistoryLimit)
	if pageErr != nil {
		return MasterChatView{}, pageErr
	}
	turns, turnErr := a.store.MasterActiveTurns(ctx, workspaceID)
	if turnErr != nil {
		return MasterChatView{}, turnErr
	}
	workOrders, workOrderErr := a.store.ListWorkOrdersForConversationV2(ctx, sessions.Active)
	if workOrderErr != nil {
		return MasterChatView{}, workOrderErr
	}
	modelCfg := cfg
	if sessions.Model != "" {
		modelCfg.Model = sessions.Model
	}
	ref := a.masterAttachmentReference(ctx, modelCfg)
	vision := false
	for _, capability := range ref.Capabilities {
		vision = vision || capability == "vision"
	}
	return MasterChatView{ContextBudgetChars: masterAttachmentBudget(modelCfg, ref), SupportsImages: vision, Configured: configured, Sessions: sessions, History: history, Config: cfg, Truncated: truncated || page.HasMore, Briefing: briefing, Before: page.Before, ActiveTurns: turns, WorkOrders: workOrders, Hiring: a.hiringCardsForConversation(ctx, workOrders)}, nil
}
