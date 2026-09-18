package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

// Наряд принадлежит разговору, в котором его собрали, и уходит вместе с ним.
//
// Удаление чата чистило реплики и ходы, а наряды оставляло: один висел сиротой
// удалённого разговора, второй всплывал в пустом чате `legacy`, куда человек
// попадает, удалив все остальные. Убрать его было нечем — удаления у наряда не
// существовало.
func TestDeletingConversationTakesItsWorkOrders(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { application.Shutdown(context.Background()) })
	world := openTestWorld(t, application)
	ctx := context.Background()

	newOrder := func(conversationID, goal string) domain.WorkOrder {
		order := managedWorkOrderV2()
		order.WorkspaceID = world.ID
		order.ConversationID = conversationID
		order.Goal = goal
		order.Workspace = domain.WorkspacePlan{Mode: "existing", Path: world.Path, Isolation: "snapshot"}
		saved, saveErr := application.SaveWorkOrderV2(ctx, order)
		if saveErr != nil {
			t.Fatal(saveErr)
		}
		return saved
	}
	kept := newOrder("chat_kept", "Оставить этот наряд")
	doomed := newOrder("chat_doomed", "Убрать вместе с чатом")
	if err = application.store.SaveMasterConversation(ctx, MasterSession{
		ID: "chat_doomed", WorkspaceID: world.ID, Title: "Разговор под удаление", Mode: "auto", WorkMode: "plan",
	}); err != nil {
		t.Fatal(err)
	}

	if _, err = application.UpdateMasterSession(ctx, MasterSessionUpdate{Action: "delete", ID: "chat_doomed"}); err != nil {
		t.Fatalf("разговор без живого квеста обязан удаляться: %v", err)
	}
	if _, err = application.WorkOrderV2(ctx, doomed.ID); err == nil {
		t.Fatal("наряд удалённого разговора остался сиротой")
	}
	if _, err = application.WorkOrderV2(ctx, kept.ID); err != nil {
		t.Fatalf("удаление чужого разговора унесло посторонний наряд: %v", err)
	}

	// Наряд без квеста убирается отдельно — он договор, а не работа.
	if err = application.DeleteWorkOrderV2(ctx, kept.ID); err != nil {
		t.Fatalf("наряд без квеста обязан убираться: %v", err)
	}
	if _, err = application.WorkOrderV2(ctx, kept.ID); err == nil {
		t.Fatal("наряд остался после удаления")
	}
}

