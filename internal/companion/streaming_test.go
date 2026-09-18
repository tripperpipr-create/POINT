package companion_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/companion"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
	"local-agent-workbench/internal/storage"
)

func TestCompanionChatEmitsGatherProgress(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "companion-progress.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_ = store.SaveWorkspace(context.Background(), domain.Workspace{ID: "ws", Path: t.TempDir(), Name: "demo"})
	_ = store.SaveBlueprint(context.Background(), domain.AgentBlueprint{
		ID: "bp", Name: "Scout", SystemPrompt: "scout", Provider: domain.ProviderOllama, PrimaryModel: "llama",
		AllowedTools: []string{"read_file"}, MaxSteps: 3, MaxDurationSeconds: 30, ApprovalMode: domain.ApprovalSafe,
	})
	_, _ = store.EnsureProjectAgentsForWorkspace(context.Background(), "ws")

	type stepEvent struct{ step, status string }
	var events []stepEvent
	svc := companion.Service{Store: store, ProjectContext: companionProjectContext{}}
	_, err = svc.Chat(context.Background(), companion.ChatRequest{
		WorkspaceID: "ws",
		Message:     "Что сейчас в проекте?",
		OnProgress: func(step, status string) {
			events = append(events, stepEvent{step: step, status: status})
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := ""
	for _, event := range events {
		joined += event.step + ":" + event.status + ";"
	}
	for _, need := range []string{"gather:running", "roster:running", "memory:running", "gather:done", "local:running", "local:done"} {
		if !strings.Contains(joined, need) {
			t.Fatalf("missing progress step %q in %q", need, joined)
		}
	}
}

func TestCompanionChatEmitsGrowingDeltas(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "companion-delta.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_ = store.SaveWorkspace(context.Background(), domain.Workspace{ID: "ws", Path: t.TempDir(), Name: "demo"})
	now := time.Now().UTC()
	cfg := domain.CompanionConfig{
		ID: "companion-ws", WorkspaceID: "ws", Preset: "balanced",
		Provider: domain.ProviderOllama, BaseURL: "http://127.0.0.1:11434", Model: "planner",
		Temperature: 0.2, MaxOutputTokens: 2048, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.SaveCompanionConfig(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	envelope := `{"reply":"Разбор файла","level":"suggestion","questions":[],"proposal":null}`
	chunks := []string{envelope[:12], envelope[12:25], envelope[25:]}
	var deltas []string
	svc := companion.Service{
		Store:          store,
		ProjectContext: companionProjectContext{},
		ModelFactory: func(providers.Config) (providers.Model, error) {
			return chunkedCompanionModel{chunks: chunks}, nil
		},
	}
	response, err := svc.Chat(context.Background(), companion.ChatRequest{
		WorkspaceID: "ws",
		Message:     "Что в текущем файле?",
		Focus:       companion.ChatFocus{File: "main.go", Line: 10},
		OnDelta: func(reply string) {
			deltas = append(deltas, reply)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Reply != "Разбор файла" {
		t.Fatalf("reply=%q", response.Reply)
	}
	if len(deltas) == 0 {
		t.Fatal("expected streaming deltas")
	}
	for i := 1; i < len(deltas); i++ {
		if len(deltas[i]) <= len(deltas[i-1]) {
			t.Fatalf("deltas must grow: %#v", deltas)
		}
	}
	if deltas[len(deltas)-1] != "Разбор файла" {
		t.Fatalf("last delta=%q", deltas[len(deltas)-1])
	}
}

// Человек видит, что именно ушло модели.
//
// В панели компаньона под каждым ответом раскрывается «Сведения»: режим,
// провайдер, модель и список фактов проекта. Когда список пуст, там прямо
// написано «Факты проекта для этого ответа не использовались» — это утверждение
// о приватности, и оно обязано быть верным.
//
// Держится оно на том, что каждый путь ответа кладёт в FactsUsed собранный
// контекст. Путей несколько (детерминированный, через модель, откат после сбоя
// модели), они написаны по отдельности, и забыть поле в новом — значит сказать
// человеку «ничего не отправляли» при отправленном коде.
func TestCompanionReportsGatheredFactsOnEveryPath(t *testing.T) {
	setup := func(t *testing.T, withModel bool, message string) companion.ChatResponse {
		t.Helper()
		store, err := storage.Open(filepath.Join(t.TempDir(), "facts.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		ctx := context.Background()
		workspaceID := "ws-facts"
		if err = store.SaveWorkspace(ctx, domain.Workspace{ID: workspaceID, Path: t.TempDir(), Name: "demo"}); err != nil {
			t.Fatal(err)
		}
		if err = store.SaveProjectAgent(ctx, domain.ProjectAgent{
			ID: "project-agent", WorkspaceID: workspaceID, Name: "Builder", SystemPrompt: "Build carefully",
			Provider: domain.ProviderOllama, PrimaryModel: "test", AllowedTools: []string{"read_file"},
			CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
		config := domain.CompanionConfig{
			ID: "companion-facts", WorkspaceID: workspaceID, Preset: "balanced",
			CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		}
		if withModel {
			config.Provider = domain.ProviderOpenAI
			config.ProviderPreset = "openai"
			config.BaseURL = "https://example.test/v1"
			config.Model = "planner"
		}
		if err = store.SaveCompanionConfig(ctx, config); err != nil {
			t.Fatal(err)
		}
		svc := companion.Service{Store: store}
		if withModel {
			svc.ModelFactory = func(providers.Config) (providers.Model, error) {
				return companionModel{content: `{"reply":"Предлагаю безопасный план.","level":"warning","questions":[],"proposal":{"title":"Усилить auth","rationale":"Нужно закрыть риск","unknowns":["Поддерживаемые клиенты"],"objectives":["Сохранить API"],"constraints":[],"definitionOfDone":["Проверки пройдены"],"importance":"important"}}`}, nil
			}
		}
		response, err := svc.Chat(ctx, companion.ChatRequest{
			WorkspaceID: workspaceID, Message: message, APIKey: "transient-secret",
			// Фокус IDE — то, что человек считает своим кодом: имя файла и
			// выделение попадают в контекст, значит обязаны попасть и в отчёт.
			Focus: companion.ChatFocus{File: "internal/app/app.go", Line: 42, Snippet: "func main() {}"},
		})
		if err != nil {
			t.Fatal(err)
		}
		return response
	}

	for _, item := range []struct {
		имя       string
		сМоделью  bool
		сообщение string
		режим     string
	}{
		{"детерминированный путь", false, "Что не так в файле?", "deterministic"},
		{"путь через модель", true, "Спланируй auth", "model"},
	} {
		t.Run(item.имя, func(t *testing.T) {
			response := setup(t, item.сМоделью, item.сообщение)
			// Подтест обязан доказать, каким путём он пошёл: без этого «путь через
			// модель» тихо сворачивал в детерминированный и ничего не проверял.
			if response.Mode != item.режим {
				t.Fatalf("ожидался режим %q, получен %q — подтест проверяет не тот путь", item.режим, response.Mode)
			}
			if len(response.FactsUsed) == 0 {
				t.Fatalf("ответ не сообщает ни одного факта — человеку будет сказано, что контекст не использовался")
			}
			// Защита от холостого хода: список обязан называть именно то, что
			// пришло из фокуса, а не любой посторонний факт.
			joined := strings.Join(response.FactsUsed, " ")
			if !strings.Contains(joined, "ideFocus=") {
				t.Errorf("в отчёте нет фокуса IDE, хотя он был отправлен: %v", response.FactsUsed)
			}
		})
	}
}

// «Стоп» действительно останавливает работу модели.
//
// Кнопка ⏹ в панели компаньона обрывает запрос расширения к ядру
// (companionChatAbort.abort()), ядро передаёт контекст запроса дальше —
// r.Context() → CompanionChat → chatWithModel → model.Stream(ctx, …). Если
// однажды в этой цепочке появится context.Background() — частая правка «чтобы
// отмена не мешала», — интерфейс будет показывать «остановлено», а модель
// продолжит генерировать и тратить деньги.
//
// Проверяем поведение: отменяем контекст вызова и требуем, чтобы отмена дошла
// до самой модели.
func TestCompanionChatCancellationReachesTheModel(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "cancel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	workspaceID := "ws-cancel"
	if err = store.SaveWorkspace(ctx, domain.Workspace{ID: workspaceID, Path: t.TempDir(), Name: "demo"}); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveProjectAgent(ctx, domain.ProjectAgent{
		ID: "project-agent", WorkspaceID: workspaceID, Name: "Builder", SystemPrompt: "Build carefully",
		Provider: domain.ProviderOllama, PrimaryModel: "test", AllowedTools: []string{"read_file"},
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveCompanionConfig(ctx, domain.CompanionConfig{
		ID: "companion-cancel", WorkspaceID: workspaceID, Preset: "balanced",
		Provider: domain.ProviderOpenAI, ProviderPreset: "openai", BaseURL: "https://example.test/v1", Model: "planner",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	model := &blockingCompanionModel{started: make(chan struct{}), finished: make(chan error, 1)}
	svc := companion.Service{
		Store:        store,
		ModelFactory: func(providers.Config) (providers.Model, error) { return model, nil },
	}

	callCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		_, chatErr := svc.Chat(callCtx, companion.ChatRequest{
			WorkspaceID: workspaceID, Message: "Спланируй auth", APIKey: "transient-secret",
		})
		done <- chatErr
	}()

	// Защита от холостого хода: модель обязана начать работу, иначе отмена
	// «сработает» просто потому, что звать было некого.
	select {
	case <-model.started:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("модель не была вызвана — проверка отмены прошла бы вхолостую")
	}

	cancel()

	select {
	case reason := <-model.finished:
		if reason == nil {
			t.Fatal("модель досчитала до конца: отмена до неё не дошла, генерация и расход продолжались бы")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("модель не заметила отмены за отведённое время")
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Chat не вернулся после отмены")
	}
}

func TestExtractStreamingReply(t *testing.T) {
	cases := []struct {
		name   string
		raw    string
		want   string
		wantOK bool
	}{
		{name: "empty", raw: "", wantOK: false},
		{name: "before key", raw: `{"level":"suggestion"`, wantOK: false},
		{name: "partial reply", raw: `{"reply":"Привет`, want: "Привет", wantOK: true},
		{name: "complete reply", raw: `{"reply":"Готово","level":"suggestion","questions":[],"proposal":null}`, want: "Готово", wantOK: true},
		{name: "escaped newline", raw: `{"reply":"строка1\nстрока2`, want: "строка1\nстрока2", wantOK: true},
		{name: "escaped quote", raw: `{"reply":"скажи \"да\"`, want: `скажи "да"`, wantOK: true},
		{name: "fenced partial", raw: "```json\n{\"reply\":\"Код", want: "Код", wantOK: true},
		{name: "fenced complete", raw: "```json\n{\"reply\":\"Ок\",\"proposal\":null}\n```", want: "Ок", wantOK: true},
		{name: "unicode escape", raw: `{"reply":"\u041f\u0440\u0438\u0432\u0435\u0442`, want: "Привет", wantOK: true},
		{name: "extra fields before", raw: `{"level":"warning","reply":"внимание`, want: "внимание", wantOK: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := companion.ExtractStreamingReply(tc.raw)
			if ok != tc.wantOK {
				t.Fatalf("ok=%v want %v (got %q)", ok, tc.wantOK, got)
			}
			if got != tc.want {
				t.Fatalf("reply=%q want %q", got, tc.want)
			}
		})
	}
}

func TestPreferLeanGather(t *testing.T) {
	focus := companion.ChatFocus{File: "internal/companion/service.go", Line: 10, Snippet: "func Chat()"}
	if !companion.PreferLeanGather("Что не так в этом файле?", focus) {
		t.Fatal("expected lean for focused file question")
	}
	if companion.PreferLeanGather("Покажи статус гильдии", focus) {
		t.Fatal("guild status must force full gather")
	}
	if companion.PreferLeanGather("Сколько токенов потратили?", focus) {
		t.Fatal("usage must force full gather")
	}
	if companion.PreferLeanGather("Создай агента reviewer", focus) {
		t.Fatal("agent creation must force full gather")
	}
	if companion.PreferLeanGather("Что не так?", companion.ChatFocus{}) {
		t.Fatal("empty focus must not lean")
	}
}
