// Договор разговора с Мастером: что он получает, чем отвечает и что видит.
package orchestrator

import (
	"context"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
)

// Пределы разговора. Число знаков реплики парное с интерфейсом: композер
// обязан знать ту же границу, иначе человек узнаёт о ней после отправки —
// текст уже ушёл из поля. Сверяется договорённостью 25 в ui/contracts.mjs.
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
	Skills *MasterSkillSession
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
