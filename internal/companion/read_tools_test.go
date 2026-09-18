package companion_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/agent"
	"local-agent-workbench/internal/companion"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
	"local-agent-workbench/internal/storage"
	"local-agent-workbench/internal/workspace"
)

// Модель, которая на каждом круге просит инструмент, пока ей его предлагают, а
// без инструментов отвечает конвертом. Так проверяется и обычный ход, и
// потолок: настоящая модель ведёт себя так же, если задача ей не по зубам.
type toolHungryModel struct {
	requests []providers.ModelRequest
	answer   string
}

func (m *toolHungryModel) Stream(_ context.Context, request providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.requests = append(m.requests, request)
	if len(request.Tools) > 0 {
		// Аргументы разные на каждом круге: одинаковый вызов отсекается как
		// повтор, и потолок тогда проверялся бы не он, а дедупликация.
		call := providers.ToolCall{
			ID:        fmt.Sprintf("call-%d", len(m.requests)),
			Name:      "git_branches",
			Arguments: json.RawMessage(fmt.Sprintf(`{"attempt":%d}`, len(m.requests))),
		}
		if err := emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &call}); err != nil {
			return err
		}
		return emit(providers.ModelEvent{Kind: providers.EventUsage, InputTokens: 40, OutputTokens: 8})
	}
	if err := emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: m.answer}); err != nil {
		return err
	}
	return emit(providers.ModelEvent{Kind: providers.EventUsage, InputTokens: 60, OutputTokens: 12})
}

// Модель, которая просит инструмент один раз — и только если его предложили, —
// а дальше отвечает по результату. Просить неполученное она не должна: так же
// ведёт себя настоящая модель, которой список инструментов не показали.
type oneCallModel struct {
	requests  []providers.ModelRequest
	toolReply string
}

func (m *oneCallModel) Stream(_ context.Context, request providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.requests = append(m.requests, request)
	if len(m.requests) == 1 && len(request.Tools) > 0 {
		call := providers.ToolCall{ID: "call-1", Name: "git_branches", Arguments: json.RawMessage(`{}`)}
		if err := emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &call}); err != nil {
			return err
		}
		return emit(providers.ModelEvent{Kind: providers.EventUsage, InputTokens: 40, OutputTokens: 8})
	}
	if err := emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: m.toolReply}); err != nil {
		return err
	}
	return emit(providers.ModelEvent{Kind: providers.EventUsage, InputTokens: 70, OutputTokens: 15})
}

// Модель, которая просит инструмент на каждом круге — и тогда, когда их уже не
// предлагают. Так ведёт себя настоящая: в логе разговора о diff коммитов Qwen
// продолжала звать git_diff после потолка, и цикл шёл, пока ядро не оборвало
// ответ по таймауту.
type relentlessToolModel struct {
	requests []providers.ModelRequest
}

func (m *relentlessToolModel) Stream(_ context.Context, request providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.requests = append(m.requests, request)
	// Тоже с разными аргументами: здесь проверяется потолок, а не повтор.
	call := providers.ToolCall{
		ID:        fmt.Sprintf("call-%d", len(m.requests)),
		Name:      "git_branches",
		Arguments: json.RawMessage(fmt.Sprintf(`{"attempt":%d}`, len(m.requests))),
	}
	if err := emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &call}); err != nil {
		return err
	}
	return emit(providers.ModelEvent{Kind: providers.EventUsage, InputTokens: 40, OutputTokens: 8})
}

// Модель, которая присылает конверт, только если формат назван в этом круге.
// Так ведёт себя настоящая: схема стоит в системном сообщении, и к концу
// разговора её отделяют результаты инструментов и прошлые реплики.
type formatForgetfulModel struct {
	requests []providers.ModelRequest
	answer   string
	asked    bool
}

func (m *formatForgetfulModel) Stream(_ context.Context, request providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.requests = append(m.requests, request)
	if len(request.Tools) > 0 && !m.asked {
		m.asked = true
		call := providers.ToolCall{ID: "call-1", Name: "git_branches", Arguments: json.RawMessage(`{}`)}
		if err := emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &call}); err != nil {
			return err
		}
		return emit(providers.ModelEvent{Kind: providers.EventUsage, InputTokens: 40, OutputTokens: 8})
	}
	reminded := false
	for _, message := range request.Messages {
		if strings.Contains(message.Content, "Reminder about the answer format") {
			reminded = true
		}
	}
	reply := "Ветки: master и dev."
	if reminded {
		reply = m.answer
	}
	if err := emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: reply}); err != nil {
		return err
	}
	return emit(providers.ModelEvent{Kind: providers.EventUsage, InputTokens: 60, OutputTokens: 12})
}

// Модель, которая просит один и тот же вызов с теми же аргументами, пока ей
// предлагают инструменты, — так вёл себя живой разговор о diff коммитов.
type repeatingToolModel struct {
	requests []providers.ModelRequest
	answer   string
}

func (m *repeatingToolModel) Stream(_ context.Context, request providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.requests = append(m.requests, request)
	if len(request.Tools) > 0 {
		call := providers.ToolCall{ID: fmt.Sprintf("call-%d", len(m.requests)), Name: "git_branches", Arguments: json.RawMessage(`{}`)}
		if err := emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &call}); err != nil {
			return err
		}
		return emit(providers.ModelEvent{Kind: providers.EventUsage, InputTokens: 40, OutputTokens: 8})
	}
	if err := emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: m.answer}); err != nil {
		return err
	}
	return emit(providers.ModelEvent{Kind: providers.EventUsage, InputTokens: 60, OutputTokens: 12})
}

// Модель, у которой круг обрывается на полуслове: клиент отвалился, ядро сняло
// запрос по таймауту. Ровно так выглядел живой отказ «Локальное ядро не ответило
// за 90 с».
type brokenRoundModel struct {
	cancel context.CancelFunc
}

func (m *brokenRoundModel) Stream(ctx context.Context, _ providers.ModelRequest, _ func(providers.ModelEvent) error) error {
	m.cancel()
	return ctx.Err()
}

// Инструмент, который отвечает отказом: так ведёт себя git в песочнице без
// метаданных или чтение файла за границей рабочей папки.
type failingReadTools struct{}

func (t *failingReadTools) Definitions() []domain.ToolDefinition {
	return []domain.ToolDefinition{{Name: "git_branches", Description: "list branches", InputSchema: json.RawMessage(`{"type":"object"}`)}}
}

func (t *failingReadTools) Execute(_ context.Context, _ string, _ json.RawMessage) domain.ToolResult {
	return domain.ToolResult{OK: false, Error: &domain.ToolError{Code: "git_unavailable", Message: "workspace is not a usable Git repository"}}
}

// Инструмент с огромной выдачей: так отвечает чтение большого файла или длинная
// история коммитов — модель видит только начало.
type hugeReadTools struct{}

func (t *hugeReadTools) Definitions() []domain.ToolDefinition {
	// Имя то же, что зовёт модель в этих тестах: сценарий проверяет обрезку, а
	// не выбор инструмента.
	return []domain.ToolDefinition{{Name: "git_branches", Description: "list branches", InputSchema: json.RawMessage(`{"type":"object"}`)}}
}

func (t *hugeReadTools) Execute(_ context.Context, _ string, _ json.RawMessage) domain.ToolResult {
	return domain.ToolResult{OK: true, Output: json.RawMessage(`"` + strings.Repeat("a", 9000) + `"`)}
}

// Рабочая папка с неполным индексом: так выглядит большой репозиторий, где
// обход остановился на пределе записей.
type partialIndexContext struct{}

func (c *partialIndexContext) ProjectMap(_ context.Context, _ int) (workspace.ProjectMap, error) {
	return workspace.ProjectMap{Status: workspace.IndexStatus{
		State: "ready", Files: 511, Symbols: 30, Partial: true, LimitReason: "entries",
	}}, nil
}

func (c *partialIndexContext) SearchContextWithRelations(_ context.Context, _ string, _, _ int, _ bool) (workspace.ContextSearchResult, error) {
	return workspace.ContextSearchResult{}, nil
}

