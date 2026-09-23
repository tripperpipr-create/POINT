package storage

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

func storageWorkOrder() domain.WorkOrder {
	return domain.NormalizeWorkOrder(domain.WorkOrder{
		WorkspaceID: "workspace-v2", State: "ready", Goal: "Build", Scope: []string{"API"}, Criteria: []domain.AcceptanceCriterion{{ID: "c1", Kind: "verification", Text: "healthy", Tool: "run_command", Arguments: json.RawMessage(`{"command":"go test ./..."}`)}},
		Workspace: domain.WorkspacePlan{Mode: "managed", Path: `C:\Users\Test\Point\Projects\build`, Isolation: "snapshot"},
		Stack:     domain.StackPresetRef{ID: "web", Version: "1", Category: "web", Source: "benchmark"},
		Routing:   domain.ModelRoutingPolicy{Mode: "fixed", FixedConnectionID: "c", FixedModel: "m", FallbackMode: "auto"},
		Budget:    domain.BudgetEnvelope{Preset: "small", Tokens: 1000, ActiveSeconds: 60, MaxParallel: 1, MaxAttempts: 1},
		Delivery:  domain.DeliveryPolicy{ApplyMode: "automatic", CommitMode: "none", KeepPartialDays: 30},
	})
}

func TestWorkOrderApprovalAtomicallyMaterializesRoster(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "hub-v2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	if err = store.SaveConnection(ctx, domain.Connection{ID: "c", Provider: domain.ProviderOpenAI, PresetID: "openai", DisplayName: "OpenAI", BaseURL: "https://api.openai.com/v1", Status: domain.ConnectionConnected, DefaultModel: "m", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	order := storageWorkOrder()
	order.Roster = domain.AgentRosterPlan{
		Permanent: []domain.AgentDraft{{ID: "backend-owner", Name: "Backend owner", Role: "backend", Mission: "Own the API", RequiredTools: []string{"read_file"}, RequiresConsent: true}},
		Temporary: []domain.SubagentPlan{{ParentAgentID: "backend-owner", Role: "migration specialist", Mission: "Prepare migrations", RequiredTools: []string{"read_file"}}},
	}
	order.Budget.MaxProjectAgents = 2
	order = domain.NormalizeWorkOrder(order)
	created, err := store.SaveWorkOrderV2(ctx, order)
	if err != nil {
		t.Fatal(err)
	}
	if blueprints, listErr := store.ListBlueprints(ctx); listErr != nil || len(blueprints) != 0 {
		t.Fatalf("agents were created before approval: %#v err=%v", blueprints, listErr)
	}
	approval, err := store.ApproveWorkOrderV2(ctx, created.ID, created.Version, domain.WorkOrderDigest(created), "roster-once")
	if err != nil {
		t.Fatal(err)
	}
	if len(approval.AgentIDs) != 2 {
		t.Fatalf("materialized agent ids=%#v", approval.AgentIDs)
	}
	blueprints, err := store.ListBlueprints(ctx)
	if err != nil || len(blueprints) != 1 {
		t.Fatalf("blueprints=%#v err=%v", blueprints, err)
	}
	agents, err := store.ListProjectAgents(ctx, created.WorkspaceID)
	if err != nil || len(agents) != 2 {
		t.Fatalf("project agents=%#v err=%v", agents, err)
	}
	var temporary *domain.ProjectAgent
	for i := range agents {
		if agents[i].Temporary {
			temporary = &agents[i]
		}
	}
	if temporary == nil || temporary.ParentAgentID != "backend-owner" {
		t.Fatalf("temporary subagent lost approved parent: %#v", temporary)
	}
}

