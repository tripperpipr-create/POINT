package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/sandbox"
)

type failingFastAgentSandbox struct{ *strongWorkOrderSandbox }

func (failingFastAgentSandbox) Create(context.Context, sandbox.CreateRequest) (domain.SandboxRecord, error) {
	return domain.SandboxRecord{}, errors.New("controlled sandbox failure")
}

func fastAgentV2Fixture(t *testing.T, goProject bool) (*App, domain.ProjectAgent, string) {
	t.Helper()
	t.Setenv("POINT_AGENT_HUB_V2", "1")
	t.Setenv("REDIS_ADDR", "")
	root := t.TempDir()
	if goProject {
		if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test/fast\n\ngo 1.25\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	} else if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	backend := &strongWorkOrderSandbox{recordingSandboxBackend: &recordingSandboxBackend{Manager: &sandbox.Manager{Root: t.TempDir()}}}
	application, err := New(t.TempDir(), WithSandboxBackend(backend))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { application.Shutdown(context.Background()) })
	view, err := application.OpenWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	connection := saveTestConnection(t, application, "fast-agent-v2", "openai", "FastAgent v2")
	agent, err := application.SaveProjectAgent(domain.ProjectAgent{
		WorkspaceID: view.Workspace.ID, Name: "Fast", RoleDescription: "Developer",
		ConnectionID: connection.ID, Provider: domain.ProviderOpenAI, ProviderPreset: "openai",
		BaseURL: connection.BaseURL, PrimaryModel: "gpt-test", ContextWindowTokens: 32768,
		MaxOutputTokens: 2048, MaxSteps: 12, MaxDurationSeconds: 600,
		AllowedTools: []string{"list_files", "read_file", "search_code", "propose_patch", "run_command"},
		ApprovalMode: domain.ApprovalSafe,
	})
	if err != nil {
		t.Fatal(err)
	}
	return application, agent, root
}

