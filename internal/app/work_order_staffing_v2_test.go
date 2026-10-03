package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
)

// holdStaffing не даёт фоновому подбору стартовать: тест видит наряд таким,
// каким его оставил ход Мастера.
func holdStaffing(application *App) {
	application.staffing.mu.Lock()
	application.staffing.stopping = true
	application.staffing.mu.Unlock()
}

// TODO Q15: ход Мастера кончается до подбора. Наряд сохранён с отметкой
// подбора, утвердить его нельзя, а фоновая задача дописывает версию с составом.
func TestMasterTurnEndsBeforeStaffingAndStaffingWritesNextVersion(t *testing.T) {
	application, world := rosterTestApp(t, "dispatcher")
	agent := rosterTestAgent(t, application, "Backend", "Backend-разработчик", "Держит серверную часть проекта")
	holdStaffing(application)
	proposal := rosterTestProposal(world.ID, "qp-staffing", "Собрать backend API с /health", false)
	id, err := application.saveMasterWorkOrderV2(context.Background(), &proposal, nil, "conversation-staffing", nil)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := application.WorkOrderV2(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if pending.State != "staffing" || !pending.Roster.Selecting || len(pending.Roster.Permanent) != 0 || pending.Version != 1 {
		t.Fatalf("ход должен оставить наряд в подборе: state=%s roster=%#v v%d", pending.State, pending.Roster, pending.Version)
	}
	if _, err = application.ApproveWorkOrderV2(context.Background(), id, ApproveWorkOrderV2Request{Version: pending.Version, Digest: pending.Digest, IdempotencyKey: "approve-staffing"}); err == nil {
		t.Fatal("наряд в подборе утверждён")
	}
	cfg, err := application.masterConfig(context.Background(), world.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = application.completeWorkOrderStaffingV2(context.Background(), id, cfg, ""); err != nil {
		t.Fatal(err)
	}
	staffed, err := application.WorkOrderV2(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if staffed.Version != 2 || staffed.Roster.Selecting || len(staffed.Roster.Permanent) != 1 || staffed.Roster.Permanent[0].ID != agent.ID {
		t.Fatalf("подбор не дописал состав следующей версией: v%d roster=%#v", staffed.Version, staffed.Roster)
	}
	bindings, err := application.store.ListAgentSelectionBindings(context.Background(), id)
	if err != nil || len(bindings) != 1 || bindings[0].AgentID != agent.ID {
		t.Fatalf("привязки подбора: %#v %v", bindings, err)
	}
}

// Результат подбора по устаревшей версии выбрасывается: человек или Мастер
// успели изменить наряд.
func TestStaffingDropsResultForStaleVersion(t *testing.T) {
	application, world := rosterTestApp(t, "dispatcher")
	rosterTestAgent(t, application, "Backend", "Backend-разработчик", "Держит серверную часть проекта")
	holdStaffing(application)
	proposal := rosterTestProposal(world.ID, "qp-stale", "Собрать backend API с /health", false)
	id, err := application.saveMasterWorkOrderV2(context.Background(), &proposal, nil, "conversation-stale", nil)
	if err != nil {
		t.Fatal(err)
	}
	basis, err := application.WorkOrderV2(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	revised := basis
	revised.Version++
	revised.Goal = "Собрать backend API с /health и /ready"
	if _, err = application.SaveWorkOrderV2(context.Background(), revised); err != nil {
		t.Fatal(err)
	}
	_, changed, err := application.applyWorkOrderStaffingV2(context.Background(), basis, nil, "none", agentSelectionResult{}, nil, domain.OrchestratorConfig{})
	if err != nil || !changed {
		t.Fatalf("устаревший результат не распознан: changed=%v err=%v", changed, err)
	}
	current, err := application.WorkOrderV2(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if current.Version != 2 || !strings.Contains(current.Goal, "/ready") || !current.Roster.Selecting {
		t.Fatalf("устаревший подбор переписал новую версию: v%d %q %#v", current.Version, current.Goal, current.Roster)
	}
}

// Ядро остановилось посреди подбора: восстановление мира запускает его заново.
func TestWorldRecoveryResumesInterruptedStaffing(t *testing.T) {
	application, world := rosterTestApp(t, "dispatcher")
	agent := rosterTestAgent(t, application, "Backend", "Backend-разработчик", "Держит серверную часть проекта")
	holdStaffing(application)
	proposal := rosterTestProposal(world.ID, "qp-resume", "Собрать backend API с /health", false)
	id, err := application.saveMasterWorkOrderV2(context.Background(), &proposal, nil, "conversation-resume", nil)
	if err != nil {
		t.Fatal(err)
	}
	application.staffing.mu.Lock()
	application.staffing.stopping = false
	application.staffing.mu.Unlock()
	application.resumeWorkOrderStaffingV2(context.Background(), world.ID)
	application.waitWorkOrderStaffing()
	staffed, err := application.WorkOrderV2(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if staffed.Roster.Selecting || len(staffed.Roster.Permanent) != 1 || staffed.Roster.Permanent[0].ID != agent.ID {
		t.Fatalf("прерванный подбор не продолжен: %#v", staffed.Roster)
	}
}

// Готовый наряд с отметкой подбора домен не принимает.
func TestSelectingWorkOrderCannotBeReady(t *testing.T) {
	order := domain.WorkOrder{ID: "w", Version: 1, State: "ready", Goal: "g", Criteria: []domain.AcceptanceCriterion{{ID: "c", Text: "t", Kind: "manual"}}, Roster: domain.AgentRosterPlan{Selecting: true}}
	if err := domain.ValidateWorkOrder(order); err == nil || !strings.Contains(err.Error(), "selection") {
		t.Fatalf("ready в подборе принят: %v", err)
	}
}

// Q15: проваленный подбор повторяется кнопкой «Подобрать снова»: следующая
// версия снова в подборе, без причины отказа, и подбор дописывает состав.
func TestFailedStaffingCanBeRetried(t *testing.T) {
	application, world := rosterTestApp(t, "dispatcher")
	agent := rosterTestAgent(t, application, "Backend", "Backend-разработчик", "Держит серверную часть проекта")
	holdStaffing(application)
	ctx := context.Background()
	proposal := rosterTestProposal(world.ID, "qp-restaff", "Собрать backend API с /health", false)
	id, err := application.saveMasterWorkOrderV2(ctx, &proposal, nil, "conversation-restaff", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = application.RestaffWorkOrderV2(ctx, id, ""); err == nil {
		t.Fatal("повтор подбора, который идёт и не проваливался")
	}
	cfg, err := application.masterConfig(ctx, world.ID)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := application.WorkOrderV2(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = application.applyWorkOrderStaffingV2(ctx, pending, nil, "", agentSelectionResult{}, errors.New("комплектовщик не ответил"), cfg); err != nil {
		t.Fatal(err)
	}
	failed, err := application.WorkOrderV2(ctx, id)
	if err != nil || failed.Roster.SelectionError == "" || failed.Roster.Selecting {
		t.Fatalf("отказ подбора не записан: %#v %v", failed.Roster, err)
	}
	retried, err := application.RestaffWorkOrderV2(ctx, id, "")
	if err != nil {
		t.Fatal(err)
	}
	if retried.Version != failed.Version+1 || !retried.Roster.Selecting || retried.Roster.SelectionError != "" || retried.State != "staffing" {
		t.Fatalf("повтор подбора: v%d %#v %s", retried.Version, retried.Roster, retried.State)
	}
	if err = application.completeWorkOrderStaffingV2(ctx, id, cfg, ""); err != nil {
		t.Fatal(err)
	}
	staffed, err := application.WorkOrderV2(ctx, id)
	if err != nil || len(staffed.Roster.Permanent) != 1 || staffed.Roster.Permanent[0].ID != agent.ID {
		t.Fatalf("повторный подбор не дописал состав: %#v %v", staffed.Roster, err)
	}
}
