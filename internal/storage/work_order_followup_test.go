package storage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

// Новая версия после вердикта идёт новым квестом. Агент, которого создало
// прошлое утверждение и который остался в ростере новой версии, берётся как
// есть, а не создаётся вторично; без списка исполнителей запуск отказывал
// «approved roster has no runnable agents». Судимый квест, стоящий в статусе
// своего вердикта, остаётся с ним, а не уходит в паузу правки без выхода.
func TestFollowUpApprovalKeepsAgentsAndLeavesJudgedQuestAlone(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "hub-v2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	if err = store.SaveConnection(ctx, domain.Connection{ID: "c", Provider: domain.ProviderOpenAI, PresetID: "openai", DisplayName: "OpenAI", BaseURL: "https://api.openai.com/v1", Status: domain.ConnectionConnected, DefaultModel: "m", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	order := storageWorkOrder()
	order.Roster = domain.AgentRosterPlan{Permanent: []domain.AgentDraft{{ID: "backend-owner", Name: "Backend owner", Role: "backend", Mission: "Own the API", RequiredTools: []string{"read_file"}, RequiresConsent: true}}}
	order.Budget.MaxProjectAgents = 1
	first, err := store.SaveWorkOrderV2(ctx, domain.NormalizeWorkOrder(order))
	if err != nil {
		t.Fatal(err)
	}
	approval, err := store.ApproveWorkOrderV2(ctx, first.ID, first.Version, domain.WorkOrderDigest(first), "followup-v1")
	if err != nil || len(approval.AgentIDs) != 1 {
		t.Fatalf("первое утверждение: %#v err=%v", approval.AgentIDs, err)
	}
	if status, err := store.FinalizeWorkOrderQuestV2(ctx, approval.QuestID, gateEvidence("followup-v1-evidence", approval.QuestID, first, false)); err != nil || status != domain.QuestBlocked {
		t.Fatalf("v1: %s err=%v", status, err)
	}
	revised := approval.WorkOrder
	revised.Version++
	revised.State = "ready"
	revised.Goal = "Build the revised API"
	if revised, err = store.SaveWorkOrderV2(ctx, domain.NormalizeWorkOrder(revised)); err != nil {
		t.Fatal(err)
	}
	old := questByID(t, store, first.WorkspaceID, approval.QuestID)
	if old.Status != domain.QuestBlocked || old.ControllerState == WorkOrderScopeRevisionState {
		t.Fatalf("судимый квест ушёл в паузу правки: %s/%s", old.Status, old.ControllerState)
	}
	second, err := store.ApproveWorkOrderV2(ctx, revised.ID, revised.Version, domain.WorkOrderDigest(revised), "followup-v2")
	if err != nil {
		t.Fatal(err)
	}
	if second.QuestID == approval.QuestID || len(second.AgentIDs) != 1 || second.AgentIDs[0] != approval.AgentIDs[0] {
		t.Fatalf("новая версия без исполнителей или на старом квесте: quest=%s agents=%v", second.QuestID, second.AgentIDs)
	}
}

// Пауза рестарта — не решение человека: починка при старте, которая
// пропускает квесты, приостановленные человеком, не должна пропускать
// квест, приостановленный рестартом, а продолжение берёт статус из неё.
func TestRecoveryPauseStaysVisibleToStartupRepairAndResume(t *testing.T) {
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
	approval, err := store.ApproveWorkOrderV2(ctx, order.ID, order.Version, domain.WorkOrderDigest(order), "recovery-pause")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	run := domain.FlowRun{ID: "recovery-flow-run", FlowID: "recovery-flow", WorkspaceID: order.WorkspaceID, QuestID: approval.QuestID, Status: domain.RunFailed, NodeStates: map[string]domain.FlowNodeState{}, StartedAt: now, FinishedAt: &now}
	if err = store.SaveFlowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.Exec(`UPDATE quests SET status='paused',controller_state='paused',flow_id=?,flow_run_id=?,finished_at=NULL WHERE id=?`, run.FlowID, run.ID, approval.QuestID); err != nil {
		t.Fatal(err)
	}
	if err = store.RecordWorkOrderQuestPauseV2(ctx, approval.QuestID, domain.QuestRunning, "restart"); err != nil {
		t.Fatal(err)
	}
	candidates, err := store.ListTerminalFlowPendingWorkOrdersV2(ctx)
	if err != nil || len(candidates) != 1 || candidates[0].QuestID != approval.QuestID {
		t.Fatalf("пауза рестарта спрятала квест от починки: %#v err=%v", candidates, err)
	}
	status, err := store.ControlWorkOrderQuestV2(ctx, approval.QuestID, "resume", "")
	if err != nil || status != domain.QuestRunning {
		t.Fatalf("продолжение не взяло статус из паузы рестарта: %s err=%v", status, err)
	}
}