func TestPrepareFastAgentV2BuildsVerifiedGoWorkOrder(t *testing.T) {
	application, projectAgent, root := fastAgentV2Fixture(t, true)
	prepared, err := application.prepareFastAgentV2(context.Background(), FastAgentRequest{
		ProfileID: projectAgent.ID, Task: "Add a health endpoint",
		ContextItems: []domain.RunContextInput{{Kind: domain.ContextWorkspaceFile, Path: "go.mod"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.order.Workspace.Path != root || prepared.order.Workspace.Isolation != "snapshot" && prepared.order.Workspace.Isolation != "git_worktree" {
		t.Fatalf("unexpected workspace plan: %#v", prepared.order.Workspace)
	}
	criterion := prepared.order.Criteria[0]
	if criterion.Kind != "verification" || criterion.Tool != "run_command" || string(criterion.Arguments) != `{"command":"go test ./..."}` {
		t.Fatalf("Go verification was not frozen into the WorkOrder: %#v", criterion)
	}
	if len(prepared.order.Completion.Checks) != 2 || prepared.order.Completion.Checks[1].Command != "go test ./..." {
		t.Fatalf("completion profile did not preserve the exact command: %#v", prepared.order.Completion)
	}
	if len(prepared.order.Network) != 0 || len(prepared.order.Secrets) != 0 || prepared.order.Delivery.CommitMode != "none" {
		t.Fatalf("short WorkOrder gained unapproved authority: %#v", prepared.order)
	}
	if len(prepared.snapshots) != 1 || len(prepared.order.Sources) != 1 || prepared.snapshots[0].Digest != prepared.order.Sources[0].Digest {
		t.Fatalf("context was not frozen as source evidence: snapshots=%#v refs=%#v", prepared.snapshots, prepared.order.Sources)
	}
	if !prepared.brief.FastAgent || prepared.brief.WorkOrder == nil {
		t.Fatalf("FastAgent brief lost the v2 execution contract: %#v", prepared.brief)
	}
}

func TestPrepareFastAgentV2UsesManualReviewForUnknownStack(t *testing.T) {
	application, projectAgent, _ := fastAgentV2Fixture(t, false)
	prepared, err := application.prepareFastAgentV2(context.Background(), FastAgentRequest{ProfileID: projectAgent.ID, Task: "Update notes"})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.order.Criteria[0].Kind != "manual" || len(prepared.order.Completion.Checks) != 1 || prepared.order.Completion.Checks[0].Kind != domain.CompletionCheckAcceptance {
		t.Fatalf("unknown stack must require human review: %#v %#v", prepared.order.Criteria, prepared.order.Completion)
	}
}

func TestFastAgentV2RejectsStalePreflightBeforePersistence(t *testing.T) {
	application, projectAgent, _ := fastAgentV2Fixture(t, true)
	world, err := application.requireWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	before, err := application.store.ListQuests(context.Background(), world.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = application.StartFastAgent(FastAgentRequest{ProfileID: projectAgent.ID, Task: "Change code", PreflightFingerprint: "sha256:stale"}); err == nil {
		t.Fatal("stale preflight unexpectedly launched FastAgent")
	}
	after, err := application.store.ListQuests(context.Background(), world.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("stale preflight persisted a quest: before=%d after=%d", len(before), len(after))
	}
}

func TestFastAgentV2BudgetRejectionLeavesNoLaunchEntities(t *testing.T) {
	application, projectAgent, _ := fastAgentV2Fixture(t, true)
	world, err := application.requireWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = application.SaveHubBudget(HubBudgetSettings{DailyCents: 1, HardStop: true}); err != nil {
		t.Fatal(err)
	}
	cost := int64(1)
	if err = application.store.InsertUsageRecord(context.Background(), domain.UsageRecord{
		ID: "usage-fast-budget", WorkspaceID: world.ID, CostCents: &cost, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = application.StartFastAgent(FastAgentRequest{ProfileID: projectAgent.ID, Task: "Change code"}); err == nil {
		t.Fatal("exhausted budget unexpectedly launched FastAgent")
	}
	quests, err := application.store.ListQuests(context.Background(), world.ID)
	if err != nil || len(quests) != 0 {
		t.Fatalf("budget rejection left quests: %#v err=%v", quests, err)
	}
	executions, err := application.store.ListExecutions(context.Background(), world.ID, 100)
	if err != nil || len(executions) != 0 {
		t.Fatalf("budget rejection left executions: %#v err=%v", executions, err)
	}
}

func TestFastAgentV2SandboxFailureLeavesNoLaunchEntities(t *testing.T) {
	application, projectAgent, _ := fastAgentV2Fixture(t, true)
	base := &strongWorkOrderSandbox{recordingSandboxBackend: &recordingSandboxBackend{Manager: &sandbox.Manager{Root: t.TempDir()}}}
	application.sandboxBackend = failingFastAgentSandbox{strongWorkOrderSandbox: base}
	world, err := application.requireWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = application.StartFastAgent(FastAgentRequest{ProfileID: projectAgent.ID, Task: "Change code"}); err == nil {
		t.Fatal("sandbox failure unexpectedly launched FastAgent")
	}
	quests, err := application.store.ListQuests(context.Background(), world.ID)
	if err != nil || len(quests) != 0 {
		t.Fatalf("sandbox failure left quests: %#v err=%v", quests, err)
	}
	_, found, err := application.store.WriterLeaseV2(context.Background(), world.ID)
	if err != nil || found {
		t.Fatalf("sandbox failure left a writer lease: found=%v err=%v", found, err)
	}
	executions, err := application.store.ListExecutions(context.Background(), world.ID, 100)
	if err != nil || len(executions) != 0 {
		t.Fatalf("sandbox failure left executions: %#v err=%v", executions, err)
	}
	runtimeState, err := application.RuntimeState()
	if err != nil {
		t.Fatal(err)
	}
	if len(runtimeState.WorkOrders) != 0 || len(runtimeState.Runs) != 0 {
		t.Fatalf("sandbox failure leaked runtime launch entities: workOrders=%#v runs=%#v", runtimeState.WorkOrders, runtimeState.Runs)
	}
}

func TestFastAgentV2PostCommitStartFailureIsTerminalAndRecoverable(t *testing.T) {
	application, projectAgent, _ := fastAgentV2Fixture(t, true)
	world, err := application.requireWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	application.engine.StopAll()
	if _, err = application.StartFastAgent(FastAgentRequest{ProfileID: projectAgent.ID, Task: "Change code"}); err == nil {
		t.Fatal("stopped engine unexpectedly launched FastAgent")
	}
	quests, err := application.store.ListQuests(context.Background(), world.ID)
	if err != nil || len(quests) != 1 || quests[0].Status != domain.QuestBlocked {
		t.Fatalf("post-commit failure did not block its quest: %#v err=%v", quests, err)
	}
	lease, found, err := application.store.WriterLeaseV2(context.Background(), world.ID)
	if err != nil || !found || lease.State != "released" {
		t.Fatalf("post-commit failure retained its writer lease: %#v found=%v err=%v", lease, found, err)
	}
	runs, err := application.store.ListRunsForWorkspace(context.Background(), world.ID, 10)
	if err != nil || len(runs) != 1 || runs[0].Status != domain.RunFailed {
		t.Fatalf("post-commit failure left a non-terminal run: %#v err=%v", runs, err)
	}
	executions, err := application.store.ListExecutions(context.Background(), world.ID, 10)
	if err != nil || len(executions) != 1 || executions[0].Status != domain.RunFailed {
		t.Fatalf("post-commit failure left a non-terminal execution: %#v err=%v", executions, err)
	}
	record, err := application.store.GetSandbox(context.Background(), executions[0].SandboxID)
	if err != nil || record.ClosedAt == nil {
		t.Fatalf("post-commit failure left an open sandbox: %#v err=%v", record, err)
	}
	reservations, err := application.store.ListBudgetReservations(context.Background(), world.ID, 10)
	if err != nil || len(reservations) != 1 || reservations[0].Status != domain.BudgetReleased {
		t.Fatalf("post-commit failure retained its budget reservation: %#v err=%v", reservations, err)
	}
	state, err := application.RuntimeState()
	if err != nil || len(state.WorkOrders) != 1 || state.WorkOrders[0].Runtime == nil || state.WorkOrders[0].Runtime.Status != domain.QuestBlocked {
		t.Fatalf("blocked work order is not visible after post-commit failure: %#v err=%v", state.WorkOrders, err)
	}
}

func TestFastAgentV2CommitFailureClosesPhysicalSandboxAndRollsBackLaunch(t *testing.T) {
	application, projectAgent, _ := fastAgentV2Fixture(t, true)
	ctx := context.Background()
	prepared, err := application.prepareFastAgentV2(ctx, FastAgentRequest{ProfileID: projectAgent.ID, Task: "Hold writer lease"})
	if err != nil {
		t.Fatal(err)
	}
	holder, err := application.prepareWorkOrderWorkspaceV2(ctx, prepared.order)
	if err != nil {
		t.Fatal(err)
	}
	approval, err := application.store.CreateApprovedWorkOrderV2(ctx, holder, prepared.snapshots, "holder")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = application.StartFastAgent(FastAgentRequest{ProfileID: projectAgent.ID, Task: "Competing launch"}); err == nil {
		t.Fatal("competing writer unexpectedly committed")
	}
	backend := application.sandboxBackend.(*strongWorkOrderSandbox).recordingSandboxBackend
	backend.mu.Lock()
	records := append([]domain.SandboxRecord(nil), backend.records...)
	backend.mu.Unlock()
	if len(records) != 1 {
		t.Fatalf("expected one physical sandbox attempt, got %#v", records)
	}
	if _, statErr := os.Stat(records[0].Path); !os.IsNotExist(statErr) {
		t.Fatalf("failed transaction left physical sandbox %q: %v", records[0].Path, statErr)
	}
	quests, err := application.store.ListQuests(ctx, holder.WorkspaceID)
	if err != nil || len(quests) != 1 || quests[0].ID != approval.QuestID {
		t.Fatalf("failed transaction changed durable quests: %#v err=%v", quests, err)
	}
	runs, err := application.store.ListRunsForWorkspace(ctx, holder.WorkspaceID, 10)
	if err != nil || len(runs) != 0 {
		t.Fatalf("failed transaction left runs: %#v err=%v", runs, err)
	}
	executions, err := application.store.ListExecutions(ctx, holder.WorkspaceID, 10)
	if err != nil || len(executions) != 0 {
		t.Fatalf("failed transaction left executions: %#v err=%v", executions, err)
	}
	reservations, err := application.store.ListBudgetReservations(ctx, holder.WorkspaceID, 10)
	if err != nil || len(reservations) != 0 {
		t.Fatalf("failed transaction left budget reservations: %#v err=%v", reservations, err)
	}
}
