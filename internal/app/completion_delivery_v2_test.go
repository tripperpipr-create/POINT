package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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
		// A service that never started is still a failed delivery.
		{name: "services started", serviceExitCode: 0, expectRunning: true, expectedStatus: domain.QuestCompleted},
		{name: "services failed to start", serviceExitCode: 1, expectRunning: false, expectedStatus: domain.QuestBlocked},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("REDIS_ADDR", "")
			var stopped atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/health" && stopped.Load() {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				w.WriteHeader(http.StatusOK)
			}))
			t.Cleanup(server.Close)
			health := "docker compose up -d && timeout 90 bash -c 'until curl -sf " + server.URL + "/health >/dev/null; do sleep 2; done'"
			outage := "docker compose stop postgres && sleep 3 && [ \"$(curl -s -o /dev/null -w '%{http_code}' " + server.URL + "/health)\" = \"503\" ] && [ \"$(curl -s -o /dev/null -w '%{http_code}' " + server.URL + "/live)\" = \"200\" ]"
			healthArgs, _ := json.Marshal(map[string]string{"command": health})
			outageArgs, _ := json.Marshal(map[string]string{"command": outage})
			runner := &scriptedCompletionRunner{results: map[string]int{}}
			starts := 0
			runner.onRun = func(command string) {
				switch withoutComposeProject(command) {
				case "docker compose up -d --wait":
					starts++
					stopped.Store(false)
					if starts == 2 {
						runner.results[command] = testCase.serviceExitCode
					}
				case "docker compose stop postgres":
					stopped.Store(true)
				case "docker compose start postgres":
					stopped.Store(false)
				}
			}
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
			assignReadyRosterForTest(t, application, &order)
			order.Criteria = []domain.AcceptanceCriterion{
				{ID: "health-200", Kind: "verification", Tool: "run_command", Text: "Сервис отвечает", Arguments: healthArgs},
				{ID: "health-503-live-200", Kind: "verification", Tool: "run_command", Text: "Сбой базы виден", Arguments: outageArgs},
			}
			order.Delivery = domain.DeliveryPolicy{
				ApplyMode: "automatic", CommitMode: "none", KeepPartialDays: 30,
				KeepServicesRunning: true, ApplicationURL: server.URL,
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
			// Host criteria run in the quest's own Compose project, from a clean
			// slate and torn down afterwards; the profile then starts the
			// delivered application in the user's own project.
			stack := "docker compose -p " + composeProjectNameV2(quest.ID) + " "
			want := []string{
				stack + "down -v --remove-orphans", stack + "up -d --wait", stack + "stop postgres", stack + "start postgres",
				stack + "down -v --remove-orphans", "vendor/bin/phpunit", "docker compose up -d --wait",
			}
			if strings.Join(runner.commands, " | ") != strings.Join(want, " | ") {
				t.Fatalf("Compose criteria must run after delivery and before the completion profile: %#v", runner.commands)
			}
			for _, id := range []string{"health-200", "health-503-live-200"} {
				check, found := recorded[id]
				if !found || !check.Satisfied || check.ExitCode == nil {
					t.Fatalf("host Compose criterion %s has no passing evidence: %#v", id, check)
				}
			}
			if testCase.expectRunning && bundle.Assurance != domain.WorkOrderAssuranceVerified {
				t.Fatalf("passing managed criteria must produce verified assurance, got %s", bundle.Assurance)
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

func TestUnsupportedComposeCriterionNeverRunsOnHost(t *testing.T) {
	runner := &scriptedCompletionRunner{results: map[string]int{}}
	application := &App{completionCheckRunner: runner}
	criterion := domain.AcceptanceCriterion{
		ID: "custom", Kind: "verification", Tool: "run_command",
		Arguments: json.RawMessage(`{"command":"docker compose down --volumes"}`),
	}
	order := domain.WorkOrder{
		Criteria: []domain.AcceptanceCriterion{criterion},
		Delivery: domain.DeliveryPolicy{ApplicationURL: "http://localhost:8080"},
	}
	bundle := domain.EvidenceBundle{
		Criteria:           []domain.CriterionEvidence{{CriterionID: criterion.ID}},
		VerificationChecks: []domain.VerificationCheck{{ID: criterion.ID, Kind: "acceptance"}},
	}
	checks := application.runDeferredComposeCriteriaV2(context.Background(), order, t.TempDir(), &bundle)
	if len(runner.commands) != 0 || len(checks) != 1 || checks[0].ExitCode != nil || checks[0].Satisfied {
		t.Fatalf("unsupported host command must remain unavailable: commands=%v checks=%#v", runner.commands, checks)
	}
}

func TestApprovedBuildStackAndHealthRunOnDeliveredHost(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()
	commands := []string{"docker compose build", "docker compose up -d", "curl -sf " + server.URL + "/health"}
	order := domain.WorkOrder{}
	for i, command := range commands {
		args, _ := json.Marshal(map[string]string{"command": command})
		order.Criteria = append(order.Criteria, domain.AcceptanceCriterion{
			ID: []string{"build", "stack-up", "health-ok"}[i], Kind: "verification", Tool: "run_command", Arguments: args,
		})
	}
	bundle := domain.EvidenceBundle{}
	for _, criterion := range order.Criteria {
		bundle.Criteria = append(bundle.Criteria, domain.CriterionEvidence{CriterionID: criterion.ID})
		bundle.VerificationChecks = append(bundle.VerificationChecks, domain.VerificationCheck{ID: criterion.ID})
	}
	runner := &scriptedCompletionRunner{results: map[string]int{}}
	application := &App{completionCheckRunner: runner}
	checks := application.runDeferredComposeCriteriaV2(context.Background(), order, t.TempDir(), &bundle)
	want := "docker compose -p point-verify down -v --remove-orphans | docker compose -p point-verify build | " +
		"docker compose -p point-verify up -d | docker compose -p point-verify down -v --remove-orphans"
	if len(checks) != 3 || strings.Join(runner.commands, " | ") != want {
		t.Fatalf("host checks=%#v, shell commands=%#v", checks, runner.commands)
	}
	for _, check := range checks {
		if !check.Satisfied || check.ExitCode == nil || *check.ExitCode != 0 {
			t.Fatalf("criterion %s was not verified: %#v", check.ID, check)
		}
	}
}