func TestRevisingApprovedWorkOrderPausesQuestAndRecordsDiff(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "hub-v2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	order, err := store.SaveWorkOrderV2(ctx, storageWorkOrder())
	if err != nil {
		t.Fatal(err)
	}
	approval, err := store.ApproveWorkOrderV2(ctx, order.ID, order.Version, domain.WorkOrderDigest(order), "revision-once")
	if err != nil {
		t.Fatal(err)
	}
	revised := approval.WorkOrder
	revised.Version++
	revised.State = "ready"
	revised.Goal = "Build the revised API"
	revised = domain.NormalizeWorkOrder(revised)
	if _, err = store.SaveWorkOrderV2(ctx, revised); err != nil {
		t.Fatal(err)
	}
	diffs, err := store.ListWorkOrderDiffsV2(ctx, order.ID)
	if err != nil || len(diffs) != 1 || !diffs[0].RequiresApproval || len(diffs[0].ChangedFields) == 0 {
		t.Fatalf("revision diff=%#v err=%v", diffs, err)
	}
	quests, err := store.ListQuests(ctx, order.WorkspaceID)
	if err != nil || len(quests) != 1 || quests[0].ID != approval.QuestID || quests[0].Status != domain.QuestPaused {
		t.Fatalf("active quest was not paused: quests=%#v err=%v", quests, err)
	}
	reapproved, err := store.ApproveWorkOrderV2(ctx, revised.ID, revised.Version, domain.WorkOrderDigest(revised), "revision-two")
	if err != nil || reapproved.QuestID != approval.QuestID {
		t.Fatalf("revision approval created a different quest: first=%#v next=%#v err=%v", approval, reapproved, err)
	}
	quests, err = store.ListQuests(ctx, order.WorkspaceID)
	if err != nil || len(quests) != 1 || quests[0].Status != domain.QuestPreflight || quests[0].Title != revised.Goal {
		t.Fatalf("reapproved quest was not rebound: quests=%#v err=%v", quests, err)
	}
}

