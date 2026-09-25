package orchestrator

import (
	"context"
	"encoding/json"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
	"strings"
	"testing"
)

func TestTaskIntakeWithoutAgentsAndWithLockedTeam(t *testing.T) {
	store := newChatStoreStub()
	b := domain.TaskBrief{Mode: domain.TaskModePrecise, Goal: "Function with a complete contract", ResultKind: "code", Criteria: []domain.AcceptanceCriterion{{ID: "c1", Text: "Follows contract", Kind: "manual"}}}
	model := &turnModel{rounds: []roundScript{{text: "Ready", calls: []providers.ToolCall{proposeBriefCall("b1", "Function", "", b)}}}}
	service := ChatService{Store: store, ModelFactory: model.factory()}
	req := ChatRequest{WorkspaceID: "ws", TaskIntake: true, Message: "Return the function exactly as described", Config: domain.OrchestratorConfig{Provider: domain.ProviderOllama, Model: "model"}}
	response, err := service.Chat(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if response.Proposal == nil || response.Proposal.Brief.State != "ready" || len(response.Questions) != 0 {
		t.Fatalf("complete precise task was interviewed: %#v", response)
	}
	if model.requests[0].ContextWindowTokens != intakeContextWindowTokens {
		t.Fatal("intake runtime context not set")
	}
	if domain.IsTaskBriefApproved(*response.Proposal.Brief) {
		t.Fatal("model approved task")
	}
	if response.Proposal.Brief.SourceRequest != req.Message {
		t.Fatal("original detailed request was lost")
	}
	for _, decision := range response.Proposal.Brief.Decisions {
		if decision.Topic == "Подготовка исполнителя" {
			t.Fatal("Master invented an agent-preparation decision")
		}
	}
	prior := response.Proposal
	prior.TeamAgentIDsLocked = true
	prior.TeamAgentIDs = []string{"chosen"}
	store.proposals[0] = *prior
	store.agents = []domain.ProjectAgent{{ID: "chosen", Name: "Chosen"}, {ID: "other", Name: "Other"}}
	req.ProposalID = prior.ID
	b.Goal = "Updated function"
	model.rounds = []roundScript{{text: "Updated", calls: []providers.ToolCall{proposeBriefCall("b2", "Function", prior.ID, b)}}}
	response, err = service.Chat(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if response.Proposal.Brief.Version != 2 || len(response.Proposal.TeamAgentIDs) != 0 {
		t.Fatalf("Master retained forbidden agent selection: %#v", response.Proposal)
	}
	b.Mode = domain.TaskModeProject
	model.rounds = []roundScript{{text: "Уточню", calls: []providers.ToolCall{
		proposeBriefCall("b3", "Function", prior.ID, b),
		toolCall("q1", masterActionAskClarifications, map[string]any{"items": []map[string]any{{"text": "Which output?", "kind": "text"}}}),
	}}}
	response, err = service.Chat(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if response.Proposal.Brief.State != "discussion" || len(response.Proposal.Brief.OpenQuestions) != 1 {
		t.Fatal("model question did not block approval")
	}
}

// Неверное задание чинится внутри хода: замечания сервера возвращаются
// результатом инструмента, и модель присылает исправленное следующим кругом.
// Отдельного запроса «почини секции» больше нет.
func TestTaskIntakeFixesInvalidBriefWithinTheTurn(t *testing.T) {
	store := newChatStoreStub()
	invalid := domain.TaskBrief{
		Mode: domain.TaskModePrecise, Goal: "Create a health endpoint", ResultKind: "workspace_change",
		Permissions: domain.TaskPermissions{WriteFiles: true, ExecuteCommands: true},
		Criteria: []domain.AcceptanceCriterion{
			{ID: "c1", Text: "Endpoint returns 200", Kind: "verification"},
			{ID: "c2", Text: "Configuration exists", Kind: "manual"},
		},
	}
	fixed := invalid
	fixed.Criteria = []domain.AcceptanceCriterion{
		{ID: "c1", Text: "Endpoint returns 200", Kind: "verification", Tool: "execute_command", Arguments: json.RawMessage(`{"command":"curl --fail http://localhost/health"}`)},
		{ID: "c2", Text: "Configuration exists", Kind: "manual"},
	}
	model := &turnModel{rounds: []roundScript{
		{calls: []providers.ToolCall{proposeBriefCall("b1", "Health", "", invalid)}},
		{text: "Задание готово.", calls: []providers.ToolCall{proposeBriefCall("b2", "Health", "", fixed)}},
	}}
	skills := NewMasterSkillSession("intake", nil)
	service := ChatService{Store: store, Skills: skills, ModelFactory: model.factory()}
	response, err := service.Chat(context.Background(), ChatRequest{
		WorkspaceID: "ws", TaskIntake: true, Message: "Create a health endpoint",
		Config: domain.OrchestratorConfig{Provider: domain.ProviderOllama, Model: "model"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(model.requests) != 2 || response.Proposal == nil {
		t.Fatalf("invalid draft was not fixed in the loop: rounds=%d response=%#v", len(model.requests), response)
	}
	got := response.Proposal.Brief.Criteria
	if len(got) != 2 || got[0].Tool != "execute_command" || got[1].Tool != "" {
		t.Fatalf("fixed brief lost: %#v", got)
	}
	last := model.requests[1].Messages[len(model.requests[1].Messages)-1]
	if last.Role != "tool" || !strings.Contains(last.Content, "invalid_brief") {
		t.Fatalf("server remarks did not reach the model: %#v", last)
	}
	if skills.Operation.Repairs != 1 || skills.Operation.ContractError {
		t.Fatalf("repair accounting wrong: %+v", skills.Operation)
	}
	if response.Reply != "Задание готово." {
		t.Fatalf("reply lost: %q", response.Reply)
	}
}

// Ход Мастера стоил денег и времени, и это обязано доехать до реплики.
//
// Колонки под расход в companion_messages были с самого начала, но у Мастера их
// не заполнял никто: persistReply писал режим, модель и основания — и ничего о
// цене. Окно «Сведения об ответе» показывало нули там, где должна стоять цена
// хода, а окно, которое врёт про расход, хуже отсутствующего.
func TestMasterTurnRemembersWhatItCost(t *testing.T) {
	store := newChatStoreStub()
	service := ChatService{Store: store, ModelFactory: func(providers.Config) (providers.Model, error) {
		return &meteredModel{reply: "Ready", inputTokens: 1200, outputTokens: 340}, nil
	}}
	if _, err := service.Chat(context.Background(), ChatRequest{
		WorkspaceID: "ws", TaskIntake: true, Message: "Prepare the task",
		Config: domain.OrchestratorConfig{Provider: domain.ProviderOllama, Model: "model"},
	}); err != nil {
		t.Fatal(err)
	}
	var reply *domain.CompanionMessage
	for index := range store.messages {
		if store.messages[index].Role == "assistant" {
			reply = &store.messages[index]
		}
	}
	if reply == nil {
		t.Fatal("ответ Мастера не сохранён")
	}
	if reply.InputTokens != 1200 || reply.OutputTokens != 340 || reply.TotalTokens != 1540 {
		t.Fatalf("расход хода потерян: in=%d out=%d total=%d", reply.InputTokens, reply.OutputTokens, reply.TotalTokens)
	}
	if reply.LatencyMs < 0 {
		t.Fatalf("отрицательная задержка: %d", reply.LatencyMs)
	}
}

// meteredModel отвечает и сообщает расход — как настоящий провайдер.
type meteredModel struct {
	reply        string
	inputTokens  int
	outputTokens int
}

func (m *meteredModel) Stream(_ context.Context, _ providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	if err := emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: m.reply}); err != nil {
		return err
	}
	return emit(providers.ModelEvent{Kind: providers.EventUsage, InputTokens: m.inputTokens, OutputTokens: m.outputTokens})
}

// «Ответить иначе» просит другой путь, а не повторяет вопрос.
//
// Без этого признака кнопка отправляла бы ту же реплику заново, и при низкой
// температуре модель возвращала бы тот же ответ слово в слово: нажатие без
// последствий.
func TestRejectedAnswerAsksForAnotherPath(t *testing.T) {
	store := newChatStoreStub()
	var seen providers.ModelRequest
	service := ChatService{Store: store, ModelFactory: func(providers.Config) (providers.Model, error) {
		return &scriptedModel{reply: "Ready", seen: &seen}, nil
	}}
	req := ChatRequest{WorkspaceID: "ws", TaskIntake: true, Message: "Prepare the task",
		Config: domain.OrchestratorConfig{Provider: domain.ProviderOllama, Model: "model"}}
	if _, err := service.Chat(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	for _, message := range seen.Messages {
		if strings.Contains(message.Content, "Предыдущий ответ") {
			t.Fatal("обычный ход просит другой путь без просьбы человека")
		}
	}
	req.PreviousAnswerRejected = true
	if _, err := service.Chat(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	asked := false
	for _, message := range seen.Messages {
		if strings.Contains(message.Content, "Предыдущий ответ") {
			asked = true
		}
	}
	if !asked {
		t.Fatal("просьба о другом пути не доехала до модели")
	}
}

// Как Мастер пришёл к ответу — часть ответа, а не служебный след.
//
// Рассуждение модели и раунды читающих инструментов ядро собирало с самого
// начала: первое уходило обратно в модель, второе — в контекст. Наружу не
// выходило ничего, и полторы минуты хода human видел одно слово «Думает…».
func TestMasterTurnShowsHowItGotThere(t *testing.T) {
	store := newChatStoreStub()
	service := ChatService{Store: store, ReadTools: readingToolsStub{}, ModelFactory: func(providers.Config) (providers.Model, error) {
		return &thinkingModel{reply: "Ready"}, nil
	}}
	response, err := service.Chat(context.Background(), ChatRequest{
		WorkspaceID: "ws", TaskIntake: true, Message: "Prepare the task",
		Config: domain.OrchestratorConfig{Provider: domain.ProviderOllama, Model: "model"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(response.Reasoning, "смотрю на ростер") {
		t.Fatalf("рассуждение не дошло до ответа: %q", response.Reasoning)
	}
	if len(response.Steps) != 1 {
		t.Fatalf("раунды инструментов потеряны: %#v", response.Steps)
	}
	step := response.Steps[0]
	if step.Tool != "read_file" || step.Argument != "internal/app/app.go" {
		t.Fatalf("раунд описан невнятно: %#v", step)
	}
	if step.Failed || step.Result == "" {
		t.Fatalf("удачный раунд выглядит неудачным или пустым: %#v", step)
	}
	// Сырой JSON аргументов в ленте разговора — шум, а не объяснение: наружу
	// уходит только то, что человеку что-то говорит.
	if strings.Contains(step.Argument, "{") {
		t.Fatalf("в раунд просочился сырой JSON: %q", step.Argument)
	}
	// Хранится вместе с репликой, иначе пропадёт при первом переоткрытии панели.
	var reply *domain.CompanionMessage
	for index := range store.messages {
		if store.messages[index].Role == "assistant" {
			reply = &store.messages[index]
		}
	}
	if reply == nil || reply.Reasoning == "" || len(reply.Steps) != 1 {
		t.Fatalf("путь к ответу не сохранён вместе с репликой: %#v", reply)
	}
}

// thinkingModel рассуждает, зовёт инструмент и только потом отвечает — как
// настоящая модель с включённым рассуждением.
type thinkingModel struct {
	reply  string
	called bool
}

func (m *thinkingModel) Stream(_ context.Context, _ providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	if err := emit(providers.ModelEvent{Kind: providers.EventReasoning, Reasoning: &providers.ReasoningBlock{Type: "text", Text: "смотрю на ростер и на файл приложения"}}); err != nil {
		return err
	}
	if !m.called {
		m.called = true
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{
			ID: "call-1", Name: "read_file", Arguments: json.RawMessage(`{"path":"internal/app/app.go"}`),
		}})
	}
	return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: m.reply})
}

// readingToolsStub отвечает так же, как настоящий читающий реестр.
type readingToolsStub struct{}

func (readingToolsStub) Definitions() []domain.ToolDefinition {
	return []domain.ToolDefinition{{Name: "read_file", Description: "read"}}
}

func (readingToolsStub) Execute(context.Context, string, json.RawMessage) domain.ToolResult {
	output, _ := json.Marshal("package app\n\nfunc New() {}")
	return domain.ToolResult{OK: true, Output: output}
}

func TestTaskIntakePromptKeepsInterviewCheap(t *testing.T) {
	prompt := taskIntakePrompt + NewMasterSkillSession("intake", nil).Prompt(nil, false)
	if !strings.Contains(prompt, "До двух существенных уточнений") {
		t.Fatal("prompt must cap clarifications at two")
	}
	if !strings.Contains(prompt, "Очевидные безопасные дефолты") {
		t.Fatal("prompt must prefer defaults over interview")
	}
	if !strings.Contains(prompt, "Текст ответа не пересказывает карточку") {
		t.Fatal("prompt must keep the card out of the reply text")
	}
	if !strings.Contains(prompt, "выбранная пользователем установка разрешает лишь нужные реестры") {
		t.Fatal("prompt must treat network hosts as consequence of stack choice")
	}
	if strings.Contains(taskIntakePrompt, "Сеть только явно согласованная, иначе []") {
		t.Fatal("old absolute network ban must not remain")
	}
	if strings.Contains(prompt, "reply — 1–3 предложения") {
		t.Fatal("the answer form, not the prompt, decides reply length")
	}
	if !strings.Contains(masterActionSchema(t, masterActionAskClarifications), `"maxItems":2`) {
		t.Fatal("schema must cap clarifications at two")
	}
}

// Слабая модель охотно заполняет proposalId выдуманным значением: поле есть в
// схеме, а проект пустой. Ход не имеет права падать из-за этого — иначе весь
// разбор сгорает, а человек видит «задание не найдено» на первом же сообщении.
func TestTaskIntakeIgnoresUnknownProposalIDFromModel(t *testing.T) {
	store := newChatStoreStub()
	brief := domain.TaskBrief{
		Mode: domain.TaskModePrecise, Goal: "Собрать REST-эндпоинт", ResultKind: "code",
		Criteria: []domain.AcceptanceCriterion{{ID: "c1", Text: "Эндпоинт отвечает", Kind: "manual"}},
	}
	model := &turnModel{rounds: []roundScript{{text: "Готово", calls: []providers.ToolCall{proposeBriefCall("b1", "Эндпоинт", "qp_из_воздуха", brief)}}}}
	service := ChatService{Store: store, ModelFactory: model.factory()}
	req := ChatRequest{
		WorkspaceID: "ws", TaskIntake: true, Message: "Сделай эндпоинт",
		Config: domain.OrchestratorConfig{Provider: domain.ProviderOllama, Model: "model"},
	}
	response, err := service.Chat(context.Background(), req)
	if err != nil {
		t.Fatalf("выдуманный идентификатор уронил ход: %v", err)
	}
	if response.Proposal == nil {
		t.Fatal("задание не сохранено")
	}
	if response.Proposal.ID == "qp_из_воздуха" {
		t.Fatal("идентификатор от модели стал идентификатором карточки")
	}

	// Тот же идентификатор от человека — настоящая ошибка: он указал карточку,
	// которой в этом проекте нет, и подменять её новой нельзя.
	req.ProposalID = "qp_из_воздуха"
	if _, err = service.Chat(context.Background(), req); err == nil {
		t.Fatal("ссылка человека на чужую карточку принята молча")
	} else if !strings.Contains(err.Error(), "не найдено в этом проекте") {
		t.Fatalf("другая ошибка: %v", err)
	}
}

// Уточнение с выбором обязано принести сам выбор.
//
// Модель вернула kind=single и пустой options — схема это пропускала (required
// требует ключ, а не содержимое), а нормализация не трогала: вид оставался
// «одиночный выбор», вариантов не было. В карточку ввода уезжал вопрос с нулём
// чипов и подписью «Выберите вариант или напишите ответ», выбирать в котором
// нечего. Вид уточнения задаёт список вариантов, а не слово от модели.
func TestClarificationWithoutOptionsBecomesFreeText(t *testing.T) {
	store := newChatStoreStub()
	brief := domain.TaskBrief{Mode: domain.TaskModePrecise, Goal: "Развернуть Symfony", ResultKind: "code"}
	model := &turnModel{rounds: []roundScript{{text: "Уточните", calls: []providers.ToolCall{
		proposeBriefCall("b1", "Symfony", "", brief),
		toolCall("q1", masterActionAskClarifications, map[string]any{"items": []domain.MasterQuestion{
			{Text: "Какой состав нужен?", Kind: "single"},
			{Text: "Какая версия Symfony?", Kind: "single", Options: []string{"7.1"}},
		}}),
	}}}}
	service := ChatService{Store: store, ModelFactory: model.factory()}
	req := ChatRequest{WorkspaceID: "ws", TaskIntake: true, Message: "Разверни Symfony", Config: domain.OrchestratorConfig{Provider: domain.ProviderOllama, Model: "model"}}
	response, err := service.Chat(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Clarifications) != 2 {
		t.Fatalf("оба уточнения должны дойти: %#v", response.Clarifications)
	}
	for _, question := range response.Clarifications {
		if question.Kind != "text" {
			t.Fatalf("выбор без вариантов остался выбором: %#v", question)
		}
		if len(question.Options) != 0 {
			t.Fatalf("у свободного ответа не должно быть вариантов: %#v", question)
		}
	}
}

// Схема обязана запрещать пустой options у выбора: промпт требует варианты
// словами, но грамматику слабая модель соблюдает надёжнее прозы.
func TestTaskIntakeSchemaDemandsOptionsForChoice(t *testing.T) {
	raw := masterActionSchema(t, masterActionAskClarifications)
	if !strings.Contains(raw, `"minItems":2`) {
		t.Fatal("schema must require at least two options for a choice")
	}
	if !strings.Contains(raw, `"maxItems":0`) {
		t.Fatal("schema must keep free-text clarifications without options")
	}
}

// Промпт и schema не дают Мастеру выбирать или создавать исполнителя.
func TestTaskIntakePromptDelegatesRosterToSelector(t *testing.T) {
	prompt := taskIntakePrompt + NewMasterSkillSession("intake", nil).Prompt(nil, false)
	if strings.Contains(taskIntakePrompt, "read_roster") {
		t.Fatal("Master still calls the legacy roster observer")
	}
	if !strings.Contains(prompt, "отдельный комплектовщик") {
		t.Fatal("prompt does not delegate composition to the dedicated selector")
	}
	// Прямая просьба создать агента — не задание. Без этой строки «создай
	// агента» становится квестом с таким названием: маршрут по ключевым словам
	// снят, и подхватить её больше некому.
	if !strings.Contains(prompt, "Прямую просьбу создать агента") {
		t.Fatal("prompt turns a hiring request into a task brief")
	}
	raw := masterActionSchema(t, masterActionProposeBrief)
	if strings.Contains(raw, `"hire"`) || strings.Contains(raw, `"agentIds"`) {
		t.Fatal("schema still grants Master roster authority")
	}
}

// Исполнителя заводят на последнем этапе квеста, а не по слову в реплике.
//
// В разговоре Мастера стояла лазейка по ключевым словам: intentOf уводил ход в
// старый маршрут, и тот сразу клал в очередь карточку найма. Разбор намеренно
// грубый — isTeamCreationRequest ловит подстроку «команд», — поэтому «добавь
// команды в CLI» посреди обсуждения работы давало человеку не разбор задания, а
// предложение нанять. Теперь нехватку исполнителя считает assessAgentGap по
// утверждённому заданию, а карточку готовит proposeRoleGapHire.
func TestTaskIntakeNeverProposesAgentByKeyword(t *testing.T) {
	for _, message := range []string{"добавь команды в CLI", "создай агента для бэкенда", "подбери отряд под задачу"} {
		store := newChatStoreStub()
		// Чертёж есть: без него старый маршрут промолчал бы и сам, и проверка
		// доказывала бы пустоту хранилища, а не снятую лазейку.
		store.blueprints = []domain.AgentBlueprint{{ID: "bp-1", Name: "Разработчик", RoleDescription: "правит бэкенд", Mission: "чинит команды CLI и отряд сборки"}}
		service := ChatService{
			Store: store,
			NewID: func(prefix string) string { return prefix + "-1" },
			ModelFactory: func(providers.Config) (providers.Model, error) {
				return &stubbornModel{replies: []string{"Исполнителя заводят в Гильдии или при утверждении квеста."}}, nil
			},
		}
		response, err := service.Chat(context.Background(), ChatRequest{
			WorkspaceID: "ws-1", TaskIntake: true, Message: message,
			Config: domain.OrchestratorConfig{Provider: domain.ProviderOllama, Model: "model"},
		})
		if err != nil {
			t.Fatalf("%q: %v", message, err)
		}
		if response.ActionProposal != nil || len(store.actionProposals) > 0 {
			t.Fatalf("%q: разговор положил в очередь карточку найма: %#v", message, store.actionProposals)
		}
		if response.Hire != nil {
			t.Fatalf("%q: разговор предложил нанять по ключевому слову", message)
		}
	}
}
