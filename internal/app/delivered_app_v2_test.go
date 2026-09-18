package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

type recordingDeliveredAppRunner struct {
	calls [][]string
}

func (runner *recordingDeliveredAppRunner) Run(_ context.Context, _ string, arguments ...string) (string, error) {
	runner.calls = append(runner.calls, append([]string(nil), arguments...))
	return "services ready", nil
}

func TestDeliveredApplicationStartIsReceiptBoundAndIdempotent(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	runner := &recordingDeliveredAppRunner{}
	application, err := New(t.TempDir(), WithDeliveredAppRunner(runner))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { application.Shutdown(context.Background()) })
	workspace := openTestWorld(t, application)
	if err = os.WriteFile(filepath.Join(workspace.Path, "compose.yaml"), []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	exitCode := 0
	order := domain.WorkOrder{
		WorkspaceID: workspace.ID, State: "ready", Goal: "deliver app", Scope: []string{"app"},
		Criteria:  []domain.AcceptanceCriterion{{ID: "tests", Kind: "verification", Text: "tests pass", Tool: "run_command", Arguments: json.RawMessage(`{"command":"go test ./..."}`), ExpectedExitCode: &exitCode}},
		Workspace: domain.WorkspacePlan{Mode: "existing", Path: workspace.Path, Isolation: "snapshot"},
		Stack:     domain.StackPresetRef{ID: "web", Version: "1", Category: "web", Source: "benchmark"},
		Routing:   domain.ModelRoutingPolicy{Mode: "fixed", FixedConnectionID: "connection", FixedModel: "model"},
		Budget:    domain.BudgetEnvelope{Preset: "small", Tokens: 1000, ActiveSeconds: 60, MaxParallel: 1, MaxAttempts: 1},
		Delivery:  domain.DeliveryPolicy{ApplyMode: "automatic", CommitMode: "none", KeepPartialDays: 30},
		CreatedAt: now, UpdatedAt: now,
	}
	order, err = application.store.SaveWorkOrderV2(context.Background(), order)
	if err != nil {
		t.Fatal(err)
	}
	approval, err := application.store.ApproveWorkOrderV2(context.Background(), order.ID, order.Version, domain.WorkOrderDigest(order), "approve-delivered-app")
	if err != nil {
		t.Fatal(err)
	}
	revision := "sha256:workspace"
	bundle := domain.EvidenceBundle{
		Version: domain.CurrentWorkOrderEvidenceVersion, ID: "evidence-delivered-app", QuestID: approval.QuestID, PointVersion: Version,
		BriefDigest: domain.WorkOrderDigest(order), SourceDigest: domain.WorkOrderSourceDigest(order),
		EnvironmentDigest: workOrderEnvironmentDigestV2(order), SourceVersions: []domain.SourceSnapshotRef{}, StackPreset: order.Stack,
		Criteria:           []domain.CriterionEvidence{{CriterionID: "tests", Satisfied: true, Tool: "run_command", Command: "go test ./...", ExitCode: &exitCode}},
		VerificationChecks: []domain.VerificationCheck{{ID: "tests", Kind: "acceptance", Command: "go test ./...", ExitCode: &exitCode, Satisfied: true}},
		ModelCalls:         []domain.ModelCallLedgerEntry{{ID: "model-call-app", Provider: "test", Model: "model", Role: "writer", CostKnown: true, UsageReported: true, CreatedAt: now}},
		DeliveryVerified:   true, DeliveryTarget: workspace.Path, WorkspaceRevision: revision, CreatedAt: now,
		DeliveryReceipt: &domain.DeliveryReceipt{
			ID: "delivery-app", QuestID: approval.QuestID, WorkOrderDigest: domain.WorkOrderDigest(order),
			Target: workspace.Path, WorkspaceRevision: revision, ComposeFile: "compose.yaml", DeliveredAt: now,
		},
	}
	if status, finalizeErr := application.store.FinalizeWorkOrderQuestV2(context.Background(), approval.QuestID, bundle); finalizeErr != nil || status != domain.QuestCompleted {
		t.Fatalf("finalize status=%s err=%v", status, finalizeErr)
	}
	deliveredOrder, err := application.WorkOrderV2(context.Background(), order.ID)
	if err != nil || deliveredOrder.Runtime == nil || deliveredOrder.Runtime.DeliveryReceipt == nil || deliveredOrder.Runtime.DeliveryReceipt.ID != "delivery-app" || deliveredOrder.Runtime.Evidence == nil || deliveredOrder.Runtime.Evidence.Version != domain.CurrentWorkOrderEvidenceVersion {
		t.Fatalf("work order runtime does not expose delivery receipt: %#v err=%v", deliveredOrder.Runtime, err)
	}
	request := DeliveredApplicationControlRequestV2{
		Version: order.Version, WorkOrderDigest: domain.WorkOrderDigest(order), DeliveryReceiptID: "delivery-app", IdempotencyKey: "start-once",
	}
	first, err := application.ControlDeliveredApplicationV2(context.Background(), approval.QuestID, "start", request)
	if err != nil || first.Status != "running" || first.Replayed {
		t.Fatalf("first=%#v err=%v", first, err)
	}
	second, err := application.ControlDeliveredApplicationV2(context.Background(), approval.QuestID, "start", request)
	if err != nil || second.Status != "running" || !second.Replayed {
		t.Fatalf("replay=%#v err=%v", second, err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("idempotent start executed %d times", len(runner.calls))
	}
	if got := runner.calls[0]; len(got) != 5 || got[0] != "compose" || got[3] != "up" || got[4] != "-d" {
		t.Fatalf("unexpected docker arguments: %#v", got)
	}
}
