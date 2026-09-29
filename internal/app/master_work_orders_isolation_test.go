package app

import (
	"context"
	"errors"
	"testing"

	"local-agent-workbench/internal/domain"
)

// TestFirstConversationOfNewProjectDoesNotInheritAnotherProjectsWorkOrder
// закрепляет изоляцию первого разговора. Первый разговор каждого проекта
// называется `legacy`, а все ядра пишут в одну базу. Выборка нарядов по одному
// идентификатору беседы отдавала первому чату нового проекта карточку чужого
// `legacy`: его черновик продолжался новой версией в чужом мире, а уборка
// открытых черновиков удаляла их у владельца.
func TestFirstConversationOfNewProjectDoesNotInheritAnotherProjectsWorkOrder(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { application.Shutdown(context.Background()) })
	ctx := context.Background()
	connection := saveTestConnection(t, application, "master-isolation", "openai", "Master isolation")
	configure := func(world domain.Workspace) {
		t.Helper()
		if err := application.store.SaveOrchestratorConfig(ctx, domain.OrchestratorConfig{
			ID: "master-" + world.ID, WorkspaceID: world.ID, Preset: "conductor", ConnectionID: connection.ID,
			Provider: domain.ProviderOpenAI, ProviderPreset: "openai", Model: "gpt-test",
			PlanningDepth: 70, Parallelism: 50, ApprovalStrictness: 40, TeamPreference: 80,
		}); err != nil {
			t.Fatal(err)
		}
	}
	proposal := func(id string, world domain.Workspace, goal string) domain.QuestProposal {
		return domain.QuestProposal{
			ID: id, WorkspaceID: world.ID, TeamAgentIDs: []string{},
			Brief: &domain.TaskBrief{
				State: "discussion", Goal: goal, ResultKind: "web",
				Scope:         []string{"CI"},
				OpenQuestions: []string{"Какие ветки считать незащищёнными?"},
				Criteria:      []domain.AcceptanceCriterion{{ID: "review", Kind: "manual", Text: "человек проверил"}},
				Budget:        domain.TaskBudget{Tokens: 120000, ActiveSeconds: 1800, MaxParallel: 1, MaxReplans: 1, MaxAttempts: 1, MaxProjectAgents: 1},
			},
		}
	}

	worldA := openTestWorld(t, application)
	configure(worldA)
	first := proposal("qp-project-a", worldA, "Деплой из CI проекта A")
	orderA, err := application.saveMasterWorkOrderV2(ctx, &first, nil, "legacy", nil)
	if err != nil {
		t.Fatal(err)
	}

	worldB := openTestWorld(t, application)
	if worldB.ID == worldA.ID {
		t.Fatal("тест требует двух разных проектов")
	}
	configure(worldB)
	if orders, err := application.store.ListWorkOrdersForConversationV2(ctx, worldB.ID, "legacy"); err != nil || len(orders) != 0 {
		t.Fatalf("первый разговор нового проекта уже видит наряды: %#v, %v", orders, err)
	}
	second := proposal("qp-project-b", worldB, "Отчёт проекта B")
	orderB, err := application.saveMasterWorkOrderV2(ctx, &second, nil, "legacy", nil)
	if err != nil {
		t.Fatal(err)
	}
	if orderB == orderA {
		t.Fatalf("первый разговор проекта B продолжил наряд проекта A: %q", orderB)
	}

	ownA, err := application.store.ListWorkOrdersForConversationV2(ctx, worldA.ID, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	if len(ownA) != 1 || ownA[0].ID != orderA || ownA[0].WorkspaceID != worldA.ID || ownA[0].Goal != "Деплой из CI проекта A" {
		t.Fatalf("черновик проекта A пропал или переписан проектом B: %#v", ownA)
	}
	ownB, err := application.store.ListWorkOrdersForConversationV2(ctx, worldB.ID, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	if len(ownB) != 1 || ownB[0].ID != orderB || ownB[0].WorkspaceID != worldB.ID {
		t.Fatalf("первый разговор проекта B видит не свой наряд: %#v", ownB)
	}
}

// TestQuestControlsRefuseAnotherProjectsQuest: идентификатор квеста виден из
// ядра любого проекта, потому что база общая. Окно проекта B не ставит на
// паузу и не отменяет работу проекта A.
func TestQuestControlsRefuseAnotherProjectsQuest(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { application.Shutdown(context.Background()) })
	ctx := context.Background()
	worldA := openTestWorld(t, application)
	order := managedWorkOrderV2()
	order.WorkspaceID = worldA.ID
	order.Workspace = domain.WorkspacePlan{Mode: "existing", Path: worldA.Path, Isolation: "snapshot"}
	assignReadyRosterForTest(t, application, &order)
	if order, err = application.SaveWorkOrderV2(ctx, order); err != nil {
		t.Fatal(err)
	}
	approval, err := application.store.ApproveWorkOrderV2(ctx, order.ID, order.Version, domain.WorkOrderDigest(order), "foreign-control")
	if err != nil {
		t.Fatal(err)
	}
	before, err := application.WorkOrderQuestV2(ctx, approval.QuestID)
	if err != nil {
		t.Fatal(err)
	}

	openTestWorld(t, application)
	if _, err = application.ControlWorkOrderQuestV2(ctx, approval.QuestID, "cancel", WorkOrderQuestControlRequest{}); !errors.Is(err, errForeignWorld) {
		t.Fatalf("отмена квеста чужого проекта должна быть отвергнута, получено: %v", err)
	}
	after, err := application.WorkOrderQuestV2(ctx, approval.QuestID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != before.Status {
		t.Fatalf("чужое окно изменило статус квеста: %s → %s", before.Status, after.Status)
	}
}
