package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
)

// roundScript — один круг хода модели: что она думает, говорит и вызывает.
type roundScript struct {
	think string
	text  string
	calls []providers.ToolCall
}

// turnModel отыгрывает ход по кругам и запоминает каждый запрос. Когда круги
// кончились, повторяется последний — так проверяются упрямые модели.
type turnModel struct {
	rounds   []roundScript
	requests []providers.ModelRequest
}

func (m *turnModel) Stream(_ context.Context, req providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.requests = append(m.requests, req)
	round := m.rounds[min(len(m.requests)-1, len(m.rounds)-1)]
	if round.think != "" {
		if err := emit(providers.ModelEvent{Kind: providers.EventReasoning, Reasoning: &providers.ReasoningBlock{Type: "text", Text: round.think}}); err != nil {
			return err
		}
	}
	if round.text != "" {
		if err := emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: round.text}); err != nil {
			return err
		}
	}
	for i := range round.calls {
		call := round.calls[i]
		if err := emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &call}); err != nil {
			return err
		}
	}
	return nil
}

func (m *turnModel) factory() ModelFactory {
	m.requests = nil
	return func(providers.Config) (providers.Model, error) { return m, nil }
}

func toolCall(id, name string, arguments any) providers.ToolCall {
	raw, _ := json.Marshal(arguments)
	return providers.ToolCall{ID: id, Name: name, Arguments: raw}
}

func proposeBriefCall(id, title, proposalID string, brief domain.TaskBrief) providers.ToolCall {
	return toolCall(id, masterActionProposeBrief, map[string]any{"title": title, "proposalId": proposalID, "brief": brief})
}

func masterActionSchema(t *testing.T, name string) string {
	t.Helper()
	for _, definition := range masterActionDefinitions() {
		if definition.Name == name {
			return string(definition.InputSchema)
		}
	}
	t.Fatalf("нет инструмента разговора %q", name)
	return ""
}

func masterActionSchemas() []byte {
	raw, _ := json.Marshal(masterActionDefinitions())
	return raw
}

func intakeRequest(message string) ChatRequest {
	return ChatRequest{WorkspaceID: "ws", TaskIntake: true, Message: message, Config: domain.OrchestratorConfig{Provider: domain.ProviderOllama, Model: "model"}}
}

func validBrief() domain.TaskBrief {
	return domain.TaskBrief{Mode: domain.TaskModePrecise, Goal: "Bounded goal", ResultKind: "code", Criteria: []domain.AcceptanceCriterion{{ID: "c1", Text: "Done", Kind: "manual"}}}
}

// Вопрос — это вопрос. Прежний конверт заставлял модель заворачивать в JSON и
// ответ на «что делает этот файл?»; теперь текст идёт в ленту как есть, без
// карточки и без схемы в системном сообщении.
func TestMasterAnswersQuestionWithPlainText(t *testing.T) {
	store := newChatStoreStub()
	answer := "Файл `app.go` собирает ядро.\n\n- создаёт хранилище\n- поднимает HTTP"
	model := &turnModel{rounds: []roundScript{{text: answer}}}
	var replies []string
	service := ChatService{Store: store, ReadTools: readingToolsStub{}, ModelFactory: model.factory(), OnProgress: func(kind, text, _ string) {
		if kind == "reply" {
			replies = append(replies, text)
		}
	}}
	response, err := service.Chat(context.Background(), intakeRequest("Что делает app.go?"))
	if err != nil {
		t.Fatal(err)
	}
	if response.Reply != answer || response.Proposal != nil || response.Mode != "model" {
		t.Fatalf("вопрос стал заданием или текст потерян: %#v", response)
	}
	if len(replies) == 0 || replies[len(replies)-1] != answer {
		t.Fatalf("текст не дошёл до ленты потоком: %q", replies)
	}
	request := model.requests[0]
	if strings.Contains(request.Messages[0].Content, "Контракт JSON") || request.JSONSchema != nil {
		t.Fatal("в запросе остался JSON-конверт")
	}
	names := map[string]bool{}
	for _, tool := range request.Tools {
		names[tool.Name] = true
	}
	for _, want := range []string{"read_file", masterActionProposeBrief, masterActionAskClarifications, masterActionSuggestMemory} {
		if !names[want] {
			t.Fatalf("инструмент %s не предложен модели: %v", want, names)
		}
	}
}

