package app

import (
	"context"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

func TestWorkOrderEvidenceLedgerIsQuestScopedAndNeverCopiesSensitiveContext(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { application.Shutdown(context.Background()) })
	world := openTestWorld(t, application)
	now := time.Now().UTC()
	agent := domain.ProjectAgent{
		ID: "agent-ledger", WorkspaceID: world.ID, Name: "Verifier", RoleDescription: "verification",
		ConnectionID: "connection-ledger", Provider: domain.ProviderOpenAI, PrimaryModel: "model-ledger",
		CreatedAt: now, UpdatedAt: now,
	}
	if err = application.store.SaveProjectAgent(context.Background(), agent); err != nil {
		t.Fatal(err)
	}
	root := domain.Quest{ID: "quest-ledger", WorkspaceID: world.ID, Title: "Ledger", Status: domain.QuestRunning, CreatedAt: now, UpdatedAt: now}
	if err = application.store.SaveQuest(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	run := domain.Run{
		ID: "run-ledger", WorkspaceID: world.ID, ProfileID: agent.ID, Provider: string(domain.ProviderOpenAI), Model: agent.PrimaryModel,
		Status: domain.RunCompleted, StartedAt: now, DurationMs: 321,
		ContextItems: []domain.RunContextItem{
			{ID: "safe", Kind: domain.ContextWorkspaceFile, Path: "src/main.go", Category: "workspace_file", Digest: "sha256:safe", Size: 42, Content: "must not enter evidence"},
			{ID: "secret", Kind: domain.ContextWorkspaceFile, Path: ".env", Category: "workspace_file", Digest: "sha256:secret", Size: 99, Content: "TOKEN=must-not-enter-evidence"},
		},
	}
	if err = application.store.SaveRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	execution := domain.ExecutionInstance{
		ID: "execution-ledger", WorkspaceID: world.ID, ProjectAgentID: agent.ID, QuestID: root.ID, RunID: run.ID,
		SandboxID: "sandbox-ledger", FlowRunID: "flow-run-ledger", FlowNodeID: "verify", Status: domain.RunCompleted, StartedAt: now,
	}
	if err = application.store.SaveExecution(context.Background(), execution); err != nil {
		t.Fatal(err)
	}
	// Acceptance checks have their own Run for events, but no model call.
	checkRun := domain.Run{ID: "run-deterministic-accept", WorkspaceID: world.ID, Status: domain.RunFailed, StartedAt: now}
	if err = application.store.SaveRun(context.Background(), checkRun); err != nil {
		t.Fatal(err)
	}
	if err = application.store.SaveExecution(context.Background(), domain.ExecutionInstance{
		ID: "execution-deterministic-accept", WorkspaceID: world.ID, ProjectAgentID: agent.ID,
		QuestID: root.ID, RunID: checkRun.ID, FlowNodeID: "accept", Status: domain.RunFailed, StartedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err = application.store.SaveSandbox(context.Background(), domain.SandboxRecord{
		ID: execution.SandboxID, WorkspaceID: world.ID, ExecutionID: execution.ID, Kind: "copy", Backend: "docker",
		BackendImage: "point/test:1", BackendImageDigest: "sha256:image", Path: t.TempDir(), CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	cost := int64(7)
	if err = application.store.InsertUsageRecord(context.Background(), domain.UsageRecord{
		ID: "usage-ledger", WorkspaceID: world.ID, ExecutionID: execution.ID, QuestID: root.ID, ProjectAgentID: agent.ID,
		Provider: string(domain.ProviderOpenAI), Model: agent.PrimaryModel, InputTokens: 10, OutputTokens: 4, TotalTokens: 14,
		CostCents: &cost, LatencyMs: 123, Outcome: "ok", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	order := managedWorkOrderV2()
	order.WorkspaceID, order.Version = world.ID, 1
	bundle := domain.EvidenceBundle{}
	application.collectWorkOrderEvidenceLedgersV2(context.Background(), order, root, &bundle)
	if len(bundle.DockerImages) != 1 || bundle.DockerImages[0] != "sha256:image" {
		t.Fatalf("docker image ledger=%#v", bundle.DockerImages)
	}
	if len(bundle.ModelCalls) != 1 || bundle.ModelCalls[0].ConnectionID != agent.ConnectionID || !bundle.ModelCalls[0].CostKnown || !bundle.ModelCalls[0].UsageReported || bundle.ModelCalls[0].CostCents != cost {
		t.Fatalf("model call ledger=%#v", bundle.ModelCalls)
	}
	if len(bundle.ContextDisclosures) != 1 || bundle.ContextDisclosures[0].Path != "src/main.go" || bundle.ContextDisclosures[0].Digest != "sha256:safe" {
		t.Fatalf("context disclosure ledger=%#v", bundle.ContextDisclosures)
	}
}
