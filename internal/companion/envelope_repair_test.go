package companion_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/companion"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
)

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
