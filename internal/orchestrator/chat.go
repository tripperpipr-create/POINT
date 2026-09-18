package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
	"local-agent-workbench/internal/textutil"
)

// Разговор с Мастером.
//
// Мастер — это диспетчер: он не правит код и не лезет в редактор. Он слушает
// задачу, подбирает отряд и предлагает квест. Поэтому его чат отличается от
// чата компаньона не тоном, а результатом: компаньон отвечает советом, мастер —
// предложением работы, которое человек принимает или отклоняет.
//
// Предложение никогда не становится работой само. Оно попадает в очередь
// решений и ждёт явного «Запустить» — это тот же структурный предохранитель,
// что и у компаньона, только с другой стороны.

const (
	maxChatMessage = 32 * 1024
	maxObjectives  = 6
	// maxQuestTitle — сколько знаков названия квеста ещё читается строкой в
	// очереди решений и в списке квестов, а не абзацем.
	maxQuestTitle = 72
)

type ChatStore interface {
	ListProjectAgents(ctx context.Context, workspaceID string) ([]domain.ProjectAgent, error)
	ListQuests(ctx context.Context, workspaceID string) ([]domain.Quest, error)
	SaveQuestProposal(ctx context.Context, proposal domain.QuestProposal) error
	// ListQuestProposals нужен, чтобы Мастер помнил, что сам же и предложил:
	// сделанное предложение ждёт решения, и следующая реплика должна знать о нём.
	ListQuestProposals(ctx context.Context, workspaceID string) ([]domain.QuestProposal, error)
	SaveCompanionActionProposal(ctx context.Context, proposal domain.CompanionActionProposal) error
	ListCompanionActionProposals(ctx context.Context, workspaceID string) ([]domain.CompanionActionProposal, error)
	SaveCompanionMessage(ctx context.Context, message domain.CompanionMessage) error
	ListChatMessages(ctx context.Context, workspaceID, speaker string, limit int) ([]domain.CompanionMessage, error)
	// ListBlueprints нужен для разговора с пустым ростером: без агентов Мастер
	// не отказывает, а предлагает, кого нанять под задачу.
	ListBlueprints(ctx context.Context) ([]domain.AgentBlueprint, error)
}

type ChatService struct {
	// OnProgress — живой след хода: kind называет событие, text остаётся
	// короткой человеческой строкой, detail несёт подробности в JSON. Три
	// аргумента, а не два, потому что лента показывает и то, и другое: строку
	// ожидания и раскрываемые подробности под ней.
	OnProgress func(kind, text, detail string)
	ReadTools  TaskReadTools
	Store      ChatStore
	Capability CapabilityCheck
	Situation  SituationCheck
	Now        func() time.Time
	NewID      func(prefix string) string
	// ModelFactory — чем Мастер разговаривает. Пусто в тестах и когда модель не
	// настроена: тогда отвечает детерминированный движок.
	ModelFactory     ModelFactory
	SelectionSignals func(context.Context, string) map[string]CandidateSignal
}

func (s ChatService) assignParty(ctx context.Context, req ChatRequest, agents []domain.ProjectAgent, proposed []string) Assignment {
	var signals map[string]CandidateSignal
	if s.SelectionSignals != nil {
		signals = s.SelectionSignals(ctx, req.WorkspaceID)
	}
	return AssignPartyWithSignals(req.Config, s.runnableAgents(agents), proposed, req.Message, signals)
}

func (s ChatService) blockersFor(agent domain.ProjectAgent) []string {
	if s.Capability == nil {
		return nil
	}
	return s.Capability(agent)
}

func (s ChatService) runnableAgents(agents []domain.ProjectAgent) []domain.ProjectAgent {
	result := make([]domain.ProjectAgent, 0, len(agents))
	for _, agent := range agents {
		if len(s.blockersFor(agent)) == 0 {
			result = append(result, agent)
		}
	}
	return result
}

type ChatRequest struct {
	AutoRunReadOnly bool
	Summary         string
	WorkMode        string
	Context         string
	Images          []providers.ImageContent
	ResponseMode    string
	Memory          string
	TaskIntake      bool
	ProposalID      string
	WorkspaceID     string
	Message         string
	Config          domain.OrchestratorConfig
	// APIKey живёт только в памяти запроса: разговор Мастера идёт его моделью,
	// а ключ к ней хранит SecretStorage на стороне расширения.
	APIKey string
	// PreviousAnswerRejected — человек нажал «Ответить иначе». Без этого признака
	// кнопка отправляла бы тот же вопрос заново, и при низкой температуре модель
	// возвращала бы тот же ответ слово в слово: нажатие без последствий.
	PreviousAnswerRejected bool
}

// PartyMember объясняет, почему агент попал в отряд. Оценка и совпавшие термины
// считались и раньше, но оставались внутри: человек видел состав без причин и
// не мог ни поспорить, ни поправить. Показанное обоснование делает подбор
// проверяемым, а не авторитетным.
type PartyMember struct {
	AllowedTools []string `json:"allowedTools,omitempty"`
	AgentID      string   `json:"agentId"`
	Name         string   `json:"name"`
	Role         string   `json:"role,omitempty"`
	// Score — соответствие задаче, и только там, где задача была. Ростер
	// перечисляется без неё, а числом это поле быть не переставало: рядом с
	// каждым агентом стоял «0» с подсказкой «оценка соответствия задаче», то
	// есть оценка, которой никто не выставлял. Пустое поле интерфейс не рисует.
	Score   *int     `json:"score,omitempty"`
	Matched []string `json:"matched,omitempty"`
	// Blocking — почему этот агент не сможет довести работу до конца.
	// Пустой список означает, что он к квесту готов.
	Blocking []string `json:"blocking,omitempty"`
}

// Situation — что прямо сейчас требует внимания. Считает приложение тем же
// кодом, который наполняет очередь решений: у Мастера не может быть своей
// арифметики происходящего, иначе он и экран будут спорить о числах.
type Situation struct {
	WaitingDecisions  int
	PendingChangeSets int
	RunningExecutions int
	ActiveQuests      int
	// Project — чем занят проект, а не только сколько в нём агентов.
	//
	// На «расскажи про сам проект» Мастер отвечал «не понял вопроса»: в снимке
	// мира были ростер, квесты и очередь решений — и ни слова о самом проекте.
	// Диспетчер, не знающий, чем распоряжается, разговором не является.
	Project ProjectFacts
}

// ProjectFacts — короткая справка о проекте: то же, что видит человек в
// заголовке Чертога и в карточке индекса.
type ProjectFacts struct {
	Name string `json:"name,omitempty"`
	// Empty — рабочая папка пуста: ни файлов, ни модулей.
	//
	// Вывод из нулей модель не делала: получив files=0, она всё равно
	// спрашивала «создать новый проект или использовать существующий код?» у
	// пустого репозитория. Пустота — такой же факт, как язык или точка входа, и
	// называется прямо, а не оставляется на арифметику читателя.
	Empty            bool      `json:"empty,omitempty"`
	IndexState       string    `json:"indexState"`
	IndexUpdatedAt   time.Time `json:"indexUpdatedAt,omitempty"`
	Files            int       `json:"files"`
	Symbols          int       `json:"symbols"`
	Languages        []string  `json:"languages,omitempty"`
	Modules          []string  `json:"modules,omitempty"`
	Entrypoints      []string  `json:"entrypoints,omitempty"`
	BuildCommands    []string  `json:"buildCommands,omitempty"`
	TestCommands     []string  `json:"testCommands,omitempty"`
	KeySymbols       []string  `json:"keySymbols,omitempty"`
	ActiveQuests     []string  `json:"activeQuests,omitempty"`
	RecentChecks     []string  `json:"recentChecks,omitempty"`
	RecentChangeSets []string  `json:"recentChangeSets,omitempty"`
	Sources          []string  `json:"sources,omitempty"`
}

// SituationCheck отдаёт снимок состояния мира.
type SituationCheck func(ctx context.Context, workspaceID string) (Situation, error)

// ChatAction — куда можно уйти прямо из разговора.
//
// Раньше Мастер только рассказывал, и любое действие человек искал сам. Кнопка
// рядом с ответом — это не украшение: разговор с диспетчером и есть место, где
// решают, куда идти дальше.
type ChatAction struct {
	Label string `json:"label"`
	Tab   string `json:"tab"`
	Hint  string `json:"hint,omitempty"`
}

