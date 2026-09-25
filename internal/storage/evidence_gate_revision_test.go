package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

// Вердикт шлюза и улики неизменяемы и принадлежат одному квесту. Новая
// версия наряда на том же квесте раньше наследовала вердикт прошлой:
// проваленная v2 становилась completed по доказательствам v1, а доказанная v2
// оставалась blocked по провалу v1, и её улики не могли сохраниться вовсе.
// После вердикта новая версия идёт новым квестом со своим вердиктом.
func TestEvidenceGateJudgesEachApprovedVersion(t *testing.T) {
	for _, tc := range []struct {
		name          string
		firstPasses   bool
		secondPasses  bool
		firstOutcome  domain.QuestStatus
		secondOutcome domain.QuestStatus
	}{
		{"проваленная v2 после принятой v1", true, false, domain.QuestCompleted, domain.QuestBlocked},
		{"доказанная v2 после проваленной v1", false, true, domain.QuestBlocked, domain.QuestCompleted},
	} {
		t.Run(tc.name, func(t *testing.T) {
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
			approval, err := store.ApproveWorkOrderV2(ctx, first.ID, first.Version, domain.WorkOrderDigest(first), "gate-v1")
			if err != nil {
				t.Fatal(err)
			}
			if status, err := store.FinalizeWorkOrderQuestV2(ctx, approval.QuestID, gateEvidence("evidence-v1", approval.QuestID, first, tc.firstPasses)); err != nil || status != tc.firstOutcome {
				t.Fatalf("v1: %s err=%v", status, err)
			}
			revised := approval.WorkOrder
			revised.Version++
			revised.State = "ready"
			revised.Goal = "Build the revised API"
			revised = domain.NormalizeWorkOrder(revised)
			if revised, err = store.SaveWorkOrderV2(ctx, revised); err != nil {
				t.Fatal(err)
			}
			second, err := store.ApproveWorkOrderV2(ctx, revised.ID, revised.Version, domain.WorkOrderDigest(revised), "gate-v2")
			if err != nil {
				t.Fatalf("переутверждение после вердикта: %v", err)
			}
			if second.QuestID == approval.QuestID {
				t.Fatal("новая версия после вердикта продолжила закрытый квест")
			}
			status, err := store.FinalizeWorkOrderQuestV2(ctx, second.QuestID, gateEvidence("evidence-v2", second.QuestID, revised, tc.secondPasses))
			if err != nil {
				t.Fatal(err)
			}
			if status != tc.secondOutcome {
				t.Fatalf("v2 получила вердикт %s вместо %s: судили по прошлой версии", status, tc.secondOutcome)
			}
			var version int
			if err = store.db.QueryRowContext(ctx, `SELECT version FROM work_order_completion_gates_v2 WHERE quest_id=?`, second.QuestID).Scan(&version); err != nil || version != revised.Version {
				t.Fatalf("вердикт не привязан к v2: version=%d err=%v", version, err)
			}
			if replay, err := store.FinalizeWorkOrderQuestV2(ctx, approval.QuestID, gateEvidence("evidence-v1", approval.QuestID, first, tc.firstPasses)); err != nil || replay != tc.firstOutcome {
				t.Fatalf("вердикт v1 изменился: %s err=%v", replay, err)
			}
		})
	}
}

func gateEvidence(id, questID string, order domain.WorkOrder, passes bool) domain.EvidenceBundle {
	digest := domain.WorkOrderDigest(order)
	exit := 0
	if !passes {
		exit = 1
	}
	return domain.EvidenceBundle{
		Version: domain.CurrentWorkOrderEvidenceVersion, PointVersion: "test", ID: id, QuestID: questID, BriefDigest: digest, SourceDigest: domain.WorkOrderSourceDigest(order),
		EnvironmentDigest: "environment-digest", StackPreset: order.Stack, SourceVersions: []domain.SourceSnapshotRef{}, WorkspaceRevision: "workspace-tree-hash", DeliveryVerified: true,
		DeliveryReceipt:    &domain.DeliveryReceipt{ID: "delivery-" + id, QuestID: questID, WorkOrderDigest: digest, Target: order.Workspace.Path, WorkspaceRevision: "workspace-tree-hash", DeliveredAt: time.Now().UTC()},
		Criteria:           []domain.CriterionEvidence{{CriterionID: "c1", Satisfied: passes, Command: "go test ./...", ExitCode: storageTestIntPtr(exit)}},
		VerificationChecks: []domain.VerificationCheck{{ID: "c1", Kind: "acceptance", Command: "go test ./...", ExitCode: storageTestIntPtr(exit), Satisfied: passes}},
		ModelCalls:         storageTestModelCalls(),
	}
}
