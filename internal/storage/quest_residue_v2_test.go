package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

// Q14: у отменённого квеста после перезапуска оставались milestone
// `running`, фаза запуска, этап-квест `active` и узел `waiting_approval` —
// карточка и полоса «Нужно ваше решение» показывали работу, которой никто не
// делает. Восстановление своего мира закрывает их, а квест соседнего мира не
// трогает.
func TestRecoveryClosesResidueOfClosedQuest(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "residue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	quests := map[string]string{}
	for _, world := range []string{"w", "other"} {
		order := storageWorkOrder()
		order.WorkspaceID = world
		order.Workspace.Path = t.TempDir()
		order, err = s.SaveWorkOrderV2(ctx, order)
		if err != nil {
			t.Fatal(err)
		}
		approval, approveErr := s.ApproveWorkOrderV2(ctx, order.ID, order.Version, domain.WorkOrderDigest(order), "residue-"+world)
		if approveErr != nil {
			t.Fatal(approveErr)
		}
		questID := approval.QuestID
		quests[world] = questID
		if _, err = s.db.ExecContext(ctx, `UPDATE milestone_runtimes_v2 SET payload_json=json_set(payload_json,'$.status','running') WHERE quest_id=?`, questID); err != nil {
			t.Fatal(err)
		}
		if _, err = s.db.ExecContext(ctx, `UPDATE quests SET status='cancelled',controller_json='{"launchPhase":"runtime_building","statusMessage":"готово"}' WHERE id=?`, questID); err != nil {
			t.Fatal(err)
		}
		if err = s.SaveQuest(ctx, domain.Quest{ID: "stage-" + world, WorkspaceID: world, ParentID: questID, FlowNodeID: "a", Title: "stage", Status: domain.QuestActive, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
		if err = s.SaveFlowRun(ctx, domain.FlowRun{ID: "flowrun-" + world, FlowID: "flow", WorkspaceID: world, QuestID: questID, Status: domain.RunRunning, StartedAt: now,
			NodeStates: map[string]domain.FlowNodeState{"a": {Status: "waiting_approval"}, "b": {Status: "completed"}}}); err != nil {
			t.Fatal(err)
		}
	}

	if err = s.RecoverAbandonedWork(ctx, "w"); err != nil {
		t.Fatal(err)
	}

	own, err := s.GetQuest(ctx, quests["w"])
	if err != nil {
		t.Fatal(err)
	}
	if _, live := own.Controller["launchPhase"]; live || own.Status != domain.QuestCancelled {
		t.Fatalf("фаза запуска пережила исход: %s %v", own.Status, own.Controller)
	}
	runtimes, err := s.ListMilestoneRuntimesV2(ctx, quests["w"], 1)
	if err != nil || len(runtimes) != 1 || runtimes[0].Status != domain.QuestCancelled {
		t.Fatalf("milestone закрытого квеста живой: %+v %v", runtimes, err)
	}
	if stage, _ := s.GetQuest(ctx, "stage-w"); stage.Status != domain.QuestCancelled {
		t.Fatalf("этап закрытого квеста живой: %s", stage.Status)
	}
	run, err := s.GetFlowRun(ctx, "flowrun-w")
	if err != nil {
		t.Fatal(err)
	}
	if run.Status == domain.RunRunning || run.NodeStates["a"].Status != "skipped" || run.NodeStates["b"].Status != "completed" {
		t.Fatalf("прогон закрытого квеста ждёт решения: %s %+v", run.Status, run.NodeStates)
	}

	other, _ := s.GetQuest(ctx, quests["other"])
	if other.Controller["launchPhase"] != "runtime_building" {
		t.Fatalf("тронут квест соседнего мира: %v", other.Controller)
	}
	if runtimes, _ = s.ListMilestoneRuntimesV2(ctx, quests["other"], 1); len(runtimes) != 1 || runtimes[0].Status != domain.QuestRunning {
		t.Fatalf("тронут milestone соседнего мира: %+v", runtimes)
	}
	if run, _ = s.GetFlowRun(ctx, "flowrun-other"); run.Status != domain.RunRunning || run.NodeStates["a"].Status != "waiting_approval" {
		t.Fatalf("тронут прогон соседнего мира: %s %+v", run.Status, run.NodeStates)
	}
}