// intentOf — о чём спрашивают. Различение грубое и намеренно предсказуемое:
// детерминированный режим обязан вести себя одинаково на одинаковых словах.
func intentOf(message string) string {
	lower := strings.ToLower(strings.TrimSpace(message))
	has := func(words ...string) bool {
		for _, word := range words {
			if strings.Contains(lower, word) {
				return true
			}
		}
		return false
	}
	// Создание сущностей Hub — не coding-задача. Иначе «создай backend-агента»
	// превращалось в квест с таким названием, а «подбери отряд» — в работу для
	// уже существующего отряда. Эти намерения идут раньше общего work-маркера.
	if isAgentCreationRequest(lower) {
		return "agent"
	}
	if isTeamCreationRequest(lower) {
		return "team"
	}
	// Явная задача сильнее упоминания.
	//
	// Слова сводки и справки — «статус», «состояние», «команды» — обычные слова
	// предметной области, и раньше любое из них перебивало глагол: «Добавь
	// команды в CLI» получало в ответ справку о самом Мастере, «Обнови статус
	// заказа» — отчёт об очереди решений. Задача при этом не терялась молча —
	// она терялась необъяснимо: какое слово помешало, из ответа не следовало
	// никак. Просьба, начатая глаголом, — работа, чего бы она ни касалась.
	if looksLikeWork(message) {
		return "work"
	}
	switch {
	case has("что ты умеешь", "что умеешь", "помощь", "справка", "команды", "как пользоваться"):
		return "help"
	case has("что сейчас", "что происходит", "статус", "состояние", "что ждёт", "что ждет", "чем занят", "расскажи о проекте", "как проект"):
		return "status"
	case has("кто есть", "кто у меня", "ростер", "какие агенты", "список агентов", "кто в отряде"):
		return "roster"
	// Список точных фраз узнавал «кто есть в ростере», но не «сколько есть
	// агентов», «покажи агентов» и даже «агенты?». Мастер на них отвечал общей
	// строкой — той же, что на «привет», — хотя состав он знает и по соседнему
	// чипу выдаёт. Спрашивающий видел не отказ, а неотличимую от приветствия
	// реплику и не мог понять, поняли его или нет.
	//
	// Поэтому: предмет разговора — про персонажей, а форма — вопрос о составе
	// или количестве. Оба условия вместе, иначе «добавь агента» уедет в справку
	// о ростере вместо работы (эту защиту даёт и looksLikeWork выше).
	// «Какой агент нужен под эту задачу?» — вопрос о выборе, а не о составе.
	// Отвечать на него описью ростера значит отвечать не на то: под пустой
	// ростер Мастер обязан предложить, кого нанять под уже названную задачу.
	// Эту ветку ведёт разбор ниже по течению, поэтому здесь её пропускаем.
	case has("агент", "персонаж", "отряд", "ростер") &&
		has("сколько", "покажи", "показать", "список", "перечисл", "какие", "кто", "есть ли", "имеются", "?") &&
		!has("нужен", "нужна", "нужно", "нанять", "взять", "подойдёт", "подойдет", "выбрать", "какой", "какого", "кого"):
		return "roster"
	case has("квест", "задач", "работ", "запуск") && has("сколько", "какие", "покажи", "показать", "список", "идут", "идёт", "идет"):
		return "status"
	case isGreeting(lower):
		return "greeting"
	default:
		return "talk"
	}
}

