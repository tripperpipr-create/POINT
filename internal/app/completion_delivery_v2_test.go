package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

// TestDeliveredResultIsCheckedByTheApprovedProfile walks the real terminal
// path — approval, Flow callback, delivery, profile, receipt, gate — instead of
// assembling a bundle by hand. It is the wiring that decides whether a quest
// can claim a running application, so it is the wiring that has to be proven.
func TestDeliveredResultIsCheckedByTheApprovedProfile(t *testing.T) {
	for _, testCase := range []struct {
		name            string
		serviceExitCode int
		expectRunning   bool
		expectedStatus  domain.QuestStatus
	}{
		// The criterion is manual, so a delivered result waits for a person —
		// but an application that never started is a failed delivery, and no
		// manual criterion can turn that into something to review.
		{name: "services started", serviceExitCode: 0, expectRunning: true, expectedStatus: domain.QuestNeedsReview},
		{name: "services failed to start", serviceExitCode: 1, expectRunning: false, expectedStatus: domain.QuestBlocked},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("REDIS_ADDR", "")
			runner := &scriptedCompletionRunner{results: map[string]int{"docker compose up -d --wait": testCase.serviceExitCode}}
			application, err := New(t.TempDir(), WithCompletionCheckRunner(runner))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { application.Shutdown(context.Background()) })
			ctx := context.Background()
			world := openTestWorld(t, application)
			if err = os.WriteFile(filepath.Join(world.Path, "compose.yaml"), []byte("services: {}\n"), 0o600); err != nil {
				t.Fatal(err)
			}

			order := managedWorkOrderV2()
			order.WorkspaceID = world.ID
			order.Workspace = domain.WorkspacePlan{Mode: "existing", Path: world.Path, Isolation: "snapshot"}
			order.Criteria = []domain.AcceptanceCriterion{{ID: "review", Kind: "manual", Text: "Проверить результат"}}
			order.Delivery = domain.DeliveryPolicy{
				ApplyMode: "automatic", CommitMode: "none", KeepPartialDays: 30,
				KeepServicesRunning: true, ApplicationURL: "http://localhost:8080",
			}
			order.Completion = domain.CompletionProfile{ID: "mvp-web", Version: "1", Checks: []domain.CompletionCheck{
				{Kind: domain.CompletionCheckAcceptance},
				{Kind: "automated_tests", Command: "vendor/bin/phpunit"},
				{Kind: "service_start", Command: "docker compose up -d --wait"},
			}}
			order, err = application.SaveWorkOrderV2(ctx, order)
			if err != nil {
				t.Fatal(err)
			}
			approval, err := application.store.ApproveWorkOrderV2(ctx, order.ID, order.Version, domain.WorkOrderDigest(order), "profile-delivery-"+testCase.name)
			if err != nil {
				t.Fatal(err)
			}
			quest, err := application.workOrderQuestV2(ctx, world.ID, approval.QuestID)
			if err != nil {
				t.Fatal(err)
			}
			quest.Status = domain.QuestRunning
			if err = application.store.SaveQuest(ctx, quest); err != nil {
				t.Fatal(err)
			}
			// Completion also requires a model-call ledger, so the quest has to
			// carry the spend it really made.
			cost := int64(7)
			if err = application.store.InsertUsageRecord(ctx, domain.UsageRecord{
				ID: "usage-" + quest.ID, WorkspaceID: world.ID, QuestID: quest.ID,
				Provider: "test", Model: "model", InputTokens: 100, OutputTokens: 40, TotalTokens: 140,
				CostCents: &cost, LatencyMs: 1200, Outcome: "completed", CreatedAt: time.Now().UTC(),
			}); err != nil {
				t.Fatal(err)
			}

			application.finalizeQuestAfterFlow(quest.ID, true)

			bundle, err := application.EvidenceBundle(ctx, quest.ID)
			if err != nil {
				t.Fatal(err)
			}
			recorded := map[string]domain.VerificationCheck{}
			for _, check := range bundle.VerificationChecks {
				recorded[check.ID] = check
			}
			tests, ok := recorded[domain.CompletionCheckEvidenceID("automated_tests")]
			if !ok || tests.Command != "vendor/bin/phpunit" || tests.ExitCode == nil || !tests.Satisfied {
				t.Fatalf("approved test command was not executed on the delivered result: %#v", bundle.VerificationChecks)
			}
			services, ok := recorded[domain.CompletionCheckEvidenceID("service_start")]
			if !ok || services.Satisfied != testCase.expectRunning {
				t.Fatalf("service check = %#v", services)
			}
			if bundle.DeliveryReceipt == nil {
				t.Fatal("verified delivery produced no receipt")
			}
			if bundle.DeliveryReceipt.ServicesRunning != testCase.expectRunning {
				t.Fatalf("receipt claims running=%v while the service check said %v",
					bundle.DeliveryReceipt.ServicesRunning, services.Satisfied)
			}
			limitation := strings.Join(bundle.KnownLimitations, " | ")
			if testCase.expectRunning && strings.Contains(limitation, "service_start") {
				t.Fatalf("a passing profile must not be reported as failed: %q", limitation)
			}
			if !testCase.expectRunning && !strings.Contains(limitation, "service_start") {
				// The delivered result stays in the project; the card has to say
				// that it failed its own checks.
				t.Fatalf("failed profile is not stated in the bundle: %q", limitation)
			}
			final, err := application.workOrderQuestV2(ctx, world.ID, quest.ID)
			if err != nil || final.Status != testCase.expectedStatus {
				t.Fatalf("quest status=%v want %v err=%v", final.Status, testCase.expectedStatus, err)
			}
		})
	}
}
