package domain

import (
	"strings"
	"testing"
	"time"
)

func evidenceGateOrder(kind string) WorkOrder {
	return NormalizeWorkOrder(WorkOrder{
		State: "ready", Goal: "Ship", Scope: []string{"API"},
		Criteria:  []AcceptanceCriterion{{ID: "c1", Kind: kind, Text: "works"}},
		Workspace: WorkspacePlan{Mode: "managed", Path: `C:\Point\Projects\ship`, Isolation: "snapshot"},
		Stack:     StackPresetRef{ID: "test", Version: "1", Category: "web", Source: "benchmark"},
		Routing:   ModelRoutingPolicy{Mode: "fixed", FixedConnectionID: "c", FixedModel: "m"},
		Budget:    BudgetEnvelope{Preset: "small", Tokens: 1, ActiveSeconds: 1, MaxParallel: 1, MaxAttempts: 1},
		Delivery:  DeliveryPolicy{ApplyMode: "automatic", CommitMode: "none", KeepPartialDays: 30},
	})
}

func TestWorkOrderEvidenceStatusRequiresDeliveryAndAllCriteria(t *testing.T) {
	order := evidenceGateOrder("verification")
	bundle := EvidenceBundle{
		Version: CurrentWorkOrderEvidenceVersion, PointVersion: "test", ID: "e1", QuestID: "q1", BriefDigest: WorkOrderDigest(order), SourceDigest: WorkOrderSourceDigest(order),
		EnvironmentDigest: "env", StackPreset: order.Stack, SourceVersions: []SourceSnapshotRef{}, WorkspaceRevision: "tree-hash", DeliveryVerified: true,
		DeliveryReceipt:    &DeliveryReceipt{ID: "d1", QuestID: "q1", WorkOrderDigest: WorkOrderDigest(order), Target: order.Workspace.Path, WorkspaceRevision: "tree-hash", DeliveredAt: time.Now().UTC()},
		Criteria:           []CriterionEvidence{{CriterionID: "c1", Satisfied: true, Command: "go test ./...", ExitCode: intPointer(0)}},
		VerificationChecks: []VerificationCheck{{ID: "c1", Kind: "acceptance", Command: "go test ./...", ExitCode: intPointer(0), Satisfied: true}},
		ModelCalls:         evidenceGateModelCalls(),
	}
	status, err := WorkOrderEvidenceStatus(order, bundle)
	if err != nil || status != QuestCompleted {
		t.Fatalf("status=%s err=%v", status, err)
	}
	bundle.DeliveryVerified = false
	if status, err = WorkOrderEvidenceStatus(order, bundle); err != nil || status != QuestBlocked {
		t.Fatalf("undelivered status=%s err=%v", status, err)
	}
}

func TestWorkOrderEvidenceStatusRejectsMissingOrMismatchedMachineProof(t *testing.T) {
	order := evidenceGateOrder("verification")
	bundle := EvidenceBundle{
		Version: CurrentWorkOrderEvidenceVersion, PointVersion: "test", ID: "e1", QuestID: "q1", BriefDigest: WorkOrderDigest(order), SourceDigest: WorkOrderSourceDigest(order),
		EnvironmentDigest: "env", StackPreset: order.Stack, SourceVersions: []SourceSnapshotRef{}, WorkspaceRevision: "tree-hash", DeliveryVerified: true,
		DeliveryReceipt: &DeliveryReceipt{ID: "d1", QuestID: "q1", WorkOrderDigest: WorkOrderDigest(order), Target: order.Workspace.Path, WorkspaceRevision: "tree-hash", DeliveredAt: time.Now().UTC()},
		Criteria:        []CriterionEvidence{{CriterionID: "c1", Satisfied: true, Command: "go test ./...", ExitCode: intPointer(0)}},
		ModelCalls:      evidenceGateModelCalls(),
	}
	if status, err := WorkOrderEvidenceStatus(order, bundle); err != nil || status != QuestBlocked {
		t.Fatalf("missing check status=%s err=%v", status, err)
	}
	bundle.VerificationChecks = []VerificationCheck{{ID: "c1", Kind: "acceptance", Command: "go test ./...", ExitCode: intPointer(1), Satisfied: true}}
	if status, err := WorkOrderEvidenceStatus(order, bundle); err != nil || status != QuestBlocked {
		t.Fatalf("mismatched exit status=%s err=%v", status, err)
	}
}

