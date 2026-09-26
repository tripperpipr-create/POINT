package storage

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
)

// A quest with a manual criterion used to end as "completed with limitations"
// with no way to record that the human did the check. Now it waits for the
// decision, and the decision moves the gate and the quest.
func TestManualCriterionDecisionMovesTheGate(t *testing.T) {
	for _, tc := range []struct {
		decision  string
		status    domain.QuestStatus
		assurance string
	}{
		{domain.ManualReviewAccepted, domain.QuestCompleted, domain.WorkOrderAssuranceVerified},
		{domain.ManualReviewRejected, domain.QuestBlocked, domain.WorkOrderAssuranceFailed},
	} {
		t.Run(tc.decision, func(t *testing.T) {
			store, err := Open(filepath.Join(t.TempDir(), "hub-v2.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			ctx := context.Background()
			order := storageWorkOrder()
			order.Criteria = append(order.Criteria, domain.AcceptanceCriterion{ID: "c2", Kind: "manual", Text: "Сбой базы виден в /health"})
			order.Milestones[0].CriterionIDs = append(order.Milestones[0].CriterionIDs, "c2")
			saved, err := store.SaveWorkOrderV2(ctx, order)
			if err != nil {
				t.Fatal(err)
			}
			approval, err := store.ApproveWorkOrderV2(ctx, saved.ID, saved.Version, domain.WorkOrderDigest(saved), "manual-"+tc.decision)
			if err != nil {
				t.Fatal(err)
			}
			bundle := gateEvidence("evidence-manual", approval.QuestID, saved, true)
			bundle.Criteria = append(bundle.Criteria, domain.CriterionEvidence{CriterionID: "c2", Summary: "Требуется ручная приёмка"})
			if status, err := store.FinalizeWorkOrderQuestV2(ctx, approval.QuestID, bundle); err != nil || status != domain.QuestNeedsReview {
				t.Fatalf("pending manual criterion: status=%s err=%v", status, err)
			}
			if _, err = store.ReviewManualCriterionV2(ctx, domain.ManualCriterionReview{QuestID: approval.QuestID, CriterionID: "c1", Decision: domain.ManualReviewAccepted}); err == nil {
				t.Fatal("a machine-verified criterion accepted a human decision")
			}
			status, err := store.ReviewManualCriterionV2(ctx, domain.ManualCriterionReview{QuestID: approval.QuestID, CriterionID: "c2", Decision: tc.decision, Note: "проверил на стенде"})
			if err != nil || status != tc.status {
				t.Fatalf("decision %s: status=%s err=%v", tc.decision, status, err)
			}
			quest, err := store.GetQuest(ctx, approval.QuestID)
			if err != nil || quest.Status != tc.status {
				t.Fatalf("quest status=%s err=%v", quest.Status, err)
			}
			effective, err := store.GetEvidenceBundle(ctx, approval.QuestID)
			if err != nil || effective.Assurance != tc.assurance {
				t.Fatalf("effective assurance=%q err=%v", effective.Assurance, err)
			}
			for _, item := range effective.Criteria {
				if item.CriterionID == "c2" && (item.Review != tc.decision || !strings.Contains(item.Summary, "проверил на стенде")) {
					t.Fatalf("decision is not visible in evidence: %#v", item)
				}
			}
			if _, err = store.ReviewManualCriterionV2(ctx, domain.ManualCriterionReview{QuestID: approval.QuestID, CriterionID: "c2", Decision: domain.ManualReviewAccepted}); err == nil {
				t.Fatal("a recorded decision was overwritten")
			}
		})
	}
}
