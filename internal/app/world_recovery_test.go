package app

import (
	"context"
	"testing"

	"local-agent-workbench/internal/domain"
)

// TestCoreStartupRecoversOnlyItsOwnWorld закрепляет, что холодный старт ядра
// одного проекта не трогает живую работу другого. Ядра делят одну базу, и
// прежде старт проекта B ставил на паузу идущий квест проекта A и прерывал
// ход его Мастера. Брошенную работу A разбирает ядро A, когда поднимается.
func TestCoreStartupRecoversOnlyItsOwnWorld(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	ctx := context.Background()
	dataDir := t.TempDir()
	coreA, err := New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	worldA := openTestWorld(t, coreA)
	order := managedWorkOrderV2()
	order.WorkspaceID = worldA.ID
	order.Workspace = domain.WorkspacePlan{Mode: "existing", Path: worldA.Path, Isolation: "snapshot"}
	assignReadyRosterForTest(t, coreA, &order)
	if order, err = coreA.SaveWorkOrderV2(ctx, order); err != nil {
		t.Fatal(err)
	}
	approval, err := coreA.store.ApproveWorkOrderV2(ctx, order.ID, order.Version, domain.WorkOrderDigest(order), "own-world-recovery")
	if err != nil {
		t.Fatal(err)
	}
	quest, err := coreA.workOrderQuestV2(ctx, worldA.ID, approval.QuestID)
	if err != nil {
		t.Fatal(err)
	}
	quest.Status = domain.QuestRunning
	if err = coreA.store.SaveQuest(ctx, quest); err != nil {
		t.Fatal(err)
	}
	turn := domain.MasterTurn{ID: "turn-a", WorkspaceID: worldA.ID, ConversationID: "legacy", Status: "streaming", Reply: "Думаю над деплоем", RequestHash: "hash-a"}
	if err = coreA.store.SaveMasterTurn(ctx, turn); err != nil {
		t.Fatal(err)
	}

	// Проект B поднимает своё ядро на той же базе, пока ядро A работает.
	boundaryB := t.TempDir()
	coreB, err := New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err = coreB.SetWorkspaceBoundary(boundaryB); err != nil {
		t.Fatal(err)
	}
	coreB.Startup(ctx)
	if _, err = coreB.OpenWorkspace(boundaryB); err != nil {
		t.Fatal(err)
	}
	coreB.Shutdown(ctx)

	live, err := coreA.workOrderQuestV2(ctx, worldA.ID, quest.ID)
	if err != nil {
		t.Fatal(err)
	}
	if live.Status != domain.QuestRunning {
		t.Fatalf("старт ядра B изменил идущий квест проекта A: %s", live.Status)
	}
	liveTurn, err := coreA.store.MasterTurn(ctx, worldA.ID, turn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if liveTurn.Status != "streaming" {
		t.Fatalf("старт ядра B прервал ход Мастера проекта A: %s", liveTurn.Status)
	}
	coreA.Shutdown(ctx)

	// Ядро A поднимается заново: теперь это его брошенная работа.
	restartedA, err := New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restartedA.Shutdown(context.Background()) })
	if err = restartedA.SetWorkspaceBoundary(worldA.Path); err != nil {
		t.Fatal(err)
	}
	restartedA.Startup(ctx)
	recovered, err := restartedA.workOrderQuestV2(ctx, worldA.ID, quest.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status != domain.QuestPaused {
		t.Fatalf("перезапуск ядра A не поставил брошенный квест на паузу: %s", recovered.Status)
	}
	interrupted, err := restartedA.store.MasterTurn(ctx, worldA.ID, turn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if interrupted.Status != "interrupted" {
		t.Fatalf("перезапуск ядра A не закрыл брошенный ход Мастера: %s", interrupted.Status)
	}
}
