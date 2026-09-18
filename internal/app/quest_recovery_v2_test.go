package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

func TestInterruptedQuestBecomesPauseInsteadOfClaimingProgress(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { application.Shutdown(context.Background()) })
	ctx := context.Background()
	workspace := openTestWorld(t, application)
	now := time.Now().UTC()
	exitCode := 0
	order := domain.WorkOrder{
		WorkspaceID: workspace.ID, State: "ready", Goal: "survive a restart", Scope: []string{"api"},
		Criteria:  []domain.AcceptanceCriterion{{ID: "tests", Kind: "verification", Text: "tests pass", Tool: "run_command", Arguments: json.RawMessage(`{"command":"go test ./..."}`), ExpectedExitCode: &exitCode}},
		Workspace: domain.WorkspacePlan{Mode: "existing", Path: workspace.Path, Isolation: "snapshot"},
		Stack:     domain.StackPresetRef{ID: "api", Version: "1", Category: "api", Source: "benchmark"},
		Routing:   domain.ModelRoutingPolicy{Mode: "fixed", FixedConnectionID: "connection", FixedModel: "model"},
		Budget:    domain.BudgetEnvelope{Preset: "small", Tokens: 1000, ActiveSeconds: 60, MaxParallel: 1, MaxAttempts: 1},
		Delivery:  domain.DeliveryPolicy{ApplyMode: "automatic", CommitMode: "none", KeepPartialDays: 30},
		CreatedAt: now, UpdatedAt: now,
	}
	order, err = application.store.SaveWorkOrderV2(ctx, order)
	if err != nil {
		t.Fatal(err)
	}
	approval, err := application.store.ApproveWorkOrderV2(ctx, order.ID, order.Version, domain.WorkOrderDigest(order), "approve-restart")
	if err != nil {
		t.Fatal(err)
	}
	quest, err := application.workOrderQuestV2(ctx, workspace.ID, approval.QuestID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = application.setWorkOrderQuestStatusV2(ctx, quest, domain.QuestRunning, "Квест выполняется"); err != nil {
		t.Fatal(err)
	}

	// The process that was running this quest is gone; a new core must not
	// present its abandoned state as work in progress.
	application.pauseInterruptedWorkOrderQuestsV2(ctx)

	recovered, err := application.workOrderQuestV2(ctx, workspace.ID, approval.QuestID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status != domain.QuestPaused || recovered.FinishedAt != nil {
		t.Fatalf("recovered quest status=%s finished=%v", recovered.Status, recovered.FinishedAt)
	}
	if message, _ := recovered.Controller["statusMessage"].(string); message != questRecoveryMessageV2 {
		t.Fatalf("recovered quest message=%q", message)
	}
	interrupted, err := application.store.ListInterruptedWorkOrderQuestsV2(ctx)
	if err != nil || len(interrupted) != 0 {
		t.Fatalf("paused quest is not interrupted any more: %#v err=%v", interrupted, err)
	}
}
