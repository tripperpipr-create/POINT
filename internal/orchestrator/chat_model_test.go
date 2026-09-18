package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
)

// scriptedModel отвечает заранее заданным текстом или падает заданной ошибкой.
type scriptedModel struct {
	reply string
	fail  error
	seen  *providers.ModelRequest
}

func (m *scriptedModel) Stream(_ context.Context, req providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	if m.seen != nil {
		*m.seen = req
	}
	if m.fail != nil {
		return m.fail
	}
	return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: m.reply})
}

type masterReadLoopModel struct {
	requests []providers.ModelRequest
}

func (m *masterReadLoopModel) Stream(_ context.Context, req providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.requests = append(m.requests, req)
	if len(m.requests) == 1 {
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{
			ID: "read-1", Name: "read_file", Arguments: json.RawMessage(`{"path":"internal/app/app.go"}`),
		}})
	}
	return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: `{"reply":"Причина найдена в прочитанном файле.","questions":[],"proposal":null}`})
}

func TestMasterChatModelUsesReadToolsAndContinues(t *testing.T) {
	model := &masterReadLoopModel{}
	service := ChatService{
		ReadTools: readingToolsStub{},
		ModelFactory: func(providers.Config) (providers.Model, error) {
			return model, nil
		},
	}
	envelope, err := service.chatWithModel(context.Background(), ChatRequest{
		WorkspaceID: "ws", Message: "Почему это сломалось?",
		Config: domain.OrchestratorConfig{Provider: domain.ProviderOllama, Model: "model"},
	}, "Проект: test.", nil)
	if err != nil {
		t.Fatal(err)
	}
	if envelope.Reply != "Причина найдена в прочитанном файле." {
		t.Fatalf("финальный ответ потерян: %#v", envelope)
	}
	if len(model.requests) != 2 || len(model.requests[0].Tools) != 1 {
		t.Fatalf("читающие инструменты не переданы в первый раунд: %#v", model.requests)
	}
	messages := model.requests[1].Messages
	if len(messages) == 0 || messages[len(messages)-1].Role != "tool" || !strings.Contains(messages[len(messages)-1].Content, "package app") {
		t.Fatalf("результат инструмента не вернулся модели: %#v", messages)
	}
}

