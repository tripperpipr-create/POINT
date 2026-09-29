package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/storage"
)

// runningQuestWithFlowChildrenForTest готовит квест наряда в `running` с
// двумя этапами Flow — незавершённым и завершённым — и milestone в работе.
func runningQuestWithFlowChildrenForTest(t *testing.T, application *App, key string) (domain.Quest, domain.WorkOrderApproval) {
	t.Helper()
	ctx := context.Background()
	workspace, approval := approvedTestWorkOrder(t, application, key)
	quest, err := application.workOrderQuestV2(ctx, workspace.ID, approval.QuestID)
	if err != nil {
		t.Fatal(err)
	}
	if quest, err = application.setWorkOrderQuestStatusV2(ctx, quest, domain.QuestRunning, "Квест выполняется"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	run := domain.FlowRun{ID: "flowrun-" + key, FlowID: "flow-" + key, WorkspaceID: workspace.ID, QuestID: quest.ID, Status: domain.RunRunning, NodeStates: map[string]domain.FlowNodeState{}, StartedAt: now}
	if err = application.store.SaveFlowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	quest.FlowRunID = run.ID
	quest.Controller["launchPhase"] = "launching"
	quest.Controller["currentMilestoneId"] = "milestone-1"
	if err = application.store.SaveQuest(ctx, quest); err != nil {
		t.Fatal(err)
	}
	for id, status := range map[string]domain.QuestStatus{"child-open-" + key: domain.QuestActive, "child-done-" + key: domain.QuestCompleted} {
		child := domain.Quest{ID: id, WorkspaceID: workspace.ID, ParentID: quest.ID, Title: id, Status: status, FlowRunID: run.ID, FlowNodeID: id, Kind: "milestone", CreatedAt: now, UpdatedAt: now}
		if err = application.store.SaveQuest(ctx, child); err != nil {
			t.Fatal(err)
		}
	}
	runtime := domain.MilestoneRuntime{MilestoneID: "milestone-1", Status: domain.QuestRunning, FlowRunID: run.ID, Attempt: 1, StartedAt: &now, UpdatedAt: now}
	if err = application.store.SaveMilestoneRuntimeV2(ctx, quest.ID, approval.WorkOrder.ID, approval.WorkOrder.Version, runtime); err != nil {
		t.Fatal(err)
	}
	return quest, approval
}

// Отмена, когда ни один этап не исполняется: колбэка завершения прогона нет,
// и прежде этапы Flow оставались активными во всех вкладках, milestone —
// `running`, а карточка читала фазу запуска.
func TestCancelClosesFlowChildrenMilestoneAndLaunchPhase(t *testing.T) {
	application := newTestApp(t)
	ctx := context.Background()
	quest, approval := runningQuestWithFlowChildrenForTest(t, application, "cancel-tree")
	if _, err := application.store.ControlWorkOrderQuestV2(ctx, quest.ID, "cancel", ""); err != nil {
		t.Fatal(err)
	}
	open, err := application.store.GetQuest(ctx, "child-open-cancel-tree")
	if err != nil || open.Status != domain.QuestCancelled || open.FinishedAt == nil {
		t.Fatalf("незавершённый этап не закрыт отменой: %s finished=%v err=%v", open.Status, open.FinishedAt, err)
	}
	done, err := application.store.GetQuest(ctx, "child-done-cancel-tree")
	if err != nil || done.Status != domain.QuestCompleted {
		t.Fatalf("отмена переписала завершённый этап: %s err=%v", done.Status, err)
	}
	runtimes, err := application.store.ListMilestoneRuntimesV2(ctx, quest.ID, approval.WorkOrder.Version)
	if err != nil {
		t.Fatal(err)
	}
	for _, runtime := range runtimes {
		if runtime.MilestoneID == "milestone-1" && runtime.Status != domain.QuestCancelled {
			t.Fatalf("milestone после отмены: %s", runtime.Status)
		}
	}
	root, err := application.store.GetQuest(ctx, quest.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, has := root.Controller["launchPhase"]; has {
		t.Fatalf("у отменённого квеста осталась фаза запуска: %v", root.Controller)
	}
	if root.Controller["statusMessage"] != "Квест отменён" || root.Controller["currentMilestoneId"] != "milestone-1" {
		t.Fatalf("контроллер после отмены: %v", root.Controller)
	}
}

// Отменённый прогон присылает завершение уже после отмены. Прежде оно
// переводило milestone в `blocked` и публиковало «Заблокировано».
func TestLateFlowFailureKeepsCancellation(t *testing.T) {
	application := newTestApp(t)
	ctx := context.Background()
	quest, approval := runningQuestWithFlowChildrenForTest(t, application, "late-callback")
	if _, err := application.store.ControlWorkOrderQuestV2(ctx, quest.ID, "cancel", ""); err != nil {
		t.Fatal(err)
	}
	application.finalizeQuestAfterFlow(quest.ID, false)
	latest, err := application.store.GetQuest(ctx, quest.ID)
	if err != nil || latest.Status != domain.QuestCancelled {
		t.Fatalf("поздний колбэк переписал отмену: %s err=%v", latest.Status, err)
	}
	if _, err = application.store.GetEvidenceBundle(ctx, quest.ID); err == nil {
		t.Fatal("отменённому квесту вынесен вердикт")
	}
	runtimes, err := application.store.ListMilestoneRuntimesV2(ctx, quest.ID, approval.WorkOrder.Version)
	if err != nil {
		t.Fatal(err)
	}
	for _, runtime := range runtimes {
		if runtime.Status == domain.QuestBlocked {
			t.Fatalf("milestone отменённого квеста стал blocked: %#v", runtime)
		}
	}
	messages, err := application.store.ListChatMessages(ctx, quest.WorkspaceID, "master", 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range messages {
		if strings.Contains(message.Content, "Заблокировано") {
			t.Fatalf("в ленту ушёл исход отменённого квеста: %q", message.Content)
		}
	}
}

// Запуск держит снимок квеста минутами, пока модель планирует. Отмена за это
// время прежде перезаписывалась снимком.
func TestLaunchProgressDoesNotReviveCancelledQuest(t *testing.T) {
	application := newTestApp(t)
	ctx := context.Background()
	workspace, approval := approvedTestWorkOrder(t, application, "launch-progress")
	snapshot, err := application.workOrderQuestV2(ctx, workspace.ID, approval.QuestID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot, err = application.setWorkOrderQuestStatusV2(ctx, snapshot, domain.QuestPreflight, "Проверяем окружение"); err != nil {
		t.Fatal(err)
	}
	snapshot.Controller["source"] = "work_order_v2"
	if _, err = application.store.ControlWorkOrderQuestV2(ctx, approval.QuestID, "cancel", ""); err != nil {
		t.Fatal(err)
	}
	if err = application.updateWorkOrderLaunchProgressV2(ctx, &snapshot, "launching", "Flow готов"); !errors.Is(err, storage.ErrQuestStatusChanged) {
		t.Fatalf("прогресс запуска записан поверх отмены: %v", err)
	}
	latest, err := application.store.GetQuest(ctx, approval.QuestID)
	if err != nil || latest.Status != domain.QuestCancelled {
		t.Fatalf("отмена потеряна: %s err=%v", latest.Status, err)
	}
	if latest.Controller["launchPhase"] != nil {
		t.Fatalf("у отменённого квеста появилась фаза запуска: %v", latest.Controller)
	}
}

// Попытка исправления строится по снимку, снятому до доставки и проверок на
// хосте. Отмена, пришедшая за это время, прежде перезаписывалась им.
func TestRepairAttemptDoesNotOverwriteCancellation(t *testing.T) {
	application, quest, _ := approvedHostCheckQuestForTest(t, 3)
	ctx := context.Background()
	var err error
	for _, status := range []domain.QuestStatus{domain.QuestVerifying, domain.QuestApplying} {
		if quest, err = application.setWorkOrderQuestStatusV2(ctx, quest, status, string(status)); err != nil {
			t.Fatal(err)
		}
	}
	approval, err := application.store.WorkOrderApprovalByQuestV2(ctx, quest.ID)
	if err != nil {
		t.Fatal(err)
	}
	before, err := application.store.ListMilestoneRuntimesV2(ctx, quest.ID, approval.WorkOrder.Version)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = application.store.ControlWorkOrderQuestV2(ctx, quest.ID, "cancel", ""); err != nil {
		t.Fatal(err)
	}
	exitCode := 1
	checks := []domain.VerificationCheck{{ID: "build", Satisfied: false, ExitCode: &exitCode, Summary: "build failed"}}
	if application.startWorkOrderRepairAttemptV2(ctx, approval, quest, domain.EvidenceBundle{}, checks) {
		t.Fatal("отменённому квесту начата новая попытка")
	}
	latest, err := application.store.GetQuest(ctx, quest.ID)
	if err != nil || latest.Status != domain.QuestCancelled {
		t.Fatalf("отмена перезаписана попыткой исправления: %s err=%v", latest.Status, err)
	}
	if latest.Controller["repairAttempt"] != nil {
		t.Fatalf("попытка записана отменённому квесту: %v", latest.Controller)
	}
	after, err := application.store.ListMilestoneRuntimesV2(ctx, quest.ID, approval.WorkOrder.Version)
	if err != nil {
		t.Fatal(err)
	}
	for i := range after {
		if after[i].Status == domain.QuestDraft && (i >= len(before) || before[i].Status != domain.QuestDraft) {
			t.Fatalf("milestone отменённого квеста сброшен к новой попытке: %#v", after[i])
		}
	}
}

// E6: квест уже стоял в needs_review, а контроллер держал фазу запуска
// `launching` и «Проверки пройдены; переносим результат в проект». Вердикт
// шлюза закрывает контроллер вместе со статусом.
func TestVerdictClosesLaunchPhaseAndMessage(t *testing.T) {
	application, quest, _ := approvedHostCheckQuestForTest(t, 1)
	ctx := context.Background()
	quest.Controller["launchPhase"] = "launching"
	quest.Controller["currentMilestoneId"] = "milestone-1"
	if err := application.store.SaveQuest(ctx, quest); err != nil {
		t.Fatal(err)
	}
	application.finalizeQuestAfterFlow(quest.ID, true)
	latest, err := application.store.GetQuest(ctx, quest.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !domain.IsTerminalQuestStatus(latest.Status) {
		t.Fatalf("вердикт не вынесен: %s", latest.Status)
	}
	if _, has := latest.Controller["launchPhase"]; has {
		t.Fatalf("фаза запуска пережила вердикт: %v", latest.Controller)
	}
	bundle, err := application.store.GetEvidenceBundle(ctx, quest.ID)
	if err != nil {
		t.Fatal(err)
	}
	if message, _ := latest.Controller["statusMessage"].(string); message == "" || message != bundle.OutcomeSummary {
		t.Fatalf("сообщение контроллера %q не итог вердикта %q", message, bundle.OutcomeSummary)
	}
	if latest.Controller["currentMilestoneId"] != "milestone-1" {
		t.Fatalf("вердикт стёр текущий milestone: %v", latest.Controller)
	}
}

// Колбэк этапа, пришедший после отмены дерева, прежде писал этапу `running`
// поверх `cancelled`: отменённый квест снова показывал этап в работе.
func TestLateStageCallbackKeepsCancelledStage(t *testing.T) {
	application := newTestApp(t)
	ctx := context.Background()
	quest, _ := runningQuestWithFlowChildrenForTest(t, application, "late-stage")
	if _, err := application.store.ControlWorkOrderQuestV2(ctx, quest.ID, "cancel", ""); err != nil {
		t.Fatal(err)
	}
	application.setFlowChildQuestStatus(quest.FlowRunID, "child-open-late-stage", domain.QuestRunning)
	stage, err := application.store.GetQuest(ctx, "child-open-late-stage")
	if err != nil || stage.Status != domain.QuestCancelled {
		t.Fatalf("поздний колбэк вернул отменённый этап в работу: %s err=%v", stage.Status, err)
	}
}

// Решение надзора, отказ сети, артефакт этапа держат снимок квеста, снятый до
// отмены. Запись таким снимком не возвращает квест в прежний статус.
func TestStaleQuestSnapshotDoesNotReviveCancellation(t *testing.T) {
	application := newTestApp(t)
	ctx := context.Background()
	quest, _ := runningQuestWithFlowChildrenForTest(t, application, "stale-snapshot")
	snapshot, err := application.store.GetQuest(ctx, quest.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = application.store.ControlWorkOrderQuestV2(ctx, quest.ID, "cancel", ""); err != nil {
		t.Fatal(err)
	}
	bumpQuestCounter(&snapshot, "supervisionContinues")
	if err = application.saveLoadedQuest(ctx, snapshot, snapshot.Status, "test"); !errors.Is(err, storage.ErrQuestStatusChanged) {
		t.Fatalf("снимок, снятый до отмены, записан: %v", err)
	}
	latest, err := application.store.GetQuest(ctx, quest.ID)
	if err != nil || latest.Status != domain.QuestCancelled {
		t.Fatalf("отмена потеряна: %s err=%v", latest.Status, err)
	}
}

// Квест без наряда: поздний колбэк отменённого Flow прежде выносил ему
// completed или failed поверх отмены.
func TestLateFlowCompletionKeepsCancelledQuest(t *testing.T) {
	application := newTestApp(t)
	ctx := context.Background()
	workspace, err := application.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	quest := domain.Quest{ID: "quest-v1-cancelled", WorkspaceID: workspace.Workspace.ID, Title: "v1", Status: domain.QuestCancelled, CreatedAt: now, UpdatedAt: now, FinishedAt: &now}
	if err = application.store.SaveQuest(ctx, quest); err != nil {
		t.Fatal(err)
	}
	application.finalizeQuestAfterFlow(quest.ID, true)
	latest, err := application.store.GetQuest(ctx, quest.ID)
	if err != nil || latest.Status != domain.QuestCancelled {
		t.Fatalf("поздний колбэк переписал отмену: %s err=%v", latest.Status, err)
	}
}