func intPointer(value int) *int { return &value }

// A manual criterion is part of the contract: until a human decides it the
// work is delivered but not accepted. "completed with limitations" used to
// stand in for that and read as done.
func TestWorkOrderEvidenceStatusWaitsForHumanOnManualCriterion(t *testing.T) {
	order := evidenceGateOrder("manual")
	bundle := EvidenceBundle{
		Version: CurrentWorkOrderEvidenceVersion, PointVersion: "test", ID: "e1", QuestID: "q1", BriefDigest: WorkOrderDigest(order), SourceDigest: WorkOrderSourceDigest(order),
		EnvironmentDigest: "env", StackPreset: order.Stack, SourceVersions: []SourceSnapshotRef{}, WorkspaceRevision: "tree-hash", DeliveryVerified: true,
		DeliveryReceipt: &DeliveryReceipt{ID: "d1", QuestID: "q1", WorkOrderDigest: WorkOrderDigest(order), Target: order.Workspace.Path, WorkspaceRevision: "tree-hash", DeliveredAt: time.Now().UTC()},
		Criteria:        []CriterionEvidence{{CriterionID: "c1", Satisfied: true}},
		ModelCalls:      evidenceGateModelCalls(),
	}
	status, err := WorkOrderEvidenceStatus(order, bundle)
	if err != nil || status != QuestNeedsReview {
		t.Fatalf("status=%s err=%v", status, err)
	}
	if verdict := WorkOrderEvidenceVerdict(order, bundle); verdict.Assurance != WorkOrderAssurancePartial {
		t.Fatalf("assurance=%q", verdict.Assurance)
	}
	accepted, verdict := ApplyManualReviews(order, bundle, []ManualCriterionReview{{QuestID: "q1", CriterionID: "c1", Decision: ManualReviewAccepted, Note: "проверил вручную", CreatedAt: time.Now().UTC()}})
	if verdict.Status != QuestCompleted || accepted.Assurance != WorkOrderAssuranceVerified || accepted.Criteria[0].Review != ManualReviewAccepted {
		t.Fatalf("accepted manual criterion: status=%s assurance=%q criteria=%#v", verdict.Status, accepted.Assurance, accepted.Criteria)
	}
	for _, line := range accepted.KnownLimitations {
		if strings.Contains(line, "manual") {
			t.Fatalf("stale gate reason survived acceptance: %q", line)
		}
	}
	if _, verdict = ApplyManualReviews(order, bundle, []ManualCriterionReview{{QuestID: "q1", CriterionID: "c1", Decision: ManualReviewRejected, CreatedAt: time.Now().UTC()}}); verdict.Status != QuestBlocked {
		t.Fatalf("rejected manual criterion must block, got %s", verdict.Status)
	}
}

func TestWorkOrderEvidenceStatusLeavesManualDeliveryForReview(t *testing.T) {
	order := evidenceGateOrder("verification")
	order.Delivery.ApplyMode = "manual"
	bundle := EvidenceBundle{
		Version: CurrentWorkOrderEvidenceVersion, PointVersion: "test", ID: "e1", QuestID: "q1", BriefDigest: WorkOrderDigest(order), SourceDigest: WorkOrderSourceDigest(order),
		EnvironmentDigest: "env", StackPreset: order.Stack, SourceVersions: []SourceSnapshotRef{}, WorkspaceRevision: "isolated-tree-hash", DeliveryVerified: true,
		DeliveryReceipt:    &DeliveryReceipt{ID: "d1", QuestID: "q1", WorkOrderDigest: WorkOrderDigest(order), Target: "isolated_review", WorkspaceRevision: "isolated-tree-hash", DeliveredAt: time.Now().UTC()},
		Criteria:           []CriterionEvidence{{CriterionID: "c1", Satisfied: true, Command: "go test ./...", ExitCode: intPointer(0)}},
		VerificationChecks: []VerificationCheck{{ID: "c1", Kind: "acceptance", Command: "go test ./...", ExitCode: intPointer(0), Satisfied: true}},
		ModelCalls:         evidenceGateModelCalls(),
	}
	status, err := WorkOrderEvidenceStatus(order, bundle)
	if err != nil || status != QuestNeedsReview {
		t.Fatalf("status=%s err=%v", status, err)
	}
}

