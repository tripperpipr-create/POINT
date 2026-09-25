package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/storage"
)

func approvedTestWorkOrder(t *testing.T, application *App, key string) (domain.Workspace, domain.WorkOrderApproval) {
	t.Helper()
	ctx := context.Background()
	workspace := openTestWorld(t, application)
	now := time.Now().UTC()
	exitCode := 0
	order := domain.WorkOrder{
		WorkspaceID: workspace.ID, State: "ready", Goal: "keep human decisions", Scope: []string{"api"},
		Criteria:  []domain.AcceptanceCriterion{{ID: "tests", Kind: "verification", Text: "tests pass", Tool: "run_command", Arguments: json.RawMessage(`{"command":"go test ./..."}`), ExpectedExitCode: &exitCode}},
		Workspace: domain.WorkspacePlan{Mode: "existing", Path: workspace.Path, Isolation: "snapshot"},
		Stack:     domain.StackPresetRef{ID: "api", Version: "1", Category: "api", Source: "benchmark"},
		Routing:   domain.ModelRoutingPolicy{Mode: "fixed", FixedConnectionID: "connection", FixedModel: "model"},
		Budget:    domain.BudgetEnvelope{Preset: "small", Tokens: 1000, ActiveSeconds: 60, MaxParallel: 1, MaxAttempts: 1},
		Delivery:  domain.DeliveryPolicy{ApplyMode: "automatic", CommitMode: "none", KeepPartialDays: 30},
		CreatedAt: now, UpdatedAt: now,
	}
	order, err := application.store.SaveWorkOrderV2(ctx, order)
	if err != nil {
		t.Fatal(err)
	}
	approval, err := application.store.ApproveWorkOrderV2(ctx, order.ID, order.Version, domain.WorkOrderDigest(order), key)
	if err != nil {
		t.Fatal(err)
	}
	return workspace, approval
}

// Рестарт застал квест на проверке окружения. «Продолжить» обязано вернуть
// его туда же — к новой проверке и запуску, — а не в `running` без единого
// исполнителя: пауза восстановления теперь записана с исходным статусом.
func TestResumeAfterRestartReturnsQuestToWhereRestartFoundIt(t *testing.T) {
	application := newTestApp(t)
	ctx := context.Background()
	workspace, approval := approvedTestWorkOrder(t, application, "restart-preflight")
	application.pauseInterruptedWorkOrderQuestsV2(ctx)
	quest, err := application.workOrderQuestV2(ctx, workspace.ID, approval.QuestID)
	if err != nil || quest.Status != domain.QuestPaused {
		t.Fatalf("восстановление не поставило паузу: %s err=%v", quest.Status, err)
	}
	status, err := application.store.ControlWorkOrderQuestV2(ctx, approval.QuestID, "resume", "")
	if err != nil {
		t.Fatal(err)
	}
	if status != domain.QuestPreflight {
		t.Fatalf("продолжение после рестарта вернуло %s вместо preflight: карточка показала бы работу, которой нет", status)
	}
}

