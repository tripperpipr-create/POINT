package companion_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/agent"
	"local-agent-workbench/internal/companion"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
)

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