func isAgentCreationRequest(lower string) bool {
	if strings.Contains(lower, "квест") || (!strings.Contains(lower, "агент") && !strings.Contains(lower, "персонаж")) {
		return false
	}
	for _, marker := range []string{"создай", "создать", "добавь", "добавить", "найми", "нанять", "заведи", "собери", "настрой"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func isTeamCreationRequest(lower string) bool {
	if strings.Contains(lower, "квест") || (!strings.Contains(lower, "отряд") && !strings.Contains(lower, "команд")) {
		return false
	}
	for _, marker := range []string{"создай", "создать", "собери", "собрать", "сформируй", "сформировать", "подбери", "подобрать", "выбери", "выбрать", "назначь", "нужен отряд", "нужна команд"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func isBareQuestCreationRequest(message string) bool {
	plain := strings.Trim(strings.ToLower(strings.TrimSpace(message)), " .,!?:;—-")
	for _, command := range []string{"создай квест", "создать квест", "подготовь квест", "поставь квест"} {
		if plain == command {
			return true
		}
	}
	return false
}

// asksWhichAgent — вопрос о выборе исполнителя: «какой агент нужен», «кого
// нанять». Мастер такой вопрос понимает, и отвечать на него «не понял» —
// неправда; отвечать описью ростера — не на тот вопрос. Верный ответ зависит от
// того, названа ли задача, поэтому решение принимается там, где это известно.
func asksWhichAgent(message string) bool {
	lower := strings.ToLower(message)
	subject := false
	for _, word := range []string{"агент", "персонаж", "исполнител", "кого", "кто"} {
		if strings.Contains(lower, word) {
			subject = true
			break
		}
	}
	if !subject {
		return false
	}
	for _, word := range []string{"нужен", "нужна", "нужно", "нанять", "взять", "подойдёт", "подойдет", "выбрать", "какой", "какого"} {
		if strings.Contains(lower, word) {
			return true
		}
	}
	return false
}

// isGreeting — приветствие и вежливость, на которые уместен приглашающий ответ.
// Всё остальное непонятое — не приветствие, и отвечать на него приглашением
// значит выдавать непонимание за радушие.
func isGreeting(lower string) bool {
	for _, word := range []string{
		"привет", "здравств", "добрый день", "добрый вечер", "доброе утро", "доброй ночи",
		"хай", "ку", "здорово", "hello", "hi", "hey", "как дела", "как ты", "спасибо", "благодарю", "пока", "до связи",
	} {
		if strings.Contains(lower, word) {
			return true
		}
	}
	return false
}

// CapabilityCheck возвращает причины, по которым агент не сможет завершить
// работу. Проверку выполняет тот же код, что показывает готовность в карточке
// агента: Мастер и настройка обязаны судить по одним правилам, иначе отряд
// собирается из тех, кого форма уже признала неработоспособными.
type CapabilityCheck func(agent domain.ProjectAgent) []string

type ChatResponse struct {
	ConversationSummary string   `json:"conversationSummary,omitempty"`
	MemorySuggestions   []string `json:"memorySuggestions,omitempty"`
	Reply               string   `json:"reply"`
	// Mode — кто ответил: "model" или "deterministic". FallbackReason заполнен
	// только когда модель настроена, но ответила не она: молчаливый откат
	// неотличим от исправной работы.
	Mode           string                  `json:"mode"`
	Model          string                  `json:"model,omitempty"`
	FallbackReason string                  `json:"fallbackReason,omitempty"`
	Party          []PartyMember           `json:"party,omitempty"`
	PartyWhy       string                  `json:"partyWhy,omitempty"`
	Proposal       *domain.QuestProposal   `json:"proposal,omitempty"`
	Questions      []string                `json:"questions,omitempty"`
	Clarifications []domain.MasterQuestion `json:"clarifications,omitempty"`
	Facts          []string                `json:"facts,omitempty"`
	// Actions — куда можно уйти прямо отсюда.
	Actions []ChatAction `json:"actions,omitempty"`
	// AgentDraft — уточнение черновика исполнителя от модели. Подбор идёт без
	// неё: ядро соберёт ростер и черновик в любом случае, а здесь модель лишь
	// называет специалиста точнее, чем словарь ролей. Пустое поле не стоит
	// ничего — обязательных шагов на послушности модели больше не держим.
	AgentDraft *AgentDraftProposal `json:"agentDraft,omitempty"`
	// Hire — кого нанять, когда нанимать некого. Пустой ростер не повод
	// отказывать: Мастер называет подходящую роль и объясняет выбор, а найм
	// по-прежнему остаётся решением человека.
	Hire *HireSuggestion `json:"hire,omitempty"`
	// ActionProposal — проверяемый черновик сущности Hub. Мастер может
	// подготовить агента или отряд, но создание происходит только после явной
	// кнопки человека, тем же безопасным маршрутом, что и предложения Компаньона.
	ActionProposal *domain.CompanionActionProposal `json:"actionProposal,omitempty"`
	// Usage — что ход стоил. Колонки под расход в companion_messages были с
	// самого начала, но у Мастера их не заполнял никто: окно «Сведения об
	// ответе» показывало нули там, где должна стоять цена.
	Usage masterTurnUsage `json:"-"`
	// Reasoning и Steps показываются в ленте свёрнутыми блоками: «как я к этому
	// пришёл» и «что я посмотрел в проекте». До этого ход был непрозрачен
	// целиком — полторы минуты одного слова «Думает…».
	Reasoning string                `json:"reasoning,omitempty"`
	Steps     []domain.ChatTurnStep `json:"steps,omitempty"`
}

// AgentDraftProposal — черновик исполнителя словами модели. Всё, чего здесь
// нет, задаёт сервер: идентификатор, чертёж, согласие человека и права.
type AgentDraftProposal struct {
	Name          string   `json:"name"`
	Role          string   `json:"role"`
	Mission       string   `json:"mission"`
	RequiredTools []string `json:"requiredTools,omitempty"`
}

// HireSuggestion — предложение нанять агента под конкретную задачу.
type HireSuggestion struct {
	BlueprintID string   `json:"blueprintId"`
	Name        string   `json:"name"`
	Role        string   `json:"role,omitempty"`
	Why         string   `json:"why"`
	Tools       []string `json:"tools,omitempty"`
	Matched     []string `json:"matched,omitempty"`
}

// blueprintHaystack — весь текст чертежа, по которому его можно узнать.
func blueprintHaystack(blueprint domain.AgentBlueprint) string {
	return strings.Join([]string{
		blueprint.Name, blueprint.RoleDescription, blueprint.Mission,
		strings.Join(blueprint.Goals, " "), strings.Join(blueprint.SkillIDs, " "),
		strings.Join(blueprint.AllowedTools, " "),
	}, " ")
}

// RankHires выстраивает чертежи по близости к задаче. Наблюдатель ростера
// показывает человеку несколько вариантов, а разговор берёт первый: подбор у
// них один, иначе карточка найма и реплика Мастера начинают советовать разное.
func RankHires(message string, blueprints []domain.AgentBlueprint, limit int) []HireSuggestion {
	if len(blueprints) == 0 || limit <= 0 {
		return nil
	}
	type ranked struct {
		suggestion HireSuggestion
		score      int
		order      int
	}
	candidates := make([]ranked, 0, len(blueprints))
	for index, blueprint := range blueprints {
		matched := matchedTokens(message, blueprintHaystack(blueprint))
		why := "подходит как универсальная отправная точка — уточните задачу, и я предложу точнее"
		if len(matched) > 0 {
			why = "совпало с задачей: " + strings.Join(matched, ", ")
		}
		candidates = append(candidates, ranked{
			suggestion: HireSuggestion{
				BlueprintID: blueprint.ID,
				Name:        blueprint.Name,
				Role:        strings.TrimSpace(blueprint.RoleDescription),
				Why:         why,
				Tools:       blueprint.AllowedTools,
				Matched:     matched,
			},
			score: len(matched),
			order: index,
		})
	}
	// Порядок каталога — запасной ключ: при равном совпадении список не должен
	// перетасовываться от хода к ходу, иначе человек видит каждый раз другой
	// «лучший» чертёж без единой причины.
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].order < candidates[j].order
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	result := make([]HireSuggestion, 0, len(candidates))
	for _, candidate := range candidates {
		result = append(result, candidate.suggestion)
	}
	return result
}

// suggestHire подбирает чертёж под задачу. Точного попадания не требуется:
// человеку нужна отправная точка с объяснением, а не безошибочный выбор.
func suggestHire(message string, blueprints []domain.AgentBlueprint) *HireSuggestion {
	ranked := RankHires(message, blueprints, 1)
	if len(ranked) == 0 {
		return nil
	}
	best := ranked[0]
	return &best
}

// matchedTokens — слова задачи, встретившиеся в тексте. Общий случай того же,
// что matchedTerms делает для агента.
func matchedTokens(goal, haystack string) []string {
	inHaystack := map[string]bool{}
	for _, token := range textutil.Tokens(haystack) {
		inHaystack[token] = true
	}
	seen := map[string]bool{}
	matched := make([]string, 0, 4)
	for _, token := range textutil.Tokens(goal) {
		if len(matched) >= 4 {
			break
		}
		if inHaystack[token] && !seen[token] {
			seen[token] = true
			matched = append(matched, token)
		}
	}
	return matched
}

// matchedTerms возвращает слова задачи, встретившиеся в описании агента. Это и
// есть причина выбора на языке пользователя: не «оценка 36», а «совпало:
// тесты, биллинг».
func matchedTerms(agent domain.ProjectAgent, goal string) []string {
	haystack := textutil.Tokens(strings.Join([]string{
		agent.Name, agent.RoleDescription, agent.Mission,
		strings.Join(agent.Goals, " "), strings.Join(agent.SkillIDs, " "),
	}, " "))
	inHaystack := make(map[string]bool, len(haystack))
	for _, token := range haystack {
		inHaystack[token] = true
	}
	seen := map[string]bool{}
	matched := make([]string, 0, 4)
	for _, token := range textutil.Tokens(goal) {
		if len(matched) >= 4 {
			break
		}
		if inHaystack[token] && !seen[token] {
			seen[token] = true
			matched = append(matched, token)
		}
	}
	return matched
}

// chatPartyWhy — почему отряд такой. В разговоре это не то же, что при запуске.
//
// AssignParty — реализация запасного пути, и при настроенной модели она честно
// помечает себя «модель недоступна, детерминированный выбор»: там, откуда её
// зовут при старте квеста, планировщик действительно пробовал и не смог.
// В разговоре не пробовал никто. Отряд здесь предварительный — его показывает
// движок Point, — а при запуске состав пересоберёт модель, и он может выйти
// другим. Чужая формулировка сообщала бы о поломке там, где всё исправно, и
// молчала бы о пересборке, из-за которой запущенный отряд не совпадёт с
// показанным. Строка эта не только на экране: она же уходит в предложение
// квеста и оттуда — в очередь решений.
func chatPartyWhy(cfg domain.OrchestratorConfig, assignment Assignment) string {
	if !UsesModelPlanner(cfg) {
		return assignment.Reason
	}
	return fmt.Sprintf("пресет %s · отряд %d · предварительно, движком Point · при запуске состав пересоберёт модель %s",
		cfg.Preset, len(assignment.AgentIDs), cfg.Model)
}

// pendingOwnProposal — предложение, которое Мастер сделал в этом разговоре и
// которое всё ещё ждёт решения.
//
// Ищется по своей же переписке, а не по всей очереди: `proposalId` стоит на той
// реплике, что его создала, поэтому чужие предложения — компаньона — Мастеру не
// припишутся. Дойдя до последнего названного предложения, поиск останавливается:
// если человек его уже решил, звать назад к более старым — навязчивость.
//
// Отказ хранилища здесь не ошибка хода: разговор продолжается, просто без
// напоминания. Соврать «ничего не ждёт» он при этом не может — эта функция
// отвечает только на вопрос «о чём напомнить», а не «что в очереди».
func (s ChatService) pendingOwnProposal(ctx context.Context, workspaceID string) *domain.QuestProposal {
	history, err := s.History(ctx, workspaceID, 20)
	if err != nil || len(history) == 0 {
		return nil
	}
	proposals, err := s.Store.ListQuestProposals(ctx, workspaceID)
	if err != nil {
		return nil
	}
	byID := make(map[string]domain.QuestProposal, len(proposals))
	for _, proposal := range proposals {
		byID[proposal.ID] = proposal
	}
	for index := len(history) - 1; index >= 0; index-- {
		id := strings.TrimSpace(history[index].ProposalID)
		if id == "" {
			continue
		}
		proposal, ok := byID[id]
		if ok && (proposal.Status == "pending" || proposal.Status == "modified") {
			return &proposal
		}
		return nil
	}
	return nil
}

// pendingOwnActionProposal находит последний ещё не решённый черновик Hub,
// который Мастер приложил к своей реплике. Повтор «создай агента» не должен
// плодить одинаковые карточки в очереди решений.
func (s ChatService) pendingOwnActionProposal(ctx context.Context, workspaceID string, kind domain.CompanionActionKind) *domain.CompanionActionProposal {
	history, err := s.History(ctx, workspaceID, 20)
	if err != nil || len(history) == 0 {
		return nil
	}
	proposals, err := s.Store.ListCompanionActionProposals(ctx, workspaceID)
	if err != nil {
		return nil
	}
	byID := make(map[string]domain.CompanionActionProposal, len(proposals))
	for _, proposal := range proposals {
		byID[proposal.ID] = proposal
	}
	for index := len(history) - 1; index >= 0; index-- {
		id := strings.TrimSpace(history[index].ActionProposalID)
		if id == "" {
			continue
		}
		proposal, ok := byID[id]
		if ok && proposal.Kind == kind && (proposal.Status == "pending" || proposal.Status == "modified") {
			return &proposal
		}
		// Последнее действие уже решено или было другого рода. Более старое не
		// возвращаем: это снова подняло бы карточку, от которой человек ушёл.
		return nil
	}
	return nil
}

func uniqueAgentName(base string, agents []domain.ProjectAgent) string {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "Новый агент"
	}
	used := make(map[string]bool, len(agents))
	for _, agent := range agents {
		used[strings.ToLower(strings.TrimSpace(agent.Name))] = true
	}
	if !used[strings.ToLower(base)] {
		return base
	}
	for suffix := 2; suffix < 1000; suffix++ {
		candidate := fmt.Sprintf("%s %d", base, suffix)
		if !used[strings.ToLower(candidate)] {
			return candidate
		}
	}
	return base + " · новый"
}

func (s ChatService) prepareAgentAction(ctx context.Context, req ChatRequest, agents []domain.ProjectAgent, goal, continuationPrompt, continuationLabel string) (*domain.CompanionActionProposal, *HireSuggestion, error) {
	if pending := s.pendingOwnActionProposal(ctx, req.WorkspaceID, domain.CompanionActionCreateAgent); pending != nil {
		if pending.Agent != nil {
			// Прямой черновик агента мог уже ждать решения, когда человек затем
			// назвал работу. Привязываем первую такую задачу к существующей
			// карточке вместо второго черновика и не теряем её после Apply.
			if pending.ContinuationPrompt == "" && strings.TrimSpace(continuationPrompt) != "" {
				pending.ContinuationPrompt = strings.TrimSpace(continuationPrompt)
				pending.ContinuationLabel = strings.TrimSpace(continuationLabel)
				pending.UpdatedAt = s.now()
				if err := s.Store.SaveCompanionActionProposal(ctx, *pending); err != nil {
					return nil, nil, err
				}
			}
			return pending, nil, nil
		}
	}
	blueprints, err := s.Store.ListBlueprints(ctx)
	if err != nil {
		return nil, nil, err
	}
	hire := suggestHire(goal, blueprints)
	if hire == nil {
		return nil, nil, nil
	}
	var blueprint *domain.AgentBlueprint
	for index := range blueprints {
		if blueprints[index].ID == hire.BlueprintID {
			blueprint = &blueprints[index]
			break
		}
	}
	if blueprint == nil {
		return nil, nil, errors.New("чертёж предложенного агента не найден")
	}
	draft := domain.ProjectAgentFromBlueprint(req.WorkspaceID, *blueprint)
	draft.Name = uniqueAgentName(draft.Name, agents)
	now := s.now()
	proposal := domain.CompanionActionProposal{
		ID: s.newID("hubaction"), WorkspaceID: req.WorkspaceID,
		Kind: domain.CompanionActionCreateAgent, Title: "Создать агента · " + draft.Name,
		Rationale: hire.Why, Agent: &draft, Status: "pending",
		ContinuationPrompt: strings.TrimSpace(continuationPrompt), ContinuationLabel: strings.TrimSpace(continuationLabel),
		CreatedAt: now, UpdatedAt: now,
	}
	if err = s.Store.SaveCompanionActionProposal(ctx, proposal); err != nil {
		return nil, nil, err
	}
	return &proposal, hire, nil
}

func boundedTeamName(goal string) string {
	name := "Отряд · " + questTitle(goal)
	runes := []rune(name)
	if len(runes) > 88 {
		return strings.TrimSpace(string(runes[:87])) + "…"
	}
	return name
}

func (s ChatService) prepareTeamAction(ctx context.Context, req ChatRequest, agents []domain.ProjectAgent) (*domain.CompanionActionProposal, []PartyMember, string, error) {
	if pending := s.pendingOwnActionProposal(ctx, req.WorkspaceID, domain.CompanionActionCreateTeam); pending != nil {
		if pending.Team != nil {
			members := s.partyMembers(agents, pending.Team.AgentIDs, req.Message)
			return pending, members, pending.Rationale, nil
		}
	}
	assignment := s.assignParty(ctx, req, agents, nil)
	if len(assignment.AgentIDs) == 0 {
		return nil, nil, "", nil
	}
	members := s.partyMembers(agents, assignment.AgentIDs, req.Message)
	why := chatPartyWhy(req.Config, assignment)
	now := s.now()
	team := domain.Team{
		ID: s.newID("team"), WorkspaceID: req.WorkspaceID, Name: boundedTeamName(req.Message),
		Description: why, AgentIDs: append([]string(nil), assignment.AgentIDs...), CreatedAt: now, UpdatedAt: now,
	}
	proposal := domain.CompanionActionProposal{
		ID: s.newID("hubaction"), WorkspaceID: req.WorkspaceID,
		Kind: domain.CompanionActionCreateTeam, Title: "Создать отряд · " + team.Name,
		Rationale: why, Team: &team, Status: "pending", CreatedAt: now, UpdatedAt: now,
	}
	if err := s.Store.SaveCompanionActionProposal(ctx, proposal); err != nil {
		return nil, nil, "", err
	}
	return &proposal, members, why, nil
}

func (s ChatService) partyMembers(agents []domain.ProjectAgent, ids []string, goal string) []PartyMember {
	byID := make(map[string]domain.ProjectAgent, len(agents))
	for _, agent := range agents {
		byID[agent.ID] = agent
	}
	members := make([]PartyMember, 0, len(ids))
	for _, id := range ids {
		agent, ok := byID[id]
		if !ok {
			continue
		}
		score := scoreAgentForGoal(agent, goal)
		members = append(members, PartyMember{
			AgentID: agent.ID, Name: agent.Name, Role: strings.TrimSpace(agent.RoleDescription),
			Score: &score, Matched: matchedTerms(agent, goal), Blocking: s.blockersFor(agent),
		})
	}
	return members
}

// unansweredTask — задача, к которой Мастер обещал вернуться.
//
// Пустой ростер и неработоспособный отряд — два случая, когда он отвечает
// «сделайте вот это, и я предложу квест»: «наймите его, и я сразу предложу
// квест», «поправьте их настройку, и я предложу квест». Человек уходит, делает,
// что просили, и возвращается со словом, которое само по себе не задача:
// «нанял», «поправил», «готово». Просить описать задачу заново — значит не
// сдержать обещание, данное двумя репликами выше, где эта задача и записана.
//
// Берём её оттуда же. Признак того, что обещание осталось невыполненным, —
// прошлый ответ Мастера не создал предложения, а вопрос перед ним был задачей.
// Условие срабатывает только когда препятствие уже снято: пустой ростер
// перехватывается раньше, до разбора намерения, а неработоспособный отряд
// проверяется заново на общем пути.
func (s ChatService) unansweredTask(ctx context.Context, workspaceID string) string {
	history, err := s.History(ctx, workspaceID, 12)
	if err != nil {
		return ""
	}
	// Идём назад от реплики перед текущей: она уже сохранена и рассматривать её
	// саму незачем. Ищем не строго предыдущий ход, а последнюю задачу, которая
	// так и не получила предложения: между ней и сегодняшним днём человек мог
	// спросить что-то ещё — «какой агент нужен», «спасибо», — и обещание от
	// этого не перестаёт быть невыполненным.
	for index := len(history) - 2; index >= 0; index-- {
		message := history[index]
		// Дошли до хода, который предложением закончился: всё, что было до него,
		// уже отвечено, и звать назад некуда.
		if message.Role == "assistant" && strings.TrimSpace(message.ProposalID) != "" {
			return ""
		}
		if message.Role == "user" && looksLikeWork(message.Content) {
			return strings.TrimSpace(message.Content)
		}
	}
	return ""
}

// clarifyingQuestions — то, чего Мастеру не хватает, чтобы отряд был осмысленным.
// Спрашивать всегда — навязчиво; не спрашивать никогда — значит собирать отряд
// наугад и списывать промах на пользователя.
func clarifyingQuestions(message string, party []domain.ProjectAgent) []string {
	questions := make([]string, 0, 2)
	if len([]rune(strings.TrimSpace(message))) < 24 {
		questions = append(questions, "Что считать готовым результатом?")
	}
	if len(party) == 0 {
		questions = append(questions, "Кто из ростера ближе всего к этой задаче?")
	}
	return questions
}

func (s ChatService) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}

func (s ChatService) newID(prefix string) string {
	if s.NewID != nil {
		return s.NewID(prefix)
	}
	return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
}

// looksLikeWork отличает задачу от вопроса. Мастер предлагает квест только на
// задачу: на «что сейчас происходит» отвечать созданием работы — грубость,
// которая быстро приучает не разговаривать с диспетчером вовсе.
func looksLikeWork(message string) bool {
	lower := strings.ToLower(message)
	if strings.HasSuffix(strings.TrimSpace(lower), "?") {
		return false
	}
	for _, marker := range []string{
		"сделай", "почини", "исправь", "добавь", "убери", "удали", "перепиши",
		"разбери", "собери", "напиши", "обнови", "прогони", "проверь", "запусти",
		"реализуй", "внедри", "оптимизируй", "отрефактори", "покрой",
		// «создай» отсутствовало, и «создай отряд под миграции» уходило в общий
		// ответ — при том что «собери» рядом работало. Такие пропуски человек
		// объяснить не может: одна просьба стала квестом, соседняя нет.
		"создай", "настрой", "перенеси", "замени", "вынеси", "ускорь", "почисти",
		"дополни", "доработай", "поправь", "верни", "отключи", "включи",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	// Задача, написанная по-английски, — всё ещё задача.
	//
	// «fix flaky billing test» не содержит ни одного русского глагола и уходило
	// в общий ответ «опишите задачу»: человек её описал, а Мастер просил описать.
	// Терялась она при этом необъяснимо — про язык в ответе не было ни слова,
	// ровно как когда-то про слово «статус».
	//
	// Сверяем по целым словам, а не по вхождению: «fix» внутри «prefix» — не
	// просьба чинить, и подстрочный поиск повторил бы ту же ловушку.
	for _, token := range textutil.Tokens(lower) {
		if englishWorkVerbs[token] {
			return true
		}
	}
	return false
}

// Список короткий и намеренно однозначный: только повелительные глаголы, которые
// в разговоре о коде не бывают существительным. «test» и «check» сюда не входят
// по этой самой причине.
var englishWorkVerbs = map[string]bool{
	"fix": true, "add": true, "remove": true, "delete": true, "update": true,
	"refactor": true, "implement": true, "rewrite": true, "migrate": true,
	"rename": true, "optimize": true, "upgrade": true, "bump": true, "revert": true,
}

// questTitle делает из фразы название квеста: первая строка, без хвостовой
// пунктуации, с заглавной буквы и в разумной длине.
//
// Длинное режется по слову и с многоточием. Обрубок посреди слова читается как
// законченное название — а название живёт дольше разговора: оно уходит в
// очередь решений, в сам квест и в хронику, где рядом уже нет исходной задачи,
// по которой можно было бы догадаться, что конец потерян.
func questTitle(message string) string {
	title := strings.TrimSpace(strings.SplitN(message, "\n", 2)[0])
	lower := strings.ToLower(title)
	// Быстрый старт уже подставляет разговорную команду. В названии квеста
	// остаётся сама работа, а не интерфейсное «Создай квест: …».
	for _, prefix := range []string{"создай квест", "создать квест", "подготовь квест", "поставь квест"} {
		if !strings.HasPrefix(lower, prefix) {
			continue
		}
		if rest := strings.TrimSpace(strings.TrimLeft(title[len(prefix):], ":—- ")); rest != "" {
			if strings.HasPrefix(strings.ToLower(rest), "на ") {
				rest = strings.TrimSpace(rest[len("на "):])
			}
			title = rest
		}
		break
	}
	// Вторая фраза остаётся в задаче и целях, но не превращает название в
	// абзац. Это особенно важно для естественного «создай квест на X. Затем Y».
	titleRunes := []rune(title)
	for index, char := range titleRunes {
		if (char == '.' || char == '!' || char == '?') && index+1 < len(titleRunes) && unicode.IsSpace(titleRunes[index+1]) {
			title = string(titleRunes[:index])
			break
		}
	}
	title = strings.TrimRight(title, ".!,; ")
	if title == "" {
		return "Задача без названия"
	}
	runes := []rune(title)
	if len(runes) > maxQuestTitle {
		cut := runes[:maxQuestTitle]
		// Ищем последний пробел в отрезанном. Не нашли во второй половине —
		// значит слово длиннее полуназвания, и резать по нему нечего.
		for index := len(cut) - 1; index >= maxQuestTitle/2; index-- {
			if unicode.IsSpace(cut[index]) {
				cut = cut[:index]
				break
			}
		}
		title = strings.TrimRight(strings.TrimSpace(string(cut)), ".,;:!-— ") + "…"
		runes = []rune(title)
	}
	return strings.ToUpper(string(runes[0])) + string(runes[1:])
}

// NormalizeQuestTitle даёт всем путям создания квеста то же короткое имя,
// которое использует Мастер. Сырые предложения модели не должны обходить
// правила интерфейса при фактическом старте квеста.
func NormalizeQuestTitle(message string) string { return questTitle(message) }

// objectivesFor — что предстоит сделать, по словам самого человека.
//
// Раньше в цели попадал перечень отряда: строки вида «ИМЯ: роль агента». Это не
// работа, а состав, и он же показан карточкой рядом — цели дублировали соседа и
// не говорили о задаче ничего.
//
// Хуже другое: подробности задачи не доходили до квеста вовсе. Названием
// становится первая строка, описанием квеста — объяснение подбора отряда, и всё
// написанное дальше оставалось только репликой в переписке. Агент, который
// потом берётся за работу, не видел ни частоты падения, ни подозрений, ни
// требования воспроизвести локально — один заголовок. Теперь эти строки и есть
// цели: человек уже написал, что нужно сделать.
func objectivesFor(message string) []string {
	objectives := []string{"Разобраться в текущем состоянии и воспроизвести задачу"}
	// Первая строка стала названием, поэтому берём написанное после неё.
	for index, line := range strings.Split(message, "\n") {
		if index == 0 || len(objectives) >= maxObjectives-1 {
			continue
		}
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			objectives = append(objectives, trimmed)
		}
	}
	return append(objectives, "Подтвердить результат проверкой, а не словами")
}

// situationOf — снимок мира и признак того, что он получен.
//
// Второе значение важнее, чем кажется. Отсутствие поставщика или отказ хранилища
// возвращались теми же нулями, что и спокойный проект, и «Ничего не ждёт вашего
// решения» Мастер говорил одинаково уверенно — и когда проверил, и когда не смог.
// Хуже того, очередь в шапке приходит другим запросом: он мог и уцелеть, и тогда
// человек читал «3 ЖДУТ РЕШЕНИЯ» рядом с обещанием тишины.
func (s ChatService) situationOf(ctx context.Context, workspaceID string) (Situation, bool) {
	if s.Situation == nil {
		return Situation{}, false
	}
	snapshot, err := s.Situation(ctx, workspaceID)
	if err != nil {
		return Situation{}, false
	}
	return snapshot, true
}

// statusReply — что происходит прямо сейчас, числами, а не настроением.
func (s ChatService) statusReply(snapshot Situation, known bool, agents, active int) string {
	lines := make([]string, 0, 5)
	switch {
	// Незнание называется незнанием. Остальные числа ниже посчитаны отдельно и
	// от этого снимка не зависят — их Мастер приводит по-прежнему.
	case !known:
		lines = append(lines, "Свести очередь решений и наборы изменений сейчас не удалось — этих чисел у меня нет.")
	case snapshot.WaitingDecisions > 0:
		lines = append(lines, fmt.Sprintf("Ждут вашего решения: %d. Это первое, что стоит разобрать.", snapshot.WaitingDecisions))
	default:
		lines = append(lines, "Ничего не ждёт вашего решения.")
	}
	if snapshot.RunningExecutions > 0 {
		lines = append(lines, fmt.Sprintf("Сейчас выполняется: %d.", snapshot.RunningExecutions))
	}
	if snapshot.PendingChangeSets > 0 {
		lines = append(lines, fmt.Sprintf("Наборов изменений на ревью: %d.", snapshot.PendingChangeSets))
	}
	lines = append(lines, fmt.Sprintf("Активных квестов: %d. В ростере %s.", active, textutil.Count(agents, "агент", "агента", "агентов")))
	return strings.Join(lines, "\n")
}

// rosterReply перечисляет отряд и честно говорит, кто к работе не готов.
func (s ChatService) rosterReply(agents []domain.ProjectAgent) string {
	if len(agents) == 0 {
		return "Ростер пуст — нанимать пока некого."
	}
	ready := 0
	for _, agent := range agents {
		if len(s.blockersFor(agent)) == 0 {
			ready++
		}
	}
	names := make([]string, 0, len(agents))
	for _, agent := range agents {
		names = append(names, agent.Name)
	}
	return fmt.Sprintf("В ростере %s: %s. Готовы к квесту: %d из %d — причины неготовности в карточках.",
		textutil.Count(len(agents), "агент", "агента", "агентов"), strings.Join(names, ", "), ready, len(agents))
}

// rosterMembers показывает тот же состав в структурном виде, чтобы интерфейс
// нарисовал причины неготовности рядом с именами.
func (s ChatService) rosterMembers(agents []domain.ProjectAgent) []PartyMember {
	members := make([]PartyMember, 0, len(agents))
	for _, agent := range agents {
		members = append(members, PartyMember{
			AllowedTools: append([]string(nil), agent.AllowedTools...),
			AgentID:      agent.ID,
			Name:         agent.Name,
			Role:         strings.TrimSpace(agent.RoleDescription),
			Blocking:     s.blockersFor(agent),
		})
	}
	return members
}

// actionsFor — куда уйти прямо из разговора. Показываем только то, что сейчас
// имеет смысл: кнопка в пустоту хуже её отсутствия.
func (s ChatService) actionsFor(snapshot Situation) []ChatAction {
	// Неполученный снимок — нули, а нули не порождают кнопок: звать разбирать
	// очередь, о размере которой мы не знаем, — та же выдумка, только кликабельная.
	actions := make([]ChatAction, 0, 3)
	if snapshot.WaitingDecisions > 0 {
		actions = append(actions, ChatAction{
			Label: fmt.Sprintf("Разобрать очередь (%d)", snapshot.WaitingDecisions),
			Tab:   "decisions",
			Hint:  "решения, без которых работа стоит",
		})
	}
	if snapshot.PendingChangeSets > 0 {
		actions = append(actions, ChatAction{
			Label: fmt.Sprintf("Ревью изменений (%d)", snapshot.PendingChangeSets),
			Tab:   "changesets",
		})
	}
	if snapshot.ActiveQuests > 0 {
		actions = append(actions, ChatAction{Label: "Открыть квесты", Tab: "quests"})
	}
	return actions
}

// Chat — один ход разговора с Мастером.
func (s ChatService) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	if req.WorkMode == "discuss" {
		return s.DiscussTask(ctx, req)
	}
	if req.TaskIntake {
		// Hub entity commands retain their existing reviewed creation path.
		// This routing never selects precise/project work modes.
		entityIntent := intentOf(req.Message)
		if req.ProposalID != "" || (entityIntent != "agent" && entityIntent != "team") {
			return s.DiscussTask(ctx, req)
		}
	}
	req.Message = strings.TrimSpace(req.Message)
	if req.Message == "" {
		return ChatResponse{}, errors.New("сообщение мастеру не может быть пустым")
	}
	if len(req.Message) > maxChatMessage {
		return ChatResponse{}, errors.New("сообщение мастеру длиннее 32 КиБ")
	}

	agents, err := s.Store.ListProjectAgents(ctx, req.WorkspaceID)
	if err != nil {
		return ChatResponse{}, err
	}
	quests, err := s.Store.ListQuests(ctx, req.WorkspaceID)
	if err != nil {
		return ChatResponse{}, err
	}

	active := 0
	for _, quest := range quests {
		if quest.Status == "active" || quest.Status == "running" {
			active++
		}
	}
	// «Размер отряда» — это предел из политики, а не состав, который вышел.
	//
	// Числом он спорил сам с собой в одной строке: «агентов в ростере: 2 · размер
	// отряда: 3» — отряда из трёх при ростере из двух не бывает. И с экраном:
	// рядом стоял состав из двух. Подбор берёт из политики верхнюю границу и
	// упирается в то, сколько агентов есть, — так это и называется.
	facts := []string{
		fmt.Sprintf("агентов в ростере: %d", len(agents)),
		fmt.Sprintf("активных квестов: %d", active),
		fmt.Sprintf("отряд по политике: до %d", PartySize(req.Config)),
	}

	response := ChatResponse{Mode: "deterministic", Facts: facts}

	// Снимок мира берётся один раз за ход и переиспользуется. Два снимка берутся
	// в разные мгновения, и один ответ мог сказать «ждут решения: 3» и подписать
	// кнопку «разобрать очередь (2)». Модельная ветка спрашивает его для
	// обоснования, детерминированная — для сводки и кнопок; спрашивать дважды
	// незачем и вредно.
	var (
		cachedSituation Situation
		cachedKnown     bool
		situationTaken  bool
	)
	situation := func() (Situation, bool) {
		if !situationTaken {
			cachedSituation, cachedKnown = s.situationOf(ctx, req.WorkspaceID)
			situationTaken = true
		}
		return cachedSituation, cachedKnown
	}

	if err = s.persist(ctx, req.WorkspaceID, "user", req.Message, "", req.Config); err != nil {
		return ChatResponse{}, err
	}

	intent := intentOf(req.Message)
	if intent == "work" && isBareQuestCreationRequest(req.Message) {
		response.Reply = "Какую задачу превратить в квест? Опишите результат и, если важно, ограничения — я подготовлю карточку и подберу отряд."
		return response, s.persistReply(ctx, req, response, "")
	}
	// Do not spend a planner request when the hard readiness filter has already
	// proved that nobody can execute the task. Show the rejected candidates and
	// their exact blockers so the user can repair the roster.
	if intent == "work" && len(agents) > 0 && len(s.runnableAgents(agents)) == 0 {
		response.Party = s.rosterMembers(agents)
		response.Reply = "Ни один из подходящих агентов не сможет довести работу до конца — поправьте их настройку, и я предложу квест."
		response.Actions = []ChatAction{{Label: "Открыть гильдию", Tab: "agents", Hint: "поправить настройку агента — там же, где его создавали"}}
		return response, s.persistReply(ctx, req, response, "")
	}

	// Разговор ведёт модель Мастера; работа остаётся за детерминированным путём.
	//
	// Предложение квеста собирает отряд по проверке годности и кладёт его в
	// очередь решений — это права, а не беседа, и отдавать их модели незачем.
	// Всё остальное — вопросы, объяснения, уточнения — как раз то, где список
	// ключевых слов проигрывал живой речи.
	// Модель Мастера участвует в любом ходе: в разговоре она отвечает сама, в
	// работе — формулирует квест и называет, кого бы взяла. Решает при этом не
	// она: состав идёт через AssignParty, годность считает тот же код, что и
	// карточка агента, а запуск остаётся за человеком.
	var modelSaid *masterEnvelope
	if UsesModelPlanner(req.Config) && intent != "agent" && intent != "team" {
		// Fetch enough turns to summarize the early conversation while keeping a
		// small verbatim tail for pronouns and immediate follow-ups.
		history, historyErr := s.Store.ListChatMessages(ctx, req.WorkspaceID, "master", 48)
		if historyErr != nil {
			return ChatResponse{}, historyErr
		}
		blockers := make(map[string][]string, len(agents))
		for _, agent := range agents {
			blockers[agent.ID] = s.blockersFor(agent)
		}
		snapshot, _ := situation()
		// Предложения нужны модели, чтобы помнить своё же предложение: «а можно
		// без него?» задают сразу после него, и отвечать на это счётчиком
		// «ждут решения: 1» — не разговор.
		proposals, proposalsErr := s.Store.ListQuestProposals(ctx, req.WorkspaceID)
		if proposalsErr != nil {
			return ChatResponse{}, proposalsErr
		}
		world := masterWorldPrompt(agents, blockers, quests, proposals, active, snapshot)
		envelope, modelErr := s.chatWithModel(ctx, req, world, history)
		if modelErr == nil {
			modelSaid = &envelope
			response.Mode = "model"
			response.Model = req.Config.Model
			// Разговор модель заканчивает сама. Работу заканчивает движок:
			// предложение квеста — это запись в очереди решений, и собирать её
			// должен проверенный путь.
			if intent != "work" {
				response.Reply = envelope.Reply
				response.Questions = envelope.Questions
				response.Actions = s.actionsFor(snapshot)
				return response, s.persistReply(ctx, req, response, "")
			}
		}
		// Молчаливый откат неотличим от исправной работы: человек настроил
		// модель и вправе знать, что отвечала не она. Успех сюда доходит только
		// на задаче — её заканчивает движок, и откатываться не с чего.
		if modelErr != nil {
			response.FallbackReason = modelErr.Error()
		}
	}

	// Создание агента и отряда — отдельные проверяемые действия Hub. Мастер
	// готовит карточку, а человек применяет, правит или отклоняет её прямо в
	// разговоре. Coding-квест из фразы «создай агента» здесь не появляется.
	if intent == "agent" {
		action, hire, actionErr := s.prepareAgentAction(ctx, req, agents, req.Message, "", "")
		if actionErr != nil {
			return ChatResponse{}, actionErr
		}
		response.ActionProposal = action
		response.Hire = hire
		if action == nil {
			response.Reply = "Готовых чертежей агента пока нет. Откройте Гильдию и создайте первый чертёж — после этого я подготовлю карточку по одной просьбе."
			response.Actions = []ChatAction{{Label: "Открыть гильдию", Tab: "agents"}}
		} else if action.Status == "modified" || hire == nil {
			response.Reply = fmt.Sprintf("Черновик «%s» уже ждёт вашего решения. Его можно создать, изменить или отклонить прямо здесь.", action.Agent.Name)
		} else {
			response.Reply = fmt.Sprintf("Подготовил агента «%s» на основе подходящего чертежа. Проверьте роль и модель, затем создайте или измените карточку.", action.Agent.Name)
		}
		return response, s.persistReply(ctx, req, response, "")
	}

	if intent == "team" {
		if len(agents) == 0 {
			action, hire, actionErr := s.prepareAgentAction(ctx, req, agents, req.Message, req.Message, "ПРОДОЛЖИТЬ ПОДБОР ОТРЯДА")
			if actionErr != nil {
				return ChatResponse{}, actionErr
			}
			response.ActionProposal, response.Hire = action, hire
			if action == nil {
				response.Reply = "Собирать отряд пока не из кого, и готовых чертежей нет. Сначала создайте агента в Гильдии."
				response.Actions = []ChatAction{{Label: "Открыть гильдию", Tab: "agents"}}
			} else {
				response.Reply = fmt.Sprintf("Собирать отряд пока не из кого. Подготовил первого агента «%s» — создайте его, затем повторите подбор.", action.Agent.Name)
			}
			return response, s.persistReply(ctx, req, response, "")
		}
		action, members, why, actionErr := s.prepareTeamAction(ctx, req, agents)
		if actionErr != nil {
			return ChatResponse{}, actionErr
		}
		response.ActionProposal, response.Party, response.PartyWhy = action, members, why
		if action == nil {
			response.Reply = "Не смог подобрать ни одного исполнителя. Уточните задачу или проверьте готовность агентов в Гильдии."
			response.Actions = []ChatAction{{Label: "Открыть гильдию", Tab: "agents"}}
		} else {
			response.Reply = fmt.Sprintf("Подобрал %s. Состав можно изменить перед созданием отряда — решение остаётся за вами.", textutil.Count(len(members), "исполнитель", "исполнителя", "исполнителей"))
		}
		return response, s.persistReply(ctx, req, response, "")
	}

	// Пустой ростер — не тупик, но и не единственная тема разговора.
	//
	// Нанимать некого — значит первый шаг работы это найм, и Мастер обязан его
	// предложить, а не отправить человека разбираться самому. Но предлагал он это
	// на всё подряд: ветка стояла до разбора намерения, и «что ты умеешь», «что
	// сейчас», «кто есть» и даже «привет» получали одно и то же предложение
	// нанять. Пока ростер пуст, Мастер переставал уметь что-либо ещё.
	//
	// Хуже того, чертёж подбирался по тексту самого сообщения: на «привет» он
	// выбирался по слову «привет», а ответ уверял, что подобран «под то, что вы
	// описали», — хотя не описали ничего.
	//
	// Найм предлагается там, где он и есть ответ: на задачу. Про справку, сводку
	// и состав Мастер рассказывает по-прежнему — ростер для этого не нужен.
	if len(agents) == 0 && intent == "work" {
		action, hire, actionErr := s.prepareAgentAction(ctx, req, agents, req.Message, req.Message, "ПРОДОЛЖИТЬ ЗАДАЧУ")
		if actionErr != nil {
			return ChatResponse{}, actionErr
		}
		response.ActionProposal, response.Hire = action, hire
		if action == nil {
			response.Reply = "В ростере пусто, и готовых чертежей тоже нет — соберите первого агента вручную в Гильдии, и я подключусь."
			response.Actions = []ChatAction{{Label: "Открыть гильдию", Tab: "agents"}}
		} else {
			// Подсказок здесь нет: нанимают кнопкой на карточке, а отправленная
			// Мастеру строка «Нанять „X"?» ничего не нанимает.
			response.Reply = fmt.Sprintf(
				"Задачу понял, но выполнять её пока некому. Под неё подходит «%s» — %s. Наймите его, и я сразу предложу квест.",
				action.Agent.Name, action.Rationale)
		}
		return response, s.persistReply(ctx, req, response, "")
	}

	// Задача, к которой вернулись по обещанию Мастера. Пустая строка означает,
	// что человек описал работу сам, здесь и сейчас.
	resumed := ""

	// Разговор — не только «задача или не задача».
	//
	// Раньше всё, что не похоже на задачу, получало одну и ту же строку про
	// размер ростера. Диспетчер, который умеет ровно одно, и выглядит как
	// умеющий ровно одно: спросить у него о состоянии дел было нельзя.
	switch intent {
	// Снимок мира берётся один раз на ход.
	//
	// Сводка и кнопки перехода спрашивали его каждая для себя, а снимок — это
	// вся очередь решений целиком: наборы, прогоны, узлы Flow, предложения.
	// Двойная работа здесь не главное: два снимка берутся в разные мгновения, и
	// один ответ мог сказать «ждут решения: 3» и подписать кнопку «разобрать
	// очередь (2)». Числа в одном ответе обязаны сходиться.
	case "help":
		snapshot, _ := situation()
		response.Reply = strings.Join([]string{
			"Я диспетчер: со мной можно говорить о проекте обычными словами.",
			"Спросите «что сейчас» — расскажу, что ждёт решения и что выполняется.",
			"Спросите «кто есть» — перечислю отряд и кто из них готов к работе.",
			"Скажите «создай агента» или «подбери отряд» — подготовлю редактируемую карточку и подожду подтверждения.",
			"Скажите «создай квест» или просто опишите работу — подберу отряд и предложу квест.",
			"Квест не стартует сам: запуск всегда за вами.",
		}, "\n")
		response.Actions = s.actionsFor(snapshot)
		return response, s.persistReply(ctx, req, response, "")

	case "status":
		snapshot, known := situation()
		response.Reply = s.statusReply(snapshot, known, len(agents), active)
		response.Actions = s.actionsFor(snapshot)
		return response, s.persistReply(ctx, req, response, "")

	case "roster":
		response.Reply = s.rosterReply(agents)
		response.Party = s.rosterMembers(agents)
		response.Actions = []ChatAction{{Label: "Открыть гильдию", Tab: "agents"}}
		return response, s.persistReply(ctx, req, response, "")

	case "greeting", "talk":
		snapshot, _ := situation()
		// Реплика, которая не команда и не вопрос, чаще всего продолжает
		// предыдущий ход. Пока ответ на неё был один на все случаи, «давай»,
		// «ок» и «а можно без него?» сразу после предложенного квеста получали
		// приглашение описать задачу — разговор обрывался ровно там, где человек
		// отвечал Мастеру. Предложение при этом никуда не девалось: оно висело
		// в очереди решений и в переписке выше, но в ответе о нём не было ни слова.
		if waiting := s.pendingOwnProposal(ctx, req.WorkspaceID); waiting != nil {
			response.Reply = fmt.Sprintf(
				"Предложение «%s» ещё ждёт вашего решения — запустить или отклонить его можно прямо в переписке выше. Если задача другая, опишите её, и я соберу отряд под неё.",
				waiting.Title)
			// Подсказки здесь нет намеренно.
			//
			// Чип с вопросом подставляется в поле ввода человека и уходит в
			// следующей реплике — то есть Мастер получает собственный вопрос и
			// отвечает на него тем же текстом. «Что поменять в предложении?»
			// вдобавок обещало то, чего Мастер не умеет: править предложение из
			// разговора нечем, его правят карточкой. Вопрос, на который нельзя
			// ответить, хуже отсутствия вопроса.
			response.Actions = s.actionsFor(snapshot)
			return response, s.persistReply(ctx, req, response, "")
		}
		// Обещание, которое надо сдержать: препятствие снято, задача известна —
		// возвращаемся к ней, а не просим описать её заново.
		if task := s.unansweredTask(ctx, req.WorkspaceID); task != "" && len(agents) > 0 {
			resumed, req.Message = task, task
			break
		}
		if len(agents) == 0 {
			// Ростер пуст, а задача уже названа: разговор идёт о ней, и на
			// «какой агент нужен» отвечать надо ролью под ту самую задачу.
			// Подбирать под текст вопроса — как раз то, из-за чего на «привет»
			// приходил чертёж, выбранный по слову «привет».
			if task := s.unansweredTask(ctx, req.WorkspaceID); task != "" {
				blueprints, bpErr := s.Store.ListBlueprints(ctx)
				if bpErr != nil {
					return ChatResponse{}, bpErr
				}
				if hire := suggestHire(task, blueprints); hire != nil {
					response.Hire = hire
					response.Reply = fmt.Sprintf(
						"Под задачу «%s» я бы взял «%s» — %s. Наймите его, и я предложу квест.",
						questTitle(task), hire.Name, hire.Why)
					return response, s.persistReply(ctx, req, response, "")
				}
			}
			// «В ростере 0 агентов» — счётная строка, а не разговор.
			response.Reply = "В ростере пока никого. Опишите задачу — подберу, кого под неё нанять."
		} else if intent == "greeting" {
			response.Reply = fmt.Sprintf(
				"Сейчас в ростере %s, активных квестов %d. Опишите задачу — подберу отряд и предложу квест, или спросите «что сейчас».",
				textutil.Count(len(agents), "агент", "агента", "агентов"), active)
		} else if asksWhichAgent(req.Message) {
			// Вопрос понят, не хватает не понимания, а задачи.
			response.Reply = "Под какую задачу? Опишите её — подберу исполнителя из ростера и предложу квест."
		} else {
			// Непонятое и приветствие звучали одинаково, и по ответу нельзя было
			// понять, разобрал ли Мастер вопрос: «привет» и «сколько есть
			// агентов» получали слово в слово одну строку. Признать непонимание
			// честнее и полезнее, чем выдать его за приглашение.
			response.Reply = fmt.Sprintf(
				"Не понял вопроса. Скажу, что сейчас в работе, или перечислю ростер (сейчас в нём %s) — или опишите задачу, и я предложу квест.",
				textutil.Count(len(agents), "агент", "агента", "агентов"))
		}
		// Подсказки — то, что можно сказать Мастеру, а не то, о чём он спрашивает.
		//
		// Чип уходит в его же адрес следующей репликой. «Что нужно сделать в
		// проекте?» возвращалось ему вопросом, на который он отвечал этой же
		// строкой, — разговор ходил по кругу. Эти две реплики работают: на них
		// он отвечает сводкой и составом.
		response.Questions = []string{"что сейчас?", "кто есть в ростере?"}
		response.Actions = s.actionsFor(snapshot)
		return response, s.persistReply(ctx, req, response, "")
	}

	// Состав, названный моделью, — предложение, а не назначение: AssignParty
	// отбросит несуществующие идентификаторы и обрежет по политике отряда.
	var proposedIDs []string
	if modelSaid != nil && modelSaid.Proposal != nil {
		proposedIDs = modelSaid.Proposal.AgentIDs
	}
	assignment := s.assignParty(ctx, req, agents, proposedIDs)
	byID := make(map[string]domain.ProjectAgent, len(agents))
	for _, agent := range agents {
		byID[agent.ID] = agent
	}
	party := make([]domain.ProjectAgent, 0, len(assignment.AgentIDs))
	names := make([]string, 0, len(assignment.AgentIDs))
	for _, id := range assignment.AgentIDs {
		if agent, ok := byID[id]; ok {
			party = append(party, agent)
			names = append(names, agent.Name)
		}
	}

	members := make([]PartyMember, 0, len(party))
	blocked := 0
	for _, agent := range party {
		blockers := s.blockersFor(agent)
		if len(blockers) > 0 {
			blocked++
		}
		score := scoreAgentForGoal(agent, req.Message)
		members = append(members, PartyMember{
			AgentID:  agent.ID,
			Name:     agent.Name,
			Role:     strings.TrimSpace(agent.RoleDescription),
			Score:    &score,
			Matched:  matchedTerms(agent, req.Message),
			Blocking: blockers,
		})
	}

	// Весь отряд неработоспособен — предлагать квест бессмысленно: он упрётся
	// в первую же правку. Честнее назвать причину, чем создать заведомо
	// провальное предложение и ждать, пока человек сам догадается.
	if len(members) > 0 && blocked == len(members) {
		response.Party = members
		response.Reply = "Ни один из подходящих агентов не сможет довести работу до конца — поправьте их настройку, и я предложу квест."
		// Кнопкой, а не подсказкой: подсказка уходит Мастеру следующей репликой,
		// а «открыть карточку агента» ему не адресуется — это переход по разделам.
		// Отправленная ему, она возвращала тот же отказ, потому что в мире ничего
		// не поменялось. Чинят агента в гильдии, туда и ведём.
		response.Actions = []ChatAction{{
			Label: "Открыть гильдию", Tab: "agents",
			Hint: "поправить настройку агента — там же, где его создавали",
		}}
		return response, s.persistReply(ctx, req, response, "")
	}

	// Прежнее предложение ищется до того, как появится новое, иначе поиск нашёл
	// бы его же. Молчать о нём нельзя: очередь решений растёт от каждой задачи,
	// и человек, описывающий вторую, не видит, что первая всё ещё ждёт. Ветки,
	// которые до предложения не доходят, за этот поиск не платят.
	previous := s.pendingOwnProposal(ctx, req.WorkspaceID)

	// Та же задача второй раз — то же предложение, а не второе такое же.
	//
	// Повторяют задачу по-разному: не заметили ответа, вернулись к разговору,
	// нажали не туда. Раньше каждый повтор клал в очередь ещё одно решение с тем
	// же названием, и Мастер сообщал о «предыдущем предложении» с заголовком,
	// который только что назвал сам, — читалось как сбой. Отличить уточнение
	// задачи от новой он не умеет, но дословный повтор — случай без сомнений.
	if previous != nil && previous.Title == questTitle(req.Message) {
		response.Proposal = previous
		response.Reply = fmt.Sprintf(
			"Это та же задача: предложение «%s» уже ждёт вашего решения — запустить или отклонить его можно прямо здесь.",
			previous.Title)
		return response, s.persistReply(ctx, req, response, previous.ID)
	}

	// Просьба поправить предложенный квест — правка, а не второй квест.
	//
	// «Убери Разведчика из отряда» начинается рабочим глаголом, и разбор считал
	// это новой работой: в очередь ложилось второе предложение с названием
	// «Убери Разведчика из отряда», а первое продолжало ждать. Человек получал
	// две записи вместо одной поправленной и разбирался руками.
	//
	// Правит модель — она читала и просьбу, и снимок мира с идентификаторами
	// ждущих предложений. Права при этом те же: состав проходит через
	// AssignParty, годность считает тот же код, запуск остаётся за человеком.
	if amended, handled, amendErr := s.amendPendingProposal(ctx, req, modelSaid, members, names, blocked, assignment, response); handled {
		return amended, amendErr
	}

	partyWhy := chatPartyWhy(req.Config, assignment)
	// Задача — то, что человек написал; при возврате по обещанию — та задача, к
	// которой вернулись, а не слово «нанял», которым он об этом сообщил.
	taskText := req.Message
	if resumed != "" {
		taskText = resumed
	}
	// Формулировку берём у модели, когда она её дала: она читала и задачу, и
	// снимок мира. Пустые поля добирает движок — предложение не должно зависеть
	// от того, насколько словоохотлива модель.
	title := questTitle(req.Message)
	objectives := objectivesFor(req.Message)
	definitionOfDone := []string{"Изменения приняты", "Проверка завершилась успешно после последней правки"}
	var constraints []string
	if modelSaid != nil && modelSaid.Proposal != nil {
		if modelSaid.Proposal.Title != "" {
			title = questTitle(modelSaid.Proposal.Title)
		}
		if len(modelSaid.Proposal.Objectives) > 0 {
			objectives = modelSaid.Proposal.Objectives
		}
		if len(modelSaid.Proposal.DefinitionOfDone) > 0 {
			definitionOfDone = modelSaid.Proposal.DefinitionOfDone
		}
		constraints = modelSaid.Proposal.Constraints
	}
	proposal := domain.QuestProposal{
		ID:          s.newID("qp"),
		WorkspaceID: req.WorkspaceID,
		Title:       title,
		// Задача словами человека — она и есть содержание квеста. Без неё в
		// описание квеста уходило объяснение выбора отряда, а оттуда в контекст
		// исполняющего агента: он читал, как его выбирали, вместо задачи.
		Task:               taskText,
		Rationale:          partyWhy,
		Objectives:         objectives,
		Constraints:        constraints,
		DefinitionOfDone:   definitionOfDone,
		TeamAgentIDs:       assignment.AgentIDs,
		SelectionBreakdown: assignment.Breakdown,
		Importance:         domain.QuestNormal,
		Status:             "pending",
		CreatedAt:          s.now(),
	}
	// Потолок расхода. Без него квест уходит в работу без всякого предела:
	// проверка бюджета пропускает квест без бюджета целиком. Оценку ставил
	// только компаньон, а Мастер — основной путь создания квестов — не ставил.
	proposal.EstimateTokens = domain.EstimateQuestTokens(proposal)
	if err = s.Store.SaveQuestProposal(ctx, proposal); err != nil {
		return ChatResponse{}, err
	}

	response.Proposal = &proposal
	response.Party = members
	response.PartyWhy = partyWhy
	response.Questions = clarifyingQuestions(req.Message, party)
	switch {
	case len(names) == 0:
		response.Reply = "Подобрать отряд не удалось — уточните задачу или проверьте ростер."
	case blocked > 0:
		response.Reply = fmt.Sprintf("Собрал отряд: %s. Из них %d не сможет завершить работу — причина указана в составе. Квест предложен, но лучше сначала поправить настройку.",
			strings.Join(names, ", "), blocked)
	default:
		response.Reply = fmt.Sprintf("Собрал отряд: %s. Квест предложен и ждёт вашего решения — сам он не стартует.",
			strings.Join(names, ", "))
		// Модель объясняет выбор своими словами — это и есть разница между
		// диспетчером и справочником. Но только здесь: в ветках отказа и
		// оговорок говорит движок, потому что там речь о том, чего модель не
		// знает — о годности агентов и о прежнем предложении в очереди.
		if modelSaid != nil && strings.TrimSpace(modelSaid.Reply) != "" {
			response.Reply = strings.TrimSpace(modelSaid.Reply) + "\n\n" + response.Reply
			if len(modelSaid.Questions) > 0 {
				response.Questions = modelSaid.Questions
			}
		}
	}
	// Вернулись к задаче по обещанию — говорим об этом прямо: реплика человека
	// была «нанял», и без оговорки ответ выглядел бы так, будто Мастер собрал
	// отряд под слово «нанял».
	if resumed != "" {
		response.Reply = fmt.Sprintf("Возвращаюсь к задаче «%s». ", questTitle(resumed)) + response.Reply
	}
	// Второе предложение не отменяет первое: отличить уточнение задачи от новой
	// задачи Мастер не умеет, а угадывать и молча снимать чужое решение — хуже,
	// чем назвать его. Иначе очередь растёт от каждой реплики, а замечает это
	// человек уже по счётчику в шапке, не понимая, откуда там столько.
	if previous != nil {
		response.Reply += fmt.Sprintf(" Предыдущее предложение «%s» всё ещё ждёт решения — если оно больше не нужно, отклоните его.", previous.Title)
	}
	return response, s.persistReply(ctx, req, response, proposal.ID)
}

