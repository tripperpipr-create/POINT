package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

// busyPortRunner — хост, у которого порт приложения занят, пока его не
// освободят.
type busyPortRunner struct {
	mu   sync.Mutex
	busy bool
}

func (runner *busyPortRunner) Run(_ context.Context, _, command string) (int, string, error) {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if strings.HasPrefix(withoutComposeProject(command), "docker compose up") && runner.busy {
		return 1, "Error response from daemon: Bind for 0.0.0.0:8080 failed: port is already allocated", nil
	}
	return 0, "", nil
}

func (runner *busyPortRunner) free() {
	runner.mu.Lock()
	runner.busy = false
	runner.mu.Unlock()
}

func busyPortQuestForTest(t *testing.T) (*App, domain.Quest, *busyPortRunner) {
	t.Helper()
	t.Setenv("REDIS_ADDR", "")
	runner := &busyPortRunner{busy: true}
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
	order.Budget.MaxAttempts = 3
	order.Delivery = domain.DeliveryPolicy{ApplyMode: "automatic", CommitMode: "none", KeepPartialDays: 30}
	assignReadyRosterForTest(t, application, &order)
	up, _ := json.Marshal(map[string]string{"command": "docker compose up -d"})
	order.Criteria = []domain.AcceptanceCriterion{{ID: "up", Kind: "verification", Tool: "run_command", Text: "Стек поднимается", Arguments: up}}
	order, err = application.SaveWorkOrderV2(ctx, order)
	if err != nil {
		t.Fatal(err)
	}
	approval, err := application.store.ApproveWorkOrderV2(ctx, order.ID, order.Version, domain.WorkOrderDigest(order), "recheck-"+t.Name())
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

// Q14: занятый порт на хосте после доставки был окончательным вердиктом, и
// исправить его можно было только новой версией наряда. Теперь квест ждёт
// человека, «Проверить снова» повторяет проверки на той же ревизии, а
// вынесенный вердикт по-прежнему окончателен.
func TestBusyHostPortHoldsForRecheckInsteadOfVerdict(t *testing.T) {
	application, quest, runner := busyPortQuestForTest(t)
	ctx := context.Background()

	application.finalizeQuestAfterFlow(quest.ID, true)

	held, err := application.workOrderQuestV2(ctx, quest.WorkspaceID, quest.ID)
	if err != nil {
		t.Fatal(err)
	}
	if held.Status != domain.QuestAwaitingUser || !hostRecheckPending(held) {
		t.Fatalf("занятый порт вынес вердикт вместо паузы: status=%s controller=%v", held.Status, held.Controller)
	}
	if _, final, _ := application.store.WorkOrderVerdictV2(ctx, quest.ID); final {
		t.Fatal("пауза на среде истратила вердикт квеста")
	}
	if state, _ := hostRecheckRecord(held); len(state.Actions) == 0 || !strings.Contains(state.Actions[0], "8080") {
		t.Fatalf("причина паузы не названа: %+v", state.Actions)
	}

	runner.free()
	if _, err = application.ControlWorkOrderQuestV2(ctx, quest.ID, "recheck", WorkOrderQuestControlRequest{}); err != nil {
		t.Fatal(err)
	}
	application.workOrderLaunchWG.Wait()
	done, err := application.workOrderQuestV2(ctx, quest.WorkspaceID, quest.ID)
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != domain.QuestCompleted {
		t.Fatalf("повтор после освобождения порта: status=%s message=%v", done.Status, done.Controller["statusMessage"])
	}
	if _, err = application.ControlWorkOrderQuestV2(ctx, quest.ID, "recheck", WorkOrderQuestControlRequest{}); err == nil {
		t.Fatal("повтор проверки у квеста с вердиктом принят")
	}
}

// «Завершить квест» выносит вердикт по итогу, на котором квест встал.
func TestFinalizeHeldHostCheckGivesTheVerdict(t *testing.T) {
	application, quest, _ := busyPortQuestForTest(t)
	ctx := context.Background()
	application.finalizeQuestAfterFlow(quest.ID, true)
	if _, err := application.ControlWorkOrderQuestV2(ctx, quest.ID, "finalize", WorkOrderQuestControlRequest{}); err != nil {
		t.Fatal(err)
	}
	application.workOrderLaunchWG.Wait()
	final, err := application.workOrderQuestV2(ctx, quest.WorkspaceID, quest.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != domain.QuestBlocked && final.Status != domain.QuestNeedsReview {
		t.Fatalf("вердикт по удержанному итогу: %s", final.Status)
	}
}

func TestEnvironmentOnlyHostFailures(t *testing.T) {
	code := 1
	env := domain.VerificationCheck{ID: "up", Satisfied: false, ExitCode: &code, Summary: "порт занят\nНужно действие: порт 8080 занят — app"}
	broken := domain.VerificationCheck{ID: "build", Satisfied: false, ExitCode: &code, Summary: "undefined: pgxpool"}
	passed := domain.VerificationCheck{ID: "health", Satisfied: true}
	if actions := environmentOnlyHostFailuresV2([]domain.VerificationCheck{env, passed}); len(actions) != 1 || !strings.Contains(actions[0], "8080") {
		t.Fatalf("отказ среды не распознан: %q", actions)
	}
	if actions := environmentOnlyHostFailuresV2([]domain.VerificationCheck{env, broken}); actions != nil {
		t.Fatalf("провал кода спрятан за паузой среды: %q", actions)
	}
}