// Предел исследования кончает поиск, а не ход. Раньше пятый круг с чтением
// ронял весь ответ ошибкой, и человек терял всё уже прочитанное.
func TestMasterExplorationLimitAnswersInsteadOfFailing(t *testing.T) {
	store := newChatStoreStub()
	model := &explorerModel{}
	skills := NewMasterSkillSession("intake", nil)
	var retries []string
	service := ChatService{Store: store, Skills: skills, ReadTools: readingToolsStub{}, ModelFactory: func(providers.Config) (providers.Model, error) { return model, nil },
		OnProgress: func(kind, text, _ string) {
			if kind == "retry" {
				retries = append(retries, text)
			}
		}}
	response, err := service.Chat(context.Background(), intakeRequest("Найди, где рвётся поток"))
	if err != nil {
		t.Fatalf("предел исследования уронил ход: %v", err)
	}
	if response.Reply != "Ответ по собранному." {
		t.Fatalf("ответ не получен: %q", response.Reply)
	}
	if len(model.requests) != masterExploreRounds+1 {
		t.Fatalf("кругов %d вместо %d", len(model.requests), masterExploreRounds+1)
	}
	for _, tool := range model.requests[masterExploreRounds].Tools {
		if !IsMasterActionTool(tool.Name) {
			t.Fatalf("после предела модели снова предложено чтение: %s", tool.Name)
		}
	}
	if skills.Operation.Repairs != 1 || len(retries) != 1 {
		t.Fatalf("вынужденный круг не учтён: repairs=%d retries=%q", skills.Operation.Repairs, retries)
	}
}

// explorerModel читает новый файл, пока ему дают читать, и отвечает, когда
// чтение забрали.
type explorerModel struct{ requests []providers.ModelRequest }

func (m *explorerModel) Stream(_ context.Context, req providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.requests = append(m.requests, req)
	for _, tool := range req.Tools {
		if tool.Name == "read_file" {
			call := toolCall(fmt.Sprintf("r%d", len(m.requests)), "read_file", map[string]string{"path": fmt.Sprintf("file%d.go", len(m.requests))})
			return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &call})
		}
	}
	return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "Ответ по собранному."})
}

// Размышляющая модель часто оформляет задание молча. Лишний круг ради
// вежливой фразы стоил бы полминуты ожидания — ход заканчивается сразу.
func TestMasterSilentBriefEndsTurnWithShortReply(t *testing.T) {
	store := newChatStoreStub()
	model := &turnModel{rounds: []roundScript{{think: "это поручение", calls: []providers.ToolCall{proposeBriefCall("b1", "Цель", "", validBrief())}}}}
	service := ChatService{Store: store, ModelFactory: model.factory()}
	response, err := service.Chat(context.Background(), intakeRequest("Сделай ограниченную цель"))
	if err != nil {
		t.Fatal(err)
	}
	if len(model.requests) != 1 {
		t.Fatalf("ради пустой реплики сделан лишний круг: %d", len(model.requests))
	}
	if response.Proposal == nil || response.Reply != "Оформил задание — карточка ниже." {
		t.Fatalf("задание или короткий ответ потеряны: %#v", response)
	}
}

// Что модель сказала перед чтением файла, человек уже видел в потоке: итог
// хода обязан это сохранить, а не показать только последний круг.
func TestMasterReplyKeepsTextOfEveryRound(t *testing.T) {
	store := newChatStoreStub()
	model := &turnModel{rounds: []roundScript{
		{text: "Посмотрю файл.", calls: []providers.ToolCall{toolCall("r1", "read_file", map[string]string{"path": "app.go"})}},
		{text: "Нашёл причину."},
	}}
	service := ChatService{Store: store, ReadTools: readingToolsStub{}, ModelFactory: model.factory()}
	response, err := service.Chat(context.Background(), intakeRequest("Почему падает?"))
	if err != nil {
		t.Fatal(err)
	}
	if response.Reply != "Посмотрю файл.\n\nНашёл причину." {
		t.Fatalf("текст кругов потерян: %q", response.Reply)
	}
}