// Индекс, собранный некоторое время назад: снимок отстаёт от диска ровно на
// столько, сколько человек успел поправить после сборки.
type agedIndexContext struct{ age time.Duration }

func (c *agedIndexContext) ProjectMap(_ context.Context, _ int) (workspace.ProjectMap, error) {
	return workspace.ProjectMap{Status: workspace.IndexStatus{
		State: "ready", Files: 511, Symbols: 30, BuiltAt: time.Now().Add(-c.age),
	}}, nil
}

func (c *agedIndexContext) SearchContextWithRelations(_ context.Context, _ string, _, _ int, _ bool) (workspace.ContextSearchResult, error) {
	return workspace.ContextSearchResult{}, nil
}

type fakeReadTools struct {
	calls  []string
	output string
}

func (t *fakeReadTools) Definitions() []domain.ToolDefinition {
	return []domain.ToolDefinition{{Name: "git_branches", Description: "list branches", InputSchema: json.RawMessage(`{"type":"object"}`)}}
}

func (t *fakeReadTools) Execute(_ context.Context, name string, _ json.RawMessage) domain.ToolResult {
	t.calls = append(t.calls, name)
	return domain.ToolResult{OK: true, Output: json.RawMessage(t.output)}
}

func companionToolStore(t *testing.T, name string) (*storage.SQLite, string) {
	t.Helper()
	store, err := storage.Open(filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	ctx := context.Background()
	workspaceID := "ws-companion-tools"
	_ = store.SaveWorkspace(ctx, domain.Workspace{ID: workspaceID, Path: t.TempDir(), Name: "demo"})
	_ = store.SaveCompanionConfig(ctx, domain.CompanionConfig{
		ID: "companion-tools", WorkspaceID: workspaceID, Preset: "balanced", Provider: domain.ProviderOllama,
		BaseURL: "http://127.0.0.1:11434", Model: "planner", Temperature: 0.2, MaxOutputTokens: 1200,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	return store, workspaceID
}

func TestCompanionAnswersFromReadToolResult(t *testing.T) {
	store, workspaceID := companionToolStore(t, "companion-tool-ok.db")
	tools := &fakeReadTools{output: `{"current":"master","branches":[{"name":"master"},{"name":"feature/x"}]}`}
	model := &oneCallModel{toolReply: `{"reply":"Ветки: master и feature/x.","level":"suggestion","questions":[],"proposal":null}`}
	svc := companion.Service{
		Store: store, Tools: tools,
		ModelFactory: func(providers.Config) (providers.Model, error) { return model, nil },
	}
	response, err := svc.Chat(context.Background(), companion.ChatRequest{WorkspaceID: workspaceID, Message: "Какие ветки в проекте?"})
	if err != nil {
		t.Fatal(err)
	}
	if response.Mode != "model" || response.FallbackReason != "" {
		t.Fatalf("ответ не от модели: mode=%q reason=%q", response.Mode, response.FallbackReason)
	}
	if len(tools.calls) != 1 || tools.calls[0] != "git_branches" {
		t.Fatalf("инструмент не вызван: %v", tools.calls)
	}
	if len(model.requests) != 2 {
		t.Fatalf("ожидалось два круга, было %d", len(model.requests))
	}
	// Второй круг обязан нести и вызов, и его результат: без пары модель не
	// свяжет ответ инструмента со своей же просьбой.
	roles := make([]string, 0, len(model.requests[1].Messages))
	toolPayload := ""
	for _, message := range model.requests[1].Messages {
		roles = append(roles, message.Role)
		if message.Role == "tool" {
			toolPayload = message.Content
		}
	}
	if !strings.Contains(strings.Join(roles, ","), "assistant,tool") {
		t.Fatalf("пара «вызов — результат» не дошла: %v", roles)
	}
	if !strings.Contains(toolPayload, "feature/x") {
		t.Fatalf("результат инструмента не дошёл до модели: %q", toolPayload)
	}
	if response.Reply != "Ветки: master и feature/x." {
		t.Fatalf("ответ не по результату инструмента: %q", response.Reply)
	}
}

func TestCompanionToolLoopStopsAtCeiling(t *testing.T) {
	store, workspaceID := companionToolStore(t, "companion-tool-ceiling.db")
	tools := &fakeReadTools{output: `{"branches":[]}`}
	model := &toolHungryModel{answer: `{"reply":"Больше смотреть нечего.","level":"suggestion","questions":[],"proposal":null}`}
	svc := companion.Service{
		Store: store, Tools: tools,
		ModelFactory: func(providers.Config) (providers.Model, error) { return model, nil },
	}
	response, err := svc.Chat(context.Background(), companion.ChatRequest{WorkspaceID: workspaceID, Message: "Посмотри всё"})
	if err != nil {
		t.Fatal(err)
	}
	// Три круга с инструментами и один без: на последнем модель обязана
	// ответить словами, а не просить вызов, выполнять который уже некому.
	if len(model.requests) != 4 {
		t.Fatalf("потолок не сработал: кругов %d", len(model.requests))
	}
	if len(tools.calls) != 3 {
		t.Fatalf("вызовов инструмента %d вместо трёх", len(tools.calls))
	}
	if len(model.requests[3].Tools) != 0 {
		t.Fatalf("на последнем круге инструменты всё ещё предлагались")
	}
	if response.Mode != "model" || response.Reply != "Больше смотреть нечего." {
		t.Fatalf("после потолка ответа модели нет: mode=%q reply=%q", response.Mode, response.Reply)
	}
}

func TestCompanionToolLoopStopsWhenModelIgnoresMissingTools(t *testing.T) {
	store, workspaceID := companionToolStore(t, "companion-tool-relentless.db")
	tools := &fakeReadTools{output: `{"branches":[]}`}
	model := &relentlessToolModel{}
	svc := companion.Service{
		Store: store, Tools: tools,
		ModelFactory: func(providers.Config) (providers.Model, error) { return model, nil },
	}
	response, err := svc.Chat(context.Background(), companion.ChatRequest{WorkspaceID: workspaceID, Message: "Покажи изменения в последних коммитах"})
	if err != nil {
		t.Fatal(err)
	}
	// Потолок обязан держаться и против модели, которая просьбу о вызове не
	// прекращает. Инструмент исполняется ровно три раза: дальше его на круге
	// не предлагают, и работать больше не над чем.
	if len(tools.calls) != 3 {
		t.Fatalf("вызовов инструмента %d вместо трёх — цикл не остановился", len(tools.calls))
	}
	// Три круга с инструментами, один без и один исправляющий: конверта модель
	// так и не прислала. Больше кругов означало бы, что вызов после потолка
	// снова исполняется.
	if len(model.requests) > 5 {
		t.Fatalf("кругов к модели %d — цикл ушёл дальше потолка", len(model.requests))
	}
	// Ответить словами она отказалась, поэтому человек получает откат с
	// причиной, а не молчание до таймаута ядра.
	if response.Mode == "model" {
		t.Fatalf("ответ выдан за модельный, хотя конверта не было: %q", response.Reply)
	}
	if response.Reply == "" {
		t.Fatal("человек остался без ответа")
	}
}

func TestCompanionRemindsEnvelopeInsteadOfRepairRound(t *testing.T) {
	store, workspaceID := companionToolStore(t, "companion-envelope-reminder.db")
	tools := &fakeReadTools{output: `{"branches":[]}`}
	model := &formatForgetfulModel{answer: `{"reply":"Ветки: master и dev.","level":"suggestion","questions":[],"proposal":null}`}
	svc := companion.Service{
		Store: store, Tools: tools,
		ModelFactory: func(providers.Config) (providers.Model, error) { return model, nil },
	}
	response, err := svc.Chat(context.Background(), companion.ChatRequest{WorkspaceID: workspaceID, Message: "Какие есть ветки?"})
	if err != nil {
		t.Fatal(err)
	}
	// Два круга: вызов инструмента и ответ. Третий — исправляющий — означал бы,
	// что формат снова напоминают уже после испорченного ответа.
	if len(model.requests) != 2 {
		t.Fatalf("кругов к модели %d: исправляющий круг вернулся", len(model.requests))
	}
	if response.Mode != "model" || response.Reply != "Ветки: master и dev." {
		t.Fatalf("ответ не пришёл конвертом с первого раза: mode=%q reply=%q", response.Mode, response.Reply)
	}
	// Напоминание живёт только в своём круге: в переписке оно копилось бы и
	// уезжало в историю разговора.
	if strings.Contains(model.requests[0].Messages[len(model.requests[0].Messages)-1].Content, "Reminder about the answer format") {
		t.Fatal("напоминание попало в круг, где ещё выбирают инструмент")
	}
	messages, err := store.ListChatMessages(context.Background(), workspaceID, "companion", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range messages {
		if strings.Contains(message.Content, "Reminder about the answer format") {
			t.Fatal("напоминание сохранилось в истории разговора")
		}
	}
}

func TestCompanionDoesNotRepeatIdenticalToolCall(t *testing.T) {
	store, workspaceID := companionToolStore(t, "companion-tool-repeat.db")
	tools := &fakeReadTools{output: `{"branches":[]}`}
	model := &repeatingToolModel{answer: `{"reply":"Веток нет.","level":"suggestion","questions":[],"proposal":null}`}
	svc := companion.Service{
		Store: store, Tools: tools,
		ModelFactory: func(providers.Config) (providers.Model, error) { return model, nil },
	}
	response, err := svc.Chat(context.Background(), companion.ChatRequest{WorkspaceID: workspaceID, Message: "Покажи ветки"})
	if err != nil {
		t.Fatal(err)
	}
	// Читающий вызов с теми же аргументами вернёт то же самое: исполняем один раз.
	if len(tools.calls) != 1 {
		t.Fatalf("вызовов инструмента %d вместо одного — повтор исполнился", len(tools.calls))
	}
	// Круг после повтора идёт уже без инструментов: просить их снова незачем,
	// а каждый такой круг — целое обращение к модели.
	if len(model.requests) < 3 {
		t.Fatalf("кругов к модели %d — повтор не дошёл до ответа", len(model.requests))
	}
	if len(model.requests[2].Tools) != 0 {
		t.Fatal("после повтора инструменты всё ещё предлагались")
	}
	if response.Mode != "model" || response.Reply != "Веток нет." {
		t.Fatalf("ответа по собранному нет: mode=%q reply=%q", response.Mode, response.Reply)
	}
}

func TestCompanionKeepsQuestionWhenRoundBreaks(t *testing.T) {
	store, workspaceID := companionToolStore(t, "companion-question-kept.db")
	ctx, cancel := context.WithCancel(context.Background())
	svc := companion.Service{
		Store: store,
		ModelFactory: func(providers.Config) (providers.Model, error) {
			return &brokenRoundModel{cancel: cancel}, nil
		},
	}
	// Круг оборвался вместе с ожиданием, но разговор не остаётся пустым: разбор
	// пишет сам Point и успевает его сохранить — отменённый контекст уносил и
	// местный ответ, и человек не получал ничего.
	response, err := svc.Chat(ctx, companion.ChatRequest{WorkspaceID: workspaceID, Message: "Что сломалось в логах?"})
	if err != nil {
		t.Fatalf("оборванный круг не дал даже местного разбора: %v", err)
	}
	if response.Mode == "model" || response.FallbackReason == "" {
		t.Fatalf("обрыв выдан за ответ модели: mode=%q reason=%q", response.Mode, response.FallbackReason)
	}
	saved, err := store.ListChatMessages(context.Background(), workspaceID, "companion", 10)
	if err != nil {
		t.Fatal(err)
	}
	answered := false
	for _, message := range saved {
		if message.Role == "assistant" && message.FallbackReason != "" {
			answered = true
		}
	}
	if !answered {
		t.Fatalf("местный разбор не сохранён после обрыва: %#v", saved)
	}
	// Вопрос сохранён до работы, поэтому обрыв его не уносит: человек открывает
	// ленту и видит, что сообщение дошло.
	kept := saved
	asked := 0
	for _, message := range kept {
		if message.Role == "user" && message.Content == "Что сломалось в логах?" {
			asked++
		}
	}
	if asked != 1 {
		t.Fatalf("вопрос в истории %d раз вместо одного: %#v", asked, kept)
	}

	// Удачный круг не задваивает его: сохранение вопроса живёт в одном месте.
	model := &oneCallModel{toolReply: `{"reply":"Готово.","level":"suggestion","questions":[],"proposal":null}`}
	ok := companion.Service{
		Store:        store,
		ModelFactory: func(providers.Config) (providers.Model, error) { return model, nil },
	}
	if _, err := ok.Chat(context.Background(), companion.ChatRequest{WorkspaceID: workspaceID, Message: "И что дальше?"}); err != nil {
		t.Fatal(err)
	}
	after, err := store.ListChatMessages(context.Background(), workspaceID, "companion", 10)
	if err != nil {
		t.Fatal(err)
	}
	repeated := 0
	for _, message := range after {
		if message.Role == "user" && message.Content == "И что дальше?" {
			repeated++
		}
	}
	if repeated != 1 {
		t.Fatalf("вопрос сохранён %d раз вместо одного", repeated)
	}
}

func TestCompanionFitsHistoryIntoTokenBudget(t *testing.T) {
	store, workspaceID := companionToolStore(t, "companion-context-budget.db")
	// Сорок длинных реплик: по числу они укладывались в прежний предел в сорок
	// восемь, а по объёму вытесняли из окна и контекст проекта, и сам вопрос.
	long := strings.Repeat("подробный ответ помощника с деталями. ", 300)
	base := time.Now().UTC().Add(-time.Hour)
	for index := 0; index < 40; index++ {
		role := "user"
		if index%2 == 1 {
			role = "assistant"
		}
		if err := store.SaveCompanionMessage(context.Background(), domain.CompanionMessage{
			ID: fmt.Sprintf("cm-%03d", index), WorkspaceID: workspaceID, Speaker: "companion",
			Role: role, Content: long, CreatedAt: base.Add(time.Duration(index) * time.Second),
		}); err != nil {
			t.Fatal(err)
		}
	}
	model := &oneCallModel{toolReply: `{"reply":"Готово.","level":"suggestion","questions":[],"proposal":null}`}
	svc := companion.Service{
		Store:        store,
		ModelFactory: func(providers.Config) (providers.Model, error) { return model, nil },
	}
	if _, err := svc.Chat(context.Background(), companion.ChatRequest{WorkspaceID: workspaceID, Message: "Короткий вопрос"}); err != nil {
		t.Fatal(err)
	}
	if len(model.requests) == 0 {
		t.Fatal("модель не получила запроса")
	}
	request := model.requests[0]
	// Окно по умолчанию 32K минус ответ модели: тот же расчёт, что у агента.
	budget := 32*1024 - 1200
	if estimate := agent.EstimateModelInputTokens(request.Messages, request.Tools); estimate > budget {
		t.Fatalf("вход %d токенов при бюджете %d — история не подогнана", estimate, budget)
	}
	if len(request.Messages) >= 41 {
		t.Fatalf("в запрос ушло %d сообщений — старые реплики не выброшены", len(request.Messages))
	}
	// Крайние сообщения остаются: без правил и без вопроса разговора нет.
	if request.Messages[0].Role != "system" {
		t.Fatalf("системное сообщение потерялось: %q", request.Messages[0].Role)
	}
	// Вопрос идёт последним, и напоминание о формате ответа приписано к нему же:
	// отдельной репликой оно давало две подряд от человека.
	last := request.Messages[len(request.Messages)-1]
	if last.Role != "user" || !strings.Contains(last.Content, "Короткий вопрос") {
		t.Fatalf("вопрос человека потерялся при подгонке истории: %#v", last)
	}
	if !strings.Contains(last.Content, "Reminder about the answer format") {
		t.Fatalf("напоминание о формате не доехало до круга: %q", last.Content)
	}
}

func TestCompanionRemembersFactOnRequest(t *testing.T) {
	store, workspaceID := companionToolStore(t, "companion-memory.db")
	// Модель не нужна: просьба запомнить — точный сигнал, и ходить с ней к
	// модели значило бы платить за то, что и так однозначно.
	svc := companion.Service{Store: store}
	response, err := svc.Chat(context.Background(), companion.ChatRequest{
		WorkspaceID: workspaceID,
		Message:     "Запомни: релиз собираем только через make release",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(response.Reply, "make release") {
		t.Fatalf("подтверждения не видно: %q", response.Reply)
	}
	memories, err := store.ListMemories(context.Background(), workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	saved := 0
	for _, memory := range memories {
		if memory.Kind == domain.MemoryCompanion && memory.Content == "релиз собираем только через make release" {
			saved++
			if memory.Source == "" {
				t.Fatal("у записи нет источника — человек не поймёт, откуда она взялась")
			}
		}
	}
	if saved != 1 {
		t.Fatalf("факт сохранён %d раз вместо одного: %#v", saved, memories)
	}

	// Повтор той же просьбы не заводит дубль: три одинаковые строки в памяти —
	// то же самое, что ни одной.
	again, err := svc.Chat(context.Background(), companion.ChatRequest{
		WorkspaceID: workspaceID,
		Message:     "запомни, релиз собираем только через make release",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(again.Reply, "уже в моей памяти") {
		t.Fatalf("повтор не распознан: %q", again.Reply)
	}
	after, err := store.ListMemories(context.Background(), workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(memories) {
		t.Fatalf("память выросла с %d до %d записей на повторе", len(memories), len(after))
	}
}

func TestCompanionKeepsDigestOfCompactedHistory(t *testing.T) {
	store, workspaceID := companionToolStore(t, "companion-digest.db")
	base := time.Now().UTC().Add(-time.Hour)
	// Больше, чем помещается целиком: ранние реплики уйдут в выжимку.
	for index := 0; index < 24; index++ {
		role := "user"
		content := fmt.Sprintf("ранний вопрос про биллинг номер %d", index)
		if index%2 == 1 {
			role = "assistant"
			content = fmt.Sprintf("ранний ответ про биллинг номер %d", index)
		}
		if err := store.SaveCompanionMessage(context.Background(), domain.CompanionMessage{
			ID: fmt.Sprintf("dm-%03d", index), WorkspaceID: workspaceID, Speaker: "companion",
			Role: role, Content: content, CreatedAt: base.Add(time.Duration(index) * time.Second),
		}); err != nil {
			t.Fatal(err)
		}
	}
	model := &oneCallModel{toolReply: `{"reply":"Готово.","level":"suggestion","questions":[],"proposal":null}`}
	svc := companion.Service{
		Store:        store,
		ModelFactory: func(providers.Config) (providers.Model, error) { return model, nil },
	}
	if _, err := svc.Chat(context.Background(), companion.ChatRequest{WorkspaceID: workspaceID, Message: "Что дальше?"}); err != nil {
		t.Fatal(err)
	}
	digests := func() []domain.MemoryRecord {
		t.Helper()
		memories, err := store.ListMemories(context.Background(), workspaceID)
		if err != nil {
			t.Fatal(err)
		}
		found := make([]domain.MemoryRecord, 0, 2)
		for _, memory := range memories {
			if memory.Source == "сжатие разговора" {
				found = append(found, memory)
			}
		}
		return found
	}
	saved := digests()
	if len(saved) != 1 {
		t.Fatalf("конспектов в памяти %d вместо одного", len(saved))
	}
	if !strings.Contains(saved[0].Content, "биллинг") {
		t.Fatalf("в конспекте нет ранней части разговора: %q", saved[0].Content)
	}
	if saved[0].Kind != domain.MemoryCompanion {
		t.Fatalf("конспект лёг не в память помощника: %q", saved[0].Kind)
	}

	// Следующая реплика обновляет ту же запись, а не заводит вторую: иначе
	// длинный разговор набивал бы память своими же пересказами.
	if _, err := svc.Chat(context.Background(), companion.ChatRequest{WorkspaceID: workspaceID, Message: "А теперь?"}); err != nil {
		t.Fatal(err)
	}
	if again := digests(); len(again) != 1 {
		t.Fatalf("после второй реплики конспектов %d", len(again))
	}
}

func TestCompanionMemoryIsAskedAboutAndForgotten(t *testing.T) {
	store, workspaceID := companionToolStore(t, "companion-memory-round.db")
	svc := companion.Service{Store: store}
	ask := func(message string) companion.ChatResponse {
		t.Helper()
		response, err := svc.Chat(context.Background(), companion.ChatRequest{WorkspaceID: workspaceID, Message: message})
		if err != nil {
			t.Fatal(err)
		}
		return response
	}

	// Просьба запомнить закрепляет запись: отбор памяти идёт по совпадению слов
	// с вопросом, и незакреплённый факт молчал бы на вопрос другими словами.
	ask("Запомни: релиз собираем только через make release")
	memories, err := store.ListMemories(context.Background(), workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(memories) != 1 || !memories[0].Pinned {
		t.Fatalf("просьба не закреплена: %#v", memories)
	}

	// Список строится из записей, а не из пересказа модели.
	list := ask("что ты помнишь по проекту?")
	if !strings.Contains(list.Reply, "make release") {
		t.Fatalf("в списке нет запомненного: %q", list.Reply)
	}

	// Забывание — решение человека и делается его словами.
	forgotten := ask("забудь про make release")
	if !strings.Contains(forgotten.Reply, "Забыл") {
		t.Fatalf("забывание не подтверждено: %q", forgotten.Reply)
	}
	after, err := store.ListMemories(context.Background(), workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	for _, memory := range after {
		if strings.Contains(memory.Content, "make release") {
			t.Fatalf("запись осталась после забывания: %#v", memory)
		}
	}
	empty := ask("что ты помнишь?")
	if !strings.Contains(empty.Reply, "ничего не запомнил") {
		t.Fatalf("пустая память описана неверно: %q", empty.Reply)
	}
	// Забыть то, чего нет, — не ошибка, но и не молчание.
	missing := ask("забудь про кассовые чеки")
	if !strings.Contains(missing.Reply, "нет") {
		t.Fatalf("отсутствие записи не названо: %q", missing.Reply)
	}
}

func TestCompanionAnswersDifferentlyAfterRejection(t *testing.T) {
	store, workspaceID := companionToolStore(t, "companion-rejected.db")
	model := &oneCallModel{toolReply: `{"reply":"Попробую иначе.","level":"suggestion","questions":[],"proposal":null}`}
	svc := companion.Service{
		Store:        store,
		ModelFactory: func(providers.Config) (providers.Model, error) { return model, nil },
	}
	// Обычный круг: указания про неудачный ответ в системном сообщении нет.
	if _, err := svc.Chat(context.Background(), companion.ChatRequest{WorkspaceID: workspaceID, Message: "Что в проекте важно?"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(model.requests[0].Messages[0].Content, "marked your previous answer as unhelpful") {
		t.Fatal("указание про неудачный ответ пришло без отметки человека")
	}

	// Человек отметил прошлый ответ как не помогший: следующий круг обязан нести
	// указание не повторять тот же ход.
	if _, err := svc.Chat(context.Background(), companion.ChatRequest{
		WorkspaceID: workspaceID, Message: "И всё же?", PreviousAnswerRejected: true,
	}); err != nil {
		t.Fatal(err)
	}
	last := model.requests[len(model.requests)-1]
	if !strings.Contains(last.Messages[0].Content, "marked your previous answer as unhelpful") {
		t.Fatal("отметка «не помогло» не дошла до модели")
	}
	if !strings.Contains(last.Messages[0].Content, "Do not repeat the same approach") {
		t.Fatalf("указание не говорит, что делать иначе: %q", last.Messages[0].Content)
	}
}

func TestCompanionSeesDeclinedProposals(t *testing.T) {
	store, workspaceID := companionToolStore(t, "companion-declined.db")
	now := time.Now().UTC()
	// Один отказ и одно принятое предложение: помощник должен помнить первый и
	// не тащить в контекст второе — оно уже стало работой.
	if err := store.SaveQuestProposal(context.Background(), domain.QuestProposal{
		ID: "qp-declined", WorkspaceID: workspaceID, Title: "Переписать биллинг целиком",
		Status: "ignored", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveQuestProposal(context.Background(), domain.QuestProposal{
		ID: "qp-started", WorkspaceID: workspaceID, Title: "Починить вебхук оплаты",
		Status: "started", CreatedAt: now.Add(time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveCompanionActionProposal(context.Background(), domain.CompanionActionProposal{
		ID: "ca-declined", WorkspaceID: workspaceID, Kind: domain.CompanionActionCreateSkill,
		Title: "Создать Skill · Ревью по чек-листу", Status: "ignored", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	model := &oneCallModel{toolReply: `{"reply":"Понял.","level":"suggestion","questions":[],"proposal":null}`}
	svc := companion.Service{
		Store:        store,
		ModelFactory: func(providers.Config) (providers.Model, error) { return model, nil },
	}
	if _, err := svc.Chat(context.Background(), companion.ChatRequest{WorkspaceID: workspaceID, Message: "Что предложишь?"}); err != nil {
		t.Fatal(err)
	}
	system := model.requests[0].Messages[0].Content
	if !strings.Contains(system, "Переписать биллинг целиком") {
		t.Fatal("отклонённый квест не дошёл до контекста — помощник предложит его снова")
	}
	if !strings.Contains(system, "Ревью по чек-листу") {
		t.Fatal("отклонённое действие не дошло до контекста")
	}
	if strings.Contains(system, "Починить вебхук оплаты") {
		t.Fatal("принятое предложение попало в список отказов")
	}
	if !strings.Contains(system, "DECLINED EARLIER") {
		t.Fatalf("список отказов не назван: %q", system)
	}
}

func TestCompanionPromptExplainsAnswerLevels(t *testing.T) {
	store, workspaceID := companionToolStore(t, "companion-levels.db")
	model := &oneCallModel{toolReply: `{"reply":"Готово.","level":"suggestion","questions":[],"proposal":null}`}
	svc := companion.Service{
		Store:        store,
		ModelFactory: func(providers.Config) (providers.Model, error) { return model, nil },
	}
	if _, err := svc.Chat(context.Background(), companion.ChatRequest{WorkspaceID: workspaceID, Message: "Что важно?"}); err != nil {
		t.Fatal(err)
	}
	system := model.requests[0].Messages[0].Content
	// Схема перечисляет уровни, но выбор между ними держится на критерии: без
	// него модель ставит уровень наугад, и рамка критичного ответа в интерфейсе
	// перестаёт что-либо значить.
	if !strings.Contains(system, "Choose the answer level deliberately") {
		t.Fatal("в системном сообщении нет правила выбора уровня ответа")
	}
	for _, marker := range []string{"leaking a secret", "can wait until the person", "stops meaning anything"} {
		if !strings.Contains(system, marker) {
			t.Fatalf("правило выбора уровня неполно: нет %q", marker)
		}
	}
}

func TestCompanionAnswerNamesToolsItUsed(t *testing.T) {
	store, workspaceID := companionToolStore(t, "companion-tools-used.db")
	tools := &fakeReadTools{output: `{"branches":["master"]}`}
	model := &oneCallModel{toolReply: `{"reply":"Ветка одна: master.","level":"suggestion","questions":[],"proposal":null}`}
	svc := companion.Service{
		Store: store, Tools: tools,
		ModelFactory: func(providers.Config) (providers.Model, error) { return model, nil },
	}
	response, err := svc.Chat(context.Background(), companion.ChatRequest{WorkspaceID: workspaceID, Message: "Какие ветки?"})
	if err != nil {
		t.Fatal(err)
	}
	// Ответ проверяют по тому, что помощник смотрел, а не по обещанию, что смотрел.
	named := ""
	for _, fact := range response.FactsUsed {
		if strings.HasPrefix(fact, "toolsUsed=") {
			named = fact
		}
	}
	if named != "toolsUsed=git_branches" {
		t.Fatalf("в фактах ответа нет использованных инструментов: %#v", response.FactsUsed)
	}

	// Ответ без инструментов такого факта не выдумывает.
	quiet := &oneCallModel{toolReply: `{"reply":"Отвечаю по контексту.","level":"suggestion","questions":[],"proposal":null}`}
	without := companion.Service{
		Store:        store,
		ModelFactory: func(providers.Config) (providers.Model, error) { return quiet, nil },
	}
	plain, err := without.Chat(context.Background(), companion.ChatRequest{WorkspaceID: workspaceID, Message: "И что дальше?"})
	if err != nil {
		t.Fatal(err)
	}
	for _, fact := range plain.FactsUsed {
		if strings.HasPrefix(fact, "toolsUsed=") {
			t.Fatalf("ответ без инструментов сообщил об инструментах: %q", fact)
		}
	}
}

func TestCompanionReportsToolStepsWhileWorking(t *testing.T) {
	store, workspaceID := companionToolStore(t, "companion-tool-progress.db")
	tools := &fakeReadTools{output: `{"branches":["master"]}`}
	model := &oneCallModel{toolReply: `{"reply":"Ветка одна.","level":"suggestion","questions":[],"proposal":null}`}
	svc := companion.Service{
		Store: store, Tools: tools,
		ModelFactory: func(providers.Config) (providers.Model, error) { return model, nil },
	}
	var steps []string
	if _, err := svc.Chat(context.Background(), companion.ChatRequest{
		WorkspaceID: workspaceID, Message: "Какие ветки?",
		OnProgress: func(step, status string) { steps = append(steps, step+":"+status) },
	}); err != nil {
		t.Fatal(err)
	}
	// Ожидание длится десятки секунд, и всё это время в ленте стояло одно «Сбор
	// контекста». Имя инструмента говорит, что происходит прямо сейчас.
	started, finished := false, false
	for _, step := range steps {
		if step == "tool:git_branches:running" {
			started = true
		}
		if step == "tool:git_branches:done" {
			finished = true
		}
	}
	if !started || !finished {
		t.Fatalf("шаги инструмента не доехали до интерфейса: %#v", steps)
	}
}

func TestCompanionAnswerNamesFailedTool(t *testing.T) {
	store, workspaceID := companionToolStore(t, "companion-tool-failure.db")
	tools := &failingReadTools{}
	model := &oneCallModel{toolReply: `{"reply":"Отвечаю по тому, что есть.","level":"suggestion","questions":[],"proposal":null}`}
	svc := companion.Service{
		Store: store, Tools: tools,
		ModelFactory: func(providers.Config) (providers.Model, error) { return model, nil },
	}
	response, err := svc.Chat(context.Background(), companion.ChatRequest{WorkspaceID: workspaceID, Message: "Какие ветки?"})
	if err != nil {
		t.Fatal(err)
	}
	// Отказ инструмента объясняет, почему ответ беднее обычного. В полосе
	// активности он мелькает на секунду, поэтому обязан остаться в фактах.
	failed := ""
	for _, fact := range response.FactsUsed {
		if strings.HasPrefix(fact, "toolFailures=") {
			failed = fact
		}
	}
	if failed != "toolFailures=git_branches" {
		t.Fatalf("отказ инструмента не сохранился в фактах: %#v", response.FactsUsed)
	}
}

func TestCompanionPromptTellsWhatToDoWhenToolFails(t *testing.T) {
	store, workspaceID := companionToolStore(t, "companion-tool-failure-rule.db")
	tools := &failingReadTools{}
	model := &oneCallModel{toolReply: `{"reply":"Не смог прочитать git.","level":"suggestion","questions":[],"proposal":null}`}
	svc := companion.Service{
		Store: store, Tools: tools,
		ModelFactory: func(providers.Config) (providers.Model, error) { return model, nil },
	}
	if _, err := svc.Chat(context.Background(), companion.ChatRequest{WorkspaceID: workspaceID, Message: "Какие ветки?"}); err != nil {
		t.Fatal(err)
	}
	system := model.requests[0].Messages[0].Content
	// Ответ на отказ инструмента модель выбирала сама, и уверенный текст по
	// памяти выглядел убедительнее честного «не смог прочитать».
	for _, marker := range []string{
		"say plainly in your reply what you could not check",
		"never describe the content you failed to read",
		"never repeat the same failing call",
	} {
		if !strings.Contains(system, marker) {
			t.Fatalf("в инструкциях об инструментах нет правила про отказ: %q", marker)
		}
	}
}

func TestCompanionAnswerNamesTruncatedTool(t *testing.T) {
	store, workspaceID := companionToolStore(t, "companion-tool-truncated.db")
	model := &oneCallModel{toolReply: `{"reply":"Видно начало файла.","level":"suggestion","questions":[],"proposal":null}`}
	svc := companion.Service{
		Store: store, Tools: &hugeReadTools{},
		ModelFactory: func(providers.Config) (providers.Model, error) { return model, nil },
	}
	response, err := svc.Chat(context.Background(), companion.ChatRequest{WorkspaceID: workspaceID, Message: "Что в файле?"})
	if err != nil {
		t.Fatal(err)
	}
	// Обрезка не отказ, но ответ по ней неполон: пометку о ней видела только
	// модель, а человек читал ответ как полный.
	truncated := ""
	for _, fact := range response.FactsUsed {
		if strings.HasPrefix(fact, "toolsTruncated=") {
			truncated = fact
		}
	}
	if truncated != "toolsTruncated=git_branches" {
		t.Fatalf("обрезанная выдача не отмечена в фактах: %#v", response.FactsUsed)
	}
}

func TestCompanionKnowsIndexMayBeIncomplete(t *testing.T) {
	store, workspaceID := companionToolStore(t, "companion-partial-index.db")
	model := &oneCallModel{toolReply: `{"reply":"Смотрю по индексу.","level":"suggestion","questions":[],"proposal":null}`}
	svc := companion.Service{
		Store:          store,
		ProjectContext: &partialIndexContext{},
		ModelFactory:   func(providers.Config) (providers.Model, error) { return model, nil },
	}
	response, err := svc.Chat(context.Background(), companion.ChatRequest{WorkspaceID: workspaceID, Message: "Есть ли в проекте файл billing.go?"})
	if err != nil {
		t.Fatal(err)
	}
	// Индекс упирается в лимиты на большом репозитории. Без этого признака
	// отсутствие файла в индексе читается как отсутствие файла в проекте.
	partial := false
	for _, fact := range response.FactsUsed {
		if fact == "projectIndexPartial=entries" {
			partial = true
		}
	}
	if !partial {
		t.Fatalf("неполнота индекса не попала в факты: %#v", response.FactsUsed)
	}
	system := model.requests[0].Messages[0].Content
	if !strings.Contains(system, "Never conclude that a file, symbol or feature is missing") {
		t.Fatal("модель не предупреждена, что по неполному индексу нельзя судить об отсутствии")
	}
}

func TestCompanionKnowsIndexSnapshotIsOld(t *testing.T) {
	for _, tc := range []struct {
		name    string
		age     time.Duration
		minutes string
		warned  bool
	}{
		{name: "свежий", age: time.Minute, minutes: "projectIndexAgeMinutes=1", warned: false},
		{name: "часовой", age: 2 * time.Hour, minutes: "projectIndexAgeMinutes=120", warned: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, workspaceID := companionToolStore(t, "companion-index-age.db")
			model := &oneCallModel{toolReply: `{"reply":"Смотрю по индексу.","level":"suggestion","questions":[],"proposal":null}`}
			svc := companion.Service{
				Store:          store,
				ProjectContext: &agedIndexContext{age: tc.age},
				ModelFactory:   func(providers.Config) (providers.Model, error) { return model, nil },
			}
			response, err := svc.Chat(context.Background(), companion.ChatRequest{WorkspaceID: workspaceID, Message: "Что сейчас в main.go?"})
			if err != nil {
				t.Fatal(err)
			}
			// Возраст снимка — часть ответа: без него «в файле написано» звучит
			// одинаково уверенно и через минуту после сборки, и через сутки.
			if !slices.Contains(response.FactsUsed, tc.minutes) {
				t.Fatalf("возраст индекса не попал в факты: %#v", response.FactsUsed)
			}
			system := model.requests[0].Messages[0].Content
			if warned := strings.Contains(system, "snapshot is not fresh"); warned != tc.warned {
				t.Fatalf("предупреждение об устаревшем снимке: было %v, ждали %v", warned, tc.warned)
			}
		})
	}
}

func TestCompanionKeepsAnswerCutByOutputLimit(t *testing.T) {
	// Потолок вывода режет конверт посреди строки reply: JSON не собирается,
	// хотя разбор сбоя для человека уже написан и оплачен токенами.
	cut := `{"reply":"Сборка падает на шаге тестов: не найден пакет golang.org/x/tools, поставь его и повтори прогон`
	store, workspaceID := companionToolStore(t, "companion-cut-reply.db")
	model := &oneCallModel{toolReply: cut}
	svc := companion.Service{
		Store:        store,
		ModelFactory: func(providers.Config) (providers.Model, error) { return model, nil },
	}
	response, err := svc.Chat(context.Background(), companion.ChatRequest{WorkspaceID: workspaceID, Message: "Почему падает сборка?"})
	if err != nil {
		t.Fatalf("оборванный ответ выброшен как отказ: %v", err)
	}
	if !strings.Contains(response.Reply, "не найден пакет golang.org/x/tools") {
		t.Fatalf("написанное до обрыва потеряно: %q", response.Reply)
	}
	// Обрыв виден человеку: иначе оборванная фраза читается как законченная мысль.
	if !slices.Contains(response.FactsUsed, "replyTruncated=true") {
		t.Fatalf("обрыв не отмечен в фактах: %#v", response.FactsUsed)
	}
}

func TestCompanionDoesNotPassOffScrapsAsAnswer(t *testing.T) {
	// Обрубок в пару слов не сообщает ничего, и выдать его за ответ значило бы
	// спрятать отказ: пусть лучше человек увидит отказ и повторит.
	store, workspaceID := companionToolStore(t, "companion-scrap-reply.db")
	model := &oneCallModel{toolReply: `{"reply":"Сейчас пос`}
	svc := companion.Service{
		Store:        store,
		ModelFactory: func(providers.Config) (providers.Model, error) { return model, nil },
	}
	response, err := svc.Chat(context.Background(), companion.ChatRequest{WorkspaceID: workspaceID, Message: "Почему падает сборка?"})
	if err != nil {
		t.Fatal(err)
	}
	// Разбор не удался, и разговор уходит на местный откат с названной причиной,
	// а не выдаёт обрубок за слова модели.
	if response.Mode == "model" || response.FallbackReason == "" {
		t.Fatalf("обрубок выдан за ответ модели: mode=%q reason=%q reply=%q", response.Mode, response.FallbackReason, response.Reply)
	}
	if strings.Contains(response.Reply, "Сейчас пос") {
		t.Fatalf("обрубок попал в ответ: %q", response.Reply)
	}
	// Причина отката переживает перезагрузку окна: без неё местный ответ в
	// истории неотличим от модельного, и метка происхождения пропадает.
	messages, err := store.ListCompanionMessages(context.Background(), workspaceID, 10)
	if err != nil {
		t.Fatal(err)
	}
	saved := ""
	for _, message := range messages {
		if message.Role == "assistant" {
			saved = message.FallbackReason
		}
	}
	if saved == "" {
		t.Fatal("причина отката не сохранена в истории")
	}
}

func TestCompanionChatKeepsRolesAlternating(t *testing.T) {
	store, workspaceID := companionToolStore(t, "companion-alternating.db")
	// Круг, оборванный остановкой: вопрос сохранён до обращения к модели, ответа
	// нет. В следующем разговоре это даёт две реплики человека подряд.
	if err := store.SaveCompanionMessage(context.Background(), domain.CompanionMessage{
		ID: "companionmsg-orphan", WorkspaceID: workspaceID, Speaker: "companion", Role: "user",
		Content: "Разбери сбой сборки", CreatedAt: time.Now().UTC().Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	model := &oneCallModel{toolReply: `{"reply":"Смотрю тесты.","level":"suggestion","questions":[],"proposal":null}`}
	svc := companion.Service{
		Store:        store,
		ModelFactory: func(providers.Config) (providers.Model, error) { return model, nil },
	}
	if _, err := svc.Chat(context.Background(), companion.ChatRequest{WorkspaceID: workspaceID, Message: "А теперь посмотри тесты"}); err != nil {
		t.Fatal(err)
	}
	sent := model.requests[0].Messages
	for i := 1; i < len(sent); i++ {
		if sent[i].Role == sent[i-1].Role {
			t.Fatalf("две реплики роли %q подряд на месте %d: %#v", sent[i].Role, i, sent)
		}
	}
	named := false
	for _, message := range sent {
		if strings.Contains(message.Content, "ответа не было") {
			named = true
		}
	}
	if !named {
		t.Fatalf("прерванный круг не назван, брошенный вопрос выглядит заданным дважды: %#v", sent)
	}
}

func TestCompanionKnowsItsReplyBudget(t *testing.T) {
	store, workspaceID := companionToolStore(t, "companion-reply-budget.db")
	model := &oneCallModel{toolReply: `{"reply":"Коротко: сборка падает на тестах.","level":"suggestion","questions":[],"proposal":null}`}
	svc := companion.Service{
		Store:        store,
		ModelFactory: func(providers.Config) (providers.Model, error) { return model, nil },
	}
	response, err := svc.Chat(context.Background(), companion.ChatRequest{WorkspaceID: workspaceID, Message: "Разбери сбой сборки подробно"})
	if err != nil {
		t.Fatal(err)
	}
	// Предел провайдер применяет молча: не зная о нём, модель пишет вступление
	// на всю длину и обрывается перед выводом.
	limit := model.requests[0].MaxOutputTokens
	if limit <= 0 {
		t.Fatalf("потолок вывода не задан: %d", limit)
	}
	system := model.requests[0].Messages[0].Content
	if !strings.Contains(system, fmt.Sprintf("capped at %d output tokens", limit)) {
		t.Fatal("модель не знает предела, в который её ответ обязан уложиться")
	}
	// Тот же предел человек видит в «Сведениях»: обрыв без числа не подсказывает,
	// где его подвинуть.
	if !slices.Contains(response.FactsUsed, fmt.Sprintf("replyLimitTokens=%d", limit)) {
		t.Fatalf("предел ответа не попал в факты: %#v", response.FactsUsed)
	}
}

func TestCompanionIsToldCurrentMoment(t *testing.T) {
	store, workspaceID := companionToolStore(t, "companion-current-moment.db")
	model := &oneCallModel{toolReply: `{"reply":"Отвечаю.","level":"suggestion","questions":[],"proposal":null}`}
	svc := companion.Service{
		Store:        store,
		ModelFactory: func(providers.Config) (providers.Model, error) { return model, nil },
	}
	if _, err := svc.Chat(context.Background(), companion.ChatRequest{WorkspaceID: workspaceID, Message: "Давно ли был последний запуск?"}); err != nil {
		t.Fatal(err)
	}
	// Без точки отсчёта модель считает возраст логов и коммитов от даты своего
	// обучения: «неделю назад» превращается в «полгода назад» молча.
	system := model.requests[0].Messages[0].Content
	today := time.Now().Format("2006-01-02")
	if !strings.Contains(system, "Current moment: "+today) {
		t.Fatalf("сегодняшняя дата не названа модели: %q", system[:min(len(system), 400)])
	}
	if !strings.Contains(system, "your own sense of today's date comes from training") {
		t.Fatal("модели не сказано, что её собственное представление о дате устарело")
	}
}

func TestCompanionWithoutToolsDoesNotAnnounceThem(t *testing.T) {
	store, workspaceID := companionToolStore(t, "companion-tool-absent.db")
	model := &oneCallModel{toolReply: `{"reply":"Отвечаю по контексту.","level":"suggestion","questions":[],"proposal":null}`}
	svc := companion.Service{
		Store:        store,
		ModelFactory: func(providers.Config) (providers.Model, error) { return model, nil },
	}
	if _, err := svc.Chat(context.Background(), companion.ChatRequest{WorkspaceID: workspaceID, Message: "Привет"}); err != nil {
		t.Fatal(err)
	}
	if len(model.requests) != 1 {
		t.Fatalf("без инструментов кругов должно быть один, было %d", len(model.requests))
	}
	if len(model.requests[0].Tools) != 0 {
		t.Fatalf("инструменты предложены там, где их нет: %d", len(model.requests[0].Tools))
	}
	system := model.requests[0].Messages[0].Content
	if strings.Contains(system, "read-only tools") {
		t.Fatalf("модели обещаны инструменты, которых ей не дали")
	}
}

// Модель, которая после инструмента отвечает разметкой вместо конверта, а на
// напоминание о форме — конвертом. Так вела себя Qwen3.6 на живом замере.
type sloppyAfterToolModel struct {
	requests []providers.ModelRequest
	prose    string
	envelope string
}

func (m *sloppyAfterToolModel) Stream(_ context.Context, request providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.requests = append(m.requests, request)
	switch len(m.requests) {
	case 1:
		call := providers.ToolCall{ID: "call-1", Name: "git_branches", Arguments: json.RawMessage(`{}`)}
		if err := emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &call}); err != nil {
			return err
		}
	case 2:
		if err := emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: m.prose}); err != nil {
			return err
		}
	default:
		if err := emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: m.envelope}); err != nil {
			return err
		}
	}
	return emit(providers.ModelEvent{Kind: providers.EventUsage, InputTokens: 50, OutputTokens: 20})
}

func TestCompanionRepairsEnvelopeAfterToolCall(t *testing.T) {
	store, workspaceID := companionToolStore(t, "companion-tool-repair.db")
	tools := &fakeReadTools{output: `{"current":"master","branches":[{"name":"master"}]}`}
	model := &sloppyAfterToolModel{
		prose:    "В проекте есть следующие ветки:\n\n- `master` (текущая)\n- `finquest`",
		envelope: `{"reply":"Ветки: master (текущая) и finquest.","level":"suggestion","questions":[],"proposal":null}`,
	}
	svc := companion.Service{
		Store: store, Tools: tools,
		ModelFactory: func(providers.Config) (providers.Model, error) { return model, nil },
	}
	response, err := svc.Chat(context.Background(), companion.ChatRequest{WorkspaceID: workspaceID, Message: "Какие ветки?"})
	if err != nil {
		t.Fatal(err)
	}
	if response.Mode != "model" || response.FallbackReason != "" {
		t.Fatalf("верный ответ выброшен из-за формы: mode=%q reason=%q", response.Mode, response.FallbackReason)
	}
	if response.Reply != "Ветки: master (текущая) и finquest." {
		t.Fatalf("ответ после починки не тот: %q", response.Reply)
	}
	if len(model.requests) != 3 {
		t.Fatalf("исправляющих кругов должно быть ровно один, всего кругов %d", len(model.requests))
	}
	// Круг починки идёт без инструментов: чинится форма ответа, а не сбор фактов.
	if len(model.requests[2].Tools) != 0 {
		t.Fatalf("на исправляющем круге снова предложены инструменты")
	}
	last := model.requests[2].Messages[len(model.requests[2].Messages)-1]
	if last.Role != "user" || !strings.Contains(last.Content, "JSON object") {
		t.Fatalf("напоминание о форме не дошло: %+v", last)
	}
}

// Без инструментов договор прежний: разметка вместо конверта — это откат, а не
// повод переспрашивать. Модель, не соблюдающая схему с первого хода, настроена
// неверно, и честнее сказать об этом, чем тратить второй запрос.
func TestCompanionDoesNotRepairWithoutToolCalls(t *testing.T) {
	store, workspaceID := companionToolStore(t, "companion-tool-norepair.db")
	model := &sloppyAfterToolModel{prose: "Просто текст", envelope: `{"reply":"поздно","level":"suggestion"}`}
	svc := companion.Service{
		Store:        store,
		ModelFactory: func(providers.Config) (providers.Model, error) { return model, nil },
	}
	response, err := svc.Chat(context.Background(), companion.ChatRequest{WorkspaceID: workspaceID, Message: "Привет"})
	if err != nil {
		t.Fatal(err)
	}
	if response.FallbackReason == "" {
		t.Fatalf("без инструментов ожидался откат: %#v", response)
	}
	if len(model.requests) != 1 {
		t.Fatalf("лишний круг без инструментов: %d", len(model.requests))
	}
}

// Модель, которая роняет конверт с первого круга и без единого инструмента, а
// на напоминание о форме отвечает как надо. Так вела себя Qwen3.6 на четвёртой
// реплике разговора: схема к тому моменту лежала за тысячами байт переписки.
type sloppyInLongTalkModel struct {
	requests []providers.ModelRequest
	prose    string
	envelope string
}

func (m *sloppyInLongTalkModel) Stream(_ context.Context, request providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.requests = append(m.requests, request)
	delta := m.envelope
	if len(m.requests) == 1 {
		delta = m.prose
	}
	if err := emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: delta}); err != nil {
		return err
	}
	return emit(providers.ModelEvent{Kind: providers.EventUsage, InputTokens: 80, OutputTokens: 24})
}

// Накопленная переписка отодвигает схему ровно так же, как результат
// инструмента, и круг починки положен и здесь: разговор, начавшийся в
// конверте, не становится неверно настроенным к четвёртой реплике.
func TestCompanionRepairsEnvelopeAfterHistory(t *testing.T) {
	store, workspaceID := companionToolStore(t, "companion-history-repair.db")
	ctx := context.Background()
	for index, message := range []domain.CompanionMessage{
		{ID: "msg-1", WorkspaceID: workspaceID, Role: "user", Content: "историю коммитов расскажи"},
		{ID: "msg-2", WorkspaceID: workspaceID, Role: "assistant", Content: "Вот последние 20 коммитов."},
	} {
		message.CreatedAt = time.Now().UTC().Add(time.Duration(index) * time.Second)
		if err := store.SaveCompanionMessage(ctx, message); err != nil {
			t.Fatal(err)
		}
	}
	model := &sloppyInLongTalkModel{
		prose:    "Да, я могу посмотреть содержимое коммитов и предложить названия.",
		envelope: `{"reply":"Да, могу: покажу diff каждого коммита и предложу название.","level":"suggestion","questions":[],"proposal":null}`,
	}
	svc := companion.Service{
		Store:        store,
		ModelFactory: func(providers.Config) (providers.Model, error) { return model, nil },
	}
	response, err := svc.Chat(ctx, companion.ChatRequest{WorkspaceID: workspaceID, Message: "сможешь посмотреть изменения в коммитах?"})
	if err != nil {
		t.Fatal(err)
	}
	if response.Mode != "model" || response.FallbackReason != "" {
		t.Fatalf("верный ответ выброшен из-за формы: mode=%q reason=%q", response.Mode, response.FallbackReason)
	}
	if response.Reply != "Да, могу: покажу diff каждого коммита и предложу название." {
		t.Fatalf("ответ после починки не тот: %q", response.Reply)
	}
	if len(model.requests) != 2 {
		t.Fatalf("кругов должно быть ровно два, всего %d", len(model.requests))
	}
	last := model.requests[1].Messages[len(model.requests[1].Messages)-1]
	if last.Role != "user" || !strings.Contains(last.Content, "JSON object") {
		t.Fatalf("напоминание о форме не дошло: %+v", last)
	}
}

// Надетый навык помощника обязан доехать до системного сообщения: иначе он
// хранится, показывается в настройках и не делает ничего.
func TestCompanionPutsEquippedSkillsIntoSystemMessage(t *testing.T) {
	store, workspaceID := companionToolStore(t, "companion-skill-section.db")
	model := &oneCallModel{toolReply: `{"reply":"Готово.","level":"suggestion","questions":[],"proposal":null}`}
	svc := companion.Service{
		Store:        store,
		ModelFactory: func(providers.Config) (providers.Model, error) { return model, nil },
		Skills: []domain.SkillRuntime{{
			ID: "skill-commits", Name: "Имена коммитов",
			Instructions: "Пиши подлежащее и действие.",
		}},
	}
	if _, err := svc.Chat(context.Background(), companion.ChatRequest{WorkspaceID: workspaceID, Message: "как назвать коммит?"}); err != nil {
		t.Fatal(err)
	}
	if len(model.requests) == 0 {
		t.Fatal("модель не вызвана")
	}
	system := model.requests[0].Messages[0].Content
	if !strings.Contains(system, "equipped_skills") || !strings.Contains(system, "Имена коммитов") {
		t.Fatalf("навык не доехал до системного сообщения: %s", system)
	}
	if !strings.Contains(system, "Пиши подлежащее и действие.") {
		t.Fatalf("короткий навык не вклеен целиком: %s", system)
	}
}

// Команду для самодельного инструмента пишет человек, а не модель. Просьба без
// команды предложения не рождает — companion просит её назвать.
func TestCompanionToolDraftTakesCommandFromTheHuman(t *testing.T) {
	store, workspaceID := companionToolStore(t, "companion-tool-draft.db")
	svc := companion.Service{Store: store}

	vague, err := svc.Chat(context.Background(), companion.ChatRequest{
		WorkspaceID: workspaceID, Message: "создай инструмент для прогона тестов",
	})
	if err != nil {
		t.Fatal(err)
	}
	if vague.ActionProposal != nil {
		t.Fatalf("черновик собран без названной команды: %#v", vague.ActionProposal)
	}
	if len(vague.Questions) == 0 {
		t.Fatalf("не спрошена команда: %#v", vague)
	}

	exact, err := svc.Chat(context.Background(), companion.ChatRequest{
		WorkspaceID: workspaceID, Message: "создай инструмент, который запускает `go test ./...`",
	})
	if err != nil {
		t.Fatal(err)
	}
	if exact.ActionProposal == nil || exact.ActionProposal.Kind != domain.CompanionActionCreateTool {
		t.Fatalf("предложение инструмента не подготовлено: %#v", exact.ActionProposal)
	}
	if exact.ActionProposal.Tool == nil || exact.ActionProposal.Tool.Command != "go test ./..." {
		t.Fatalf("команда взята не из сообщения: %#v", exact.ActionProposal.Tool)
	}
	if exact.ActionProposal.Status != "pending" {
		t.Fatalf("предложение не ждёт подтверждения: %q", exact.ActionProposal.Status)
	}
}

// Мост открывает доступ на время разговора и закрывает вместе с ним.
type recordingBridge struct {
	subject string
	allowed []string
	opened  int
	closed  int
}

func (b *recordingBridge) OpenToolSession(subject string, tools companion.ReadTools) (string, []string, func(), error) {
	b.subject = subject
	b.opened++
	names := make([]string, 0)
	for _, definition := range tools.Definitions() {
		names = append(names, definition.Name)
	}
	b.allowed = names
	return `{"mcpServers":{"point":{"type":"http","url":"http://127.0.0.1:1/mcp"}}}`, names, func() { b.closed++ }, nil
}

// Модель, работающая по API, моста не касается: инструменты уходят ей в теле
// запроса, и открывать ради этого сессию значит держать лишний ключ.
func TestCompanionSkipsBridgeForNetworkProviders(t *testing.T) {
	store, workspaceID := companionToolStore(t, "companion-mcp-skip.db")
	bridge := &recordingBridge{}
	model := &oneCallModel{toolReply: `{"reply":"Готово.","level":"suggestion","questions":[],"proposal":null}`}
	svc := companion.Service{
		Store: store, Tools: &fakeReadTools{output: `{"branches":["master"]}`},
		ModelFactory: func(providers.Config) (providers.Model, error) { return model, nil },
		ToolBridge:   bridge,
	}
	if _, err := svc.Chat(context.Background(), companion.ChatRequest{WorkspaceID: workspaceID, Message: "Что в проекте?"}); err != nil {
		t.Fatal(err)
	}
	if bridge.opened != 0 {
		t.Fatalf("для сетевой модели открыта сессия инструментов: %d", bridge.opened)
	}
}
