package companion_test

import (
	"context"
	"strings"
	"testing"

	"local-agent-workbench/internal/companion"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
)

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