// Правка утверждённого наряда ставит квест на паузу до утверждения новой
// версии. «Продолжить» раньше исполняло прежнюю версию с её сетью и правами.
func TestResumeRefusedWhileRevisionAwaitsApproval(t *testing.T) {
	application := newTestApp(t)
	ctx := context.Background()
	workspace, approval := approvedTestWorkOrder(t, application, "revision-resume")
	quest, err := application.workOrderQuestV2(ctx, workspace.ID, approval.QuestID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = application.setWorkOrderQuestStatusV2(ctx, quest, domain.QuestRunning, "Квест выполняется"); err != nil {
		t.Fatal(err)
	}
	revised := approval.WorkOrder
	revised.Version++
	revised.State = "ready"
	revised.Goal = "keep human decisions, revised"
	revised.Network = nil
	revised = domain.NormalizeWorkOrder(revised)
	if _, err = application.store.SaveWorkOrderV2(ctx, revised); err != nil {
		t.Fatal(err)
	}
	if _, err = application.ControlWorkOrderQuestV2(ctx, approval.QuestID, "resume", WorkOrderQuestControlRequest{}); !errors.Is(err, storage.ErrWorkOrderRevisionPending) {
		t.Fatalf("продолжение мимо утверждения новой версии: %v", err)
	}
	if _, err = application.store.ControlWorkOrderQuestV2(ctx, approval.QuestID, "resume", ""); !errors.Is(err, storage.ErrWorkOrderRevisionPending) {
		t.Fatalf("хранилище продолжило прежнюю версию: %v", err)
	}
	latest, err := application.workOrderQuestV2(ctx, workspace.ID, approval.QuestID)
	if err != nil || latest.Status != domain.QuestPaused {
		t.Fatalf("квест вышел из паузы правки: %s err=%v", latest.Status, err)
	}
}

// Человек отменил квест, пока финализатор доставлял результат. Отказ шлюза
// у отменённого квеста раньше перезаписывал отмену на `blocked` — снимком
// квеста, взятым до нажатия.
func TestFinalizationKeepsHumanCancellation(t *testing.T) {
	application := newTestApp(t)
	ctx := context.Background()
	workspace, approval := approvedTestWorkOrder(t, application, "cancel-applying")
	quest, err := application.workOrderQuestV2(ctx, workspace.ID, approval.QuestID)
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []domain.QuestStatus{domain.QuestRunning, domain.QuestVerifying, domain.QuestApplying} {
		if quest, err = application.setWorkOrderQuestStatusV2(ctx, quest, status, string(status)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = application.store.ControlWorkOrderQuestV2(ctx, approval.QuestID, "cancel", ""); err != nil {
		t.Fatal(err)
	}
	application.blockWorkOrderFinalizationV2(ctx, quest, "Evidence gate не принял итог", errors.New("quest is not in a finalizable state"))
	latest, err := application.workOrderQuestV2(ctx, workspace.ID, approval.QuestID)
	if err != nil || latest.Status != domain.QuestCancelled {
		t.Fatalf("отмена человеком перезаписана финализатором: %s err=%v", latest.Status, err)
	}
	returned, err := application.setWorkOrderQuestStatusV2(ctx, quest, domain.QuestBlocked, "stale")
	if !errors.Is(err, storage.ErrQuestStatusChanged) {
		t.Fatalf("переход по устаревшему снимку прошёл: %v", err)
	}
	// Непринятый переход не выдаёт целевой статус за состоявшийся: иначе
	// следующий переход по этому снимку ждал бы в базе `blocked`.
	if returned.Status != domain.QuestApplying {
		t.Fatalf("отказ вернул снимок с целевым статусом: %s", returned.Status)
	}
}

// Финализатор, получивший успешный Flow у квеста, который человек уже
// приостановил, не переносит результат в проект и не выносит вердикт.
func TestFinalizationDoesNotDeliverForPausedQuest(t *testing.T) {
	application := newTestApp(t)
	ctx := context.Background()
	workspace, approval := approvedTestWorkOrder(t, application, "paused-delivery")
	quest, err := application.workOrderQuestV2(ctx, workspace.ID, approval.QuestID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = application.setWorkOrderQuestStatusV2(ctx, quest, domain.QuestRunning, "Квест выполняется"); err != nil {
		t.Fatal(err)
	}
	if _, err = application.store.ControlWorkOrderQuestV2(ctx, approval.QuestID, "pause", ""); err != nil {
		t.Fatal(err)
	}
	application.finalizeWorkOrderQuestAfterFlowV2(approval, true)
	latest, err := application.workOrderQuestV2(ctx, workspace.ID, approval.QuestID)
	if err != nil || latest.Status != domain.QuestPaused {
		t.Fatalf("пауза человека не сохранена: %s err=%v", latest.Status, err)
	}
	if _, err = application.store.GetEvidenceBundle(ctx, approval.QuestID); err == nil {
		t.Fatal("у приостановленного квеста вынесен вердикт")
	}
}

// «Отдал ключ → Продолжить» возвращает квест с живым Flow в `preflight`.
// Завершившийся Flow такого квеста проверяется, а не оставляет его в
// `preflight` навсегда без улик и вердикта.
func TestFinalizationProceedsFromPreflightWithLiveFlow(t *testing.T) {
	application := newTestApp(t)
	ctx := context.Background()
	workspace, approval := approvedTestWorkOrder(t, application, "preflight-finalize")
	quest, err := application.workOrderQuestV2(ctx, workspace.ID, approval.QuestID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	run := domain.FlowRun{ID: "preflight-flow-run", FlowID: "preflight-flow", WorkspaceID: workspace.ID, QuestID: quest.ID, Status: domain.RunCompleted, NodeStates: map[string]domain.FlowNodeState{}, StartedAt: now, FinishedAt: &now}
	if err = application.store.SaveFlowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	quest.FlowID, quest.FlowRunID = run.FlowID, run.ID
	if err = application.store.SaveQuest(ctx, quest); err != nil {
		t.Fatal(err)
	}
	application.finalizeWorkOrderQuestAfterFlowV2(approval, true)
	latest, err := application.workOrderQuestV2(ctx, workspace.ID, approval.QuestID)
	if err != nil {
		t.Fatal(err)
	}
	if latest.Status == domain.QuestPreflight {
		t.Fatal("квест остался в preflight после завершения своего Flow")
	}
	if _, err = application.store.GetEvidenceBundle(ctx, approval.QuestID); err != nil {
		t.Fatalf("вердикт не вынесен: %v", err)
	}
}

// Квест новой версии в `preflight` ещё без своего Flow: запоздалое
// завершение прежнего Flow не выносит ему вердикт и ничего не доставляет.
func TestFinalizationIgnoresCompletionForQuestWithoutItsFlow(t *testing.T) {
	application := newTestApp(t)
	ctx := context.Background()
	workspace, approval := approvedTestWorkOrder(t, application, "preflight-foreign-flow")
	application.finalizeWorkOrderQuestAfterFlowV2(approval, true)
	latest, err := application.workOrderQuestV2(ctx, workspace.ID, approval.QuestID)
	if err != nil || latest.Status != domain.QuestPreflight {
		t.Fatalf("чужое завершение сдвинуло незапущенный квест: %s err=%v", latest.Status, err)
	}
	if _, err = application.store.GetEvidenceBundle(ctx, approval.QuestID); err == nil {
		t.Fatal("незапущенной версии вынесен вердикт по чужому Flow")
	}
}