func TestWorkOrderEvidenceStatusRequiresEveryProfileCheckToBeExecuted(t *testing.T) {
	order := evidenceGateOrder("verification")
	order.Completion = CompletionProfile{ID: "mvp-web", Version: "1", Checks: []CompletionCheck{
		{Kind: CompletionCheckAcceptance},
		{Kind: "automated_tests", Command: "go test ./..."},
	}}
	order = NormalizeWorkOrder(order)
	bundle := EvidenceBundle{
		Version: CurrentWorkOrderEvidenceVersion, PointVersion: "test", ID: "e1", QuestID: "q1", BriefDigest: WorkOrderDigest(order), SourceDigest: WorkOrderSourceDigest(order),
		EnvironmentDigest: "env", StackPreset: order.Stack, SourceVersions: []SourceSnapshotRef{}, WorkspaceRevision: "tree-hash", DeliveryVerified: true,
		DeliveryReceipt:    &DeliveryReceipt{ID: "d1", QuestID: "q1", WorkOrderDigest: WorkOrderDigest(order), Target: order.Workspace.Path, WorkspaceRevision: "tree-hash", DeliveredAt: time.Now().UTC()},
		Criteria:           []CriterionEvidence{{CriterionID: "c1", Satisfied: true, Command: "go test ./...", ExitCode: intPointer(0)}},
		VerificationChecks: []VerificationCheck{{ID: "c1", Kind: "acceptance", Command: "go test ./...", ExitCode: intPointer(0), Satisfied: true}},
		ModelCalls:         evidenceGateModelCalls(),
	}
	// An unavailable profile check lowers assurance without turning absence of
	// the tool into a failed execution.
	if status, err := WorkOrderEvidenceStatus(order, bundle); err != nil || status != QuestCompleted {
		t.Fatalf("unexecuted profile check status=%s err=%v", status, err)
	}
	if verdict := WorkOrderEvidenceVerdict(order, bundle); verdict.Assurance != WorkOrderAssurancePartial {
		t.Fatalf("unexecuted assurance=%q", verdict.Assurance)
	}
	executed := VerificationCheck{ID: CompletionCheckEvidenceID("automated_tests"), Kind: "automated_tests", Command: "npm test", ExitCode: intPointer(0), Satisfied: true}
	bundle.VerificationChecks = append(bundle.VerificationChecks, executed)
	if status, err := WorkOrderEvidenceStatus(order, bundle); err == nil || status != QuestBlocked {
		t.Fatalf("substituted command status=%s err=%v", status, err)
	}
	bundle.VerificationChecks[1].Command = "go test ./..."
	bundle.VerificationChecks[1].ExitCode, bundle.VerificationChecks[1].Satisfied = intPointer(1), false
	if status, err := WorkOrderEvidenceStatus(order, bundle); err != nil || status != QuestBlocked {
		t.Fatalf("failed profile check status=%s err=%v", status, err)
	}
	bundle.VerificationChecks[1].ExitCode, bundle.VerificationChecks[1].Satisfied = intPointer(0), true
	if status, err := WorkOrderEvidenceStatus(order, bundle); err != nil || status != QuestCompleted {
		t.Fatalf("executed profile status=%s err=%v", status, err)
	}
}