// Разговор ведёт модель Мастера, а не список ключевых слов.
//
// Найдено на живом экране: «Привет» и «Сколько есть агентов?» получали ответ
// слово в слово, потому что намерение разбиралось по списку фраз и вторая
// формулировка в него не попадала. Мастер — системный агент со своей ролью;
// разговаривать он должен своей моделью, а детерминированный движок остаётся
// запасным путём.
func TestMasterTalksThroughItsOwnModel(t *testing.T) {
	var seen providers.ModelRequest
	store := newChatStoreStub()
	store.agents = []domain.ProjectAgent{
		{ID: "a-1", Name: "Разведчик", RoleDescription: "читает код и объясняет"},
	}
	service := ChatService{
		Store: store,
		NewID: func(prefix string) string { return prefix + "-1" },
		ModelFactory: func(providers.Config) (providers.Model, error) {
			return &scriptedModel{reply: `{"reply":"В ростере один Разведчик, он готов к работе.","questions":["Под какую задачу его брать?"]}`, seen: &seen}, nil
		},
	}
	cfg := domain.OrchestratorConfig{Preset: "conductor", Provider: domain.ProviderOllama, Model: "qwen", BaseURL: "http://127.0.0.1:11434"}

	response, err := service.Chat(context.Background(), ChatRequest{WorkspaceID: "ws-1", Message: "Сколько есть агентов?", Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if response.Mode != "model" {
		t.Fatalf("ответил не модельный путь: mode=%q", response.Mode)
	}
	if response.Reply != "В ростере один Разведчик, он готов к работе." {
		t.Fatalf("ответ модели не дошёл до человека: %q", response.Reply)
	}
	if len(response.Questions) != 1 {
		t.Fatalf("уточняющие вопросы модели потеряны: %v", response.Questions)
	}

	// Модель обязана видеть состав ростера, иначе отвечать ей нечем и она
	// начнёт выдумывать.
	system := ""
	for _, message := range seen.Messages {
		if message.Role == "system" {
			system = message.Content
		}
	}
	if !strings.Contains(system, "Разведчик") {
		t.Fatalf("снимок мира не дошёл до модели: %q", system)
	}
	if !strings.Contains(system, "не запускаешь квесты") {
		t.Fatalf("границы роли не заданы промптом: %q", system)
	}
}

// Модель не ответила — отвечает движок Point, и человек знает почему.
//
// Молчаливый откат неотличим от исправной работы: человек настроил модель и
// вправе знать, что отвечала не она.
func TestMasterFallsBackToEngineAndSaysWhy(t *testing.T) {
	store := newChatStoreStub()
	store.agents = []domain.ProjectAgent{{ID: "a-1", Name: "Разведчик"}}
	service := ChatService{
		Store: store,
		NewID: func(prefix string) string { return prefix + "-1" },
		ModelFactory: func(providers.Config) (providers.Model, error) {
			return &scriptedModel{fail: errors.New("connection refused")}, nil
		},
	}
	cfg := domain.OrchestratorConfig{Preset: "conductor", Provider: domain.ProviderOllama, Model: "qwen", BaseURL: "http://127.0.0.1:11434"}

	response, err := service.Chat(context.Background(), ChatRequest{WorkspaceID: "ws-1", Message: "кто есть в ростере?", Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if response.Mode != "deterministic" {
		t.Fatalf("после отказа модели режим обязан быть детерминированным: %q", response.Mode)
	}
	if response.FallbackReason == "" {
		t.Fatal("откат произошёл молча — человек не узнает, что модель не ответила")
	}
	if !strings.Contains(response.FallbackReason, "connection refused") {
		t.Fatalf("причина отката не названа: %q", response.FallbackReason)
	}
	// Запасной путь остаётся полноценным ответом, а не заглушкой.
	if !strings.Contains(response.Reply, "Разведчик") {
		t.Fatalf("движок Point не ответил по существу: %q", response.Reply)
	}
}

// Мастер знает, чем распоряжается.
//
// В снимке мира были ростер, квесты и очередь решений — и ни слова о самом
// проекте. На «расскажи про проект» диспетчер отвечал «не понял вопроса»,
// потому что ответить ему было нечем.
func TestMasterWorldPromptNamesProject(t *testing.T) {
	snapshot := Situation{Project: ProjectFacts{
		Name: "ai-ide", IndexState: "ready", Files: 812, Symbols: 9400,
		Languages: []string{"go", "javascript", "css"},
	}}
	world := masterWorldPrompt(nil, nil, nil, nil, 0, snapshot)
	for _, want := range []string{"Проект: ai-ide.", "812 файлов", "9400 символов", "go, javascript, css"} {
		if !strings.Contains(world, want) {
			t.Fatalf("в снимке мира нет «%s»:\n%s", want, world)
		}
	}

	// Индекс не построен — так и сказано, а не выдумано содержимое.
	unbuilt := masterWorldPrompt(nil, nil, nil, nil, 0, Situation{Project: ProjectFacts{Name: "ai-ide", IndexState: "not_built"}})
	if !strings.Contains(unbuilt, "Карта кода не построена") {
		t.Fatalf("непостроенный индекс не назван:\n%s", unbuilt)
	}

	// Папки нет — о проекте молчим, а не пишем пустую строку.
	none := masterWorldPrompt(nil, nil, nil, nil, 0, Situation{})
	if strings.Contains(none, "Проект:") {
		t.Fatalf("проект назван при закрытой папке:\n%s", none)
	}
}

// Мусор вместо JSON — тоже повод для отката, а не для показа мусора.
func TestMasterRejectsUnparsableModelAnswer(t *testing.T) {
	store := newChatStoreStub()
	store.agents = []domain.ProjectAgent{{ID: "a-1", Name: "Разведчик"}}
	service := ChatService{
		Store: store,
		NewID: func(prefix string) string { return prefix + "-1" },
		ModelFactory: func(providers.Config) (providers.Model, error) {
			return &scriptedModel{reply: "я подумаю об этом позже"}, nil
		},
	}
	cfg := domain.OrchestratorConfig{Preset: "conductor", Provider: domain.ProviderOllama, Model: "qwen", BaseURL: "http://127.0.0.1:11434"}

	response, err := service.Chat(context.Background(), ChatRequest{WorkspaceID: "ws-1", Message: "что сейчас?", Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if response.Mode != "deterministic" || response.FallbackReason == "" {
		t.Fatalf("неразобранный ответ модели показан как есть: mode=%q reason=%q", response.Mode, response.FallbackReason)
	}
	if strings.Contains(response.Reply, "подумаю об этом позже") {
		t.Fatalf("мусор модели дошёл до человека: %q", response.Reply)
	}
}

// Модель формулирует квест, но состав отряда проверяет код.
//
// Она читала и задачу, и снимок мира — заголовок и цели у неё выходят точнее
// шаблона. Но её слово о составе ничего не решает: несуществующие агенты
// отбрасываются, размер режется политикой, годность считает тот же код, что и
// карточка агента, а запуск остаётся решением человека.
func TestModelShapesTheQuestButNotTheParty(t *testing.T) {
	store := newChatStoreStub()
	store.agents = []domain.ProjectAgent{
		{ID: "a-1", Name: "Разведчик", RoleDescription: "правит бэкенд"},
	}
	service := ChatService{
		Store: store,
		NewID: func(prefix string) string { return prefix + "-1" },
		ModelFactory: func(providers.Config) (providers.Model, error) {
			return &scriptedModel{reply: `{"reply":"Задача про вебхук, беру бэкендера.","questions":[],` +
				`"proposal":{"title":"Починить вебхук биллинга","objectives":["Воспроизвести отказ на тесте","Починить обработчик"],` +
				`"definitionOfDone":["Тест на вебхук проходит"],"agentIds":["a-1","выдуманный-агент"]}}`}, nil
		},
	}
	cfg := domain.OrchestratorConfig{Preset: "conductor", Provider: domain.ProviderOllama, Model: "qwen", BaseURL: "http://127.0.0.1:11434"}

	response, err := service.Chat(context.Background(), ChatRequest{WorkspaceID: "ws-1", Message: "почини вебхук биллинга", Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if response.Proposal == nil {
		t.Fatalf("квест не предложен: %q", response.Reply)
	}
	// Формулировка модели дошла до предложения.
	if response.Proposal.Title != "Починить вебхук биллинга" {
		t.Fatalf("заголовок модели не использован: %q", response.Proposal.Title)
	}
	if len(response.Proposal.Objectives) != 2 {
		t.Fatalf("цели модели не использованы: %v", response.Proposal.Objectives)
	}
	// Выдуманный агент в отряд не попал.
	for _, id := range response.Proposal.TeamAgentIDs {
		if id == "выдуманный-агент" {
			t.Fatalf("несуществующий агент попал в отряд: %v", response.Proposal.TeamAgentIDs)
		}
	}
	if len(response.Proposal.TeamAgentIDs) == 0 {
		t.Fatal("отряд пуст, хотя подходящий агент в ростере есть")
	}
	// Предложение ждёт решения человека, а не стартует.
	if response.Proposal.Status != "pending" {
		t.Fatalf("квест не в состоянии ожидания решения: %q", response.Proposal.Status)
	}
	if !strings.Contains(response.Reply, "сам он не стартует") {
		t.Fatalf("ответ не сообщает, что запуск за человеком: %q", response.Reply)
	}
	// Объяснение модели человек видит рядом с итогом движка.
	if !strings.Contains(response.Reply, "беру бэкендера") {
		t.Fatalf("объяснение модели потеряно: %q", response.Reply)
	}
}

// Весь предложенный моделью отряд выдуман — работает подбор движка.
func TestInventedPartyFallsBackToDeterministicAssignment(t *testing.T) {
	store := newChatStoreStub()
	store.agents = []domain.ProjectAgent{{ID: "a-1", Name: "Разведчик", RoleDescription: "правит бэкенд"}}
	service := ChatService{
		Store: store,
		NewID: func(prefix string) string { return prefix + "-1" },
		ModelFactory: func(providers.Config) (providers.Model, error) {
			return &scriptedModel{reply: `{"reply":"беру команду","questions":[],` +
				`"proposal":{"title":"Починить вебхук","objectives":["Починить"],"agentIds":["нет-такого","и-такого"]}}`}, nil
		},
	}
	cfg := domain.OrchestratorConfig{Preset: "conductor", Provider: domain.ProviderOllama, Model: "qwen", BaseURL: "http://127.0.0.1:11434"}

	response, err := service.Chat(context.Background(), ChatRequest{WorkspaceID: "ws-1", Message: "почини вебхук биллинга", Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if response.Proposal == nil || len(response.Proposal.TeamAgentIDs) != 1 || response.Proposal.TeamAgentIDs[0] != "a-1" {
		t.Fatalf("подбор движка не сработал после выдуманного отряда: %+v", response.Proposal)
	}
}

// chatStoreStub — мир в памяти: разговор Мастера проверяется без базы и сети.
type chatStoreStub struct {
	agents          []domain.ProjectAgent
	quests          []domain.Quest
	proposals       []domain.QuestProposal
	actionProposals []domain.CompanionActionProposal
	messages        []domain.CompanionMessage
}

func newChatStoreStub() *chatStoreStub { return &chatStoreStub{} }

func (s *chatStoreStub) ListProjectAgents(context.Context, string) ([]domain.ProjectAgent, error) {
	return s.agents, nil
}
func (s *chatStoreStub) ListQuests(context.Context, string) ([]domain.Quest, error) {
	return s.quests, nil
}

// Настоящее хранилище пишет предложение через ON CONFLICT(id) DO UPDATE.
// Заглушка, только добавляющая записи, не отличила бы правку на месте от
// второго предложения — то есть не проверяла бы ровно то, ради чего написана.
func (s *chatStoreStub) SaveQuestProposal(_ context.Context, proposal domain.QuestProposal) error {
	for index := range s.proposals {
		if s.proposals[index].ID == proposal.ID {
			s.proposals[index] = proposal
			return nil
		}
	}
	s.proposals = append(s.proposals, proposal)
	return nil
}
func (s *chatStoreStub) ListQuestProposals(context.Context, string) ([]domain.QuestProposal, error) {
	return s.proposals, nil
}
func (s *chatStoreStub) SaveCompanionActionProposal(_ context.Context, proposal domain.CompanionActionProposal) error {
	for index := range s.actionProposals {
		if s.actionProposals[index].ID == proposal.ID {
			s.actionProposals[index] = proposal
			return nil
		}
	}
	s.actionProposals = append(s.actionProposals, proposal)
	return nil
}
func (s *chatStoreStub) ListCompanionActionProposals(context.Context, string) ([]domain.CompanionActionProposal, error) {
	return s.actionProposals, nil
}
func (s *chatStoreStub) SaveCompanionMessage(_ context.Context, message domain.CompanionMessage) error {
	s.messages = append(s.messages, message)
	return nil
}
func (s *chatStoreStub) ListChatMessages(_ context.Context, _, _ string, limit int) ([]domain.CompanionMessage, error) {
	if limit > 0 && len(s.messages) > limit {
		return s.messages[len(s.messages)-limit:], nil
	}
	return s.messages, nil
}
func (s *chatStoreStub) ListBlueprints(context.Context) ([]domain.AgentBlueprint, error) {
	return nil, nil
}

// Мастер помнит собственное предложение.
//
// «а можно без него?», «почему этот агент?», «что ты предложил?» — вопросы,
// которые человек задаёт сразу после предложения квеста. В снимке мира были
// только счётчики, и отвечать на них модели было нечем: диспетчер, не помнящий
// своего же предложения, разговором не является.
func TestModelSeesItsOwnPendingProposal(t *testing.T) {
	var seen providers.ModelRequest
	store := newChatStoreStub()
	store.agents = []domain.ProjectAgent{{ID: "a-1", Name: "Разведчик", RoleDescription: "правит бэкенд"}}
	store.proposals = []domain.QuestProposal{{
		ID: "qp-1", WorkspaceID: "ws-1", Title: "Починить вебхук биллинга",
		Objectives: []string{"Воспроизвести отказ на тесте"}, TeamAgentIDs: []string{"a-1"},
		Status: "pending",
	}, {
		// Решённое предложение в разговоре не нужно: оно уже не ждёт человека.
		ID: "qp-0", WorkspaceID: "ws-1", Title: "Старое и отклонённое", Status: "rejected",
	}}
	store.quests = []domain.Quest{{ID: "q-1", Title: "Обновить схему миграций", Status: "active"}}
	service := ChatService{
		Store: store,
		NewID: func(prefix string) string { return prefix + "-1" },
		ModelFactory: func(providers.Config) (providers.Model, error) {
			return &scriptedModel{reply: `{"reply":"Предложил починку вебхука, взял Разведчика.","questions":[]}`, seen: &seen}, nil
		},
	}
	cfg := domain.OrchestratorConfig{Preset: "conductor", Provider: domain.ProviderOllama, Model: "qwen", BaseURL: "http://127.0.0.1:11434"}

	if _, err := service.Chat(context.Background(), ChatRequest{WorkspaceID: "ws-1", Message: "а можно без него?", Config: cfg}); err != nil {
		t.Fatal(err)
	}
	system := ""
	for _, message := range seen.Messages {
		if message.Role == "system" {
			system = message.Content
		}
	}
	if !strings.Contains(system, "Починить вебхук биллинга") {
		t.Fatalf("модель не знает о собственном предложении: %q", system)
	}
	if !strings.Contains(system, "отряд: Разведчик") {
		t.Fatalf("состав предложенного отряда не дошёл до модели: %q", system)
	}
	if !strings.Contains(system, "Воспроизвести отказ на тесте") {
		t.Fatalf("цели предложения не дошли до модели: %q", system)
	}
	if !strings.Contains(system, "Обновить схему миграций") {
		t.Fatalf("идущий квест не дошёл до модели: %q", system)
	}
	// Решённое предложение человека уже не ждёт — в разговоре ему не место.
	if strings.Contains(system, "Старое и отклонённое") {
		t.Fatalf("решённое предложение попало в снимок мира: %q", system)
	}
}

// Просьба поправить квест правит его, а не создаёт второй.
//
// «Убери Разведчика из отряда» начинается рабочим глаголом, и разбор считал это
// новой работой: в очередь ложилось второе предложение с этим же названием, а
// первое продолжало ждать. Человек получал две записи вместо одной поправленной.
func TestAmendingProposalUpdatesItInPlace(t *testing.T) {
	store := newChatStoreStub()
	store.agents = []domain.ProjectAgent{
		{ID: "a-1", Name: "Разведчик", RoleDescription: "читает код"},
		{ID: "a-2", Name: "Кузнец", RoleDescription: "правит бэкенд"},
	}
	store.proposals = []domain.QuestProposal{{
		ID: "qp-1", WorkspaceID: "ws-1", Title: "Починить вебхук биллинга",
		Objectives: []string{"Починить обработчик"}, TeamAgentIDs: []string{"a-1", "a-2"},
		Status: "pending",
	}}
	service := ChatService{
		Store: store,
		NewID: func(prefix string) string { return prefix + "-new" },
		ModelFactory: func(providers.Config) (providers.Model, error) {
			return &scriptedModel{reply: `{"reply":"Убрал Разведчика, остаётся Кузнец.","questions":[],` +
				`"proposal":{"proposalId":"qp-1","title":"Починить вебхук биллинга",` +
				`"objectives":["Починить обработчик","Покрыть тестом"],"agentIds":["a-2"]}}`}, nil
		},
	}
	cfg := domain.OrchestratorConfig{Preset: "conductor", Provider: domain.ProviderOllama, Model: "qwen", BaseURL: "http://127.0.0.1:11434"}

	response, err := service.Chat(context.Background(), ChatRequest{WorkspaceID: "ws-1", Message: "убери Разведчика из отряда", Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if response.Proposal == nil {
		t.Fatalf("предложение пропало: %q", response.Reply)
	}
	if response.Proposal.ID != "qp-1" {
		t.Fatalf("создано новое предложение вместо правки: %q", response.Proposal.ID)
	}
	// В очереди по-прежнему одно предложение, а не два.
	pending := 0
	for _, proposal := range store.proposals {
		if proposal.Status == "pending" {
			pending += 1
		}
	}
	if pending != 1 {
		t.Fatalf("в очереди %d предложений вместо одного: %+v", pending, store.proposals)
	}
	if len(response.Proposal.Objectives) != 2 {
		t.Fatalf("цели не обновились: %v", response.Proposal.Objectives)
	}
	// Правка не запускает квест: решение остаётся за человеком.
	if response.Proposal.Status != "pending" {
		t.Fatalf("правка сменила состояние предложения: %q", response.Proposal.Status)
	}
	if !strings.Contains(response.Reply, "ждёт вашего решения") {
		t.Fatalf("ответ не сообщает, что решение за человеком: %q", response.Reply)
	}
	if !strings.Contains(response.Reply, "Убрал Разведчика") {
		t.Fatalf("объяснение модели потеряно: %q", response.Reply)
	}
}

// Выдуманный идентификатор предложения не даёт править ничего.
func TestAmendingUnknownProposalFallsBackToNewOne(t *testing.T) {
	store := newChatStoreStub()
	store.agents = []domain.ProjectAgent{{ID: "a-1", Name: "Кузнец", RoleDescription: "правит бэкенд"}}
	store.proposals = []domain.QuestProposal{{
		ID: "qp-1", WorkspaceID: "ws-1", Title: "Починить вебхук", Status: "rejected",
	}}
	service := ChatService{
		Store: store,
		NewID: func(prefix string) string { return prefix + "-new" },
		ModelFactory: func(providers.Config) (providers.Model, error) {
			return &scriptedModel{reply: `{"reply":"правлю","questions":[],` +
				`"proposal":{"proposalId":"qp-выдуманный","title":"Новая задача","objectives":["Сделать"],"agentIds":["a-1"]}}`}, nil
		},
	}
	cfg := domain.OrchestratorConfig{Preset: "conductor", Provider: domain.ProviderOllama, Model: "qwen", BaseURL: "http://127.0.0.1:11434"}

	response, err := service.Chat(context.Background(), ChatRequest{WorkspaceID: "ws-1", Message: "почини вебхук биллинга", Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if response.Proposal == nil || response.Proposal.ID != "qp-new" {
		t.Fatalf("по выдуманному идентификатору обязано создаваться новое предложение: %+v", response.Proposal)
	}
	// Решённое предложение осталось решённым.
	for _, proposal := range store.proposals {
		if proposal.ID == "qp-1" && proposal.Status != "rejected" {
			t.Fatalf("решённое предложение изменили: %+v", proposal)
		}
	}
}

// Задача доходит до квеста своими словами.
//
// В предложении её негде было держать, и в описание квеста уходил Rationale —
// объяснение выбора отряда («пресет conductor · отряд 1 · движком Point»).
// Оттуда оно попадало в контекст исполняющего агента: он читал, как его
// выбирали, вместо того что нужно сделать. Заголовок задачу не заменяет — он
// обрезан до строки для очереди решений.
func TestProposalCarriesTheTaskAsWritten(t *testing.T) {
	store := newChatStoreStub()
	store.agents = []domain.ProjectAgent{{ID: "a-1", Name: "Кузнец", RoleDescription: "правит бэкенд"}}
	service := ChatService{Store: store, NewID: func(prefix string) string { return prefix + "-1" }}
	cfg := domain.OrchestratorConfig{Preset: "conductor"}

	task := "почини вебхук биллинга: приходит 500 на повторной доставке, воспроизводится на стейдже"
	response, err := service.Chat(context.Background(), ChatRequest{WorkspaceID: "ws-1", Message: task, Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if response.Proposal == nil {
		t.Fatalf("квест не предложен: %q", response.Reply)
	}
	if response.Proposal.Task != task {
		t.Fatalf("задача не сохранена дословно: %q", response.Proposal.Task)
	}
	// Обоснование выбора отряда остаётся при себе и задачу не подменяет.
	if response.Proposal.Rationale == "" {
		t.Fatal("обоснование выбора отряда потеряно")
	}
	if response.Proposal.Rationale == response.Proposal.Task {
		t.Fatal("обоснование и задача слиплись в одно поле — ровно то, что чинилось")
	}
}

// Квест Мастера уходит в работу с потолком расхода.
//
// Проверка бюджета пропускает квест без бюджета целиком:
// «BudgetTokens <= 0 && BudgetCents <= 0 → return nil». Оценку ставил только
// компаньон, а Мастер — основной путь создания квестов — не ставил, и его
// квесты работали без всякого предела трат. Одинаковые квесты не должны
// получать разный потолок в зависимости от того, кто их предложил.
func TestMasterProposalCarriesASpendCeiling(t *testing.T) {
	store := newChatStoreStub()
	store.agents = []domain.ProjectAgent{{ID: "a-1", Name: "Кузнец", RoleDescription: "правит бэкенд"}}
	service := ChatService{Store: store, NewID: func(prefix string) string { return prefix + "-1" }}
	cfg := domain.OrchestratorConfig{Preset: "conductor"}

	response, err := service.Chat(context.Background(), ChatRequest{
		WorkspaceID: "ws-1", Message: "почини вебхук биллинга", Config: cfg,
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Proposal == nil {
		t.Fatalf("квест не предложен: %q", response.Reply)
	}
	if response.Proposal.EstimateTokens <= 0 {
		t.Fatal("квест уходит без потолка расхода — проверка бюджета его пропустит целиком")
	}
	// Тот же потолок, что поставил бы компаньон: правило одно на обоих.
	if want := domain.EstimateQuestTokens(*response.Proposal); response.Proposal.EstimateTokens != want {
		t.Fatalf("потолок посчитан своим правилом: %d вместо %d", response.Proposal.EstimateTokens, want)
	}

	// Правка меняет цели и состав — потолок обязан пересчитаться.
	store.proposals = []domain.QuestProposal{{
		ID: "qp-9", WorkspaceID: "ws-1", Title: "Починить вебхук", Status: "pending",
		Objectives: []string{"Одна цель"}, TeamAgentIDs: []string{"a-1"}, EstimateTokens: 1,
	}}
	amender := ChatService{
		Store: store,
		NewID: func(prefix string) string { return prefix + "-new" },
		ModelFactory: func(providers.Config) (providers.Model, error) {
			return &scriptedModel{reply: `{"reply":"добавил цели","questions":[],` +
				`"proposal":{"proposalId":"qp-9","title":"Починить вебхук",` +
				`"objectives":["Первая","Вторая","Третья"],"agentIds":["a-1"]}}`}, nil
		},
	}
	modelCfg := domain.OrchestratorConfig{Preset: "conductor", Provider: domain.ProviderOllama, Model: "qwen", BaseURL: "http://127.0.0.1:11434"}
	amended, err := amender.Chat(context.Background(), ChatRequest{WorkspaceID: "ws-1", Message: "добавь цели про тесты", Config: modelCfg})
	if err != nil {
		t.Fatal(err)
	}
	if amended.Proposal == nil || amended.Proposal.EstimateTokens <= 1 {
		t.Fatalf("после правки потолок не пересчитан: %+v", amended.Proposal)
	}
}

// Рассуждающая модель показывает ход мысли перед структурой ответа, и внутри
// мысли встречаются фигурные скобки. Разбор искал первую `{` по всему тексту,
// находил её в размышлении и объявлял исправный ответ «не по схеме»: человек
// видел откат на пустом месте. Блок размышления снимается до поиска скобок.
func TestDecodeMasterEnvelopeSkipsReasoningBlock(t *testing.T) {
	raw := "<think>Проверю, нужна ли структура {objectives} и сколько вопросов задать</think>\n" +
		`{"reply":"Готов начать","questions":["Какой срок?"]}`
	envelope, err := decodeMasterEnvelope(raw)
	if err != nil {
		t.Fatalf("ответ рассуждающей модели обязан разбираться: %v", err)
	}
	if envelope.Reply != "Готов начать" {
		t.Fatalf("текст ответа: %q", envelope.Reply)
	}
	if len(envelope.Questions) != 1 || envelope.Questions[0] != "Какой срок?" {
		t.Fatalf("вопросы: %#v", envelope.Questions)
	}
}

// Ограда кода вокруг структуры — обычный ответ модели, которую попросили
// вернуть JSON.
func TestDecodeMasterEnvelopeAcceptsFencedJSON(t *testing.T) {
	envelope, err := decodeMasterEnvelope("```json\n" + `{"reply":"Ок"}` + "\n```")
	if err != nil {
		t.Fatalf("огороженный ответ обязан разбираться: %v", err)
	}
	if envelope.Reply != "Ок" {
		t.Fatalf("текст ответа: %q", envelope.Reply)
	}
}
