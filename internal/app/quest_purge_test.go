package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
)

// Снос делает то, на чём удаление останавливалось три раза подряд.
//
// Каждый из этих трёх отказов сам по себе честен, и вместе они оставляли
// человека, решившего убрать работу, без единого способа это сделать: живой
// прогон, идущая схема и незакрытый набор правок держали квест по очереди, а
// правки в проекте оставались и после того, как квест наконец удавалось убрать.
func TestPurgeQuestStopsRevertsAndDeletesWhereDeleteRefuses(t *testing.T) {
	application := newTestApp(t)
	root := t.TempDir()
	view, err := application.OpenWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	quest, err := application.SaveQuest(domain.Quest{Title: "Починить оплату", WorkspaceID: view.Workspace.ID})
	if err != nil {
		t.Fatal(err)
	}
	// Файл, который агент переписал: снос обязан вернуть его к исходному.
	target := filepath.Join(root, "billing.go")
	if err = os.WriteFile(target, []byte("было\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err = application.store.SaveExecution(ctx, domain.ExecutionInstance{
		ID: "exec-purge-1", WorkspaceID: view.Workspace.ID, QuestID: quest.ID, Status: domain.RunRunning,
	}); err != nil {
		t.Fatal(err)
	}
	if err = application.store.SaveFlowRun(ctx, domain.FlowRun{
		ID: "flowrun-purge-1", WorkspaceID: view.Workspace.ID, QuestID: quest.ID, Status: domain.RunRunning,
	}); err != nil {
		t.Fatal(err)
	}
	if err = application.store.SaveChangeSet(ctx, domain.ChangeSet{
		ID: "set-purge-1", WorkspaceID: view.Workspace.ID, ExecutionID: "exec-purge-1", QuestID: quest.ID,
		Title: "Правка обработчика", Status: domain.ChangeSetPending,
		Items: []domain.ChangeItem{{ID: "ci-purge-1", Path: "billing.go", Kind: "modify"}},
	}); err != nil {
		t.Fatal(err)
	}

	// Сначала убеждаемся, что обычное удаление здесь по-прежнему отказывает:
	// снос не подменяет его, а отвечает на другой запрос человека.
	if err = application.DeleteQuest(quest.ID); err == nil {
		t.Fatal("обычное удаление перестало беречь живую работу")
	}

	result, err := application.PurgeQuest(quest.ID)
	if err != nil {
		t.Fatalf("снос не прошёл: %v", err)
	}
	if result.Title != "Починить оплату" || result.Deleted["quests"] != 1 {
		t.Fatalf("отчёт о сносе не сходится: %+v", result)
	}
	if result.Stopped == 0 {
		t.Fatalf("снос ничего не остановил, хотя схема шла: %+v", result)
	}
	quests, err := application.store.ListQuests(ctx, view.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range quests {
		if item.ID == quest.ID {
			t.Fatal("квест пережил снос")
		}
	}
	sets, err := application.store.ListChangeSets(ctx, view.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, set := range sets {
		if set.QuestID == quest.ID {
			t.Fatal("набор правок пережил снос квеста")
		}
	}
	runs, err := application.store.ListFlowRuns(ctx, view.Workspace.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range runs {
		if run.QuestID == quest.ID {
			t.Fatal("прогон схемы пережил снос квеста")
		}
	}
	// Повторный снос отвечает словами, а не молчанием: кнопку нажимают дважды.
	if _, err = application.PurgeQuest(quest.ID); err == nil {
		t.Fatal("снос несуществующего квеста прошёл молча")
	}
}

// Подквест уходит вместе с родителем: удаление здесь отказывало, а снос обязан
// дойти до конца — иначе останется карточка, ведущая к удалённому родителю.
func TestPurgeQuestTakesSubquestsWithIt(t *testing.T) {
	application := newTestApp(t)
	view, err := application.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	parent, err := application.SaveQuest(domain.Quest{Title: "Родитель", WorkspaceID: view.Workspace.ID})
	if err != nil {
		t.Fatal(err)
	}
	child, err := application.SaveQuest(domain.Quest{Title: "Подзадача", WorkspaceID: view.Workspace.ID, ParentID: parent.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err = application.DeleteQuest(parent.ID); err == nil {
		t.Fatal("обычное удаление перестало беречь подквест")
	}
	result, err := application.PurgeQuest(parent.ID)
	if err != nil {
		t.Fatalf("снос родителя не прошёл: %v", err)
	}
	if result.Deleted["quests"] != 2 {
		t.Fatalf("снесён не весь куст: %+v", result)
	}
	quests, err := application.store.ListQuests(context.Background(), view.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range quests {
		if item.ID == parent.ID || item.ID == child.ID {
			t.Fatalf("квест %q пережил снос куста", item.Title)
		}
	}
}

// Неудачный откат не отменяет снос и не молчит.
//
// Человек нажал «удалить со всеми изменениями»: если правку вернуть не вышло —
// файл изменён вручную, набор уже в коммите, — она остаётся в проекте, и об
// этом надо сказать вслух. Тихий снос оставил бы чужие правки без карточки,
// по которой их можно было бы найти.
func TestPurgeQuestNamesWhatItCouldNotRevert(t *testing.T) {
	application := newTestApp(t)
	root := t.TempDir()
	view, err := application.OpenWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	quest, err := application.SaveQuest(domain.Quest{Title: "Правки", WorkspaceID: view.Workspace.ID})
	if err != nil {
		t.Fatal(err)
	}
	// Набор объявлен применённым, но файла, который он менял, в проекте нет:
	// откат такого набора не может сойтись.
	if err = application.store.SaveChangeSet(ctx, domain.ChangeSet{
		ID: "set-broken", WorkspaceID: view.Workspace.ID, ExecutionID: "exec-broken", QuestID: quest.ID,
		Title: "Потерянная правка", Status: domain.ChangeSetApplied,
		Items: []domain.ChangeItem{{
			ID: "ci-broken", Path: "gone.go", Kind: "modify",
			OriginalHash: "0000", AppliedHash: "1111", OriginalContent: "было\n", ProposedContent: "стало\n",
		}},
	}); err != nil {
		t.Fatal(err)
	}
	result, err := application.PurgeQuest(quest.ID)
	if err != nil {
		t.Fatalf("снос остановился на неудачном откате: %v", err)
	}
	if result.Deleted["quests"] != 1 {
		t.Fatalf("квест не снесён: %+v", result)
	}
	if len(result.Failures) == 0 {
		t.Fatal("откат не сошёлся, а снос отчитался, будто всё вернул")
	}
	named := strings.Join([]string{result.Failures[0].Title, result.Failures[0].Reason}, " ")
	if !strings.Contains(named, "Потерянная правка") && !strings.Contains(named, "gone.go") {
		t.Fatalf("отчёт не назвал, что осталось в проекте: %+v", result.Failures)
	}
}

// Удаление разговора уносит незавершённую работу, которую в нём начали.
//
// Раньше здесь стоял отказ: «разговор ведёт квест — закройте или отмените
// квест». Человек, закрывающий переписку, получал запрет и отправлялся решать
// ту же самую задачу в двух других разделах.
func TestDeletingConversationPurgesItsUnfinishedQuest(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { application.Shutdown(context.Background()) })
	ctx := context.Background()
	world := openTestWorld(t, application)

	conversation := "chat-purge-1"
	order := managedWorkOrderV2()
	order.WorkspaceID = world.ID
	order.ConversationID = conversation
	order.Workspace = domain.WorkspacePlan{Mode: "existing", Path: world.Path, Isolation: "snapshot"}
	assignReadyRosterForTest(t, application, &order)
	order, err = application.SaveWorkOrderV2(ctx, order)
	if err != nil {
		t.Fatal(err)
	}
	approval, err := application.store.ApproveWorkOrderV2(ctx, order.ID, order.Version, domain.WorkOrderDigest(order), "purge-on-chat-delete")
	if err != nil {
		t.Fatal(err)
	}
	quest, err := application.workOrderQuestV2(ctx, world.ID, approval.QuestID)
	if err != nil {
		t.Fatal(err)
	}
	quest.Status = domain.QuestRunning
	if err = application.store.SaveQuest(ctx, quest); err != nil {
		t.Fatal(err)
	}
	if err = application.store.SaveMasterConversation(ctx, MasterSession{ID: conversation, WorkspaceID: world.ID, Title: "Оплата", Mode: "auto", WorkMode: "plan"}); err != nil {
		t.Fatal(err)
	}

	purged, err := application.purgeUnfinishedQuestsOfConversation(ctx, world.ID, conversation)
	if err != nil {
		t.Fatalf("каскад не прошёл: %v", err)
	}
	if len(purged) != 1 || purged[0].QuestID != quest.ID {
		t.Fatalf("незавершённый квест разговора не снесён: %+v", purged)
	}
	quests, err := application.store.ListQuests(ctx, world.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range quests {
		if item.ID == quest.ID {
			t.Fatal("квест пережил удаление разговора")
		}
	}
}

// Завершённую работу удаление разговора не трогает: её правки человек принял, и
// возвращать проект к состоянию до неё никто не просил.
func TestDeletingConversationKeepsFinishedQuest(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { application.Shutdown(context.Background()) })
	ctx := context.Background()
	world := openTestWorld(t, application)

	conversation := "chat-purge-2"
	order := managedWorkOrderV2()
	order.WorkspaceID = world.ID
	order.ConversationID = conversation
	order.Workspace = domain.WorkspacePlan{Mode: "existing", Path: world.Path, Isolation: "snapshot"}
	assignReadyRosterForTest(t, application, &order)
	order, err = application.SaveWorkOrderV2(ctx, order)
	if err != nil {
		t.Fatal(err)
	}
	approval, err := application.store.ApproveWorkOrderV2(ctx, order.ID, order.Version, domain.WorkOrderDigest(order), "keep-on-chat-delete")
	if err != nil {
		t.Fatal(err)
	}
	quest, err := application.workOrderQuestV2(ctx, world.ID, approval.QuestID)
	if err != nil {
		t.Fatal(err)
	}
	quest.Status = domain.QuestNeedsReview
	if err = application.store.SaveQuest(ctx, quest); err != nil {
		t.Fatal(err)
	}
	purged, err := application.purgeUnfinishedQuestsOfConversation(ctx, world.ID, conversation)
	if err != nil {
		t.Fatal(err)
	}
	if len(purged) != 0 {
		t.Fatalf("завершённый квест снесён вместе с разговором: %+v", purged)
	}
	quests, err := application.store.ListQuests(ctx, world.ID)
	if err != nil {
		t.Fatal(err)
	}
	kept := false
	for _, item := range quests {
		if item.ID == quest.ID {
			kept = true
		}
	}
	if !kept {
		t.Fatal("завершённый квест исчез вместе с разговором")
	}
}
