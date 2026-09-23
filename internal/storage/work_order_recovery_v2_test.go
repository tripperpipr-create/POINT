package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

func TestBrokenWorkOrderRecoveryIsNarrowAndIdempotent(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "recovery-v2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	order := storageWorkOrder()
	order.ID = "workorder-historic-proposal"
	if err = store.SaveQuestProposal(ctx, domain.QuestProposal{ID: "historic-proposal", WorkspaceID: order.WorkspaceID, Title: "Historic", Status: "pending", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	order, err = store.SaveWorkOrderV2(ctx, order)
	if err != nil {
		t.Fatal(err)
	}
	approval, err := store.ApproveWorkOrderV2(ctx, order.ID, order.Version, domain.WorkOrderDigest(order), "historic-approval")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	run := domain.FlowRun{ID: "historic-flow-run", FlowID: "historic-flow", WorkspaceID: order.WorkspaceID, QuestID: approval.QuestID, Status: domain.RunCompleted, NodeStates: map[string]domain.FlowNodeState{}, StartedAt: now, FinishedAt: &now}
	if err = store.SaveFlowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.Exec(`UPDATE quests SET status='cancelled',controller_state='cancelled',flow_id=?,flow_run_id=?,finished_at=? WHERE id=?`, run.FlowID, run.ID, formatTime(now), approval.QuestID); err != nil {
		t.Fatal(err)
	}
	candidates, err := store.ListBrokenWorkOrderFinalizationsV2(ctx)
	if err != nil || len(candidates) != 1 || candidates[0].QuestID != approval.QuestID {
		t.Fatalf("candidates=%#v err=%v", candidates, err)
	}
	prepared, err := store.PrepareBrokenWorkOrderFinalizationV2(ctx, candidates[0])
	if err != nil || !prepared {
		t.Fatalf("prepared=%v err=%v", prepared, err)
	}
	if prepared, err = store.PrepareBrokenWorkOrderFinalizationV2(ctx, candidates[0]); err != nil || prepared {
		t.Fatalf("replay prepared=%v err=%v", prepared, err)
	}
	var proposalStatus string
	if err = store.db.QueryRow(`SELECT status FROM quest_proposals WHERE id='historic-proposal'`).Scan(&proposalStatus); err != nil || proposalStatus != "started" {
		t.Fatalf("proposal status=%q err=%v", proposalStatus, err)
	}
	if _, err = store.db.Exec(`UPDATE quests SET status='cancelled',controller_state='cancelled',finished_at=? WHERE id=?`, formatTime(now), approval.QuestID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.Exec(`INSERT INTO work_order_quest_control_events_v2(id,quest_id,action,from_status,to_status,message,created_at) VALUES(?,?,?,?,?,?,?)`, "historic-cancel", approval.QuestID, "cancel", "running", "cancelled", "", formatTime(now)); err != nil {
		t.Fatal(err)
	}
	candidates, err = store.ListBrokenWorkOrderFinalizationsV2(ctx)
	if err != nil || len(candidates) != 0 {
		t.Fatalf("user-cancelled candidates=%#v err=%v", candidates, err)
	}
}

func TestTerminalFailedFlowPendingWorkOrderIsRecoverable(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "terminal-flow-v2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	order, err := store.SaveWorkOrderV2(ctx, storageWorkOrder())
	if err != nil {
		t.Fatal(err)
	}
	approval, err := store.ApproveWorkOrderV2(ctx, order.ID, order.Version, domain.WorkOrderDigest(order), "terminal-flow-approval")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	run := domain.FlowRun{ID: "failed-flow-run", FlowID: "failed-flow", WorkspaceID: order.WorkspaceID, QuestID: approval.QuestID, Status: domain.RunFailed, NodeStates: map[string]domain.FlowNodeState{}, StartedAt: now, FinishedAt: &now}
	if err = store.SaveFlowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.Exec(`UPDATE quests SET status='running',controller_state='running',flow_id=?,flow_run_id=?,finished_at=NULL WHERE id=?`, run.FlowID, run.ID, approval.QuestID); err != nil {
		t.Fatal(err)
	}
	candidates, err := store.ListTerminalFlowPendingWorkOrdersV2(ctx)
	if err != nil || len(candidates) != 1 || candidates[0].QuestID != approval.QuestID {
		t.Fatalf("terminal candidates=%#v err=%v", candidates, err)
	}
	if _, err = store.db.Exec(`INSERT INTO work_order_quest_control_events_v2(id,quest_id,action,from_status,to_status,message,created_at) VALUES(?,?,?,?,?,?,?)`, "failed-flow-cancel", approval.QuestID, "cancel", "running", "cancelled", "", formatTime(now)); err != nil {
		t.Fatal(err)
	}
	candidates, err = store.ListTerminalFlowPendingWorkOrdersV2(ctx)
	if err != nil || len(candidates) != 0 {
		t.Fatalf("user-cancelled terminal candidates=%#v err=%v", candidates, err)
	}
}