// Девятое обращение за круг получает отказ, а ход продолжается: раньше
// лишний вызов ронял весь ответ.
func TestMasterRoundCallLimitRefusesOnlyTheExcess(t *testing.T) {
	store := newChatStoreStub()
	var calls []providers.ToolCall
	for i := 0; i <= masterCallsPerRound; i++ {
		calls = append(calls, toolCall(fmt.Sprintf("r%d", i), "read_file", map[string]string{"path": fmt.Sprintf("f%d.go", i)}))
	}
	model := &turnModel{rounds: []roundScript{{calls: calls}, {text: "Готово"}}}
	service := ChatService{Store: store, ReadTools: readingToolsStub{}, ModelFactory: model.factory()}
	response, err := service.Chat(context.Background(), intakeRequest("Прочитай всё"))
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Steps) != masterCallsPerRound+1 {
		t.Fatalf("шаги потеряны: %#v", response.Steps)
	}
	for i, step := range response.Steps {
		if failed := i >= masterCallsPerRound; step.Failed != failed {
			t.Fatalf("шаг %d: failed=%v", i, step.Failed)
		}
	}
}

// Упрямая модель стоит карточки квеста, а не всего разговора и не всех кругов:
// после трёх отвергнутых заданий ход закрывается с её репликой.
func TestTaskIntakeKeepsReplyWhenBriefNeverPasses(t *testing.T) {
	store := newChatStoreStub()
	broken := validBrief()
	broken.Mode = "nonsense"
	model := &turnModel{rounds: []roundScript{{text: "Понял, уточню объём работ.", calls: []providers.ToolCall{proposeBriefCall("b", "Приложение", "", broken)}}}}
	skills := NewMasterSkillSession("intake", nil)
	service := ChatService{Store: store, Skills: skills, ModelFactory: model.factory()}
	response, err := service.Chat(context.Background(), intakeRequest("Сделай приложение"))
	if err != nil {
		t.Fatal(err)
	}
	if response.FallbackReason != "" || response.Mode != "model" || response.Proposal != nil {
		t.Fatalf("ход упал вместо мягкой уступки: %#v", response)
	}
	if response.Reply != "Понял, уточню объём работ." {
		t.Fatalf("реплика модели потеряна или повторена: %q", response.Reply)
	}
	if !strings.Contains(response.Reasoning, "Задание не оформлено") {
		t.Fatalf("причина отсутствия карточки не названа: %q", response.Reasoning)
	}
	if len(model.requests) != masterBriefAttempts || !skills.Operation.ContractError {
		t.Fatalf("кругов %d, contractError=%v", len(model.requests), skills.Operation.ContractError)
	}
}

// Задание строкой вместо объекта — та же структура; отказ из-за кавычек
// стоил бы человеку хода.
func TestProposeBriefAcceptsBriefSerializedAsString(t *testing.T) {
	raw, _ := json.Marshal(validBrief())
	actions := &masterActions{}
	result := actions.execute(masterActionProposeBrief, mustJSON(t, map[string]any{"title": "Цель", "brief": string(raw)}))
	if !result.OK || actions.brief == nil {
		t.Fatalf("задание строкой отвергнуто: %#v", result)
	}
}

// Реплей обучения пишется новым форматом и хранит только инструменты
// разговора: чтение проекта уже отработало и едет свидетельством.
func TestMasterReplayKeepsOnlyConversationTools(t *testing.T) {
	store := newChatStoreStub()
	model := &turnModel{rounds: []roundScript{
		{calls: []providers.ToolCall{toolCall("r1", "read_file", map[string]string{"path": "app.go"})}},
		{text: "Оформил.", calls: []providers.ToolCall{proposeBriefCall("b1", "Цель", "", validBrief())}},
	}}
	skills := NewMasterSkillSession("intake", nil)
	service := ChatService{Store: store, Skills: skills, ReadTools: readingToolsStub{}, ModelFactory: model.factory()}
	if _, err := service.Chat(context.Background(), intakeRequest("Сделай цель")); err != nil {
		t.Fatal(err)
	}
	raw, ok := domain.DecodeMasterReplay(skills.Operation.Replay)
	if !ok {
		t.Fatalf("реплей записан не нынешним форматом: %q", skills.Operation.Replay)
	}
	var request providers.ModelRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		t.Fatal(err)
	}
	if len(request.Tools) != len(masterActionDefinitions()) {
		t.Fatalf("в реплей попали чужие инструменты: %#v", request.Tools)
	}
	for _, tool := range request.Tools {
		if !IsMasterActionTool(tool.Name) {
			t.Fatalf("в реплее инструмент проекта: %s", tool.Name)
		}
	}
	if _, legacy := domain.DecodeMasterReplay(`{"model":"m","messages":[]}`); legacy {
		t.Fatal("прежний формат реплея принят за нынешний")
	}
}

