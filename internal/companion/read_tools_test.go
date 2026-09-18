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
