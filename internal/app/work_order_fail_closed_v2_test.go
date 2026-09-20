package app

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

func TestWorkOrderFinalizationFailsClosedWithoutApproval(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { application.Shutdown(context.Background()) })
	world := openTestWorld(t, application)
	now := time.Now().UTC()
	quest := domain.Quest{
		ID: domain.NewID("quest"), WorkspaceID: world.ID, Title: "orphaned v2", Status: domain.QuestRunning,
		ControllerState: string(domain.QuestRunning), Controller: map[string]any{"source": "work_order_v2", "workOrderId": "missing"},
		CreatedAt: now, UpdatedAt: now,
	}
	if err = application.store.SaveQuest(context.Background(), quest); err != nil {
		t.Fatal(err)
	}
	application.finalizeQuestAfterFlow(quest.ID, true)
	stored, err := application.workOrderQuestV2(context.Background(), world.ID, quest.ID)
	if err != nil || stored.Status != domain.QuestBlocked {
		t.Fatalf("missing approval escaped through legacy finalization: quest=%#v err=%v", stored, err)
	}
}

func TestWorkOrderFinalizationFailsClosedWithCorruptApproval(t *testing.T) {
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
	order.Routing.FixedConnectionID = "unused-for-empty-roster"
	order, err = application.SaveWorkOrderV2(context.Background(), order)
	if err != nil {
		t.Fatal(err)
	}
	approval, err := application.store.ApproveWorkOrderV2(context.Background(), order.ID, order.Version, domain.WorkOrderDigest(order), "corrupt-approval")
	if err != nil {
		t.Fatal(err)
	}
	quest, err := application.workOrderQuestV2(context.Background(), world.ID, approval.QuestID)
	if err != nil {
		t.Fatal(err)
	}
	quest.Status = domain.QuestRunning
	if err = application.store.SaveQuest(context.Background(), quest); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", application.databasePath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE work_order_approvals_v2 SET response_json='{' WHERE quest_id=?`, quest.ID); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	application.finalizeQuestAfterFlow(quest.ID, true)
	stored, err := application.workOrderQuestV2(context.Background(), world.ID, quest.ID)
	if err != nil || stored.Status != domain.QuestBlocked {
		t.Fatalf("corrupt approval escaped through legacy finalization: quest=%#v err=%v", stored, err)
	}
	lease, found, err := application.store.WriterLeaseV2(context.Background(), world.ID)
	if err != nil || !found || lease.State != "released" {
		t.Fatalf("terminal fail-closed path retained writer lease: lease=%#v found=%v err=%v", lease, found, err)
	}
}

func TestWorkOrderFinalizationFailsClosedWithCorruptMilestone(t *testing.T) {
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
	order.Routing.FixedConnectionID = "unused-for-empty-roster"
	order, err = application.SaveWorkOrderV2(context.Background(), order)
	if err != nil {
		t.Fatal(err)
	}
	approval, err := application.store.ApproveWorkOrderV2(context.Background(), order.ID, order.Version, domain.WorkOrderDigest(order), "corrupt-milestone")
	if err != nil {
		t.Fatal(err)
	}
	quest, err := application.workOrderQuestV2(context.Background(), world.ID, approval.QuestID)
	if err != nil {
		t.Fatal(err)
	}
	quest.Status = domain.QuestRunning
	quest.Controller["launchMode"] = "fast_agent_v2"
	if err = application.store.SaveQuest(context.Background(), quest); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", application.databasePath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE milestone_runtimes_v2 SET payload_json='{' WHERE quest_id=?`, quest.ID); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	application.finalizeWorkOrderQuestAfterFlowV2(approval, false)
	stored, err := application.workOrderQuestV2(context.Background(), world.ID, quest.ID)
	if err != nil || stored.Status != domain.QuestBlocked {
		t.Fatalf("corrupt milestone escaped through evidence finalization: quest=%#v err=%v", stored, err)
	}
	lease, found, err := application.store.WriterLeaseV2(context.Background(), world.ID)
	if err != nil || !found || lease.State != "released" {
		t.Fatalf("milestone failure retained writer lease: lease=%#v found=%v err=%v", lease, found, err)
	}
}
