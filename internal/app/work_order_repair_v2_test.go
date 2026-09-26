package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

type failingBuildRunner struct{ commands []string }

func (runner *failingBuildRunner) Run(_ context.Context, _, command string) (int, string, error) {
	runner.commands = append(runner.commands, command)
	switch {
	case withoutComposeProject(command) == "docker compose build":
		return 1, "#9 [app builder 4/6] RUN go build\n#9 ERROR: main.go:12: undefined: pgxpool", nil
	case strings.Contains(command, " logs "):
		return 0, "app-1  | build failed: undefined: pgxpool\n", nil
	}
	return 0, "", nil
}

func approvedHostCheckQuestForTest(t *testing.T, maxAttempts int) (*App, domain.Quest, *failingBuildRunner) {
	t.Helper()
	t.Setenv("REDIS_ADDR", "")
	runner := &failingBuildRunner{}
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
	order.Budget.MaxAttempts = maxAttempts
	order.Delivery = domain.DeliveryPolicy{ApplyMode: "automatic", CommitMode: "none", KeepPartialDays: 30}
	assignReadyRosterForTest(t, application, &order)
	build, _ := json.Marshal(map[string]string{"command": "docker compose build"})
	order.Criteria = []domain.AcceptanceCriterion{{ID: "build", Kind: "verification", Tool: "run_command", Text: "Образы собираются", Arguments: build}}
	order, err = application.SaveWorkOrderV2(ctx, order)
	if err != nil {
		t.Fatal(err)
	}
	approval, err := application.store.ApproveWorkOrderV2(ctx, order.ID, order.Version, domain.WorkOrderDigest(order), "repair-"+t.Name())
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
	cost := int64(1)
	if err = application.store.InsertUsageRecord(ctx, domain.UsageRecord{
		ID: "usage-" + quest.ID, WorkspaceID: world.ID, QuestID: quest.ID, Provider: "test", Model: "model",
		InputTokens: 10, OutputTokens: 5, TotalTokens: 15, CostCents: &cost, Outcome: "completed", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	return application, quest, runner
}

// Live run 26.09 ended «Заблокировано… повторите запуск» although the approved
// budget allowed three attempts, and the verdict made the quest impossible to
// continue. A failed host check now starts the next attempt before any
// verdict, with what the host observed.
func TestFailedHostCheckStartsTheNextAttemptBeforeAnyVerdict(t *testing.T) {
	application, quest, _ := approvedHostCheckQuestForTest(t, 2)
	ctx := context.Background()

	application.finalizeQuestAfterFlow(quest.ID, true)

	repaired, err := application.workOrderQuestV2(ctx, quest.WorkspaceID, quest.ID)
	if err != nil {
		t.Fatal(err)
	}
	if repaired.Status != domain.QuestPaused || repaired.Controller["resumeAfterRestart"] != true {
		t.Fatalf("a repairable failure must hand the quest to the extension: status=%s controller=%v", repaired.Status, repaired.Controller)
	}
	if workOrderAttemptV2(repaired) != 2 || !strings.Contains(repaired.Controller["statusMessage"].(string), "попытка 2 из 2") {
		t.Fatalf("attempt not counted: %v", repaired.Controller)
	}
	feedback, _ := repaired.Controller["repairFeedback"].(string)
	for _, want := range []string{"Образы собираются", "undefined: pgxpool", "чистом стенде"} {
		if !strings.Contains(feedback, want) {
			t.Fatalf("repair report lacks %q: %q", want, feedback)
		}
	}
	if _, final, _ := application.store.WorkOrderVerdictV2(ctx, quest.ID); final {
		t.Fatal("a repair attempt must not spend the quest's only verdict")
	}
	runtimes, err := application.store.ListMilestoneRuntimesV2(ctx, quest.ID, 1)
	if err != nil || len(runtimes) != 1 || runtimes[0].Status != domain.QuestDraft {
		t.Fatalf("milestone must be ready for the next launch: %#v err=%v", runtimes, err)
	}
	messages, err := application.store.ListChatMessages(ctx, quest.WorkspaceID, "master", 20)
	if err != nil {
		t.Fatal(err)
	}
	noticed := false
	for _, message := range messages {
		if message.Mode == "quest_notice" && strings.Contains(message.Content, "попытка 2 из 2") {
			noticed = true
		}
	}
	if !noticed {
		t.Fatalf("the feed does not say that Point is repairing: %#v", messages)
	}

	// Continuing returns the quest to a fresh launch, not to the check that
	// has just failed.
	status, err := application.store.ControlWorkOrderQuestV2(ctx, quest.ID, "resume", "")
	if err != nil || status != domain.QuestPreflight {
		t.Fatalf("repair pause resumes to %s err=%v, want preflight", status, err)
	}
}

// The last attempt ends with the verdict; «Продолжить» then says where the
// fix goes instead of moving the quest into a preflight nobody runs.
func TestLastAttemptEndsWithAVerdictThatResumeExplains(t *testing.T) {
	application, quest, _ := approvedHostCheckQuestForTest(t, 1)
	ctx := context.Background()

	application.finalizeQuestAfterFlow(quest.ID, true)

	final, err := application.workOrderQuestV2(ctx, quest.WorkspaceID, quest.ID)
	if err != nil || final.Status != domain.QuestBlocked {
		t.Fatalf("exhausted attempts must end with the verdict: status=%s err=%v", final.Status, err)
	}
	bundle, err := application.EvidenceBundle(ctx, quest.ID)
	if err != nil || !strings.Contains(bundle.HostDiagnostics, "undefined: pgxpool") {
		t.Fatalf("container logs are missing from the evidence: %q err=%v", bundle.HostDiagnostics, err)
	}
	_, err = application.ControlWorkOrderQuestV2(ctx, quest.ID, "resume", WorkOrderQuestControlRequest{})
	if err == nil || !strings.Contains(err.Error(), "Мастеру") {
		t.Fatalf("resume after a verdict must explain where the fix goes: %v", err)
	}
	after, _ := application.workOrderQuestV2(ctx, quest.WorkspaceID, quest.ID)
	if after.Status != domain.QuestBlocked {
		t.Fatalf("refused resume moved the quest to %s", after.Status)
	}
}

// quest_ae4f… of 26.09: two «Продолжить» after the verdict left the quest in
// `preflight` and rewrote its finished Flow to `running`. Startup repair gives
// both their real state back.
func TestNoopResumeAfterVerdictIsRepairedAtStartup(t *testing.T) {
	application, quest, _ := approvedHostCheckQuestForTest(t, 1)
	ctx := context.Background()
	application.finalizeQuestAfterFlow(quest.ID, true)

	stuck, err := application.workOrderQuestV2(ctx, quest.WorkspaceID, quest.ID)
	if err != nil {
		t.Fatal(err)
	}
	run := domain.FlowRun{
		ID: "flowrun-finished", FlowID: "flow-finished", WorkspaceID: quest.WorkspaceID, QuestID: quest.ID,
		Status: domain.RunRunning, StartedAt: time.Now().UTC(),
		NodeStates: map[string]domain.FlowNodeState{"implement": {Status: "completed"}, "accept": {Status: "completed"}},
	}
	if err = application.store.SaveFlowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	stuck.Status, stuck.ControllerState, stuck.FlowID, stuck.FlowRunID, stuck.FinishedAt = domain.QuestPreflight, string(domain.QuestPreflight), run.FlowID, run.ID, nil
	if err = application.store.SaveQuest(ctx, stuck); err != nil {
		t.Fatal(err)
	}

	application.reconcileNoopWorkOrderResumesV2(ctx)

	repaired, err := application.workOrderQuestV2(ctx, quest.WorkspaceID, quest.ID)
	if err != nil || repaired.Status != domain.QuestBlocked {
		t.Fatalf("stuck quest was not returned to its verdict: %s err=%v", repaired.Status, err)
	}
	restored, err := application.store.GetFlowRun(ctx, run.ID)
	if err != nil || restored.Status != domain.RunCompleted || restored.FinishedAt == nil {
		t.Fatalf("finished Flow still claims to run: %#v err=%v", restored, err)
	}
}