func TestWorkOrderV2RevisionAndApprovalAreImmutableAndIdempotent(t *testing.T) {
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
	digest := domain.WorkOrderDigest(first)
	approved, err := store.ApproveWorkOrderV2(ctx, first.ID, first.Version, digest, "start-once")
	if err != nil || approved.Status != "preflight" || approved.QuestID == "" {
		t.Fatalf("approval=%#v err=%v", approved, err)
	}
	quests, err := store.ListQuests(ctx, first.WorkspaceID)
	if err != nil || len(quests) != 1 || quests[0].ID != approved.QuestID || quests[0].Status != domain.QuestPreflight {
		t.Fatalf("approval did not atomically create preflight quest: quests=%#v err=%v", quests, err)
	}
	if quests[0].Controller["workOrderId"] != first.ID || quests[0].Controller["approvedDigest"] != digest {
		t.Fatalf("quest lost work order binding: %#v", quests[0].Controller)
	}
	forced := quests[0]
	forced.Status = domain.QuestCompleted
	if err = store.SaveQuest(ctx, forced); err == nil {
		t.Fatal("v2 quest reached completed without evidence gate")
	}
	evidence := domain.EvidenceBundle{
		Version: domain.CurrentWorkOrderEvidenceVersion, PointVersion: "test", ID: "evidence-v2", QuestID: approved.QuestID, BriefDigest: digest, SourceDigest: domain.WorkOrderSourceDigest(first),
		EnvironmentDigest: "environment-digest", StackPreset: first.Stack, SourceVersions: []domain.SourceSnapshotRef{}, WorkspaceRevision: "workspace-tree-hash", DeliveryVerified: true,
		DeliveryReceipt:    &domain.DeliveryReceipt{ID: "delivery-v2", QuestID: approved.QuestID, WorkOrderDigest: digest, Target: first.Workspace.Path, WorkspaceRevision: "workspace-tree-hash", DeliveredAt: time.Now().UTC()},
		Criteria:           []domain.CriterionEvidence{{CriterionID: "c1", Satisfied: true, Command: "go test ./...", ExitCode: storageTestIntPtr(0)}},
		VerificationChecks: []domain.VerificationCheck{{ID: "c1", Kind: "acceptance", Command: "go test ./...", ExitCode: storageTestIntPtr(0), Satisfied: true}},
		ModelCalls:         storageTestModelCalls(),
	}
	status, err := store.FinalizeWorkOrderQuestV2(ctx, approved.QuestID, evidence)
	if err != nil || status != domain.QuestCompleted {
		t.Fatalf("finalize status=%s err=%v", status, err)
	}
	quests, err = store.ListQuests(ctx, first.WorkspaceID)
	if err != nil || quests[0].Status != domain.QuestCompleted || quests[0].FinishedAt == nil {
		t.Fatalf("completed quest was not persisted: quests=%#v err=%v", quests, err)
	}
	// Reproduce the historical launch race: an older launch goroutine wrote its
	// stale `running` copy after the immutable completion gate committed.
	if _, err = store.db.ExecContext(ctx, `UPDATE quests SET status='running',controller_state='running',finished_at=NULL WHERE id=?`, approved.QuestID); err != nil {
		t.Fatal(err)
	}
	if status, err = store.FinalizeWorkOrderQuestV2(ctx, approved.QuestID, evidence); err != nil || status != domain.QuestCompleted {
		t.Fatalf("finalize replay status=%s err=%v", status, err)
	}
	quests, err = store.ListQuests(ctx, first.WorkspaceID)
	if err != nil || quests[0].Status != domain.QuestCompleted || quests[0].FinishedAt == nil {
		t.Fatalf("gate replay did not repair stale running quest: quests=%#v err=%v", quests, err)
	}
	var evidenceCount, gateCount int
	if err = store.db.QueryRow(`SELECT COUNT(1) FROM evidence_bundles WHERE quest_id=?`, approved.QuestID).Scan(&evidenceCount); err != nil {
		t.Fatal(err)
	}
	if err = store.db.QueryRow(`SELECT COUNT(1) FROM work_order_completion_gates_v2 WHERE quest_id=?`, approved.QuestID).Scan(&gateCount); err != nil {
		t.Fatal(err)
	}
	if evidenceCount != 1 || gateCount != 1 {
		t.Fatalf("finalize replay duplicated rows: evidence=%d gates=%d", evidenceCount, gateCount)
	}
	reloaded, err := store.GetWorkOrderV2(ctx, first.ID)
	if err != nil || reloaded.Runtime == nil || reloaded.Runtime.Assurance != domain.WorkOrderAssuranceVerified || reloaded.Runtime.OutcomeSummary == "" || reloaded.Runtime.Message != reloaded.Runtime.OutcomeSummary {
		t.Fatalf("public runtime lost assurance/outcome: runtime=%#v err=%v", reloaded.Runtime, err)
	}
	replay, err := store.ApproveWorkOrderV2(ctx, first.ID, first.Version, digest, "start-once")
	if err != nil || !replay.Replayed || replay.QuestID != approved.QuestID {
		t.Fatalf("replay=%#v err=%v", replay, err)
	}
	if _, err = store.ApproveWorkOrderV2(ctx, "another-work-order", first.Version, digest, "start-once"); err == nil {
		t.Fatal("idempotency key was replayed for a different work order")
	}
	changed := first
	changed.Goal = "Changed without revision"
	if _, err = store.SaveWorkOrderV2(ctx, changed); err == nil {
		t.Fatal("same version accepted different content")
	}
}