func TestReplayScoreJudgesTextAndConversationCalls(t *testing.T) {
	good := EncodeMasterReplayOutput("Оформил.", []providers.ToolCall{proposeBriefCall("b1", "Цель", "", validBrief())})
	if score, err := ReplayScore("intake", good); err != nil || score != 1 {
		t.Fatalf("годный ответ отвергнут: %d %v", score, err)
	}
	if score, err := ReplayScore("intake", "Просто ответ на вопрос."); err != nil || score != 1 {
		t.Fatalf("ответ текстом отвергнут: %d %v", score, err)
	}
	broken := validBrief()
	broken.Mode = "nonsense"
	if _, err := ReplayScore("intake", EncodeMasterReplayOutput("", []providers.ToolCall{proposeBriefCall("b1", "Цель", "", broken)})); err == nil {
		t.Fatal("неверное задание прошло оценку")
	}
	if _, err := ReplayScore("intake", EncodeMasterReplayOutput("", []providers.ToolCall{toolCall("r1", "run_command", map[string]string{"command": "rm"})})); err == nil {
		t.Fatal("реплей вызвал инструмент проекта и прошёл оценку")
	}
	if _, err := ReplayScore("intake", EncodeMasterReplayOutput("", nil)); err == nil {
		t.Fatal("пустой ответ прошёл оценку")
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// Правила проекта едут отдельным сообщением сразу за системным и названы в
// основаниях ответа: человек видит, что Мастер их прочёл.
func TestMasterTurnCarriesProjectRules(t *testing.T) {
	store := newChatStoreStub()
	model := &turnModel{rounds: []roundScript{{text: "По правилам проекта — по-русски."}}}
	service := ChatService{Store: store, ModelFactory: model.factory()}
	req := intakeRequest("Как у нас принято отвечать?")
	req.ProjectRules = "## AGENTS.md\nОтвечать по-русски."
	req.RuleSources = []string{"AGENTS.md"}
	response, err := service.Chat(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	messages := model.requests[0].Messages
	if len(messages) < 3 || !strings.Contains(messages[1].Content, "UNTRUSTED PROJECT CONVENTIONS") || !strings.Contains(messages[1].Content, "Отвечать по-русски.") {
		t.Fatalf("правила не доехали вторым сообщением: %#v", messages[:min(3, len(messages))])
	}
	if !strings.Contains(messages[2].Content, "UNTRUSTED PROJECT EVIDENCE") {
		t.Fatal("снимок мира сместился со своего места")
	}
	found := false
	for _, fact := range response.Facts {
		found = found || fact == "Правила проекта: AGENTS.md"
	}
	if !found {
		t.Fatalf("правила не названы в основаниях: %q", response.Facts)
	}
}

// Размышляющая модель, трижды молча приславшая негодное задание, — это
// несложившееся задание, а не сбой модели: ход отвечает человеку, что задание
// не прошло проверку, а не «проверьте модель».
func TestSilentRejectedBriefsAnswerInsteadOfFailing(t *testing.T) {
	store := newChatStoreStub()
	broken := validBrief()
	broken.Mode = "nonsense"
	model := &turnModel{rounds: []roundScript{{think: "оформлю", calls: []providers.ToolCall{proposeBriefCall("b", "Приложение", "", broken)}}}}
	service := ChatService{Store: store, ModelFactory: model.factory()}
	response, err := service.Chat(context.Background(), intakeRequest("Сделай приложение"))
	if err != nil {
		t.Fatal(err)
	}
	if response.Mode != "model" || response.FallbackReason != "" {
		t.Fatalf("молчаливые отказы уронили ход: %q %q", response.Mode, response.FallbackReason)
	}
	if !strings.Contains(response.Reply, "не прошло проверку") || !strings.Contains(response.Reasoning, "Задание не оформлено") {
		t.Fatalf("причина не названа: reply=%q reasoning=%q", response.Reply, response.Reasoning)
	}
}

// Предложение запомнить — попутный вызов: ход после него обязан ответить на
// сам вопрос, а не закончиться фразой про память.
func TestSuggestMemoryDoesNotEndTheTurn(t *testing.T) {
	store := newChatStoreStub()
	model := &turnModel{rounds: []roundScript{
		{calls: []providers.ToolCall{toolCall("m1", masterActionSuggestMemory, map[string]any{"entries": []string{"Предпочитает табы"}})}},
		{text: "app.go собирает ядро и поднимает HTTP."},
	}}
	service := ChatService{Store: store, ModelFactory: model.factory()}
	response, err := service.Chat(context.Background(), intakeRequest("Что делает app.go? И запомни: я предпочитаю табы"))
	if err != nil {
		t.Fatal(err)
	}
	if len(model.requests) != 2 || response.Reply != "app.go собирает ядро и поднимает HTTP." {
		t.Fatalf("вопрос остался без ответа: rounds=%d reply=%q", len(model.requests), response.Reply)
	}
	if len(response.MemorySuggestions) != 1 {
		t.Fatalf("предложение памяти потеряно: %q", response.MemorySuggestions)
	}
}

// Большие файлы, прочитанные одним кругом, на маленьком окне не роняют ход:
// результаты текущего круга ужимаются последней жертвой, и лента это видит.
func TestCurrentRoundToolResultsAreCompactedLast(t *testing.T) {
	var calls []providers.ToolCall
	for i := 0; i < 5; i++ {
		calls = append(calls, toolCall(fmt.Sprintf("r%d", i), "read_file", map[string]string{"path": fmt.Sprintf("big%d.go", i)}))
	}
	measure := &turnModel{rounds: []roundScript{{text: "Готово"}}}
	base := ChatService{Store: newChatStoreStub(), ReadTools: bigReadTools{}, ModelFactory: measure.factory()}
	if _, err := base.Chat(context.Background(), intakeRequest("Прочитай файлы")); err != nil {
		t.Fatal(err)
	}
	room := intakeContextTokens(measure.requests[0]) + 4000

	model := &turnModel{rounds: []roundScript{{text: "Прочитаю файлы.", calls: calls}, {text: "Готово"}}}
	var compacted []string
	service := ChatService{Store: newChatStoreStub(), ReadTools: bigReadTools{}, ModelFactory: model.factory(), OnProgress: func(kind, text, _ string) {
		if kind == "retry" {
			compacted = append(compacted, text)
		}
	}}
	req := intakeRequest("Прочитай файлы")
	req.ContextWindowTokens = room
	response, err := service.Chat(context.Background(), req)
	if err != nil || response.Mode != "model" {
		t.Fatalf("большие файлы уронили ход: %v %q", err, response.FallbackReason)
	}
	if response.Reply != "Прочитаю файлы.\n\nГотово" {
		t.Fatalf("ответ потерян: %q", response.Reply)
	}
	if len(compacted) == 0 || !strings.Contains(strings.Join(compacted, " "), "текущего круга") {
		t.Fatalf("сжатие текущего круга не показано: %q", compacted)
	}
}

// bigReadTools отвечает на любое чтение файлом в 16 КБ.
type bigReadTools struct{}

func (bigReadTools) Definitions() []domain.ToolDefinition {
	return []domain.ToolDefinition{{Name: "read_file", Description: "read"}}
}

func (bigReadTools) Execute(context.Context, string, json.RawMessage) domain.ToolResult {
	output, _ := json.Marshal(strings.Repeat("x", 16*1024-64))
	return domain.ToolResult{OK: true, Output: output}
}

// Последний круг хода не уходит без определений инструментов: история уже
// несёт вызовы, и провайдер вправе отвергнуть такой запрос.
func TestFinalRoundKeepsConversationToolDefinitions(t *testing.T) {
	broken := validBrief()
	broken.Mode = "nonsense"
	model := &stubbornExplorer{broken: broken}
	service := ChatService{Store: newChatStoreStub(), ReadTools: readingToolsStub{}, ModelFactory: func(providers.Config) (providers.Model, error) { return model, nil }}
	if _, err := service.Chat(context.Background(), intakeRequest("Разберись")); err != nil {
		t.Fatal(err)
	}
	last := model.requests[len(model.requests)-1]
	if len(model.requests) != masterIntakeRounds || len(last.Tools) == 0 {
		t.Fatalf("последний круг без инструментов: rounds=%d tools=%d", len(model.requests), len(last.Tools))
	}
	for _, tool := range last.Tools {
		if !IsMasterActionTool(tool.Name) {
			t.Fatalf("в последнем круге снова чтение проекта: %s", tool.Name)
		}
	}
}

// stubbornExplorer читает, пока дают, затем один раз присылает негодное
// задание — так ход доходит до последнего круга.
type stubbornExplorer struct {
	broken   domain.TaskBrief
	requests []providers.ModelRequest
}

func (m *stubbornExplorer) Stream(_ context.Context, req providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.requests = append(m.requests, req)
	for _, tool := range req.Tools {
		if tool.Name == "read_file" {
			call := toolCall(fmt.Sprintf("r%d", len(m.requests)), "read_file", map[string]string{"path": fmt.Sprintf("f%d.go", len(m.requests))})
			return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &call})
		}
	}
	if len(m.requests) == masterExploreRounds+1 {
		call := proposeBriefCall("b", "Цель", "", m.broken)
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &call})
	}
	return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "Итог по собранному."})
}