func TestWorkOrderQuestControlsDriveFlowRuntime(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { application.Shutdown(context.Background()) })
	world := openTestWorld(t, application)
	order := managedWorkOrderV2()
	order.WorkspaceID = world.ID
	order.Workspace = domain.WorkspacePlan{Mode: "existing", Path: world.Path, Isolation: "snapshot"}
	order, err = application.SaveWorkOrderV2(context.Background(), order)
	if err != nil {
		t.Fatal(err)
	}
	approval, err := application.store.ApproveWorkOrderV2(context.Background(), order.ID, order.Version, domain.WorkOrderDigest(order), "runtime-controls")
	if err != nil {
		t.Fatal(err)
	}
	quest, err := application.workOrderQuestV2(context.Background(), world.ID, approval.QuestID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	flow := domain.FlowGraph{ID: "flow-controls", WorkspaceID: world.ID, Name: "Runtime controls", CreatedAt: now, UpdatedAt: now}
	if err = application.store.SaveFlow(context.Background(), flow); err != nil {
		t.Fatal(err)
	}
	flowRun := domain.FlowRun{ID: "flow-run-controls", FlowID: flow.ID, WorkspaceID: world.ID, QuestID: quest.ID, Status: domain.RunRunning, NodeStates: map[string]domain.FlowNodeState{}, StartedAt: now}
	if err = application.store.SaveFlowRun(context.Background(), flowRun); err != nil {
		t.Fatal(err)
	}
	quest.Status = domain.QuestRunning
	quest.FlowID = flow.ID
	quest.FlowRunID = flowRun.ID
	if err = application.store.SaveQuest(context.Background(), quest); err != nil {
		t.Fatal(err)
	}
	execution := domain.ExecutionInstance{ID: "execution-pending-controls", WorkspaceID: world.ID, QuestID: quest.ID, FlowRunID: flowRun.ID, Task: "pending stage", Status: domain.RunPending, StartedAt: now}
	if err = application.store.SaveExecution(context.Background(), execution); err != nil {
		t.Fatal(err)
	}

	paused, err := application.ControlWorkOrderQuestV2(context.Background(), quest.ID, "pause", WorkOrderQuestControlRequest{})
	if err != nil || paused.Status != domain.QuestPaused || paused.FlowRunID != flowRun.ID {
		t.Fatalf("pause result=%#v err=%v", paused, err)
	}
	storedFlow, err := application.store.GetFlowRun(context.Background(), flowRun.ID)
	if err != nil || storedFlow.Status != domain.RunPaused {
		t.Fatalf("pause did not close Flow scheduler: %#v err=%v", storedFlow, err)
	}

	resumed, err := application.ControlWorkOrderQuestV2(context.Background(), quest.ID, "resume", WorkOrderQuestControlRequest{})
	if err != nil || resumed.Status != domain.QuestRunning {
		t.Fatalf("resume result=%#v err=%v", resumed, err)
	}
	storedFlow, err = application.store.GetFlowRun(context.Background(), flowRun.ID)
	if err != nil || storedFlow.Status != domain.RunRunning {
		t.Fatalf("resume did not reopen Flow scheduler: %#v err=%v", storedFlow, err)
	}
	revision := order
	revision.State = "ready"
	revision.Goal = "Новая цель после запуска"
	revised, err := application.ReviseWorkOrderV2(context.Background(), order.ID, ReviseWorkOrderV2Request{
		ExpectedVersion: order.Version, ExpectedDigest: domain.WorkOrderDigest(order), IdempotencyKey: "revise-runtime-once", WorkOrder: revision,
	})
	if err != nil || revised.Version != order.Version+1 {
		t.Fatalf("revision result=%#v err=%v", revised, err)
	}
	storedFlow, err = application.store.GetFlowRun(context.Background(), flowRun.ID)
	if err != nil || storedFlow.Status != domain.RunPaused {
		t.Fatalf("scope revision did not pause Flow scheduler: %#v err=%v", storedFlow, err)
	}

	cancelled, err := application.ControlWorkOrderQuestV2(context.Background(), quest.ID, "cancel", WorkOrderQuestControlRequest{})
	if err != nil || cancelled.Status != domain.QuestCancelled || cancelled.AffectedRuns != 1 {
		t.Fatalf("cancel result=%#v err=%v", cancelled, err)
	}
	storedFlow, err = application.store.GetFlowRun(context.Background(), flowRun.ID)
	if err != nil || storedFlow.Status != domain.RunCancelled || storedFlow.FinishedAt == nil {
		t.Fatalf("cancel did not terminate Flow: %#v err=%v", storedFlow, err)
	}
	storedExecution, err := application.store.GetExecution(context.Background(), execution.ID)
	if err != nil || storedExecution.Status != domain.RunCancelled || storedExecution.FinishedAt == nil {
		t.Fatalf("pending execution survived cancellation: %#v err=%v", storedExecution, err)
	}
}

// Заблокированный квест возобновляется проверкой окружения, а не притворством.
// Причина блокировки здесь цела (автономный проект без Docker sandbox), поэтому
// честный исход один: квест снова уходит в blocked с той же причиной — но уже
// по решению человека, а не как тупик, из которого есть только отмена.
func TestBlockedWorkOrderQuestRetriesPreflight(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { application.Shutdown(context.Background()) })
	world := openTestWorld(t, application)
	order := managedWorkOrderV2()
	order.WorkspaceID = world.ID
	order.Workspace = domain.WorkspacePlan{Mode: "existing", Path: world.Path, Isolation: "snapshot"}
	order, err = application.SaveWorkOrderV2(context.Background(), order)
	if err != nil {
		t.Fatal(err)
	}
	approval, err := application.store.ApproveWorkOrderV2(context.Background(), order.ID, order.Version, domain.WorkOrderDigest(order), "blocked-retry")
	if err != nil {
		t.Fatal(err)
	}
	quest, err := application.workOrderQuestV2(context.Background(), world.ID, approval.QuestID)
	if err != nil {
		t.Fatal(err)
	}
	quest.Status = domain.QuestBlocked
	if err = application.store.SaveQuest(context.Background(), quest); err != nil {
		t.Fatal(err)
	}
	result, err := application.ControlWorkOrderQuestV2(context.Background(), quest.ID, "resume", WorkOrderQuestControlRequest{})
	if err != nil {
		t.Fatalf("возобновление заблокированного квеста отклонено: %v", err)
	}
	if result.Status != domain.QuestPreflight {
		t.Fatalf("повтор не вернул наряд к проверке окружения: %s", result.Status)
	}
	// Запуск идёт фоном, но исход обязан появиться сам: человек не должен
	// остаться с вечным «Проверяем окружение» без причины и без кнопки.
	application.waitWorkOrderLaunches()
	after, err := application.WorkOrderQuestV2(context.Background(), quest.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != domain.QuestBlocked {
		t.Fatalf("после неудачного повтора квест остался в %s", after.Status)
	}
	message, _ := after.Controller["statusMessage"].(string)
	if strings.TrimSpace(message) == "" || message == workOrderLaunchStartedMessage {
		t.Fatalf("причина блокировки не записана: %q", message)
	}
}