func TestApproveWorkOrderV2AtomicallyStartsLinkedProposal(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "hub-v2-proposal.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	order := storageWorkOrder()
	order.ProposalID = "proposal-linked"
	if err = store.SaveQuestProposal(ctx, domain.QuestProposal{ID: order.ProposalID, WorkspaceID: order.WorkspaceID, Title: "Linked", Status: "pending", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	order, err = store.SaveWorkOrderV2(ctx, order)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ApproveWorkOrderV2(ctx, order.ID, order.Version, domain.WorkOrderDigest(order), "proposal-link-once"); err != nil {
		t.Fatal(err)
	}
	var status string
	if err = store.db.QueryRow(`SELECT status FROM quest_proposals WHERE id=?`, order.ProposalID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "started" {
		t.Fatalf("proposal status=%q", status)
	}
	if replay, replayErr := store.ApproveWorkOrderV2(ctx, order.ID, order.Version, domain.WorkOrderDigest(order), "proposal-link-once"); replayErr != nil || !replay.Replayed {
		t.Fatalf("replay=%#v err=%v", replay, replayErr)
	}
}

func TestWriterLeaseIsTransactionalAndReleasedOnlyAfterCompletion(t *testing.T) {
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
	firstApproval, err := store.ApproveWorkOrderV2(ctx, first.ID, first.Version, domain.WorkOrderDigest(first), "lease-first")
	if err != nil {
		t.Fatal(err)
	}
	lease, found, err := store.WriterLeaseV2(ctx, first.WorkspaceID)
	if err != nil || !found || lease.State != "active" || lease.QuestID != firstApproval.QuestID || lease.Token == "" {
		t.Fatalf("active writer lease was not persisted: %#v found=%v err=%v", lease, found, err)
	}

	secondDraft := storageWorkOrder()
	secondDraft.ID = "workorder-second-writer"
	second, err := store.SaveWorkOrderV2(ctx, secondDraft)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ApproveWorkOrderV2(ctx, second.ID, second.Version, domain.WorkOrderDigest(second), "lease-second-blocked"); err == nil {
		t.Fatal("second writer acquired the same workspace")
	}
	revision := "workspace-tree-hash"
	evidence := domain.EvidenceBundle{
		Version: domain.CurrentWorkOrderEvidenceVersion, PointVersion: "test", ID: "lease-evidence", QuestID: firstApproval.QuestID,
		BriefDigest: domain.WorkOrderDigest(first), SourceDigest: domain.WorkOrderSourceDigest(first), SourceVersions: []domain.SourceSnapshotRef{},
		EnvironmentDigest: "environment", StackPreset: first.Stack, WorkspaceRevision: revision, DeliveryVerified: true,
		DeliveryReceipt:    &domain.DeliveryReceipt{ID: "lease-delivery", QuestID: firstApproval.QuestID, WorkOrderDigest: domain.WorkOrderDigest(first), Target: first.Workspace.Path, WorkspaceRevision: revision, DeliveredAt: time.Now().UTC()},
		Criteria:           []domain.CriterionEvidence{{CriterionID: "c1", Satisfied: true, Command: "go test ./...", ExitCode: storageTestIntPtr(0)}},
		VerificationChecks: []domain.VerificationCheck{{ID: "c1", Kind: "acceptance", Command: "go test ./...", ExitCode: storageTestIntPtr(0), Satisfied: true}},
		ModelCalls:         storageTestModelCalls(),
	}
	if status, finalizeErr := store.FinalizeWorkOrderQuestV2(ctx, firstApproval.QuestID, evidence); finalizeErr != nil || status != domain.QuestCompleted {
		t.Fatalf("first writer did not complete: status=%s err=%v", status, finalizeErr)
	}
	lease, found, err = store.WriterLeaseV2(ctx, first.WorkspaceID)
	if err != nil || !found || lease.State != "released" || lease.ReleasedAt == nil {
		t.Fatalf("completed writer lease was not released: %#v found=%v err=%v", lease, found, err)
	}
	if _, err = store.ApproveWorkOrderV2(ctx, second.ID, second.Version, domain.WorkOrderDigest(second), "lease-second-approved"); err != nil {
		t.Fatalf("released workspace lease was not reusable: %v", err)
	}
}

func storageTestIntPtr(value int) *int { return &value }

func storageTestModelCalls() []domain.ModelCallLedgerEntry {
	return []domain.ModelCallLedgerEntry{{ID: domain.NewID("model-call"), Provider: "test", Model: "model", Role: "writer", CostKnown: true, UsageReported: true, CreatedAt: time.Now().UTC()}}
}

func TestCreateApprovedWorkOrderV2CommitsSourcesAndRuntimeAtomically(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "fast-work-order.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	first := storageWorkOrder()
	first.Workspace.Path = t.TempDir()
	snapshot := domain.SourceSnapshot{
		ID: "source-fast-first", WorkspaceID: first.WorkspaceID, Kind: "text",
		Digest: "sha256:first", ExtractedText: "first", CreatedAt: time.Now().UTC(),
	}
	first.Sources = []domain.SourceSnapshotRef{{ID: snapshot.ID, Kind: snapshot.Kind, Digest: snapshot.Digest}}
	first = domain.NormalizeWorkOrder(first)
	approval, err := store.CreateApprovedWorkOrderV2(ctx, first, []domain.SourceSnapshot{snapshot}, "fast-first")
	if err != nil {
		t.Fatal(err)
	}
	if approval.QuestID == "" || approval.WorkOrder.State != "approved" {
		t.Fatalf("approved launch is incomplete: %#v", approval)
	}
	if _, err = store.GetSourceSnapshotV2(ctx, snapshot.ID); err != nil {
		t.Fatalf("source snapshot was not committed with launch: %v", err)
	}
	if replay, replayErr := store.CreateApprovedWorkOrderV2(ctx, first, nil, "fast-first"); replayErr != nil || !replay.Replayed || replay.QuestID != approval.QuestID {
		t.Fatalf("atomic launch replay=%#v err=%v", replay, replayErr)
	}

	second := storageWorkOrder()
	second.ID = "workorder-fast-second"
	second.Workspace.Path = first.Workspace.Path
	secondSnapshot := domain.SourceSnapshot{
		ID: "source-fast-second", WorkspaceID: second.WorkspaceID, Kind: "text",
		Digest: "sha256:second", ExtractedText: "second", CreatedAt: time.Now().UTC(),
	}
	second.Sources = []domain.SourceSnapshotRef{{ID: secondSnapshot.ID, Kind: secondSnapshot.Kind, Digest: secondSnapshot.Digest}}
	second = domain.NormalizeWorkOrder(second)
	if _, err = store.CreateApprovedWorkOrderV2(ctx, second, []domain.SourceSnapshot{secondSnapshot}, "fast-second"); err == nil {
		t.Fatal("second active writer unexpectedly committed")
	}
	if _, err = store.GetWorkOrderV2(ctx, second.ID); err == nil {
		t.Fatal("failed launch left a WorkOrder behind")
	}
	if _, err = store.GetSourceSnapshotV2(ctx, secondSnapshot.ID); err == nil {
		t.Fatal("failed launch left a source snapshot behind")
	}
	quests, err := store.ListQuests(ctx, first.WorkspaceID)
	if err != nil || len(quests) != 1 || quests[0].ID != approval.QuestID {
		t.Fatalf("failed launch changed active quests: %#v err=%v", quests, err)
	}
}

func storageFastAgentLaunch(t *testing.T, order domain.WorkOrder, suffix string) FastAgentLaunchV2 {
	t.Helper()
	order = domain.NormalizeWorkOrder(order)
	contract := &domain.WorkOrderExecutionContract{
		ID: order.ID, Version: order.Version, Digest: domain.WorkOrderDigest(order), SourceDigest: domain.WorkOrderSourceDigest(order),
		Sources: order.Sources, Milestones: order.Milestones, Workspace: order.Workspace, Stack: order.Stack,
		Routing: order.Routing, Network: order.Network, Secrets: order.Secrets, Completion: order.Completion, Delivery: order.Delivery,
	}
	brief, err := domain.ApproveTaskBrief(domain.NormalizeTaskBrief(domain.TaskBrief{
		State: "ready", Mode: domain.TaskModeProject, Goal: order.Goal, ResultKind: "workspace_change",
		Scope: order.Scope, Criteria: order.Criteria, Permissions: domain.TaskPermissions{WriteFiles: true, ExecuteCommands: true},
		Budget:    domain.TaskBudget{Tokens: order.Budget.Tokens, ActiveSeconds: order.Budget.ActiveSeconds, MaxParallel: 1, MaxAttempts: 1},
		WorkOrder: contract, FastAgent: true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	questID, executionID, runID := "quest-"+suffix, "execution-"+suffix, "run-"+suffix
	profile := domain.AgentProfile{ID: "agent-" + suffix, Provider: domain.ProviderOllama, ProviderPreset: "ollama", Model: "model", MaxOutputTokens: 64}
	snapshot := domain.NewRunConfigurationSnapshot("test", profile, nil, now)
	return FastAgentLaunchV2{
		Order: order, Brief: &brief, QuestID: questID, IdempotencyKey: "launch-" + suffix,
		Sandbox:      domain.SandboxRecord{ID: "sandbox-" + suffix, WorkspaceID: order.WorkspaceID, ExecutionID: executionID, Kind: "copy", Backend: "test", Path: order.Workspace.Path, CreatedAt: now},
		Execution:    domain.ExecutionInstance{ID: executionID, WorkspaceID: order.WorkspaceID, ProjectAgentID: profile.ID, QuestID: questID, RunID: runID, SandboxID: "sandbox-" + suffix, Task: order.Goal, Status: domain.RunPending, Snapshot: snapshot, StartedAt: now},
		Run:          domain.Run{ID: runID, AgentID: "runtime-agent-" + suffix, ProfileID: profile.ID, WorkspaceID: order.WorkspaceID, Task: order.Goal, ConfigurationSnapshot: snapshot, Provider: string(profile.Provider), Model: profile.Model, Status: domain.RunPending, ToolsUsed: []string{}, ChangedFiles: []string{}, StartedAt: now},
		Reservation:  domain.BudgetReservation{ID: "budget-" + suffix, WorkspaceID: order.WorkspaceID, QuestID: questID, BudgetScopeQuestID: questID, ExecutionID: executionID, RunID: runID, Provider: string(profile.Provider), Model: profile.Model, EstimatedInputTokens: 10, MaxOutputTokens: 64, ReservedTokens: 74, CreatedAt: now},
		BudgetLimits: domain.BudgetReserveLimits{FreeRuntime: true, DayStart: now.Add(-time.Hour), MonthStart: now.Add(-time.Hour)},
	}
}

func TestCommitFastAgentLaunchV2PersistsEveryLaunchRecordTogether(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "fast-launch.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	order := storageWorkOrder()
	order.Workspace.Path = t.TempDir()
	launch := storageFastAgentLaunch(t, order, "all")
	approval, err := store.CommitFastAgentLaunchV2(context.Background(), launch)
	if err != nil {
		t.Fatal(err)
	}
	if approval.QuestID != launch.QuestID || approval.Status != string(domain.QuestRunning) {
		t.Fatalf("unexpected launch approval: %#v", approval)
	}
	quests, err := store.ListQuests(context.Background(), launch.Order.WorkspaceID)
	if err != nil || len(quests) != 1 || quests[0].ID != launch.QuestID || quests[0].Status != domain.QuestRunning || quests[0].Brief == nil || !quests[0].Brief.FastAgent {
		t.Fatalf("quest was not committed as a running FastAgent contract: %#v err=%v", quests, err)
	}
	if _, err = store.GetSandbox(context.Background(), launch.Sandbox.ID); err != nil {
		t.Fatalf("sandbox metadata missing: %v", err)
	}
	execution, err := store.GetExecution(context.Background(), launch.Execution.ID)
	if err != nil || execution.RunID != launch.Run.ID || execution.Status != domain.RunPending {
		t.Fatalf("execution missing or unlinked: %#v err=%v", execution, err)
	}
	run, err := store.GetRun(context.Background(), launch.Run.ID)
	if err != nil || run.Status != domain.RunPending {
		t.Fatalf("run missing: %#v err=%v", run, err)
	}
	reservation, err := store.BudgetReservation(context.Background(), launch.Reservation.ID)
	if err != nil || reservation.Status != domain.BudgetReserved || reservation.RunID != launch.Run.ID {
		t.Fatalf("budget reservation missing: %#v err=%v", reservation, err)
	}
	runtimes, err := store.ListMilestoneRuntimesV2(context.Background(), launch.QuestID, approval.WorkOrder.Version)
	if err != nil || len(runtimes) != 1 || runtimes[0].Status != domain.QuestRunning {
		t.Fatalf("milestone was not committed as running: %#v err=%v", runtimes, err)
	}
}

func TestCommitFastAgentLaunchV2RollsBackEveryLaunchRecordOnLateFailure(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "fast-launch-rollback.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	order := storageWorkOrder()
	order.Workspace.Path = t.TempDir()
	launch := storageFastAgentLaunch(t, order, "rollback")
	if _, err = store.db.Exec(`INSERT INTO budget_reservations(id,workspace_id,quest_id,budget_scope_quest_id,execution_id,run_id,provider,model,estimated_input_tokens,max_output_tokens,reserved_tokens,reserved_cents,actual_input_tokens,actual_output_tokens,actual_cents,usage_reported,status,created_at,reconciled_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,NULL)`,
		launch.Reservation.ID, launch.Order.WorkspaceID, "", "", "", "existing-run", "ollama", "model", 0, 0, 0, 0, 0, 0, 0, 0, domain.BudgetReleased, formatTime(time.Now().UTC())); err != nil {
		t.Fatal(err)
	}
	if _, err = store.CommitFastAgentLaunchV2(context.Background(), launch); err == nil {
		t.Fatal("late reservation conflict unexpectedly committed launch")
	}
	if _, err = store.GetWorkOrderV2(context.Background(), launch.Order.ID); err == nil {
		t.Fatal("failed commit left a work order")
	}
	if quests, listErr := store.ListQuests(context.Background(), launch.Order.WorkspaceID); listErr != nil || len(quests) != 0 {
		t.Fatalf("failed commit left a quest: %#v err=%v", quests, listErr)
	}
	if _, err = store.GetSandbox(context.Background(), launch.Sandbox.ID); err == nil {
		t.Fatal("failed commit left sandbox metadata")
	}
	if _, err = store.GetExecution(context.Background(), launch.Execution.ID); err == nil {
		t.Fatal("failed commit left an execution")
	}
	if _, err = store.GetRun(context.Background(), launch.Run.ID); err == nil {
		t.Fatal("failed commit left a run")
	}
	if _, found, leaseErr := store.WriterLeaseV2(context.Background(), launch.Order.WorkspaceID); leaseErr != nil || found {
		t.Fatalf("failed commit left a writer lease: found=%v err=%v", found, leaseErr)
	}
}

func TestCommitFastAgentLaunchV2RollsBackSaveExecutionAndSaveRunFailures(t *testing.T) {
	for _, table := range []string{"executions", "runs"} {
		t.Run(table, func(t *testing.T) {
			store, err := Open(filepath.Join(t.TempDir(), "fast-launch-"+table+".db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			order := storageWorkOrder()
			order.Workspace.Path = t.TempDir()
			launch := storageFastAgentLaunch(t, order, table)
			trigger := "fail_fast_launch_" + table
			if _, err = store.db.Exec(`CREATE TRIGGER ` + trigger + ` BEFORE INSERT ON ` + table + ` BEGIN SELECT RAISE(FAIL, 'controlled launch persistence failure'); END`); err != nil {
				t.Fatal(err)
			}
			if _, err = store.CommitFastAgentLaunchV2(context.Background(), launch); err == nil {
				t.Fatalf("controlled %s failure unexpectedly committed", table)
			}
			if _, err = store.GetWorkOrderV2(context.Background(), launch.Order.ID); err == nil {
				t.Fatalf("%s failure left a work order", table)
			}
			if quests, listErr := store.ListQuests(context.Background(), launch.Order.WorkspaceID); listErr != nil || len(quests) != 0 {
				t.Fatalf("%s failure left quests: %#v err=%v", table, quests, listErr)
			}
			if _, err = store.GetSandbox(context.Background(), launch.Sandbox.ID); err == nil {
				t.Fatalf("%s failure left sandbox metadata", table)
			}
			if _, err = store.GetExecution(context.Background(), launch.Execution.ID); err == nil {
				t.Fatalf("%s failure left an execution", table)
			}
			if _, err = store.GetRun(context.Background(), launch.Run.ID); err == nil {
				t.Fatalf("%s failure left a run", table)
			}
			if reservations, listErr := store.ListBudgetReservations(context.Background(), launch.Order.WorkspaceID, 10); listErr != nil || len(reservations) != 0 {
				t.Fatalf("%s failure left reservations: %#v err=%v", table, reservations, listErr)
			}
		})
	}
}

func TestApprovalInitializesImmutableMilestoneRuntimeForExactVersion(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "hub-v2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	order := storageWorkOrder()
	order.Workspace.Path = t.TempDir()
	order.Criteria = []domain.AcceptanceCriterion{
		{ID: "schema", Kind: "verification", Text: "schema", Tool: "run_command", Arguments: json.RawMessage(`{"command":"go test ./..."}`)},
		{ID: "api", Kind: "verification", Text: "api", Tool: "run_command", Arguments: json.RawMessage(`{"command":"go test ./..."}`)},
	}
	order.Milestones = []domain.MilestonePlan{
		{ID: "foundation", Goal: "schema", CriterionIDs: []string{"schema"}},
		{ID: "surface", Goal: "api", CriterionIDs: []string{"api"}, DependsOn: []string{"foundation"}},
	}
	order, err = store.SaveWorkOrderV2(ctx, order)
	if err != nil {
		t.Fatal(err)
	}
	approval, err := store.ApproveWorkOrderV2(ctx, order.ID, order.Version, domain.WorkOrderDigest(order), "milestones-once")
	if err != nil {
		t.Fatal(err)
	}
	runtimes, err := store.ListMilestoneRuntimesV2(ctx, approval.QuestID, order.Version)
	if err != nil || len(runtimes) != 2 {
		t.Fatalf("runtimes=%#v err=%v", runtimes, err)
	}
	if runtimes[0].MilestoneID != "foundation" || runtimes[0].Status != domain.QuestDraft || runtimes[1].MilestoneID != "surface" {
		t.Fatalf("milestone order/status lost: %#v", runtimes)
	}
}

// TestConversationWorkOrdersDoNotDeadlockOnTheSingleConnection pins the shape
// that froze the Master feed: the pool holds one SQLite connection, so a query
// issued while the list cursor is still open waits for a connection only that
// cursor can release. The deadline below turns that wait into a failed runtime
// lookup instead of a hang, so the regression is visible as a missing runtime.
func TestConversationWorkOrdersDoNotDeadlockOnTheSingleConnection(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "hub-v2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	order := storageWorkOrder()
	order.ConversationID = "conversation-1"
	saved, err := store.SaveWorkOrderV2(ctx, order)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ApproveWorkOrderV2(ctx, saved.ID, saved.Version, domain.WorkOrderDigest(saved), "feed-deadlock"); err != nil {
		t.Fatal(err)
	}

	listCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	started := time.Now()
	orders, err := store.ListWorkOrdersForConversationV2(listCtx, "conversation-1")
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("listing took %s: the cursor was still open while the runtime was read", elapsed)
	}
	if len(orders) != 1 || orders[0].Runtime == nil || orders[0].Runtime.QuestID == "" {
		t.Fatalf("listing lost the approved runtime: %#v", orders)
	}
}