func (s ChatService) persist(ctx context.Context, workspaceID, role, content, proposalID string, cfg domain.OrchestratorConfig) error {
	return s.Store.SaveCompanionMessage(ctx, domain.CompanionMessage{
		ID:          s.newID("mm"),
		WorkspaceID: workspaceID,
		Speaker:     "master",
		Role:        role,
		Content:     content,
		Mode:        "deterministic",
		Provider:    string(cfg.Provider),
		Model:       cfg.Model,
		ProposalID:  proposalID,
		CreatedAt:   s.now(),
	})
}

// persistReply сохраняет ответ целиком, а не только его текст.
//
// Основания и уточняющие вопросы складывались в ответ на текущий ход и жили до
// первой перезагрузки панели: разговор поднимался из истории голыми репликами,
// чипы вопросов пропадали, а «на чём это основано» приходилось выспрашивать
// заново. Колонки под то и другое в хронике есть с самого начала — Мастер их
// просто не заполнял.
func (s ChatService) persistReply(ctx context.Context, req ChatRequest, response ChatResponse, proposalID string) error {
	actionProposalID := ""
	if response.ActionProposal != nil {
		actionProposalID = response.ActionProposal.ID
	}
	return s.Store.SaveCompanionMessage(ctx, domain.CompanionMessage{
		ID:               s.newID("mm"),
		WorkspaceID:      req.WorkspaceID,
		Speaker:          "master",
		Role:             "assistant",
		Content:          response.Reply,
		Mode:             response.Mode,
		Provider:         string(req.Config.Provider),
		Model:            req.Config.Model,
		FactsUsed:        response.Facts,
		Questions:        response.Questions,
		Clarifications:   response.Clarifications,
		ProposalID:       proposalID,
		ActionProposalID: actionProposalID,
		FallbackReason:   response.FallbackReason,
		Reasoning:        response.Reasoning,
		Steps:            response.Steps,
		InputTokens:      response.Usage.InputTokens,
		OutputTokens:     response.Usage.OutputTokens,
		TotalTokens:      response.Usage.TotalTokens,
		LatencyMs:        response.Usage.LatencyMs,
		CreatedAt:        s.now(),
	})
}

// History возвращает разговор Мастера, не смешивая его с компаньоном.
func (s ChatService) History(ctx context.Context, workspaceID string, limit int) ([]domain.CompanionMessage, error) {
	return s.Store.ListChatMessages(ctx, workspaceID, "master", limit)
}
