package storage

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
)

func TestWorkOrderQuestControlsAreStatefulAndRedacted(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "hub-v2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	order, err := store.SaveWorkOrderV2(ctx, storageWorkOrder())
	if err != nil {
		t.Fatal(err)
	}
	approval, err := store.ApproveWorkOrderV2(ctx, order.ID, order.Version, domain.WorkOrderDigest(order), "controls-once")
	if err != nil {
		t.Fatal(err)
	}
	if status, controlErr := store.ControlWorkOrderQuestV2(ctx, approval.QuestID, "message", "API_KEY=must-not-leak"); controlErr != nil || status != domain.QuestPreflight {
		t.Fatalf("message status=%s err=%v", status, controlErr)
	}
	var storedMessage string
	if err = store.db.QueryRow(`SELECT message FROM work_order_quest_control_events_v2 WHERE quest_id=? AND action='message'`, approval.QuestID).Scan(&storedMessage); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(storedMessage, "must-not-leak") {
		t.Fatalf("quest message secret was persisted: %q", storedMessage)
	}
	if status, controlErr := store.ControlWorkOrderQuestV2(ctx, approval.QuestID, "pause", ""); controlErr != nil || status != domain.QuestPaused {
		t.Fatalf("pause status=%s err=%v", status, controlErr)
	}
	if status, controlErr := store.ControlWorkOrderQuestV2(ctx, approval.QuestID, "resume", ""); controlErr != nil || status != domain.QuestPreflight {
		t.Fatalf("resume status=%s err=%v", status, controlErr)
	}
	if status, controlErr := store.ControlWorkOrderQuestV2(ctx, approval.QuestID, "cancel", ""); controlErr != nil || status != domain.QuestCancelled {
		t.Fatalf("cancel status=%s err=%v", status, controlErr)
	}
}

// Блокировка — не конец пути: человек чинит причину и просит повторить. Квест
// при этом обязан вернуться к проверке окружения, а не притвориться идущим:
// исполнителя у него нет, и «выполняется» было бы ложью на карточке.
func TestBlockedWorkOrderQuestResumesIntoPreflight(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "hub-v2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	order, err := store.SaveWorkOrderV2(ctx, storageWorkOrder())
	if err != nil {
		t.Fatal(err)
	}
	approval, err := store.ApproveWorkOrderV2(ctx, order.ID, order.Version, domain.WorkOrderDigest(order), "blocked-resume")
	if err != nil {
		t.Fatal(err)
	}
	quests, err := store.ListQuests(ctx, order.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	var quest domain.Quest
	for _, item := range quests {
		if item.ID == approval.QuestID {
			quest = item
		}
	}
	if quest.ID == "" {
		t.Fatal("квест утверждённого наряда не найден")
	}
	quest.Status = domain.QuestBlocked
	if err = store.SaveQuest(ctx, quest); err != nil {
		t.Fatal(err)
	}
	status, err := store.ControlWorkOrderQuestV2(ctx, approval.QuestID, "resume", "")
	if err != nil {
		t.Fatalf("заблокированный квест не возобновился: %v", err)
	}
	if status != domain.QuestPreflight {
		t.Fatalf("статус после возобновления %s, ожидался preflight", status)
	}
}