func TestWorkOrderEvidenceStatusRequiresProofThatServicesAreRunning(t *testing.T) {
	order := evidenceGateOrder("verification")
	order.Delivery.KeepServicesRunning, order.Delivery.ApplicationURL = true, "http://localhost:8080"
	order = NormalizeWorkOrder(order)
	receipt := &DeliveryReceipt{
		ID: "d1", QuestID: "q1", WorkOrderDigest: WorkOrderDigest(order), Target: order.Workspace.Path,
		WorkspaceRevision: "tree-hash", URL: "http://localhost:8080", ComposeFile: "compose.yaml", DeliveredAt: time.Now().UTC(),
	}
	bundle := EvidenceBundle{
		Version: CurrentWorkOrderEvidenceVersion, PointVersion: "test", ID: "e1", QuestID: "q1", BriefDigest: WorkOrderDigest(order), SourceDigest: WorkOrderSourceDigest(order),
		EnvironmentDigest: "env", StackPreset: order.Stack, SourceVersions: []SourceSnapshotRef{}, WorkspaceRevision: "tree-hash", DeliveryVerified: true,
		DeliveryReceipt:    receipt,
		Criteria:           []CriterionEvidence{{CriterionID: "c1", Satisfied: true, Command: "go test ./...", ExitCode: intPointer(0)}},
		VerificationChecks: []VerificationCheck{{ID: "c1", Kind: "acceptance", Command: "go test ./...", ExitCode: intPointer(0), Satisfied: true}},
		ModelCalls:         evidenceGateModelCalls(),
	}
	// If the service probe could not run, delivery completes with partial
	// assurance. This is distinct from a probe that ran and failed.
	if status, err := WorkOrderEvidenceStatus(order, bundle); err != nil || status != QuestCompleted {
		t.Fatalf("unproven services status=%s err=%v", status, err)
	}
	if verdict := WorkOrderEvidenceVerdict(order, bundle); verdict.Assurance != WorkOrderAssurancePartial {
		t.Fatalf("unproven services assurance=%q", verdict.Assurance)
	}
	bundle.VerificationChecks = append(bundle.VerificationChecks, VerificationCheck{ID: CompletionCheckEvidenceID("service_start"), Kind: "service_start", Command: "docker compose up", ExitCode: intPointer(1)})
	if status, err := WorkOrderEvidenceStatus(order, bundle); err != nil || status != QuestBlocked {
		t.Fatalf("failed service start status=%s err=%v", status, err)
	}
	bundle.VerificationChecks[len(bundle.VerificationChecks)-1].ExitCode = intPointer(0)
	bundle.VerificationChecks[len(bundle.VerificationChecks)-1].Satisfied = true
	receipt.ServicesRunning = true
	if status, err := WorkOrderEvidenceStatus(order, bundle); err != nil || status != QuestCompleted {
		t.Fatalf("running services status=%s err=%v", status, err)
	}
}

func TestWorkOrderEvidenceStatusBlocksExternalConflict(t *testing.T) {
	order := evidenceGateOrder("verification")
	bundle := EvidenceBundle{
		Version: CurrentWorkOrderEvidenceVersion, PointVersion: "test", ID: "e1", QuestID: "q1", BriefDigest: WorkOrderDigest(order), SourceDigest: WorkOrderSourceDigest(order),
		EnvironmentDigest: "env", StackPreset: order.Stack, SourceVersions: []SourceSnapshotRef{}, WorkspaceRevision: "tree-hash",
		DeliveryConflict:   true,
		Criteria:           []CriterionEvidence{{CriterionID: "c1", Satisfied: true, Command: "go test ./...", ExitCode: intPointer(0)}},
		VerificationChecks: []VerificationCheck{{ID: "c1", Kind: "acceptance", Command: "go test ./...", ExitCode: intPointer(0), Satisfied: true}},
		ModelCalls:         evidenceGateModelCalls(),
	}
	if status, err := WorkOrderEvidenceStatus(order, bundle); err != nil || status != QuestBlocked {
		t.Fatalf("conflict status=%s err=%v", status, err)
	}
	// A conflict on top of failed criteria is still a failure, not a review.
	bundle.Criteria[0].Satisfied = false
	if status, err := WorkOrderEvidenceStatus(order, bundle); err != nil || status != QuestBlocked {
		t.Fatalf("failed criteria with conflict status=%s err=%v", status, err)
	}
}