func questByID(t *testing.T, store *SQLite, workspaceID, id string) domain.Quest {
	t.Helper()
	quests, err := store.ListQuests(context.Background(), workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	for _, quest := range quests {
		if quest.ID == id {
			return quest
		}
	}
	t.Fatalf("квест %s не найден", id)
	return domain.Quest{}
}

// Исполнители новой версии — из её ростера. Прежде повторное утверждение
// брало состав прошлой версии: человек утверждал агента B, а работал A, и
// удалённый агент прошлой версии блокировал наряд навсегда.
func TestFollowUpApprovalUsesTheRevisedRoster(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "hub-v2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	if err = store.SaveConnection(ctx, domain.Connection{ID: "c", Provider: domain.ProviderOpenAI, PresetID: "openai", DisplayName: "OpenAI", BaseURL: "https://api.openai.com/v1", Status: domain.ConnectionConnected, DefaultModel: "m", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	order := storageWorkOrder()
	order.Roster = domain.AgentRosterPlan{Permanent: []domain.AgentDraft{{ID: "agent-a", Name: "A", Role: "backend", Mission: "Own the API", RequiredTools: []string{"read_file"}, RequiresConsent: true}}}
	order.Budget.MaxProjectAgents = 1
	first, err := store.SaveWorkOrderV2(ctx, domain.NormalizeWorkOrder(order))
	if err != nil {
		t.Fatal(err)
	}
	approval, err := store.ApproveWorkOrderV2(ctx, first.ID, first.Version, domain.WorkOrderDigest(first), "roster-v1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.FinalizeWorkOrderQuestV2(ctx, approval.QuestID, gateEvidence("roster-v1-evidence", approval.QuestID, first, false)); err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.Exec(`DELETE FROM project_agents WHERE id='agent-a'`); err != nil {
		t.Fatal(err)
	}
	revised := approval.WorkOrder
	revised.Version++
	revised.State = "ready"
	revised.Roster = domain.AgentRosterPlan{Permanent: []domain.AgentDraft{{ID: "agent-b", Name: "B", Role: "backend", Mission: "Own the API", RequiredTools: []string{"read_file"}, RequiresConsent: true}}}
	if revised, err = store.SaveWorkOrderV2(ctx, domain.NormalizeWorkOrder(revised)); err != nil {
		t.Fatal(err)
	}
	second, err := store.ApproveWorkOrderV2(ctx, revised.ID, revised.Version, domain.WorkOrderDigest(revised), "roster-v2")
	if err != nil {
		t.Fatalf("удалённый агент прошлой версии заблокировал новую: %v", err)
	}
	if len(second.AgentIDs) != 1 || second.AgentIDs[0] != "agent-b" {
		t.Fatalf("новая версия работает не утверждённым составом: %v", second.AgentIDs)
	}
}

// Квест, перезапущенный после вердикта, правка останавливает как живой, а
// утверждение новой версии возвращает его к вердикту, а не оставляет в паузе.
func TestRelaunchedJudgedQuestIsPausedByRevisionAndClosedByApproval(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "hub-v2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	first, err := store.SaveWorkOrderV2(ctx, storageWorkOrder())
	if err != nil {
		t.Fatal(err)
	}
	approval, err := store.ApproveWorkOrderV2(ctx, first.ID, first.Version, domain.WorkOrderDigest(first), "relaunch-v1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.FinalizeWorkOrderQuestV2(ctx, approval.QuestID, gateEvidence("relaunch-v1-evidence", approval.QuestID, first, false)); err != nil {
		t.Fatal(err)
	}
	if status, err := store.ControlWorkOrderQuestV2(ctx, approval.QuestID, "resume", ""); err != nil || status != domain.QuestPreflight {
		t.Fatalf("перезапуск после вердикта: %s err=%v", status, err)
	}
	if _, err = store.db.Exec(`UPDATE quests SET status='running',controller_state='running' WHERE id=?`, approval.QuestID); err != nil {
		t.Fatal(err)
	}
	revised := approval.WorkOrder
	revised.Version++
	revised.State = "ready"
	revised.Goal = "Build the revised API"
	if revised, err = store.SaveWorkOrderV2(ctx, domain.NormalizeWorkOrder(revised)); err != nil {
		t.Fatal(err)
	}
	if old := questByID(t, store, first.WorkspaceID, approval.QuestID); old.Status != domain.QuestPaused || old.ControllerState != WorkOrderScopeRevisionState {
		t.Fatalf("правка не остановила перезапущенный квест: %s/%s", old.Status, old.ControllerState)
	}
	second, err := store.ApproveWorkOrderV2(ctx, revised.ID, revised.Version, domain.WorkOrderDigest(revised), "relaunch-v2")
	if err != nil || second.QuestID == approval.QuestID {
		t.Fatalf("утверждение новой версии: %#v err=%v", second, err)
	}
	if old := questByID(t, store, first.WorkspaceID, approval.QuestID); old.Status != domain.QuestBlocked {
		t.Fatalf("старый квест не вернулся к вердикту: %s/%s", old.Status, old.ControllerState)
	}
}

// Перезапущенный после вердикта квест, который человек сам приостановил,
// правка помечает так же, как живой, а утверждение новой версии закрывает его
// вердиктом: продолжить его рядом с новым квестом того же наряда нельзя.
func TestHumanPausedRelaunchedJudgedQuestCannotRunBesideNewVersion(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "hub-v2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	first, err := store.SaveWorkOrderV2(ctx, storageWorkOrder())
	if err != nil {
		t.Fatal(err)
	}
	approval, err := store.ApproveWorkOrderV2(ctx, first.ID, first.Version, domain.WorkOrderDigest(first), "paused-v1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.FinalizeWorkOrderQuestV2(ctx, approval.QuestID, gateEvidence("paused-v1-evidence", approval.QuestID, first, false)); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ControlWorkOrderQuestV2(ctx, approval.QuestID, "resume", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ControlWorkOrderQuestV2(ctx, approval.QuestID, "pause", ""); err != nil {
		t.Fatal(err)
	}
	revised := approval.WorkOrder
	revised.Version++
	revised.State = "ready"
	revised.Goal = "Build the revised API"
	if revised, err = store.SaveWorkOrderV2(ctx, domain.NormalizeWorkOrder(revised)); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ControlWorkOrderQuestV2(ctx, approval.QuestID, "resume", ""); !errors.Is(err, ErrWorkOrderRevisionPending) {
		t.Fatalf("приостановленный квест прежней версии продолжается после правки: %v", err)
	}
	if _, err = store.ApproveWorkOrderV2(ctx, revised.ID, revised.Version, domain.WorkOrderDigest(revised), "paused-v2"); err != nil {
		t.Fatal(err)
	}
	if old := questByID(t, store, first.WorkspaceID, approval.QuestID); old.Status != domain.QuestBlocked {
		t.Fatalf("старый квест не закрыт вердиктом: %s/%s", old.Status, old.ControllerState)
	}
}