// Вопрос длиннее тысячи знаков DiscussTask отбросит; модель узнаёт об этом
// сразу, а не человек — по пустому месту под «вопросы ниже».
func TestAskClarificationsRejectsOverlongQuestion(t *testing.T) {
	actions := &masterActions{}
	result := actions.execute(masterActionAskClarifications, mustJSON(t, map[string]any{"items": []map[string]any{{"text": strings.Repeat("в", 1001), "kind": "text"}}}))
	if result.OK || len(actions.clarifications) != 0 {
		t.Fatal("вопрос длиннее предела принят")
	}
}

// То же, но с историей разговора: выброшенные ранние реплики сдвигают индексы,
// и сжатие текущего круга обязано найти его заново, а не пропустить начало.
func TestCurrentRoundCompactionSurvivesDroppedHistory(t *testing.T) {
	var calls []providers.ToolCall
	for i := 0; i < 5; i++ {
		calls = append(calls, toolCall(fmt.Sprintf("r%d", i), "read_file", map[string]string{"path": fmt.Sprintf("big%d.go", i)}))
	}
	history := func() *chatStoreStub {
		store := newChatStoreStub()
		for i := 0; i < 6; i++ {
			store.messages = append(store.messages,
				domain.CompanionMessage{Speaker: "master", Role: "user", Content: fmt.Sprintf("ранний вопрос %d", i)},
				domain.CompanionMessage{Speaker: "master", Role: "assistant", Content: fmt.Sprintf("ранний ответ %d", i)},
			)
		}
		return store
	}
	measure := &turnModel{rounds: []roundScript{{text: "Готово"}}}
	base := ChatService{Store: newChatStoreStub(), ReadTools: bigReadTools{}, ModelFactory: measure.factory()}
	if _, err := base.Chat(context.Background(), intakeRequest("Прочитай файлы")); err != nil {
		t.Fatal(err)
	}
	room := intakeContextTokens(measure.requests[0]) + 4000

	model := &turnModel{rounds: []roundScript{{calls: calls}, {text: "Готово"}}}
	service := ChatService{Store: history(), ReadTools: bigReadTools{}, ModelFactory: model.factory()}
	req := intakeRequest("Прочитай файлы")
	req.ContextWindowTokens = room
	response, err := service.Chat(context.Background(), req)
	if err != nil || response.Mode != "model" || response.Reply != "Готово" {
		t.Fatalf("история сломала сжатие текущего круга: err=%v mode=%q reply=%q fallback=%q", err, response.Mode, response.Reply, response.FallbackReason)
	}
	if got := intakeContextTokens(model.requests[len(model.requests)-1]); got > room {
		t.Fatalf("последний запрос не влез в окно: %d > %d", got, room)
	}
}