func evidenceGateModelCalls() []ModelCallLedgerEntry {
	return []ModelCallLedgerEntry{{ID: "model-call-1", Provider: "test", Model: "model", Role: "writer", CostKnown: true, UsageReported: true, CreatedAt: time.Now().UTC()}}
}

// Шлюз обязан называть условие, на котором остановился. Прежде он отвечал
// `blocked` без слова объяснения в пяти местах, и живая приёмка показывала
// «0/2», не говоря, чего именно не хватило: разбор начинался с
// воспроизведения вслепую. Тест держит это свойство — молчаливый отказ
// вернётся первым же рефакторингом, если его не стеречь.
func TestWorkOrderEvidenceVerdictNamesTheFailedCondition(t *testing.T) {
	order := evidenceGateOrder("verification")
	complete := EvidenceBundle{
		Version: CurrentWorkOrderEvidenceVersion, PointVersion: "test", ID: "e1", QuestID: "q1", BriefDigest: WorkOrderDigest(order), SourceDigest: WorkOrderSourceDigest(order),
		EnvironmentDigest: "env", StackPreset: order.Stack, SourceVersions: []SourceSnapshotRef{}, WorkspaceRevision: "tree-hash", DeliveryVerified: true,
		DeliveryReceipt:    &DeliveryReceipt{ID: "d1", QuestID: "q1", WorkOrderDigest: WorkOrderDigest(order), Target: order.Workspace.Path, WorkspaceRevision: "tree-hash", DeliveredAt: time.Now().UTC()},
		Criteria:           []CriterionEvidence{{CriterionID: "c1", Satisfied: true, Command: "go test ./...", ExitCode: intPointer(0)}},
		VerificationChecks: []VerificationCheck{{ID: "c1", Kind: "acceptance", Command: "go test ./...", ExitCode: intPointer(0), Satisfied: true}},
		ModelCalls:         evidenceGateModelCalls(),
	}
	if verdict := WorkOrderEvidenceVerdict(order, complete); verdict.Status != QuestCompleted || verdict.Reason != "" {
		t.Fatalf("принятая работа обязана проходить молча: status=%s reason=%q", verdict.Status, verdict.Reason)
	}

	undelivered := complete
	undelivered.DeliveryVerified = false
	verdict := WorkOrderEvidenceVerdict(order, undelivered)
	if verdict.Status != QuestBlocked || verdict.Err != nil {
		t.Fatalf("недоставленная работа — честный провал, не ошибка: status=%s err=%v", verdict.Status, verdict.Err)
	}
	if !containsMissing(verdict.Missing, "delivery_verified") || verdict.Reason == "" {
		t.Fatalf("отказ не назвал условие: reason=%q missing=%v", verdict.Reason, verdict.Missing)
	}

	noCalls := complete
	noCalls.ModelCalls = nil
	if verdict = WorkOrderEvidenceVerdict(order, noCalls); !containsMissing(verdict.Missing, "model_calls") {
		t.Fatalf("пустой ledger обращений не назван: missing=%v", verdict.Missing)
	}

	failedCriterion := complete
	failedCriterion.VerificationChecks = []VerificationCheck{{ID: "c1", Kind: "acceptance", Command: "go test ./...", ExitCode: intPointer(1), Satisfied: true}}
	if verdict = WorkOrderEvidenceVerdict(order, failedCriterion); !containsMissing(verdict.Missing, "criterion:c1") {
		t.Fatalf("проваленный критерий не назван: missing=%v", verdict.Missing)
	}

	// Структурное расхождение остаётся ошибкой: доказательство недостоверно,
	// и транзакция шлюза обязана откатиться, а не сохранить bundle.
	foreign := complete
	foreign.BriefDigest = "sha256:someone-else"
	if verdict = WorkOrderEvidenceVerdict(order, foreign); verdict.Err == nil {
		t.Fatal("чужой дайджест обязан оставаться ошибкой, а не мягким отказом")
	}
}

func containsMissing(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
